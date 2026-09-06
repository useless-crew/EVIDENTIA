package blockchain

import (
	"context"

	"github.com/google/uuid"
)

// NoopService is the blockchain.Service implementation used when
// FABRIC_ENABLED=false. Every method returns ErrUnavailable.
//
// This is NOT a mock — it is a real, production-safe implementation that
// correctly communicates to callers that blockchain verification is not
// available in this deployment. The callers (blockchain_anchor_service,
// verification API) MUST treat ErrUnavailable as BLOCKCHAIN_UNAVAILABLE,
// not as a tamper finding.
//
// Using a no-op instead of nil avoids nil-dereference panics when the
// blockchain layer is disabled, while preserving the correct semantics
// (disabled ≠ unavailable-by-failure, but the external behavior is the
// same: no blockchain verification is possible).
type NoopService struct{}

func NewNoopService() Service {
	return &NoopService{}
}

func (s *NoopService) AnchorEvidence(_ context.Context, _ AnchorRequest) (AnchorResult, error) {
	return AnchorResult{}, ErrUnavailable
}

func (s *NoopService) VerifyEvidence(_ context.Context, _ VerifyRequest) (VerifyResult, error) {
	return VerifyResult{}, ErrUnavailable
}

func (s *NoopService) GetProvenance(_ context.Context, _ uuid.UUID) (*ProvenanceEntry, error) {
	return nil, ErrUnavailable
}

func (s *NoopService) GetHistory(_ context.Context, _ uuid.UUID) ([]ProvenanceEntry, error) {
	return nil, ErrUnavailable
}

func (s *NoopService) IsEnabled() bool { return false }

func (s *NoopService) Close() error { return nil }
