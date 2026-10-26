-- Roles and permissions: the default matrix and per-member overrides.
-- Part of the initial schema. Feature files are applied in filename order.

CREATE TABLE member_permission_overrides (
    id               TEXT PRIMARY KEY,
    family_member_id TEXT NOT NULL REFERENCES family_members(id) ON DELETE CASCADE,
    permission_id    TEXT NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    access_level     TEXT NOT NULL CHECK (access_level IN ('full', 'view', 'own', 'request', 'allowed', 'denied')),
    created_at       DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    updated_at       DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    UNIQUE (family_member_id, permission_id)
);

CREATE TABLE permissions (
    id          TEXT PRIMARY KEY,
    code        TEXT NOT NULL UNIQUE,
    description TEXT,
    created_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now'))
);

CREATE TABLE role_default_permissions (
    role_id       TEXT NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission_id TEXT NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    access_level  TEXT NOT NULL CHECK (access_level IN ('full', 'view', 'own', 'request', 'allowed', 'denied')),
    PRIMARY KEY (role_id, permission_id)
);

CREATE TABLE role_permissions (
    role_id       TEXT NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission_id TEXT NOT NULL REFERENCES permissions(id) ON DELETE CASCADE,
    access_level  TEXT NOT NULL CHECK (access_level IN ('full', 'view', 'own', 'request', 'allowed', 'denied')),
    PRIMARY KEY (role_id, permission_id)
);

CREATE TABLE roles (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    description TEXT,
    is_fixed    INTEGER NOT NULL DEFAULT 0 CHECK (is_fixed IN (0, 1)),
    created_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    updated_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now'))
, version INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1));

CREATE INDEX idx_member_overrides_permission_id ON member_permission_overrides(permission_id);

CREATE INDEX idx_role_permissions_permission_id ON role_permissions(permission_id);

INSERT INTO "roles" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','Parent','Family administrator with full access. Cannot be demoted or have permissions reduced.',1,'2026-10-23 13:20:31.517+00:00','2026-10-23 13:20:31.517+00:00',1);
INSERT INTO "roles" VALUES('b2c3d4e5-f6a7-8901-bcde-f12345678901','Caregiver','Optional role for grandparents, nannies, etc. Starts with limited default permissions.',0,'2026-10-23 13:20:31.517+00:00','2026-10-23 13:20:31.517+00:00',1);
INSERT INTO "roles" VALUES('c3d4e5f6-a7b8-9012-cdef-123456789012','Child','Child account with access to own tasks, points, and shared calendars.',0,'2026-10-23 13:20:31.517+00:00','2026-10-23 13:20:31.517+00:00',1);
INSERT INTO "roles" VALUES('d4e5f6a7-b8c9-0123-defa-234567890123','Wall','Wall display device with read-only access to dashboard and tasks.',0,'2026-10-23 13:20:31.517+00:00','2026-10-23 13:20:31.517+00:00',1);

INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000001','family.manage','Manage family profile, settings, and members','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000002','family.view','View family profile and settings','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000003','members.manage','Add, remove, and edit family members','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000004','members.view','View family member list and profiles','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000006','roles.manage','Edit roles and permission matrix','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000007','roles.view','View roles and permissions','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000008','tasks.manage','Create, edit, and delete any task','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000009','tasks.view','View all tasks','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000010','tasks.own.manage','Create and edit own tasks','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000011','tasks.own.view','View own tasks','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000012','tasks.approve','Approve task completions','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000013','tasks.complete','Mark tasks as complete','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000014','calendar.manage','Manage all calendar events','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000015','calendar.view','View all calendar events','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000016','calendar.own.manage','Manage own calendar events','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000017','calendar.own.view','View own calendar events','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000018','rewards.manage','Manage reward catalogue and policies','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000019','rewards.view','View rewards','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000020','rewards.redeem','Redeem rewards','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000021','rewards.own.view','View own points and rewards','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000022','audit.view','View audit log','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000023','settings.manage','Change all settings including email/SMTP','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000024','settings.view','View settings','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000025','settings.theme','Change personal theme','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000026','profile.avatar.change','Change profile picture','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000027','profile.manage','Edit own profile','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000028','sessions.manage','Manage sessions (own or child''s)','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000029','sessions.view','View sessions','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000030','backup.manage','Run and download backups','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000031','backup.restore','Restore from backup','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000032','data.reset','Reset family data','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000033','wall.pair','Pair wall display devices','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000034','wall.view','View wall display content','2026-10-23 13:20:31.527+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000035','rewards.approve','Approve or reject reward redemptions','2026-10-23 13:20:32.347+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000036','points.share','Share points with a sibling','2026-10-23 13:20:32.426+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000037','bonus.manage','Publish and decide bonus tasks','2026-10-23 13:20:32.685+00:00');
INSERT INTO "permissions" VALUES('10000000-0000-4000-8000-000000000038','bonus.grab','Claim, release and complete bonus tasks','2026-10-23 13:20:32.685+00:00');

INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000001','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000002','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000003','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000004','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000006','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000007','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000008','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000009','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000010','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000011','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000012','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000013','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000014','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000015','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000016','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000017','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000018','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000019','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000020','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000021','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000022','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000023','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000024','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000025','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000026','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000027','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000028','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000029','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000030','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000031','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000032','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000033','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000034','full');
INSERT INTO "role_permissions" VALUES('b2c3d4e5-f6a7-8901-bcde-f12345678901','10000000-0000-4000-8000-000000000002','view');
INSERT INTO "role_permissions" VALUES('b2c3d4e5-f6a7-8901-bcde-f12345678901','10000000-0000-4000-8000-000000000004','view');
INSERT INTO "role_permissions" VALUES('b2c3d4e5-f6a7-8901-bcde-f12345678901','10000000-0000-4000-8000-000000000009','view');
INSERT INTO "role_permissions" VALUES('b2c3d4e5-f6a7-8901-bcde-f12345678901','10000000-0000-4000-8000-000000000013','allowed');
INSERT INTO "role_permissions" VALUES('b2c3d4e5-f6a7-8901-bcde-f12345678901','10000000-0000-4000-8000-000000000015','view');
INSERT INTO "role_permissions" VALUES('b2c3d4e5-f6a7-8901-bcde-f12345678901','10000000-0000-4000-8000-000000000019','view');
INSERT INTO "role_permissions" VALUES('b2c3d4e5-f6a7-8901-bcde-f12345678901','10000000-0000-4000-8000-000000000024','view');
INSERT INTO "role_permissions" VALUES('b2c3d4e5-f6a7-8901-bcde-f12345678901','10000000-0000-4000-8000-000000000026','allowed');
INSERT INTO "role_permissions" VALUES('b2c3d4e5-f6a7-8901-bcde-f12345678901','10000000-0000-4000-8000-000000000027','allowed');
INSERT INTO "role_permissions" VALUES('b2c3d4e5-f6a7-8901-bcde-f12345678901','10000000-0000-4000-8000-000000000028','allowed');
INSERT INTO "role_permissions" VALUES('c3d4e5f6-a7b8-9012-cdef-123456789012','10000000-0000-4000-8000-000000000011','own');
INSERT INTO "role_permissions" VALUES('c3d4e5f6-a7b8-9012-cdef-123456789012','10000000-0000-4000-8000-000000000013','allowed');
INSERT INTO "role_permissions" VALUES('c3d4e5f6-a7b8-9012-cdef-123456789012','10000000-0000-4000-8000-000000000017','own');
INSERT INTO "role_permissions" VALUES('c3d4e5f6-a7b8-9012-cdef-123456789012','10000000-0000-4000-8000-000000000021','own');
INSERT INTO "role_permissions" VALUES('c3d4e5f6-a7b8-9012-cdef-123456789012','10000000-0000-4000-8000-000000000020','allowed');
INSERT INTO "role_permissions" VALUES('c3d4e5f6-a7b8-9012-cdef-123456789012','10000000-0000-4000-8000-000000000025','allowed');
INSERT INTO "role_permissions" VALUES('c3d4e5f6-a7b8-9012-cdef-123456789012','10000000-0000-4000-8000-000000000026','allowed');
INSERT INTO "role_permissions" VALUES('c3d4e5f6-a7b8-9012-cdef-123456789012','10000000-0000-4000-8000-000000000027','allowed');
INSERT INTO "role_permissions" VALUES('c3d4e5f6-a7b8-9012-cdef-123456789012','10000000-0000-4000-8000-000000000028','allowed');
INSERT INTO "role_permissions" VALUES('d4e5f6a7-b8c9-0123-defa-234567890123','10000000-0000-4000-8000-000000000034','view');
INSERT INTO "role_permissions" VALUES('d4e5f6a7-b8c9-0123-defa-234567890123','10000000-0000-4000-8000-000000000009','view');
INSERT INTO "role_permissions" VALUES('c3d4e5f6-a7b8-9012-cdef-123456789012','10000000-0000-4000-8000-000000000019','view');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000035','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000036','full');
INSERT INTO "role_permissions" VALUES('c3d4e5f6-a7b8-9012-cdef-123456789012','10000000-0000-4000-8000-000000000036','allowed');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000038','full');
INSERT INTO "role_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000037','full');
INSERT INTO "role_permissions" VALUES('c3d4e5f6-a7b8-9012-cdef-123456789012','10000000-0000-4000-8000-000000000038','allowed');

INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000001','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000002','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000003','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000004','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000006','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000007','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000008','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000009','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000010','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000011','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000012','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000013','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000014','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000015','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000016','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000017','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000018','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000019','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000020','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000021','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000022','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000023','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000024','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000025','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000026','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000027','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000028','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000029','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000030','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000031','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000032','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000033','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000034','full');
INSERT INTO "role_default_permissions" VALUES('b2c3d4e5-f6a7-8901-bcde-f12345678901','10000000-0000-4000-8000-000000000002','view');
INSERT INTO "role_default_permissions" VALUES('b2c3d4e5-f6a7-8901-bcde-f12345678901','10000000-0000-4000-8000-000000000004','view');
INSERT INTO "role_default_permissions" VALUES('b2c3d4e5-f6a7-8901-bcde-f12345678901','10000000-0000-4000-8000-000000000009','view');
INSERT INTO "role_default_permissions" VALUES('b2c3d4e5-f6a7-8901-bcde-f12345678901','10000000-0000-4000-8000-000000000013','allowed');
INSERT INTO "role_default_permissions" VALUES('b2c3d4e5-f6a7-8901-bcde-f12345678901','10000000-0000-4000-8000-000000000015','view');
INSERT INTO "role_default_permissions" VALUES('b2c3d4e5-f6a7-8901-bcde-f12345678901','10000000-0000-4000-8000-000000000019','view');
INSERT INTO "role_default_permissions" VALUES('b2c3d4e5-f6a7-8901-bcde-f12345678901','10000000-0000-4000-8000-000000000024','view');
INSERT INTO "role_default_permissions" VALUES('b2c3d4e5-f6a7-8901-bcde-f12345678901','10000000-0000-4000-8000-000000000026','allowed');
INSERT INTO "role_default_permissions" VALUES('b2c3d4e5-f6a7-8901-bcde-f12345678901','10000000-0000-4000-8000-000000000027','allowed');
INSERT INTO "role_default_permissions" VALUES('b2c3d4e5-f6a7-8901-bcde-f12345678901','10000000-0000-4000-8000-000000000028','allowed');
INSERT INTO "role_default_permissions" VALUES('c3d4e5f6-a7b8-9012-cdef-123456789012','10000000-0000-4000-8000-000000000011','own');
INSERT INTO "role_default_permissions" VALUES('c3d4e5f6-a7b8-9012-cdef-123456789012','10000000-0000-4000-8000-000000000013','allowed');
INSERT INTO "role_default_permissions" VALUES('c3d4e5f6-a7b8-9012-cdef-123456789012','10000000-0000-4000-8000-000000000017','own');
INSERT INTO "role_default_permissions" VALUES('c3d4e5f6-a7b8-9012-cdef-123456789012','10000000-0000-4000-8000-000000000021','own');
INSERT INTO "role_default_permissions" VALUES('c3d4e5f6-a7b8-9012-cdef-123456789012','10000000-0000-4000-8000-000000000020','allowed');
INSERT INTO "role_default_permissions" VALUES('c3d4e5f6-a7b8-9012-cdef-123456789012','10000000-0000-4000-8000-000000000025','allowed');
INSERT INTO "role_default_permissions" VALUES('c3d4e5f6-a7b8-9012-cdef-123456789012','10000000-0000-4000-8000-000000000026','allowed');
INSERT INTO "role_default_permissions" VALUES('c3d4e5f6-a7b8-9012-cdef-123456789012','10000000-0000-4000-8000-000000000027','allowed');
INSERT INTO "role_default_permissions" VALUES('c3d4e5f6-a7b8-9012-cdef-123456789012','10000000-0000-4000-8000-000000000028','allowed');
INSERT INTO "role_default_permissions" VALUES('d4e5f6a7-b8c9-0123-defa-234567890123','10000000-0000-4000-8000-000000000034','view');
INSERT INTO "role_default_permissions" VALUES('d4e5f6a7-b8c9-0123-defa-234567890123','10000000-0000-4000-8000-000000000009','view');
INSERT INTO "role_default_permissions" VALUES('c3d4e5f6-a7b8-9012-cdef-123456789012','10000000-0000-4000-8000-000000000019','view');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000035','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000036','full');
INSERT INTO "role_default_permissions" VALUES('c3d4e5f6-a7b8-9012-cdef-123456789012','10000000-0000-4000-8000-000000000036','allowed');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000038','full');
INSERT INTO "role_default_permissions" VALUES('a1b2c3d4-e5f6-7890-abcd-ef1234567890','10000000-0000-4000-8000-000000000037','full');
INSERT INTO "role_default_permissions" VALUES('c3d4e5f6-a7b8-9012-cdef-123456789012','10000000-0000-4000-8000-000000000038','allowed');
