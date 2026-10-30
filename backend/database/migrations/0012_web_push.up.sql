-- Web Push: the server's VAPID key pair (one row, created on first start), each member's browser
-- or phone subscriptions, and a flag on notifications that marks them for a push.

CREATE TABLE push_config (
    id          INTEGER PRIMARY KEY CHECK (id = 1),
    public_key  TEXT NOT NULL,
    private_key TEXT NOT NULL,
    created_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now'))
);

CREATE TABLE push_subscriptions (
    id          TEXT PRIMARY KEY,
    family_id   TEXT NOT NULL REFERENCES families(id) ON DELETE CASCADE,
    member_id   TEXT NOT NULL REFERENCES family_members(id) ON DELETE CASCADE,
    endpoint    TEXT NOT NULL UNIQUE,
    p256dh      TEXT NOT NULL,
    auth        TEXT NOT NULL,
    created_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now'))
);

CREATE INDEX idx_push_subscriptions_member ON push_subscriptions(member_id);

ALTER TABLE notifications ADD COLUMN push_pending INTEGER NOT NULL DEFAULT 0 CHECK (push_pending IN (0, 1));

CREATE INDEX idx_notifications_push_pending ON notifications(push_pending) WHERE push_pending = 1;
