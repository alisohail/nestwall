package tasks

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/platform/ids"
)

// occurrenceRef identifies one occurrence and the definition it belongs to.
type occurrenceRef struct {
	id           string
	definitionID string
	dueDate      string
}

// seriesRow is the part of a definition that edits need.
type seriesRow struct {
	rule     sql.NullString
	startsOn string
	status   string
}

// loadSeriesOccurrence loads an occurrence and its definition, scoped to the family. It returns
// ErrNotRecurring when the definition has no rule.
func loadSeriesOccurrence(ctx context.Context, tx *sql.Tx, familyID, occurrenceID string) (occurrenceRef, seriesRow, error) {
	var (
		occ    occurrenceRef
		series seriesRow
	)
	err := tx.QueryRowContext(ctx,
		`SELECT o.id, o.task_definition_id, o.due_date, d.recurrence_rule, d.starts_on, d.status
		 FROM task_occurrences o
		 JOIN task_definitions d ON d.family_id = o.family_id AND d.id = o.task_definition_id
		 WHERE o.family_id = ? AND o.id = ?`,
		familyID, occurrenceID,
	).Scan(&occ.id, &occ.definitionID, &occ.dueDate, &series.rule, &series.startsOn, &series.status)
	if errors.Is(err, sql.ErrNoRows) {
		return occ, series, ErrNotFound
	}
	if err != nil {
		return occ, series, err
	}
	if !series.rule.Valid {
		return occ, series, ErrNotRecurring
	}
	return occ, series, nil
}

// DetachOccurrence takes one occurrence out of its series, so that an edit to it does not change
// the rest of the series. The occurrence's date becomes a skip date of the series. The occurrence
// moves to a new one-off definition, whose ID is returned. Its completions are kept.
// Checklist items are not copied to the new definition.
func DetachOccurrence(ctx context.Context, tx *sql.Tx, familyID, actorID, occurrenceID string, now time.Time) (string, error) {
	occ, series, err := loadSeriesOccurrence(ctx, tx, familyID, occurrenceID)
	if err != nil {
		return "", err
	}
	if series.status != "active" {
		return "", ErrSeriesArchived
	}
	if err := addSkipDate(ctx, tx, familyID, occ.definitionID, occ.dueDate); err != nil {
		return "", err
	}

	detachedID := ids.New()
	if err := copyDefinition(ctx, tx, familyID, occ.definitionID, detachedID, occ.dueDate, nil, now); err != nil {
		return "", err
	}
	if err := copyAssignees(ctx, tx, familyID, occ.definitionID, detachedID); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE task_occurrences SET task_definition_id = ?, updated_at = ? WHERE family_id = ? AND id = ?`,
		detachedID, dbTime(now), familyID, occ.id,
	); err != nil {
		return "", err
	}
	if err := audit.Record(ctx, tx, familyID, actorID, "task.occurrence_detached", "task", &occ.id, map[string]any{
		"from_definition_id": occ.definitionID,
		"new_definition_id":  detachedID,
		"due_date":           occ.dueDate,
	}); err != nil {
		return "", err
	}
	return detachedID, nil
}

// SplitSeries applies newRule from the occurrence's date onward ("this and future").
//
// The earlier part of the series keeps its occurrences and completions. Its rule is capped so
// that it ends just before the split date. If nothing falls before the split, the earlier
// definition is archived. Untouched occurrences from the split date onward are removed.
// Occurrences with progress stay on the earlier definition.
//
// The new definition starts on the split date and takes the skip dates from that date onward.
// Its occurrences are generated with DefaultWindowDays. It returns the new definition's ID.
func SplitSeries(ctx context.Context, tx *sql.Tx, familyID, actorID, occurrenceID, newRule string, now time.Time) (string, error) {
	rule, err := ValidateRule(newRule)
	if err != nil {
		return "", err
	}
	occ, series, err := loadSeriesOccurrence(ctx, tx, familyID, occurrenceID)
	if err != nil {
		return "", err
	}
	if series.status != "active" {
		return "", ErrSeriesArchived
	}

	firstDay, err := civilDay(series.startsOn)
	if err != nil {
		return "", err
	}
	splitDay, err := civilDay(occ.dueDate)
	if err != nil {
		return "", err
	}
	earlier := 0
	if splitDay.After(firstDay) {
		days, err := occurrenceDates(series.rule.String, firstDay, firstDay, splitDay.AddDate(0, 0, -1))
		if err != nil {
			return "", err
		}
		earlier = len(days)
	}

	if earlier == 0 {
		if _, err := tx.ExecContext(ctx,
			`UPDATE task_definitions SET status = 'archived', version = version + 1, updated_at = ?
			 WHERE family_id = ? AND id = ?`,
			dbTime(now), familyID, occ.definitionID,
		); err != nil {
			return "", err
		}
	} else {
		capped, err := withCount(series.rule.String, earlier)
		if err != nil {
			return "", err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE task_definitions SET recurrence_rule = ?, version = version + 1, updated_at = ?
			 WHERE family_id = ? AND id = ?`,
			capped, dbTime(now), familyID, occ.definitionID,
		); err != nil {
			return "", err
		}
	}
	if err := deleteUntouchedOccurrences(ctx, tx, familyID, occ.definitionID, "o.due_date >= ?", occ.dueDate); err != nil {
		return "", err
	}

	newID := ids.New()
	if err := copyDefinition(ctx, tx, familyID, occ.definitionID, newID, occ.dueDate, &rule, now); err != nil {
		return "", err
	}
	if err := copyAssignees(ctx, tx, familyID, occ.definitionID, newID); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE task_definition_skip_dates SET task_definition_id = ?
		 WHERE family_id = ? AND task_definition_id = ? AND skip_date >= ?`,
		newID, familyID, occ.definitionID, occ.dueDate,
	); err != nil {
		return "", err
	}
	if _, err := GenerateWindow(ctx, tx, familyID, newID, now, DefaultWindowDays); err != nil {
		return "", err
	}
	if err := audit.Record(ctx, tx, familyID, actorID, "task.series_split", "task", &occ.id, map[string]any{
		"task_definition_id": occ.definitionID,
		"new_definition_id":  newID,
		"split_date":         occ.dueDate,
	}); err != nil {
		return "", err
	}
	return newID, nil
}

// ReplaceFutureRule changes the rule of the whole series from now onward ("all future").
//
// Untouched occurrences due after now are removed and generated again from the new rule.
// Occurrences with progress, and any occurrence already due, keep their state. Completed history
// is never rewritten.
func ReplaceFutureRule(ctx context.Context, tx *sql.Tx, familyID, actorID, occurrenceID, newRule string, now time.Time) error {
	rule, err := ValidateRule(newRule)
	if err != nil {
		return err
	}
	occ, series, err := loadSeriesOccurrence(ctx, tx, familyID, occurrenceID)
	if err != nil {
		return err
	}
	if series.status != "active" {
		return ErrSeriesArchived
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE task_definitions SET recurrence_rule = ?, version = version + 1, updated_at = ?
		 WHERE family_id = ? AND id = ?`,
		rule, dbTime(now), familyID, occ.definitionID,
	); err != nil {
		return err
	}
	if err := deleteUntouchedOccurrences(ctx, tx, familyID, occ.definitionID, "o.due_at > ?", dbTime(now)); err != nil {
		return err
	}
	if _, err := GenerateWindow(ctx, tx, familyID, occ.definitionID, now, DefaultWindowDays); err != nil {
		return err
	}
	return audit.Record(ctx, tx, familyID, actorID, "task.series_rule_changed", "task", &occ.id, map[string]any{
		"task_definition_id": occ.definitionID,
		"rule":               rule,
	})
}

// deleteUntouchedOccurrences removes the scheduled occurrences of a definition that match cond
// (SQL over alias o). An occurrence is untouched when it is still scheduled, every assignee is
// to_do, and no checklist item is done. Other occurrences are left as they are.
func deleteUntouchedOccurrences(ctx context.Context, tx *sql.Tx, familyID, definitionID, cond string, args ...any) error {
	match := `SELECT o.id FROM task_occurrences o
		WHERE o.family_id = ? AND o.task_definition_id = ? AND o.status = 'scheduled' AND ` + cond + `
		AND NOT EXISTS (SELECT 1 FROM task_occurrence_assignees oa
			WHERE oa.family_id = o.family_id AND oa.task_occurrence_id = o.id AND oa.status <> 'to_do')
		AND NOT EXISTS (SELECT 1 FROM task_occurrence_assignees oa
			JOIN task_checklist_completions cc ON cc.family_id = oa.family_id AND cc.task_occurrence_assignee_id = oa.id
			WHERE oa.family_id = o.family_id AND oa.task_occurrence_id = o.id)`
	matchArgs := append([]any{familyID, definitionID}, args...)

	deleteArgs := append([]any{familyID}, matchArgs...)
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM task_occurrence_assignees WHERE family_id = ? AND task_occurrence_id IN (`+match+`)`,
		deleteArgs...,
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM task_occurrences WHERE family_id = ? AND id IN (`+match+`)`,
		deleteArgs...,
	); err != nil {
		return err
	}
	return nil
}

// withCount returns rule with any COUNT or UNTIL replaced by COUNT=n.
func withCount(rule string, n int) (string, error) {
	parts, err := parseRule(rule)
	if err != nil {
		return "", err
	}
	kept := make([]string, 0, len(parts)+1)
	for _, p := range parts {
		if p.key == "COUNT" || p.key == "UNTIL" {
			continue
		}
		kept = append(kept, p.key+"="+p.value)
	}
	kept = append(kept, "COUNT="+strconv.Itoa(n))
	return strings.Join(kept, ";"), nil
}

// addSkipDate records a date on which a definition has no occurrence. Adding it twice is a no-op.
func addSkipDate(ctx context.Context, tx *sql.Tx, familyID, definitionID, date string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO task_definition_skip_dates (family_id, task_definition_id, skip_date)
		 VALUES (?, ?, ?)
		 ON CONFLICT (task_definition_id, skip_date) DO NOTHING`,
		familyID, definitionID, date,
	)
	return err
}

// copyDefinition creates an active definition from an existing one in the same family. It keeps
// the content, points and assignment settings, and sets the given start date and rule. A nil rule
// makes a one-off definition. Assignees are copied separately.
func copyDefinition(ctx context.Context, tx *sql.Tx, familyID, fromID, toID, startsOn string, rule *string, now time.Time) error {
	var ruleArg any
	if rule != nil {
		ruleArg = *rule
	}
	stamp := dbTime(now)
	_, err := tx.ExecContext(ctx,
		`INSERT INTO task_definitions (
			id, family_id, template_id, title, description, icon, time_zone, starts_on, due_time,
			recurrence_rule, points, priority, approval_required, penalty_value, penalty_unit,
			expires_after_minutes, late_points_percent, reminder_minutes_before,
			checklist_all_required, status, version, created_by, created_at, updated_at)
		 SELECT ?, family_id, template_id, title, description, icon, time_zone, ?, due_time,
			?, points, priority, approval_required, penalty_value, penalty_unit,
			expires_after_minutes, late_points_percent, reminder_minutes_before,
			checklist_all_required, 'active', 1, created_by, ?, ?
		 FROM task_definitions WHERE family_id = ? AND id = ?`,
		toID, startsOn, ruleArg, stamp, stamp, familyID, fromID,
	)
	return err
}

// copyAssignees gives a new definition the same assignees as an existing one.
func copyAssignees(ctx context.Context, tx *sql.Tx, familyID, fromID, toID string) error {
	members, err := loadDefinitionAssignees(ctx, tx, familyID, fromID)
	if err != nil {
		return err
	}
	for _, memberID := range members {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO task_definition_assignees (id, family_id, task_definition_id, family_member_id)
			 VALUES (?, ?, ?, ?)`,
			ids.New(), familyID, toID, memberID,
		); err != nil {
			return err
		}
	}
	return nil
}
