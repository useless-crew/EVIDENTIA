-- Evidentia — Case Involved-Party Queries
--
-- metadata is documented as sensitive on the table itself (see migration).
-- These queries return it as-is; redacting/filtering specific fields for
-- specific roles is application-layer ABAC, not implemented here.

-- name: CreateInvolvedParty :one
INSERT INTO case_involved_parties (case_id, party_type, display_name, metadata, added_by)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, case_id, party_type, display_name, metadata, added_by, created_at;

-- name: GetInvolvedPartyByID :one
SELECT id, case_id, party_type, display_name, metadata, added_by, created_at
FROM case_involved_parties
WHERE id = $1;

-- name: ListInvolvedPartiesByCase :many
SELECT id, case_id, party_type, display_name, metadata, added_by, created_at
FROM case_involved_parties
WHERE case_id = $1
ORDER BY created_at;
