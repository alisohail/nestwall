-- RBAC overhaul, part 1: remove the Wall and Caregiver roles and the wall codes,
-- and keep parent-only actions off every role except Parent.
-- Ids are the seeded ones from 0002_permissions.up.sql.

-- Members on a removed role move to Child, the least-privileged seeded role.
UPDATE family_members
   SET role_id = 'c3d4e5f6-a7b8-9012-cdef-123456789012'
 WHERE role_id IN ('b2c3d4e5-f6a7-8901-bcde-f12345678901', 'd4e5f6a7-b8c9-0123-defa-234567890123');

DELETE FROM role_permissions
 WHERE role_id IN ('b2c3d4e5-f6a7-8901-bcde-f12345678901', 'd4e5f6a7-b8c9-0123-defa-234567890123');
DELETE FROM role_default_permissions
 WHERE role_id IN ('b2c3d4e5-f6a7-8901-bcde-f12345678901', 'd4e5f6a7-b8c9-0123-defa-234567890123');
DELETE FROM roles
 WHERE id IN ('b2c3d4e5-f6a7-8901-bcde-f12345678901', 'd4e5f6a7-b8c9-0123-defa-234567890123');

-- Wall pairing is now a parent-only action, so its codes go.
DELETE FROM member_permission_overrides
 WHERE permission_id IN (SELECT id FROM permissions WHERE code IN ('wall.pair', 'wall.view'));
DELETE FROM role_permissions
 WHERE permission_id IN (SELECT id FROM permissions WHERE code IN ('wall.pair', 'wall.view'));
DELETE FROM role_default_permissions
 WHERE permission_id IN (SELECT id FROM permissions WHERE code IN ('wall.pair', 'wall.view'));
DELETE FROM permissions WHERE code IN ('wall.pair', 'wall.view');

-- Parent-only codes stay on the Parent role. Remove any other grant of them.
DELETE FROM role_permissions
 WHERE role_id <> 'a1b2c3d4-e5f6-7890-abcd-ef1234567890'
   AND permission_id IN (SELECT id FROM permissions WHERE code IN (
       'family.manage', 'members.manage', 'roles.manage', 'settings.manage',
       'backup.manage', 'backup.restore', 'data.reset', 'audit.view',
       'tasks.approve', 'rewards.approve', 'bonus.manage'));
DELETE FROM role_default_permissions
 WHERE role_id <> 'a1b2c3d4-e5f6-7890-abcd-ef1234567890'
   AND permission_id IN (SELECT id FROM permissions WHERE code IN (
       'family.manage', 'members.manage', 'roles.manage', 'settings.manage',
       'backup.manage', 'backup.restore', 'data.reset', 'audit.view',
       'tasks.approve', 'rewards.approve', 'bonus.manage'));
DELETE FROM member_permission_overrides
 WHERE family_member_id IN (
       SELECT fm.id FROM family_members fm WHERE fm.role_id <> 'a1b2c3d4-e5f6-7890-abcd-ef1234567890')
   AND permission_id IN (SELECT id FROM permissions WHERE code IN (
       'family.manage', 'members.manage', 'roles.manage', 'settings.manage',
       'backup.manage', 'backup.restore', 'data.reset', 'audit.view',
       'tasks.approve', 'rewards.approve', 'bonus.manage'));

-- Sessions are own-only for every account. A parent ends a child's sessions through members.manage.
UPDATE permissions SET description = 'Manage own sessions'
 WHERE code = 'sessions.manage';
