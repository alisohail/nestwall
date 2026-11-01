-- Allow a monthly reward limit. SQLite cannot change a CHECK constraint in place, so the table is
-- rebuilt. Redemptions reference rewards, so the rows are copied aside and re-inserted after the
-- rename. Re-inserting the parents clears the deferred foreign-key violations that the drop creates.
PRAGMA defer_foreign_keys = ON;

CREATE TEMP TABLE rewards_copy AS SELECT * FROM rewards;
CREATE TEMP TABLE reward_eligibility_copy AS SELECT * FROM reward_eligibility;

CREATE TABLE rewards_new (
    id                TEXT PRIMARY KEY,
    family_id         TEXT NOT NULL REFERENCES families (id),
    name              TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 80),
    description       TEXT CHECK (description IS NULL OR length(description) <= 500),
    icon              TEXT NOT NULL DEFAULT '🎁' CHECK (length(trim(icon)) BETWEEN 1 AND 16),
    cost              INTEGER NOT NULL CHECK (cost BETWEEN 1 AND 100000),
    status            TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    limit_period      TEXT CHECK (limit_period IS NULL OR limit_period IN ('day', 'week', 'month', 'total')),
    limit_count       INTEGER CHECK (limit_count IS NULL OR limit_count BETWEEN 1 AND 1000),
    approval_required INTEGER NOT NULL DEFAULT 0 CHECK (approval_required IN (0, 1)),
    available_from    TEXT CHECK (available_from IS NULL OR available_from GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    available_until   TEXT CHECK (available_until IS NULL OR available_until GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    -- Bit 0 is Monday. NULL means every day. The Go code checks the bits.
    available_days    INTEGER CHECK (available_days IS NULL OR available_days BETWEEN 1 AND 127),
    version           INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    archived_at       DATETIME,
    created_at        DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    updated_at        DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    UNIQUE (family_id, id),
    CHECK ((limit_period IS NULL) = (limit_count IS NULL)),
    CHECK (available_from IS NULL OR available_until IS NULL OR available_from <= available_until)
);

DROP TABLE rewards;
ALTER TABLE rewards_new RENAME TO rewards;
CREATE INDEX idx_rewards_family ON rewards (family_id, archived_at);

INSERT INTO rewards (id, family_id, name, description, icon, cost, status, limit_period, limit_count,
    approval_required, available_from, available_until, available_days, version, archived_at,
    created_at, updated_at)
SELECT id, family_id, name, description, icon, cost, status, limit_period, limit_count,
    approval_required, available_from, available_until, available_days, version, archived_at,
    created_at, updated_at
FROM rewards_copy;

INSERT INTO reward_eligibility (family_id, reward_id, family_member_id)
SELECT family_id, reward_id, family_member_id FROM reward_eligibility_copy;

DROP TABLE reward_eligibility_copy;
DROP TABLE rewards_copy;
