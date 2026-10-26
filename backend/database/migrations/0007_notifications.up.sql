-- Notifications and email: family settings, notifications, outbox and SMTP settings.
-- Part of the initial schema. Feature files are applied in filename order.

CREATE TABLE email_outbox (
    id              TEXT PRIMARY KEY,
    family_id       TEXT NOT NULL REFERENCES families(id) ON DELETE CASCADE,
    to_address      TEXT NOT NULL,
    subject         TEXT NOT NULL,
    body            TEXT NOT NULL,
    is_html         INTEGER NOT NULL DEFAULT 0 CHECK (is_html IN (0, 1)),
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'failed')),
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TEXT NOT NULL,
    last_error      TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL,
    sent_at         TEXT
);

-- One row per family. The switches apply to every member. A missing row means the defaults below.
-- email_template is the HTML body for emails; an empty value means the built-in template.
CREATE TABLE notification_settings (
    family_id       TEXT PRIMARY KEY REFERENCES families (id) ON DELETE CASCADE,
    in_app          INTEGER NOT NULL DEFAULT 1 CHECK (in_app IN (0, 1)),
    browser         INTEGER NOT NULL DEFAULT 1 CHECK (browser IN (0, 1)),
    email           INTEGER NOT NULL DEFAULT 0 CHECK (email IN (0, 1)),
    email_digest    INTEGER NOT NULL DEFAULT 0 CHECK (email_digest IN (0, 1)),
    digest_minutes  INTEGER NOT NULL DEFAULT 15 CHECK (digest_minutes BETWEEN 1 AND 1440),
    -- Unix seconds of the last digest sent for the family. NULL until the first digest.
    digest_last_sent_at INTEGER,
    email_template  TEXT NOT NULL DEFAULT '',
    updated_at      DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now'))
);

CREATE TABLE notifications (
    id                TEXT PRIMARY KEY,
    family_id         TEXT NOT NULL REFERENCES families(id),
    recipient_id      TEXT NOT NULL REFERENCES family_members(id) ON DELETE CASCADE,
    kind              TEXT NOT NULL,
    subject_member_id TEXT REFERENCES family_members(id) ON DELETE CASCADE,
    body              TEXT NOT NULL,
    read_at           DATETIME,
    email_pending     INTEGER NOT NULL DEFAULT 0 CHECK (email_pending IN (0, 1)),
    created_at        DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now'))
);

CREATE TABLE smtp_settings (
    family_id           TEXT PRIMARY KEY REFERENCES families(id) ON DELETE CASCADE,
    enabled             INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    host                TEXT NOT NULL DEFAULT '',
    port                INTEGER NOT NULL DEFAULT 587 CHECK (port BETWEEN 1 AND 65535),
    security            TEXT NOT NULL DEFAULT 'starttls' CHECK (security IN ('starttls', 'tls')),
    username            TEXT NOT NULL DEFAULT '',
    password_sealed     TEXT NOT NULL DEFAULT '',
    from_address        TEXT NOT NULL DEFAULT '',
    from_name           TEXT NOT NULL DEFAULT 'NestWall',
    send_limit_per_hour INTEGER NOT NULL DEFAULT 30 CHECK (send_limit_per_hour BETWEEN 1 AND 1000),
    updated_at          TEXT NOT NULL,
    updated_by          TEXT
);

CREATE INDEX email_outbox_due ON email_outbox (status, next_attempt_at);

CREATE INDEX email_outbox_sent ON email_outbox (family_id, status, sent_at);

CREATE INDEX idx_notifications_recipient ON notifications(recipient_id, read_at);

CREATE INDEX idx_notifications_email_pending ON notifications(family_id, email_pending) WHERE email_pending = 1;
