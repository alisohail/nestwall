-- Tasks: categories, templates, recurring definitions, occurrences, checklists and penalty intents.
-- Part of the initial schema. Feature files are applied in filename order.

CREATE TABLE task_categories (
    id                            TEXT PRIMARY KEY,
    family_id                     TEXT NOT NULL REFERENCES families(id),
    seed_key                      TEXT CHECK (seed_key IS NULL OR seed_key IN ('chores', 'school_work', 'pets', 'outdoor', 'self_care', 'activities', 'family')),
    name                          TEXT NOT NULL CHECK (length(trim(name)) > 0),
    emoji                         TEXT NOT NULL CHECK (length(trim(emoji)) > 0),
    colour                        TEXT NOT NULL CHECK (colour GLOB '#[0-9A-Fa-f][0-9A-Fa-f][0-9A-Fa-f][0-9A-Fa-f][0-9A-Fa-f][0-9A-Fa-f]'),
    sort_order                    INTEGER NOT NULL DEFAULT 0 CHECK (sort_order >= 0),
    badge_key                     TEXT CHECK (badge_key IS NULL OR length(trim(badge_key)) > 0),
    default_points                INTEGER CHECK (default_points IS NULL OR default_points >= 0),
    default_approval_required     INTEGER CHECK (default_approval_required IS NULL OR default_approval_required IN (0, 1)),
    default_expires_after_minutes INTEGER CHECK (default_expires_after_minutes IS NULL OR default_expires_after_minutes >= 0),
    default_penalty_value         INTEGER CHECK (default_penalty_value IS NULL OR default_penalty_value >= 0),
    default_penalty_unit          TEXT CHECK (default_penalty_unit IS NULL OR default_penalty_unit IN ('percent', 'points')),
    version                       INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at                    DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    updated_at                    DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    UNIQUE (family_id, id),
    CONSTRAINT task_categories_penalty_pair CHECK (
        (default_penalty_value IS NULL AND default_penalty_unit IS NULL)
        OR (default_penalty_value IS NOT NULL AND default_penalty_unit IS NOT NULL
            AND (default_penalty_unit = 'points' OR default_penalty_value <= 100))
    )
);

CREATE TABLE task_checklist_completions (
    family_id                   TEXT NOT NULL REFERENCES families(id),
    task_occurrence_assignee_id TEXT NOT NULL,
    task_checklist_item_id      TEXT NOT NULL,
    completed_at                DATETIME NOT NULL,
    completed_by_member_id      TEXT,
    PRIMARY KEY (task_occurrence_assignee_id, task_checklist_item_id),
    FOREIGN KEY (family_id, task_occurrence_assignee_id) REFERENCES task_occurrence_assignees (family_id, id) ON DELETE CASCADE,
    FOREIGN KEY (family_id, task_checklist_item_id) REFERENCES task_checklist_items (family_id, id) ON DELETE CASCADE,
    FOREIGN KEY (family_id, completed_by_member_id) REFERENCES family_members (family_id, id)
);

CREATE TABLE "task_checklist_items" (
    id                      TEXT PRIMARY KEY,
    family_id               TEXT NOT NULL REFERENCES families(id),
    task_template_id        TEXT,
    task_definition_id      TEXT,
    task_occurrence_id      TEXT,
    sort_order              INTEGER NOT NULL CHECK (sort_order >= 0),
    item_text               TEXT NOT NULL CHECK (length(trim(item_text)) > 0),
    created_at              DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    updated_at              DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    UNIQUE (family_id, id),
    UNIQUE (task_template_id, sort_order),
    UNIQUE (task_definition_id, sort_order),
    UNIQUE (task_occurrence_id, sort_order),
    CONSTRAINT task_checklist_items_one_owner CHECK (
        (task_template_id IS NOT NULL) + (task_definition_id IS NOT NULL) + (task_occurrence_id IS NOT NULL) = 1
    ),
    FOREIGN KEY (family_id, task_template_id) REFERENCES task_templates (family_id, id) ON DELETE CASCADE,
    FOREIGN KEY (family_id, task_definition_id) REFERENCES task_definitions (family_id, id) ON DELETE CASCADE,
    FOREIGN KEY (family_id, task_occurrence_id) REFERENCES task_occurrences (family_id, id) ON DELETE CASCADE
);

CREATE TABLE task_definition_assignees (
    id                  TEXT PRIMARY KEY,
    family_id           TEXT NOT NULL REFERENCES families(id),
    task_definition_id  TEXT NOT NULL,
    family_member_id    TEXT NOT NULL,
    created_at          DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    UNIQUE (family_id, id),
    UNIQUE (task_definition_id, family_member_id),
    FOREIGN KEY (family_id, task_definition_id) REFERENCES task_definitions (family_id, id) ON DELETE CASCADE,
    FOREIGN KEY (family_id, family_member_id) REFERENCES family_members (family_id, id)
);

CREATE TABLE task_definition_skip_dates (
    family_id           TEXT NOT NULL REFERENCES families(id),
    task_definition_id  TEXT NOT NULL,
    skip_date           TEXT NOT NULL CHECK (skip_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    created_at          DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    PRIMARY KEY (task_definition_id, skip_date),
    FOREIGN KEY (family_id, task_definition_id) REFERENCES task_definitions (family_id, id) ON DELETE CASCADE
);

CREATE TABLE task_definitions (
    id                      TEXT PRIMARY KEY,
    family_id               TEXT NOT NULL REFERENCES families(id),
    template_id             TEXT,
    title                   TEXT NOT NULL CHECK (length(trim(title)) > 0),
    description             TEXT,
    icon                    TEXT,
    time_zone               TEXT NOT NULL CHECK (length(trim(time_zone)) > 0),
    starts_on               TEXT NOT NULL CHECK (starts_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    due_time                TEXT CHECK (due_time IS NULL OR due_time GLOB '[0-2][0-9]:[0-5][0-9]'),
    recurrence_rule         TEXT CHECK (recurrence_rule IS NULL OR length(trim(recurrence_rule)) > 0),
    points                  INTEGER NOT NULL CHECK (points >= 0),
    priority                TEXT NOT NULL DEFAULT 'normal' CHECK (priority IN ('low', 'normal', 'high')),
    approval_required       INTEGER NOT NULL DEFAULT 1 CHECK (approval_required IN (0, 1)),
    penalty_value           INTEGER CHECK (penalty_value IS NULL OR penalty_value >= 0),
    penalty_unit            TEXT CHECK (penalty_unit IS NULL OR penalty_unit IN ('percent', 'points')),
    expires_after_minutes   INTEGER CHECK (expires_after_minutes IS NULL OR expires_after_minutes >= 0),
    late_points_percent     INTEGER CHECK (late_points_percent IS NULL OR late_points_percent BETWEEN 0 AND 100),
    reminder_minutes_before INTEGER CHECK (reminder_minutes_before IS NULL OR reminder_minutes_before >= 0),
    checklist_all_required  INTEGER NOT NULL DEFAULT 0 CHECK (checklist_all_required IN (0, 1)),
    status                  TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
    version                 INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_by              TEXT NOT NULL REFERENCES users(id),
    created_at              DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    updated_at              DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')), category_id TEXT, part_of_day TEXT CHECK (part_of_day IS NULL OR part_of_day IN ('anytime', 'morning', 'after_school', 'evening')),
    UNIQUE (family_id, id),
    CONSTRAINT task_definitions_penalty_pair CHECK (
        (penalty_value IS NULL AND penalty_unit IS NULL)
        OR (penalty_value IS NOT NULL AND penalty_unit IS NOT NULL
            AND (penalty_unit = 'points' OR penalty_value <= 100))
    ),
    FOREIGN KEY (family_id, template_id) REFERENCES task_templates (family_id, id)
);

CREATE TABLE task_occurrence_assignees (
    id                    TEXT PRIMARY KEY,
    family_id             TEXT NOT NULL REFERENCES families(id),
    task_occurrence_id    TEXT NOT NULL,
    family_member_id      TEXT NOT NULL,
    status                TEXT NOT NULL DEFAULT 'to_do' CHECK (status IN ('to_do', 'submitted', 'approved', 'rejected', 'skipped')),
    completed_at          DATETIME,
    completed_late        INTEGER NOT NULL DEFAULT 0 CHECK (completed_late IN (0, 1)),
    decided_at            DATETIME,
    decided_by_member_id  TEXT,
    decision_note         TEXT,
    version               INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at            DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    updated_at            DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    UNIQUE (family_id, id),
    UNIQUE (task_occurrence_id, family_member_id),
    CONSTRAINT task_occurrence_assignees_state CHECK (
        (status IN ('to_do', 'skipped') AND completed_at IS NULL AND decided_at IS NULL)
        OR (status = 'submitted' AND completed_at IS NOT NULL AND decided_at IS NULL)
        OR (status IN ('approved', 'rejected') AND completed_at IS NOT NULL AND decided_at IS NOT NULL)
    ),
    CONSTRAINT task_occurrence_assignees_late_needs_completion CHECK (completed_late = 0 OR completed_at IS NOT NULL),
    FOREIGN KEY (family_id, task_occurrence_id) REFERENCES task_occurrences (family_id, id) ON DELETE CASCADE,
    FOREIGN KEY (family_id, family_member_id) REFERENCES family_members (family_id, id),
    FOREIGN KEY (family_id, decided_by_member_id) REFERENCES family_members (family_id, id)
);

CREATE TABLE task_occurrences (
    id                  TEXT PRIMARY KEY,
    family_id           TEXT NOT NULL REFERENCES families(id),
    task_definition_id  TEXT NOT NULL,
    due_date            TEXT NOT NULL CHECK (due_date GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    due_at              DATETIME NOT NULL,
    expires_at          DATETIME,
    status              TEXT NOT NULL DEFAULT 'scheduled' CHECK (status IN ('scheduled', 'past_due', 'missed')),
    version             INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at          DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    updated_at          DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    UNIQUE (family_id, id),
    UNIQUE (task_definition_id, due_at),
    CONSTRAINT task_occurrences_expiry_after_due CHECK (expires_at IS NULL OR expires_at >= due_at),
    FOREIGN KEY (family_id, task_definition_id) REFERENCES task_definitions (family_id, id)
);

CREATE TABLE task_penalty_intents (
    id                  TEXT PRIMARY KEY,
    family_id           TEXT NOT NULL REFERENCES families(id),
    task_occurrence_id  TEXT NOT NULL,
    family_member_id    TEXT NOT NULL,
    penalty_value       INTEGER NOT NULL CHECK (penalty_value >= 0),
    penalty_unit        TEXT NOT NULL CHECK (penalty_unit IN ('percent', 'points')),
    penalty_points      INTEGER NOT NULL CHECK (penalty_points >= 0),
    created_at          DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')), settled_at DATETIME, cancelled_at DATETIME,
    UNIQUE (family_id, id),
    UNIQUE (task_occurrence_id, family_member_id),
    CONSTRAINT task_penalty_intents_percent_range CHECK (penalty_unit = 'points' OR penalty_value <= 100),
    FOREIGN KEY (family_id, task_occurrence_id) REFERENCES task_occurrences (family_id, id) ON DELETE CASCADE,
    FOREIGN KEY (family_id, family_member_id) REFERENCES family_members (family_id, id)
);

CREATE TABLE task_template_assignees (
    id                  TEXT PRIMARY KEY,
    family_id           TEXT NOT NULL REFERENCES families(id),
    task_template_id    TEXT NOT NULL,
    family_member_id    TEXT NOT NULL,
    created_at          DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    UNIQUE (family_id, id),
    UNIQUE (task_template_id, family_member_id),
    FOREIGN KEY (family_id, task_template_id) REFERENCES task_templates (family_id, id) ON DELETE CASCADE,
    FOREIGN KEY (family_id, family_member_id) REFERENCES family_members (family_id, id)
);

CREATE TABLE task_templates (
    id                      TEXT PRIMARY KEY,
    family_id               TEXT NOT NULL REFERENCES families(id),
    kind                    TEXT NOT NULL DEFAULT 'regular' CHECK (kind IN ('regular', 'bonus')),
    name                    TEXT NOT NULL CHECK (length(trim(name)) > 0),
    title                   TEXT,
    description             TEXT,
    icon                    TEXT,
    default_points          INTEGER CHECK (default_points IS NULL OR default_points >= 0),
    priority                TEXT CHECK (priority IS NULL OR priority IN ('low', 'normal', 'high')),
    approval_required       INTEGER CHECK (approval_required IS NULL OR approval_required IN (0, 1)),
    penalty_value           INTEGER CHECK (penalty_value IS NULL OR penalty_value >= 0),
    penalty_unit            TEXT CHECK (penalty_unit IS NULL OR penalty_unit IN ('percent', 'points')),
    expires_after_minutes   INTEGER CHECK (expires_after_minutes IS NULL OR expires_after_minutes >= 0),
    late_points_percent     INTEGER CHECK (late_points_percent IS NULL OR late_points_percent BETWEEN 0 AND 100),
    reminder_minutes_before INTEGER CHECK (reminder_minutes_before IS NULL OR reminder_minutes_before >= 0),
    due_time                TEXT CHECK (due_time IS NULL OR due_time GLOB '[0-2][0-9]:[0-5][0-9]'),
    recurrence_rule         TEXT CHECK (recurrence_rule IS NULL OR length(trim(recurrence_rule)) > 0),
    checklist_all_required  INTEGER CHECK (checklist_all_required IS NULL OR checklist_all_required IN (0, 1)),
    version                 INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_by              TEXT NOT NULL REFERENCES users(id),
    created_at              DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')),
    updated_at              DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')), category_id TEXT, part_of_day TEXT CHECK (part_of_day IS NULL OR part_of_day IN ('anytime', 'morning', 'after_school', 'evening')),
    UNIQUE (family_id, id),
    CONSTRAINT task_templates_penalty_pair CHECK (
        (penalty_value IS NULL AND penalty_unit IS NULL)
        OR (penalty_value IS NOT NULL AND penalty_unit IS NOT NULL
            AND (penalty_unit = 'points' OR penalty_value <= 100))
    )
);

CREATE UNIQUE INDEX idx_task_categories_family_name ON task_categories(family_id, lower(name));

CREATE INDEX idx_task_checklist_completions_item ON task_checklist_completions(family_id, task_checklist_item_id);

CREATE INDEX idx_task_checklist_items_family ON task_checklist_items(family_id);

CREATE INDEX idx_task_definition_assignees_member ON task_definition_assignees(family_id, family_member_id);

CREATE INDEX idx_task_definition_skip_dates_family ON task_definition_skip_dates(family_id, task_definition_id);

CREATE INDEX idx_task_definitions_category_id ON task_definitions(category_id);

CREATE INDEX idx_task_definitions_family_status ON task_definitions(family_id, status);

CREATE INDEX idx_task_definitions_family_template ON task_definitions(family_id, template_id);

CREATE INDEX idx_task_occurrence_assignees_member ON task_occurrence_assignees(family_id, family_member_id, status);

CREATE INDEX idx_task_occurrences_family_due ON task_occurrences(family_id, due_date);

CREATE INDEX idx_task_occurrences_family_status ON task_occurrences(family_id, status, due_at);

CREATE INDEX idx_task_penalty_intents_family ON task_penalty_intents(family_id, created_at);

CREATE INDEX idx_task_template_assignees_member ON task_template_assignees(family_id, family_member_id);

CREATE INDEX idx_task_templates_category_id ON task_templates(category_id);

CREATE INDEX idx_task_templates_family_kind ON task_templates(family_id, kind);

CREATE TRIGGER task_categories_not_in_use
BEFORE DELETE ON task_categories
WHEN EXISTS (SELECT 1 FROM task_templates t WHERE t.category_id = OLD.id)
  OR EXISTS (SELECT 1 FROM task_definitions d WHERE d.category_id = OLD.id)
BEGIN
    SELECT RAISE(ABORT, 'category is in use');
END;

CREATE TRIGGER task_definitions_category_family_insert
BEFORE INSERT ON task_definitions
WHEN NEW.category_id IS NOT NULL
 AND NOT EXISTS (SELECT 1 FROM task_categories c WHERE c.id = NEW.category_id AND c.family_id = NEW.family_id)
BEGIN
    SELECT RAISE(ABORT, 'category must belong to the same family');
END;

CREATE TRIGGER task_definitions_category_family_update
BEFORE UPDATE OF category_id, family_id ON task_definitions
WHEN NEW.category_id IS NOT NULL
 AND NOT EXISTS (SELECT 1 FROM task_categories c WHERE c.id = NEW.category_id AND c.family_id = NEW.family_id)
BEGIN
    SELECT RAISE(ABORT, 'category must belong to the same family');
END;

CREATE TRIGGER task_templates_bonus_no_penalty_insert
BEFORE INSERT ON task_templates
WHEN NEW.kind = 'bonus' AND (NEW.penalty_value IS NOT NULL OR NEW.penalty_unit IS NOT NULL)
BEGIN
    SELECT RAISE(ABORT, 'bonus templates cannot carry a penalty');
END;

CREATE TRIGGER task_templates_bonus_no_penalty_update
BEFORE UPDATE ON task_templates
WHEN NEW.kind = 'bonus' AND (NEW.penalty_value IS NOT NULL OR NEW.penalty_unit IS NOT NULL)
BEGIN
    SELECT RAISE(ABORT, 'bonus templates cannot carry a penalty');
END;

CREATE TRIGGER task_templates_category_family_insert
BEFORE INSERT ON task_templates
WHEN NEW.category_id IS NOT NULL
 AND NOT EXISTS (SELECT 1 FROM task_categories c WHERE c.id = NEW.category_id AND c.family_id = NEW.family_id)
BEGIN
    SELECT RAISE(ABORT, 'category must belong to the same family');
END;

CREATE TRIGGER task_templates_category_family_update
BEFORE UPDATE OF category_id, family_id ON task_templates
WHEN NEW.category_id IS NOT NULL
 AND NOT EXISTS (SELECT 1 FROM task_categories c WHERE c.id = NEW.category_id AND c.family_id = NEW.family_id)
BEGIN
    SELECT RAISE(ABORT, 'category must belong to the same family');
END;
