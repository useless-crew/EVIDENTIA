-- Evidentia — Initial Schema (Down)
--
-- Reverses 000001_init_schema.up.sql completely: roles/grants, RLS
-- policies (dropped implicitly with their tables), helper functions, every
-- table (in reverse dependency order), and the citext extension.
--
-- This is safe to run repeatedly against development/test databases. It is
-- NOT an operationally casual action against a database holding real
-- evidence — running it destroys the entire schema and every row in it.
-- Treat a production rollback of this migration as a deliberate,
-- individually-approved operation, not a routine `migrate down`.

-- ---- Database role ----
-- DROP OWNED revokes every privilege evidentia_app holds IN THIS DATABASE
-- (it owns no objects, only the GRANTs from the up migration) and is
-- always safe. DROP ROLE, however, is cluster-wide: evidentia_app can
-- legitimately be shared across multiple databases on the same Postgres
-- instance (e.g. separate dev/test databases), and Postgres correctly
-- refuses to drop a role that still holds privileges in ANY of them
-- (SQLSTATE 2BP01, dependent_objects_still_exist) — confirmed empirically
-- via this project's own migration reproducibility test, which runs this
-- migration against an isolated database while the role also exists in
-- the main one. Catching that specific error and continuing (rather than
-- failing the whole rollback) makes this migration safe to run against
-- any one database in a multi-database cluster.
DROP OWNED BY evidentia_app;
DO $$
BEGIN
    DROP ROLE IF EXISTS evidentia_app;
EXCEPTION
    WHEN dependent_objects_still_exist THEN
        RAISE NOTICE 'evidentia_app still has privileges in another database on this cluster — role left in place';
END
$$;

-- ---- Tables (reverse dependency order; RLS policies drop with their own
--      table automatically, but several policies on OTHER tables — e.g.
--      cases_select references case_members — also depend on tables
--      dropped earlier in this list. CASCADE removes those dependent
--      policies too, rather than failing with "other objects depend on
--      it"; it does not drop any table beyond the one named.) ----
DROP TABLE IF EXISTS compliance_certificates CASCADE;
DROP TABLE IF EXISTS audit_log CASCADE;
DROP TABLE IF EXISTS redactions CASCADE;
DROP TABLE IF EXISTS documents CASCADE;
DROP TABLE IF EXISTS case_involved_parties CASCADE;
DROP TABLE IF EXISTS case_members CASCADE;
DROP TABLE IF EXISTS cases CASCADE;
DROP TABLE IF EXISTS role_permissions CASCADE;
DROP TABLE IF EXISTS user_roles CASCADE;
DROP TABLE IF EXISTS users CASCADE;
DROP TABLE IF EXISTS permissions CASCADE;
DROP TABLE IF EXISTS roles CASCADE;

-- ---- RLS helper functions ----
DROP FUNCTION IF EXISTS current_app_role();
DROP FUNCTION IF EXISTS current_app_user_id();

-- ---- Extensions ----
-- Safe here because this schema is citext's only consumer. A database
-- shared with unrelated schemas would need to check for other dependents
-- before dropping it.
DROP EXTENSION IF EXISTS citext;
