package jobs

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestDeterministicTaskID_StableForSameInput(t *testing.T) {
	id := uuid.New()
	assert.Equal(t, DeterministicTaskID("some:task", id), DeterministicTaskID("some:task", id),
		"the same task type and entity id must always derive the same job id")
}

func TestDeterministicTaskID_DiffersByEntityID(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	assert.NotEqual(t, DeterministicTaskID("some:task", a), DeterministicTaskID("some:task", b))
}

func TestDeterministicTaskID_DiffersByTaskType(t *testing.T) {
	id := uuid.New()
	assert.NotEqual(t, DeterministicTaskID("task:a", id), DeterministicTaskID("task:b", id),
		"two different task types must never collide on the same entity id's job id")
}

func TestAuditVerifyChainJobID_MatchesTypeVerifyAuditChain(t *testing.T) {
	id := uuid.New()
	assert.Equal(t, DeterministicTaskID(TypeVerifyAuditChain, id), AuditVerifyChainJobID(id))
}
