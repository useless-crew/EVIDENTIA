package utils

import (
	"encoding/json"
	"fmt"
)

// MaxMetadataBytes bounds a JSONB metadata payload (cases.metadata,
// case_involved_parties.metadata, ...) accepted from a client. No project
// requirement documents a specific limit, so this is a generous but firm
// security default — large enough for genuine structured metadata, small
// enough that a client cannot use it to smuggle an oversized payload past
// the body-size limit's coarser check.
const MaxMetadataBytes = 32 * 1024

// ValidateJSONMetadata reports whether raw is acceptable as a JSONB
// metadata payload: absent/empty is valid (the caller substitutes `{}`),
// otherwise it must be well-formed JSON, a JSON object (never a bare
// array/string/number — metadata is always a key/value bag, not an
// arbitrary value), and within MaxMetadataBytes. It deliberately does NOT
// inspect the object's keys/values — metadata must never be trusted for
// authorization decisions regardless of what it contains (master prompt:
// a `{"role":"ADMIN"}` key must never affect authorization), so there is
// nothing further to validate here.
func ValidateJSONMetadata(raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	if len(raw) > MaxMetadataBytes {
		return fmt.Errorf("metadata exceeds the maximum size of %d bytes", MaxMetadataBytes)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("metadata must be valid JSON: %w", err)
	}
	if _, ok := v.(map[string]any); !ok {
		return fmt.Errorf("metadata must be a JSON object")
	}
	return nil
}
