-- Evidentia — Reference Data Seed
--
-- Seeds the fixed role catalog, the permission catalog, and a starting
-- role->permission mapping. Idempotent: every statement uses
-- ON CONFLICT DO NOTHING, so running this file twice (or against a
-- database that already has this data) is a no-op the second time, not an
-- error or a duplicate.
--
-- Deliberately NOT seeded: any user row. Master prompt §46 is explicit —
-- "Do NOT seed real users with real credentials" — and a seeded user would
-- need a real bcrypt password_hash, which requires the hashing System 3
-- owns. If a development admin user is ever needed, it must be created
-- through that system's registration flow (or an explicit, separate,
-- environment-variable-driven script), never hardcoded here.
--
-- Run via: backend/scripts/seed_db.sh (reads DATABASE_* env vars, same
-- convention as the rest of this project).

-- ---- Roles ----

INSERT INTO roles (name, description) VALUES
    ('ADMIN',     'Full administrative access.'),
    ('POLICE',    'Investigating law-enforcement officer.'),
    ('FORENSICS', 'Forensic analyst / evidence examiner.'),
    ('LAWYER',    'Legal counsel attached to specific cases.'),
    ('JUDGE',     'Judicial officer reviewing case submissions.')
ON CONFLICT ON CONSTRAINT roles_name_unique DO NOTHING;

-- ---- Permissions ----

INSERT INTO permissions (name, description, resource, action) VALUES
    ('case:create',        'Create a new case.',                          'case',        'create'),
    ('case:read',          'View a case.',                                 'case',        'read'),
    ('case:update',        'Update case metadata/status.',                 'case',        'update'),

    ('document:upload',    'Upload a document to a case.',                 'document',    'upload'),
    ('document:read',      'View document metadata.',                      'document',    'read'),
    ('document:download',  'Download a document''s content.',              'document',    'download'),
    ('document:verify',    'Verify a document''s integrity hash.',         'document',    'verify'),
    ('document:redact',    'Create a redacted derivative of a document.',  'document',    'redact'),
    ('document:share',     'Share a document with another party.',         'document',    'share'),

    ('audit:read',         'View audit log entries.',                      'audit',       'read'),
    ('audit:verify',       'Trigger/view audit-chain verification.',       'audit',       'verify'),

    ('certificate:read',   'View a compliance certificate.',               'certificate', 'read'),
    ('certificate:create', 'Generate a compliance certificate.',           'certificate', 'create'),

    ('user:read',          'View a user account.',                        'user',        'read'),
    ('user:create',        'Create a user account.',                      'user',        'create'),
    ('user:update',        'Update a user''s profile.',                    'user',        'update'),
    ('user:deactivate',    'Deactivate/suspend a user account.',           'user',        'deactivate'),
    ('user:role',          'Assign or remove a user''s role.',             'user',        'role')
ON CONFLICT ON CONSTRAINT permissions_name_unique DO NOTHING;

-- ---- Role -> Permission mapping ----
--
-- A reasonable starting point, not a final authorization policy: the
-- system that implements RBAC/ABAC enforcement owns refining this. ADMIN
-- gets every permission; other roles get what their real-world
-- responsibilities plausibly require.

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r CROSS JOIN permissions p WHERE r.name = 'ADMIN'
ON CONFLICT ON CONSTRAINT role_permissions_role_permission_unique DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p ON p.name IN (
    'case:create', 'case:read', 'case:update',
    'document:upload', 'document:read', 'document:download', 'document:verify', 'document:share',
    'audit:read'
) WHERE r.name = 'POLICE'
ON CONFLICT ON CONSTRAINT role_permissions_role_permission_unique DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p ON p.name IN (
    'case:read',
    'document:upload', 'document:read', 'document:download', 'document:verify'
) WHERE r.name = 'FORENSICS'
ON CONFLICT ON CONSTRAINT role_permissions_role_permission_unique DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p ON p.name IN (
    'case:read',
    'document:read', 'document:download', 'document:share',
    'audit:read'
) WHERE r.name = 'LAWYER'
ON CONFLICT ON CONSTRAINT role_permissions_role_permission_unique DO NOTHING;

INSERT INTO role_permissions (role_id, permission_id)
SELECT r.id, p.id FROM roles r JOIN permissions p ON p.name IN (
    'case:read',
    'document:read', 'document:download',
    'certificate:read',
    'audit:read'
) WHERE r.name = 'JUDGE'
ON CONFLICT ON CONSTRAINT role_permissions_role_permission_unique DO NOTHING;
