-- Evidentia — Hyperledger Fabric Blockchain Anchors (Down)
--
-- Reverses 000006_blockchain_anchors.up.sql.
-- This migration drops the blockchain_anchors table and its associated
-- policies, grants, and indexes. Safe to run in a development environment
-- to reset the blockchain layer; irreversible in production without a
-- corresponding data export.

-- Revoke and drop policies before dropping the table.
DROP POLICY IF EXISTS blockchain_anchors_select ON blockchain_anchors;
DROP POLICY IF EXISTS blockchain_anchors_insert ON blockchain_anchors;
DROP POLICY IF EXISTS blockchain_anchors_update ON blockchain_anchors;

-- Indexes are dropped automatically with the table; listed here for
-- clarity only.
-- idx_blockchain_anchors_document_id
-- idx_blockchain_anchors_status
-- idx_blockchain_anchors_created_at
-- idx_blockchain_anchors_fabric_tx_id
-- idx_blockchain_anchors_doc_status_created

DROP TABLE IF EXISTS blockchain_anchors;
