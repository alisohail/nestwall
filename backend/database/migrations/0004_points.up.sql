-- Points: ledger, shares, rewards, redemptions, bonus tasks and badges.
-- Part of the initial schema. Feature files are applied in filename order.

CREATE TABLE badge_definitions (
    id           TEXT PRIMARY KEY,
    family_id    TEXT NOT NULL REFERENCES families (id),
    badge_key    TEXT CHECK (badge_key IS NULL OR badge_key IN (
                     's3', 's7', 's14', 's30', 's100',
                     'p100', 'p250', 'p500', 'p1000', 'p_reward',
                     't1', 't10', 't50', 't100',
                     'k_tidy', 'k_hw', 'k_pet', 'k_green',
                     'e_early', 'e_week', 'e_month', 'e_back',
                     'k_share', 'k_help')),
    group_key    TEXT CHECK (group_key IS NULL OR group_key IN ('streaks', 'points', 'tasks', 'skills', 'timing', 'kindness')),
    name         TEXT CHECK (name IS NULL OR length(trim(name)) BETWEEN 1 AND 60),
    description  TEXT CHECK (description IS NULL OR length(trim(description)) BETWEEN 1 AND 200),
    emoji        TEXT CHECK (emoji IS NULL OR length(trim(emoji)) > 0),
    enabled      INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    version      INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at   DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    updated_at   DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    UNIQUE (family_id, id),
    UNIQUE (family_id, badge_key),
    CONSTRAINT badge_definitions_kind CHECK (
        (badge_key IS NULL AND group_key IS NULL AND name IS NOT NULL AND emoji IS NOT NULL)
        OR (badge_key IS NOT NULL AND group_key IS NOT NULL AND name IS NULL AND description IS NULL AND emoji IS NULL)
    )
);

CREATE TABLE badge_settings (
    family_id             TEXT PRIMARY KEY REFERENCES families (id),
    enabled               INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    show_on_kid_dashboard INTEGER NOT NULL DEFAULT 1 CHECK (show_on_kid_dashboard IN (0, 1)),
    show_on_wall          INTEGER NOT NULL DEFAULT 0 CHECK (show_on_wall IN (0, 1)),
    notify_child          INTEGER NOT NULL DEFAULT 1 CHECK (notify_child IN (0, 1)),
    version               INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    updated_at            DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now'))
);

CREATE TABLE bonus_claims (
    id                   TEXT PRIMARY KEY,
    family_id            TEXT NOT NULL REFERENCES families (id),
    bonus_task_id        TEXT NOT NULL,
    family_member_id     TEXT NOT NULL,
    claim_mode           TEXT NOT NULL CHECK (claim_mode IN ('first_come', 'everyone_once')),
    status               TEXT NOT NULL DEFAULT 'claimed'
                         CHECK (status IN ('claimed', 'submitted', 'approved', 'rejected', 'released')),
    claimed_at           DATETIME NOT NULL,
    released_at          DATETIME,
    completed_at         DATETIME,
    decided_at           DATETIME,
    decided_by_member_id TEXT,
    decision_note        TEXT CHECK (decision_note IS NULL OR length(decision_note) <= 200),
    version              INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at           DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    updated_at           DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    UNIQUE (family_id, id),
    -- Timestamps match the status: claimed has none, released has released_at, and so on.
    CHECK (status <> 'claimed' OR (completed_at IS NULL AND decided_at IS NULL AND released_at IS NULL)),
    CHECK (status <> 'released' OR (released_at IS NOT NULL AND completed_at IS NULL AND decided_at IS NULL)),
    CHECK (status <> 'submitted' OR (completed_at IS NOT NULL AND decided_at IS NULL AND released_at IS NULL)),
    CHECK (status NOT IN ('approved', 'rejected') OR (completed_at IS NOT NULL AND decided_at IS NOT NULL AND released_at IS NULL)),
    FOREIGN KEY (family_id, bonus_task_id) REFERENCES bonus_tasks (family_id, id),
    FOREIGN KEY (family_id, family_member_id) REFERENCES family_members (family_id, id),
    FOREIGN KEY (family_id, decided_by_member_id) REFERENCES family_members (family_id, id)
);

CREATE TABLE bonus_task_members (
    id                TEXT PRIMARY KEY,
    family_id         TEXT NOT NULL REFERENCES families (id),
    bonus_task_id     TEXT NOT NULL,
    family_member_id  TEXT NOT NULL,
    created_at        DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    UNIQUE (family_id, id),
    UNIQUE (bonus_task_id, family_member_id),
    FOREIGN KEY (family_id, bonus_task_id) REFERENCES bonus_tasks (family_id, id) ON DELETE CASCADE,
    FOREIGN KEY (family_id, family_member_id) REFERENCES family_members (family_id, id)
);

CREATE TABLE bonus_tasks (
    id                TEXT PRIMARY KEY,
    family_id         TEXT NOT NULL REFERENCES families (id),
    template_id       TEXT,
    title             TEXT NOT NULL CHECK (length(trim(title)) BETWEEN 1 AND 200),
    description       TEXT CHECK (description IS NULL OR length(description) <= 2000),
    points            INTEGER NOT NULL CHECK (points BETWEEN 1 AND 100000),
    claim_mode        TEXT NOT NULL CHECK (claim_mode IN ('first_come', 'everyone_once')),
    approval_required INTEGER NOT NULL DEFAULT 1 CHECK (approval_required IN (0, 1)),
    show_from         DATETIME,
    ends_at           DATETIME NOT NULL,
    created_by        TEXT NOT NULL REFERENCES users (id),
    created_at        DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    updated_at        DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')), version INTEGER NOT NULL DEFAULT 1, cancelled_at DATETIME,
    UNIQUE (family_id, id),
    CHECK (show_from IS NULL OR show_from < ends_at),
    FOREIGN KEY (family_id, template_id) REFERENCES task_templates (family_id, id)
);

CREATE TABLE ledger_purge_scope (
    family_id TEXT PRIMARY KEY REFERENCES families(id)
);

CREATE TABLE member_badges (
    id                    TEXT PRIMARY KEY,
    family_id             TEXT NOT NULL REFERENCES families (id),
    family_member_id      TEXT NOT NULL,
    badge_definition_id   TEXT NOT NULL,
    source                TEXT NOT NULL CHECK (source IN ('auto', 'manual')),
    note                  TEXT CHECK (note IS NULL OR length(trim(note)) BETWEEN 1 AND 200),
    awarded_by_member_id  TEXT,
    idempotency_key       TEXT NOT NULL CHECK (length(trim(idempotency_key)) BETWEEN 1 AND 120),
    awarded_at            DATETIME NOT NULL,
    created_at            DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    UNIQUE (family_id, id),
    UNIQUE (family_id, idempotency_key),
    CONSTRAINT member_badges_source CHECK (
        (source = 'auto' AND note IS NULL AND awarded_by_member_id IS NULL)
        OR (source = 'manual' AND note IS NOT NULL AND awarded_by_member_id IS NOT NULL)
    ),
    FOREIGN KEY (family_id, family_member_id) REFERENCES family_members (family_id, id),
    FOREIGN KEY (family_id, badge_definition_id) REFERENCES badge_definitions (family_id, id),
    FOREIGN KEY (family_id, awarded_by_member_id) REFERENCES family_members (family_id, id)
);

CREATE TABLE point_shares (
    id                   TEXT PRIMARY KEY,
    family_id            TEXT NOT NULL REFERENCES families (id),
    from_member_id       TEXT NOT NULL,
    to_member_id         TEXT NOT NULL,
    amount               INTEGER NOT NULL CHECK (amount BETWEEN 1 AND 100000),
    note_key             TEXT CHECK (note_key IS NULL OR note_key IN ('thank_you', 'happy_birthday', 'good_job', 'just_because')),
    status               TEXT NOT NULL CHECK (status IN ('pending', 'completed', 'declined')),
    approval_required    INTEGER NOT NULL CHECK (approval_required IN (0, 1)),
    transfer_id          TEXT NOT NULL UNIQUE,
    idempotency_key      TEXT NOT NULL CHECK (length(trim(idempotency_key)) BETWEEN 1 AND 100),
    requested_at         DATETIME NOT NULL,
    decided_at           DATETIME,
    decided_by_member_id TEXT,
    version              INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at           DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    updated_at           DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')), note_text TEXT,
    UNIQUE (family_id, id),
    -- A replayed share with the same key returns the stored row (see internal/rewards).
    UNIQUE (family_id, from_member_id, idempotency_key),
    -- A child cannot share with themselves.
    CHECK (from_member_id <> to_member_id),
    -- A share is undecided only while pending. A share that needs no approval completes at once.
    CHECK ((status = 'pending') = (decided_at IS NULL)),
    CHECK (approval_required = 1 OR status = 'completed'),
    FOREIGN KEY (family_id, from_member_id) REFERENCES family_members (family_id, id),
    FOREIGN KEY (family_id, to_member_id) REFERENCES family_members (family_id, id),
    FOREIGN KEY (family_id, decided_by_member_id) REFERENCES family_members (family_id, id)
);

CREATE TABLE points_ledger (
    id                  TEXT PRIMARY KEY,
    family_id           TEXT NOT NULL REFERENCES families(id),
    family_member_id    TEXT NOT NULL,
    entry_type          TEXT NOT NULL CHECK (entry_type IN (
                            'task_award', 'bonus_task_award', 'bonus', 'penalty', 'redemption',
                            'redemption_reversal', 'reversal', 'adjustment', 'transfer_out', 'transfer_in')),
    amount              INTEGER NOT NULL CHECK (amount <> 0),
    reason              TEXT CHECK (reason IS NULL OR length(trim(reason)) > 0),
    reference_type      TEXT CHECK (reference_type IS NULL OR length(trim(reference_type)) > 0),
    reference_id        TEXT,
    actor_member_id     TEXT,
    transfer_id         TEXT,
    idempotency_key     TEXT NOT NULL CHECK (length(trim(idempotency_key)) > 0),
    created_at          DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    UNIQUE (family_id, id),
    UNIQUE (family_id, idempotency_key),
    CONSTRAINT points_ledger_sign CHECK (
        (entry_type IN ('task_award', 'bonus_task_award', 'bonus', 'redemption_reversal', 'transfer_in') AND amount > 0)
        OR (entry_type IN ('penalty', 'redemption', 'transfer_out') AND amount < 0)
        OR (entry_type IN ('reversal', 'adjustment'))
    ),
    CONSTRAINT points_ledger_reference_pair CHECK ((reference_type IS NULL) = (reference_id IS NULL)),
    CONSTRAINT points_ledger_transfer CHECK (
        (entry_type IN ('transfer_out', 'transfer_in') AND transfer_id IS NOT NULL)
        OR (entry_type NOT IN ('transfer_out', 'transfer_in') AND transfer_id IS NULL)
    ),
    FOREIGN KEY (family_id, family_member_id) REFERENCES family_members (family_id, id),
    FOREIGN KEY (family_id, actor_member_id) REFERENCES family_members (family_id, id)
);

CREATE TABLE points_share_settings (
    family_id        TEXT PRIMARY KEY REFERENCES families (id),
    enabled          INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
    max_per_share    INTEGER NOT NULL DEFAULT 30 CHECK (max_per_share BETWEEN 1 AND 100000),
    daily_limit      INTEGER NOT NULL DEFAULT 50 CHECK (daily_limit BETWEEN 1 AND 100000),
    min_balance      INTEGER NOT NULL DEFAULT 10 CHECK (min_balance BETWEEN 0 AND 100000),
    approval_mode    TEXT NOT NULL DEFAULT 'never' CHECK (approval_mode IN ('never', 'above', 'always')),
    approval_above   INTEGER NOT NULL DEFAULT 20 CHECK (approval_above BETWEEN 0 AND 100000),
    notify_recipient INTEGER NOT NULL DEFAULT 1 CHECK (notify_recipient IN (0, 1)),
    notify_parents   INTEGER NOT NULL DEFAULT 0 CHECK (notify_parents IN (0, 1)),
    version          INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    updated_at       DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now'))
, min_per_share INTEGER NOT NULL DEFAULT 5
    CHECK (min_per_share BETWEEN 1 AND 100000));

CREATE TABLE redemptions (
    id                   TEXT PRIMARY KEY,
    family_id            TEXT NOT NULL REFERENCES families (id),
    reward_id            TEXT NOT NULL,
    family_member_id     TEXT NOT NULL,
    status               TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected', 'cancelled')),
    cost                 INTEGER NOT NULL CHECK (cost BETWEEN 1 AND 100000),
    idempotency_key      TEXT NOT NULL CHECK (length(trim(idempotency_key)) BETWEEN 1 AND 100),
    requested_at         DATETIME NOT NULL,
    decided_at           DATETIME,
    decided_by_member_id TEXT,
    decision_reason      TEXT CHECK (decision_reason IS NULL OR length(trim(decision_reason)) BETWEEN 1 AND 200),
    version              INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at           DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    updated_at           DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    UNIQUE (family_id, id),
    -- A replayed request with the same key returns the stored row (see internal/rewards).
    UNIQUE (family_id, family_member_id, idempotency_key),
    -- A request is undecided only while pending.
    CHECK ((status = 'pending') = (decided_at IS NULL)),
    -- A rejection must say why.
    CHECK (status <> 'rejected' OR decision_reason IS NOT NULL),
    FOREIGN KEY (family_id, reward_id) REFERENCES rewards (family_id, id),
    FOREIGN KEY (family_id, family_member_id) REFERENCES family_members (family_id, id),
    FOREIGN KEY (family_id, decided_by_member_id) REFERENCES family_members (family_id, id)
);

CREATE TABLE reward_eligibility (
    family_id        TEXT NOT NULL,
    reward_id        TEXT NOT NULL,
    family_member_id TEXT NOT NULL,
    PRIMARY KEY (reward_id, family_member_id),
    FOREIGN KEY (family_id, reward_id) REFERENCES rewards (family_id, id) ON DELETE CASCADE,
    FOREIGN KEY (family_id, family_member_id) REFERENCES family_members (family_id, id) ON DELETE CASCADE
);

CREATE TABLE rewards (
    id                TEXT PRIMARY KEY,
    family_id         TEXT NOT NULL REFERENCES families (id),
    name              TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 80),
    description       TEXT CHECK (description IS NULL OR length(description) <= 500),
    icon              TEXT NOT NULL DEFAULT '🎁' CHECK (length(trim(icon)) BETWEEN 1 AND 16),
    cost              INTEGER NOT NULL CHECK (cost BETWEEN 1 AND 100000),
    status            TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'inactive')),
    limit_period      TEXT CHECK (limit_period IS NULL OR limit_period IN ('day', 'week', 'total')),
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

CREATE INDEX idx_bonus_claims_family_task ON bonus_claims (family_id, bonus_task_id);

CREATE UNIQUE INDEX idx_bonus_claims_first_come_held
    ON bonus_claims (bonus_task_id)
    WHERE claim_mode = 'first_come' AND status NOT IN ('released', 'rejected');

CREATE UNIQUE INDEX idx_bonus_claims_member_live
    ON bonus_claims (bonus_task_id, family_member_id)
    WHERE status NOT IN ('released', 'rejected');

CREATE INDEX idx_bonus_tasks_family_ends ON bonus_tasks (family_id, ends_at);

CREATE UNIQUE INDEX idx_member_badges_auto_once ON member_badges (family_id, family_member_id, badge_definition_id)
    WHERE source = 'auto';

CREATE INDEX idx_member_badges_member ON member_badges (family_id, family_member_id, awarded_at);

CREATE INDEX idx_point_shares_daily ON point_shares (family_id, from_member_id, status, requested_at);

CREATE INDEX idx_point_shares_pending ON point_shares (family_id, status, requested_at);

CREATE INDEX idx_points_ledger_member ON points_ledger(family_id, family_member_id, created_at);

CREATE INDEX idx_redemptions_limit ON redemptions (family_id, family_member_id, reward_id, status, requested_at);

CREATE INDEX idx_rewards_family ON rewards (family_id, archived_at);

CREATE TRIGGER member_badges_kind_matches BEFORE INSERT ON member_badges
WHEN NOT (
    (NEW.source = 'auto' AND EXISTS (
        SELECT 1 FROM badge_definitions d
        WHERE d.family_id = NEW.family_id AND d.id = NEW.badge_definition_id AND d.badge_key IS NOT NULL))
    OR (NEW.source = 'manual' AND EXISTS (
        SELECT 1 FROM badge_definitions d
        WHERE d.family_id = NEW.family_id AND d.id = NEW.badge_definition_id AND d.badge_key IS NULL))
)
BEGIN
    SELECT RAISE(ABORT, 'badge award does not match the badge kind');
END;

CREATE TRIGGER points_ledger_no_delete
BEFORE DELETE ON points_ledger
WHEN NOT EXISTS (SELECT 1 FROM ledger_purge_scope s WHERE s.family_id = OLD.family_id)
BEGIN
    SELECT RAISE(ABORT, 'points ledger is append-only');
END;

CREATE TRIGGER points_ledger_no_update
BEFORE UPDATE ON points_ledger
WHEN NOT EXISTS (SELECT 1 FROM ledger_purge_scope s WHERE s.family_id = OLD.family_id)
BEGIN
    SELECT RAISE(ABORT, 'points ledger is append-only');
END;
