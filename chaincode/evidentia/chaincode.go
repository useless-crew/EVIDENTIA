// Package main implements the Evidentia smart contract for Hyperledger Fabric.
// It anchors evidence provenance records on the ledger and supports retrieval
// and history queries. The chaincode is intentionally minimal — it never stores
// file content, only SHA-256 hashes and custody metadata.
//
// All function arguments are strings (the Fabric wire format). Type conversion
// is performed inline where needed.
//
// Operations:
//
//	AnchorEvidence     — write a new provenance record and return the txID (INVOKE)
//	GetEvidence        — read a specific (evidenceID, version) record (QUERY)
//	GetEvidenceLatest  — find and return the highest-version record for an evidence (QUERY)
//	GetEvidenceHistory — all versions of an evidence, ordered oldest to newest (QUERY)
package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/hyperledger/fabric-contract-api-go/v2/contractapi"
)

// EvidenceRecord is the canonical state object written to the ledger for
// each anchored piece of evidence. The composite key (EvidenceID + Version)
// uniquely identifies a record — multiple versions of the same evidence (e.g.
// after redaction) are stored as separate state objects under:
//
//	CreateCompositeKey("evidence", []{evidenceID, zeroPaddedVersion})
type EvidenceRecord struct {
	EvidenceID    string `json:"evidence_id"`
	Version       int    `json:"version"`
	Hash          string `json:"hash"`
	EventType     string `json:"event_type"`
	CaseReference string `json:"case_reference"`
	ActorOrg      string `json:"actor_org"`
	AppRecordID   string `json:"app_record_id"`
	TxID          string `json:"tx_id"`
	AnchoredAt    string `json:"anchored_at"`
}

// HistoryEntry is one item in the per-evidence ledger history.
type HistoryEntry struct {
	TxID      string          `json:"tx_id"`
	Timestamp string          `json:"timestamp"`
	IsDelete  bool            `json:"is_delete"`
	Record    *EvidenceRecord `json:"record,omitempty"`
}

// EvidentiaChaincodeContract implements the Evidentia smart contract.
type EvidentiaChaincodeContract struct {
	contractapi.Contract
}

// AnchorEvidence writes an EvidenceRecord to the ledger. Returns the Fabric
// transaction ID as a plain string so the backend can store it without needing
// a separate roundtrip.
//
// Idempotent: re-submitting the same (evidenceID, version, hash) succeeds
// without error. Re-submitting with the same (evidenceID, version) but a
// different hash is rejected — preventing silent hash substitution.
func (c *EvidentiaChaincodeContract) AnchorEvidence(
	ctx contractapi.TransactionContextInterface,
	evidenceID string,
	versionStr string,
	hash string,
	eventType string,
	caseReference string,
	actorOrg string,
	appRecordID string,
) (string, error) {
	if evidenceID == "" || hash == "" || eventType == "" {
		return "", fmt.Errorf("AnchorEvidence: evidenceID, hash, and eventType are required")
	}

	version, err := strconv.Atoi(versionStr)
	if err != nil {
		return "", fmt.Errorf("AnchorEvidence: invalid version %q: %w", versionStr, err)
	}

	stateKey, err := compositeKey(ctx, evidenceID, version)
	if err != nil {
		return "", err
	}

	existing, err := ctx.GetStub().GetState(stateKey)
	if err != nil {
		return "", fmt.Errorf("AnchorEvidence: read existing state: %w", err)
	}
	if existing != nil {
		var existingRecord EvidenceRecord
		if jsonErr := json.Unmarshal(existing, &existingRecord); jsonErr != nil {
			return "", fmt.Errorf("AnchorEvidence: unmarshal existing: %w", jsonErr)
		}
		if existingRecord.Hash != hash {
			return "", fmt.Errorf("AnchorEvidence: hash mismatch — stored=%s submitted=%s", existingRecord.Hash, hash)
		}
		// Idempotent re-submission with identical hash — return the original txID.
		return existingRecord.TxID, nil
	}

	txTime, err := ctx.GetStub().GetTxTimestamp()
	if err != nil {
		return "", fmt.Errorf("AnchorEvidence: get tx timestamp: %w", err)
	}
	anchoredAt := time.Unix(txTime.GetSeconds(), int64(txTime.GetNanos())).UTC().Format(time.RFC3339)
	txID := ctx.GetStub().GetTxID()

	record := EvidenceRecord{
		EvidenceID:    evidenceID,
		Version:       version,
		Hash:          hash,
		EventType:     eventType,
		CaseReference: caseReference,
		ActorOrg:      actorOrg,
		AppRecordID:   appRecordID,
		TxID:          txID,
		AnchoredAt:    anchoredAt,
	}

	recordBytes, err := json.Marshal(record)
	if err != nil {
		return "", fmt.Errorf("AnchorEvidence: marshal record: %w", err)
	}

	if err := ctx.GetStub().PutState(stateKey, recordBytes); err != nil {
		return "", fmt.Errorf("AnchorEvidence: write state: %w", err)
	}

	return txID, nil
}

// GetEvidence retrieves the EvidenceRecord for an (evidenceID, version) pair.
// Returns "evidence not found: ..." if the key does not exist.
func (c *EvidentiaChaincodeContract) GetEvidence(
	ctx contractapi.TransactionContextInterface,
	evidenceID string,
	versionStr string,
) (*EvidenceRecord, error) {
	version, err := strconv.Atoi(versionStr)
	if err != nil {
		return nil, fmt.Errorf("GetEvidence: invalid version %q: %w", versionStr, err)
	}

	stateKey, err := compositeKey(ctx, evidenceID, version)
	if err != nil {
		return nil, err
	}

	stateBytes, err := ctx.GetStub().GetState(stateKey)
	if err != nil {
		return nil, fmt.Errorf("GetEvidence: read state: %w", err)
	}
	if stateBytes == nil {
		return nil, fmt.Errorf("evidence not found: %s version %d", evidenceID, version)
	}

	var record EvidenceRecord
	if err := json.Unmarshal(stateBytes, &record); err != nil {
		return nil, fmt.Errorf("GetEvidence: unmarshal: %w", err)
	}
	return &record, nil
}

// GetEvidenceLatest scans the composite key range for evidenceID and returns
// the record with the highest version. If no records exist, returns
// "evidence not found: ...". This is the operation called during
// blockchain verification to fetch the current on-chain hash.
func (c *EvidentiaChaincodeContract) GetEvidenceLatest(
	ctx contractapi.TransactionContextInterface,
	evidenceID string,
) (*EvidenceRecord, error) {
	iter, err := ctx.GetStub().GetStateByPartialCompositeKey("evidence", []string{evidenceID})
	if err != nil {
		return nil, fmt.Errorf("GetEvidenceLatest: range query: %w", err)
	}
	defer iter.Close()

	var latest *EvidenceRecord
	for iter.HasNext() {
		kv, iterErr := iter.Next()
		if iterErr != nil {
			return nil, fmt.Errorf("GetEvidenceLatest: iterate: %w", iterErr)
		}
		var rec EvidenceRecord
		if jsonErr := json.Unmarshal(kv.Value, &rec); jsonErr != nil {
			continue
		}
		if latest == nil || rec.Version > latest.Version {
			latest = &rec
		}
	}

	if latest == nil {
		return nil, fmt.Errorf("evidence not found: %s", evidenceID)
	}
	return latest, nil
}

// GetEvidenceHistory returns every version of an evidence record in the ledger
// state, ordered by version ascending. It does NOT use GetHistoryForKey (which
// returns Fabric block history including Asynq retry attempts) — instead it
// scans the current state for all versions, which is the authoritative
// confirmed-only view.
func (c *EvidentiaChaincodeContract) GetEvidenceHistory(
	ctx contractapi.TransactionContextInterface,
	evidenceID string,
) ([]*EvidenceRecord, error) {
	iter, err := ctx.GetStub().GetStateByPartialCompositeKey("evidence", []string{evidenceID})
	if err != nil {
		return nil, fmt.Errorf("GetEvidenceHistory: range query: %w", err)
	}
	defer iter.Close()

	var records []*EvidenceRecord
	for iter.HasNext() {
		kv, iterErr := iter.Next()
		if iterErr != nil {
			return nil, fmt.Errorf("GetEvidenceHistory: iterate: %w", iterErr)
		}
		var rec EvidenceRecord
		if jsonErr := json.Unmarshal(kv.Value, &rec); jsonErr != nil {
			continue
		}
		rCopy := rec
		records = append(records, &rCopy)
	}
	return records, nil
}

// compositeKey builds the standard composite key for evidence state lookups.
// The version is zero-padded to 10 digits so lexicographic order == numeric
// order for the GetStateByPartialCompositeKey range scans above.
func compositeKey(ctx contractapi.TransactionContextInterface, evidenceID string, version int) (string, error) {
	paddedVersion := fmt.Sprintf("%010d", version)
	key, err := ctx.GetStub().CreateCompositeKey("evidence", []string{evidenceID, paddedVersion})
	if err != nil {
		return "", fmt.Errorf("create composite key: %w", err)
	}
	return key, nil
}

func main() {
	cc, err := contractapi.NewChaincode(&EvidentiaChaincodeContract{})
	if err != nil {
		panic(fmt.Sprintf("evidentia chaincode: create: %v", err))
	}
	if err := cc.Start(); err != nil {
		panic(fmt.Sprintf("evidentia chaincode: start: %v", err))
	}
}
