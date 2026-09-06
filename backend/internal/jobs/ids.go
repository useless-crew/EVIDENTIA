package jobs

import "github.com/google/uuid"

// DeterministicTaskID derives a stable Asynq task ID from a task type and
// the trusted entity ID a task's own payload already carries (e.g.
// AuditVerifyChainJobID's verificationID) — never a randomly generated
// one. Two benefits fall out of this, both load-bearing:
//
//  1. Traceability: master prompt's "every background job must have a
//     traceable identifier" — the API can return this exact string as
//     job_id (see AuditVerifyChainJobID's own callers in
//     internal/service.AuditService) without persisting a separate
//     column anywhere, since it is always re-derivable from the entity ID
//     alone.
//  2. Defense-in-depth idempotency: asynq.TaskID(id) (see
//     NewVerifyAuditChainTask) makes Asynq itself reject a second enqueue
//     attempt carrying the SAME id (asynq.ErrTaskIDConflict) rather than
//     creating a duplicate task — a second, independent layer underneath
//     whatever database-level uniqueness constraint (e.g.
//     idx_audit_verifications_single_active) a task type's own service
//     layer already enforces, never a replacement for it.
//
// taskType/entityID are both trusted, server-generated values (a task
// type constant, a database-generated UUID) — this function itself
// accepts no client-controllable input, matching every other job-security
// rule in this package (see VerifyAuditChainPayload's own doc comment).
func DeterministicTaskID(taskType string, entityID uuid.UUID) string {
	return taskType + ":" + entityID.String()
}
