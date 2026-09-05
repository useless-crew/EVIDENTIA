package jobs

import (
	"errors"
	"testing"

	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
)

func TestCategoryOf_UnwrappedErrorIsTransient(t *testing.T) {
	assert.Equal(t, FailureCategoryTransient, CategoryOf(errors.New("database unavailable")),
		"an error with no explicit classification must default to TRANSIENT (safe: worth retrying)")
}

func TestCategoryOf_NilIsEmpty(t *testing.T) {
	assert.Equal(t, FailureCategory(""), CategoryOf(nil))
}

func TestCategoryOf_BareSkipRetryIsPermanent(t *testing.T) {
	assert.Equal(t, FailureCategoryPermanent, CategoryOf(asynq.SkipRetry))
}

func TestPermanent_ClassifiesAndSkipsRetry(t *testing.T) {
	err := Permanent(FailureCategoryPermanent, errors.New("malformed payload"))
	assert.Equal(t, FailureCategoryPermanent, CategoryOf(err))
	assert.True(t, errors.Is(err, asynq.SkipRetry), "asynq's own processor checks errors.Is(err, SkipRetry) to decide not to retry")
}

func TestPermanent_SecurityCategoryPreserved(t *testing.T) {
	err := Permanent(FailureCategorySecurity, errors.New("no longer authorized"))
	assert.Equal(t, FailureCategorySecurity, CategoryOf(err))
	assert.True(t, errors.Is(err, asynq.SkipRetry))
}

func TestPermanent_UnwrapsToOriginalError(t *testing.T) {
	original := errors.New("root cause")
	err := Permanent(FailureCategoryPermanent, original)
	assert.True(t, errors.Is(err, original), "the original error must still be reachable via errors.Is/As for callers that care")
}
