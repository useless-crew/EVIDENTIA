-- Evidentia — Secure Evidence Export (Down)

DROP POLICY IF EXISTS evidence_exports_select ON evidence_exports;
DROP POLICY IF EXISTS evidence_exports_insert ON evidence_exports;
DROP POLICY IF EXISTS evidence_exports_update ON evidence_exports;

DROP TABLE IF EXISTS evidence_exports;
