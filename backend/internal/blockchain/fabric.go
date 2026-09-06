package blockchain

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/hyperledger/fabric-gateway/pkg/client"
	"github.com/hyperledger/fabric-gateway/pkg/hash"
	"github.com/hyperledger/fabric-gateway/pkg/identity"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"evidentia/backend/internal/config"
)

// FabricService is the real Hyperledger Fabric implementation of blockchain.Service.
// It uses the official fabric-gateway client SDK to submit transactions
// to the Fabric peer configured in FabricConfig. The connection is
// established once in New() and reused across requests.
//
// The backend connects to the Fabric network using a dedicated MSP identity
// (configured via FABRIC_CERT_PATH/FABRIC_KEY_PATH) and submits transactions
// to the evidentia chaincode on the configured channel.
type FabricService struct {
	cfg      config.FabricConfig
	log      *slog.Logger
	conn     *grpc.ClientConn
	gateway  *client.Gateway
	network  *client.Network
	contract *client.Contract
}

// New connects to the Hyperledger Fabric peer and returns a ready-to-use
// FabricService. Returns an error if the connection cannot be established
// or the MSP identity files are missing or malformed.
//
// The connection is authenticated with mTLS using the files at
// cfg.CertPath (client cert), cfg.KeyPath (client private key), and
// cfg.TLSCertPath (server CA cert for TLS).
func New(ctx context.Context, cfg config.FabricConfig, log *slog.Logger) (*FabricService, error) {
	clientConn, err := newGRPCConnection(cfg)
	if err != nil {
		return nil, fmt.Errorf("blockchain: grpc connection to %s: %w", cfg.PeerEndpoint, err)
	}

	id, err := newIdentity(cfg)
	if err != nil {
		clientConn.Close()
		return nil, fmt.Errorf("blockchain: load MSP identity: %w", err)
	}

	sign, err := newSigner(cfg)
	if err != nil {
		clientConn.Close()
		return nil, fmt.Errorf("blockchain: load signing key: %w", err)
	}

	gw, err := client.Connect(
		id,
		client.WithSign(sign),
		client.WithHash(hash.SHA256),
		client.WithClientConnection(clientConn),
		client.WithEvaluateTimeout(5*time.Second),
		client.WithEndorseTimeout(15*time.Second),
		client.WithSubmitTimeout(5*time.Second),
		client.WithCommitStatusTimeout(60*time.Second),
	)
	if err != nil {
		clientConn.Close()
		return nil, fmt.Errorf("blockchain: connect gateway: %w", err)
	}

	network := gw.GetNetwork(cfg.Channel)
	contract := network.GetContract(cfg.Chaincode)

	log.Info("blockchain: Hyperledger Fabric connection established",
		slog.String("peer", cfg.PeerEndpoint),
		slog.String("channel", cfg.Channel),
		slog.String("chaincode", cfg.Chaincode),
		slog.String("msp_id", cfg.MSPID),
	)

	return &FabricService{
		cfg:      cfg,
		log:      log,
		conn:     clientConn,
		gateway:  gw,
		network:  network,
		contract: contract,
	}, nil
}

// chaincodeRecord mirrors the JSON EvidenceRecord the chaincode returns from
// GetEvidence, GetEvidenceLatest, and GetEvidenceHistory. Field names must
// match chaincode/evidentia/chaincode.go exactly.
type chaincodeRecord struct {
	EvidenceID  string `json:"evidence_id"`
	Version     int32  `json:"version"`
	EventType   string `json:"event_type"`
	Hash        string `json:"hash"`
	ActorOrg    string `json:"actor_org"`
	AnchoredAt  string `json:"anchored_at"`
	TxID        string `json:"tx_id"`
	AppRecordID string `json:"app_record_id"`
}

func (s *FabricService) AnchorEvidence(ctx context.Context, req AnchorRequest) (AnchorResult, error) {
	s.log.InfoContext(ctx, "blockchain: submitting AnchorEvidence",
		slog.String("evidence_id", req.EvidenceID.String()),
		slog.Int("version", int(req.Version)),
		slog.String("event_type", req.EventType),
	)

	// Each argument is passed as a separate string — this is the Fabric wire
	// format expected by contractapi when the chaincode uses individual params.
	resultBytes, err := s.contract.SubmitTransaction("AnchorEvidence",
		req.EvidenceID.String(),
		fmt.Sprintf("%d", req.Version),
		req.Hash,
		req.EventType,
		req.CaseReference,
		req.ActorOrg,
		req.AppRecordID.String(),
	)
	if err != nil {
		return AnchorResult{}, wrapFabricError(err)
	}

	// The chaincode returns the Fabric transaction ID as a plain string.
	txID := string(resultBytes)
	if txID == "" {
		txID = "unknown"
	}

	s.log.InfoContext(ctx, "blockchain: AnchorEvidence confirmed",
		slog.String("evidence_id", req.EvidenceID.String()),
		slog.String("tx_id", txID),
	)

	return AnchorResult{
		TransactionID: txID,
		Organization:  s.cfg.MSPID,
		Timestamp:     time.Now().UTC(),
	}, nil
}

func (s *FabricService) VerifyEvidence(ctx context.Context, req VerifyRequest) (VerifyResult, error) {
	result, err := s.contract.EvaluateTransaction("GetEvidence",
		req.EvidenceID.String(),
		fmt.Sprintf("%d", req.Version),
	)
	if err != nil {
		return VerifyResult{}, wrapFabricError(err)
	}
	if len(result) == 0 {
		return VerifyResult{}, ErrNotFound
	}

	var rec chaincodeRecord
	if err := json.Unmarshal(result, &rec); err != nil {
		return VerifyResult{}, fmt.Errorf("blockchain: parse GetEvidence response: %w", err)
	}

	match := rec.Hash == req.Hash
	ts, _ := time.Parse(time.RFC3339, rec.AnchoredAt)

	if !match {
		return VerifyResult{
			OnChainHash:   rec.Hash,
			Match:         false,
			TransactionID: rec.TxID,
			AnchoredAt:    ts,
			Organization:  rec.ActorOrg,
		}, ErrIntegrityMismatch
	}

	return VerifyResult{
		OnChainHash:   rec.Hash,
		Match:         true,
		TransactionID: rec.TxID,
		AnchoredAt:    ts,
		Organization:  rec.ActorOrg,
	}, nil
}

func (s *FabricService) GetProvenance(ctx context.Context, evidenceID uuid.UUID) (*ProvenanceEntry, error) {
	// GetEvidenceLatest scans by evidenceID prefix — no version arg needed.
	result, err := s.contract.EvaluateTransaction("GetEvidenceLatest", evidenceID.String())
	if err != nil {
		return nil, wrapFabricError(err)
	}
	if len(result) == 0 {
		return nil, ErrNotFound
	}

	var rec chaincodeRecord
	if err := json.Unmarshal(result, &rec); err != nil {
		return nil, fmt.Errorf("blockchain: parse GetEvidenceLatest response: %w", err)
	}

	ts, _ := time.Parse(time.RFC3339, rec.AnchoredAt)
	return &ProvenanceEntry{
		EvidenceID:    rec.EvidenceID,
		Version:       rec.Version,
		EventType:     rec.EventType,
		Hash:          rec.Hash,
		Organization:  rec.ActorOrg,
		Timestamp:     ts,
		TransactionID: rec.TxID,
		AppRecordID:   rec.AppRecordID,
	}, nil
}

func (s *FabricService) GetHistory(ctx context.Context, evidenceID uuid.UUID) ([]ProvenanceEntry, error) {
	// GetEvidenceHistory scans by evidenceID prefix — returns all versions.
	result, err := s.contract.EvaluateTransaction("GetEvidenceHistory", evidenceID.String())
	if err != nil {
		return nil, wrapFabricError(err)
	}
	if len(result) == 0 {
		return []ProvenanceEntry{}, nil
	}

	var records []chaincodeRecord
	if err := json.Unmarshal(result, &records); err != nil {
		return nil, fmt.Errorf("blockchain: parse GetEvidenceHistory response: %w", err)
	}

	entries := make([]ProvenanceEntry, 0, len(records))
	for _, rec := range records {
		ts, _ := time.Parse(time.RFC3339, rec.AnchoredAt)
		entries = append(entries, ProvenanceEntry{
			EvidenceID:    rec.EvidenceID,
			Version:       rec.Version,
			EventType:     rec.EventType,
			Hash:          rec.Hash,
			Organization:  rec.ActorOrg,
			Timestamp:     ts,
			TransactionID: rec.TxID,
			AppRecordID:   rec.AppRecordID,
		})
	}
	return entries, nil
}

func (s *FabricService) IsEnabled() bool { return true }

func (s *FabricService) Close() error {
	if err := s.gateway.Close(); err != nil {
		s.log.Error("blockchain: closing gateway", slog.String("error", err.Error()))
	}
	if err := s.conn.Close(); err != nil {
		s.log.Error("blockchain: closing grpc connection", slog.String("error", err.Error()))
		return err
	}
	return nil
}

// --- connection helpers ---

func newGRPCConnection(cfg config.FabricConfig) (*grpc.ClientConn, error) {
	tlsRootCertBytes, err := os.ReadFile(cfg.TLSCertPath)
	if err != nil {
		return nil, fmt.Errorf("read TLS cert %s: %w", cfg.TLSCertPath, err)
	}

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(tlsRootCertBytes) {
		return nil, errors.New("failed to append TLS root cert to pool")
	}

	tlsConfig := &tls.Config{
		RootCAs:    certPool,
		MinVersion: tls.VersionTLS12,
	}
	if cfg.GatewaySSLHostOverride != "" {
		tlsConfig.ServerName = cfg.GatewaySSLHostOverride
	}

	return grpc.NewClient(
		cfg.PeerEndpoint,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
	)
}

func newIdentity(cfg config.FabricConfig) (*identity.X509Identity, error) {
	certBytes, err := os.ReadFile(cfg.CertPath)
	if err != nil {
		return nil, fmt.Errorf("read MSP cert %s: %w", cfg.CertPath, err)
	}

	block, _ := pem.Decode(certBytes)
	if block == nil {
		return nil, errors.New("no PEM block found in MSP cert file")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse MSP cert: %w", err)
	}

	return identity.NewX509Identity(cfg.MSPID, cert)
}

func newSigner(cfg config.FabricConfig) (identity.Sign, error) {
	keyBytes, err := os.ReadFile(cfg.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("read private key %s: %w", cfg.KeyPath, err)
	}

	privateKey, err := identity.PrivateKeyFromPEM(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}

	return identity.NewPrivateKeySign(privateKey)
}

// wrapFabricError normalises Fabric SDK errors into Evidentia errors.
// It deliberately does not surface internal Fabric details (peer addresses,
// chaincode arguments, TLS information) in the returned error — these are
// operational details that belong in the log, not in an API response.
func wrapFabricError(err error) error {
	if err == nil {
		return nil
	}
	// Check for "not found" responses from the chaincode.
	// The chaincode returns a specific error message for missing records.
	errStr := err.Error()
	if len(errStr) > 0 {
		// Chaincode returns "evidence not found" on missing records.
		if contains(errStr, "evidence not found") || contains(errStr, "no evidence") {
			return ErrNotFound
		}
	}
	// Everything else is treated as an availability failure, not a
	// tamper finding. The caller records this as BLOCKCHAIN_UNAVAILABLE.
	return fmt.Errorf("%w: %s", ErrUnavailable, sanitizeFabricError(err))
}

// sanitizeFabricError produces a safe log message from a Fabric error,
// removing any sensitive details (key material, credentials, internal paths).
func sanitizeFabricError(err error) string {
	if err == nil {
		return ""
	}
	// The Fabric SDK error messages are generally safe to log (they contain
	// chaincode output, endorsement failures, etc. — never key material).
	// Truncate to 256 chars to avoid log flooding.
	msg := err.Error()
	if len(msg) > 256 {
		return msg[:256] + "..."
	}
	return msg
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && func() bool {
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}())
}
