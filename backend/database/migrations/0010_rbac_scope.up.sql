-- RBAC overhaul, part 2: own scope moves onto the base codes, and custom roles get a type.
-- Ids are the seeded ones from 0002_permissions.up.sql.

-- A role is a parent, a co-parent or a child. The fixed Parent role is 'parent'.
-- Custom roles default to 'child' until a parent creates a co-parent role.
ALTER TABLE roles ADD COLUMN role_type TEXT NOT NULL DEFAULT 'child'
    CHECK (role_type IN ('parent', 'co_parent', 'child'));
UPDATE roles SET role_type = 'parent' WHERE is_fixed = 1;

-- Own-scope grants become the base code at 'own' level. An existing base grant wins.
INSERT OR IGNORE INTO role_permissions (role_id, permission_id, access_level)
SELECT rp.role_id, base.id, 'own'
  FROM role_permissions rp
  JOIN permissions own ON own.id = rp.permission_id
  JOIN permissions base ON base.code = REPLACE(own.code, '.own.', '.')
 WHERE own.code IN ('tasks.own.view', 'calendar.own.view', 'rewards.own.view');

INSERT OR IGNORE INTO role_default_permissions (role_id, permission_id, access_level)
SELECT rd.role_id, base.id, 'own'
  FROM role_default_permissions rd
  JOIN permissions own ON own.id = rd.permission_id
  JOIN permissions base ON base.code = REPLACE(own.code, '.own.', '.')
 WHERE own.code IN ('tasks.own.view', 'calendar.own.view', 'rewards.own.view');

INSERT OR IGNORE INTO member_permission_overrides (id, family_member_id, permission_id, access_level)
SELECT lower(hex(randomblob(16))), mo.family_member_id, base.id, 'own'
  FROM member_permission_overrides mo
  JOIN permissions own ON own.id = mo.permission_id
  JOIN permissions base ON base.code = REPLACE(own.code, '.own.', '.')
 WHERE own.code IN ('tasks.own.view', 'calendar.own.view', 'rewards.own.view');

-- Task and calendar management is not a child capability, so the own.manage codes are dropped
-- rather than moved. Their handlers were never wired to a route.
DELETE FROM member_permission_overrides
 WHERE permission_id IN (SELECT id FROM permissions WHERE code LIKE '%.own.%');
DELETE FROM role_permissions
 WHERE permission_id IN (SELECT id FROM permissions WHERE code LIKE '%.own.%');
DELETE FROM role_default_permissions
 WHERE permission_id IN (SELECT id FROM permissions WHERE code LIKE '%.own.%');
DELETE FROM permissions WHERE code LIKE '%.own.%';
