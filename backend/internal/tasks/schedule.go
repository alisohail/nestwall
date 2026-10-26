package tasks

import (
	"context"
	"database/sql"
	"time"

	"nestwall/backend/internal/platform/ids"
)

// ScheduleResult counts what one scheduler run changed.
type ScheduleResult struct {
	Generated int // occurrences created inside the rolling window
	PastDue   int // occurrences that reached their due time
	Missed    int // occurrences that reached their expiry
	Penalties int // penalty intents recorded for people who were still to do
}

// recurringRef identifies a repeating definition that the scheduler keeps generated.
type recurringRef struct {
	familyID string
	id       string
}

// RunSchedule brings every repeating definition and occurrence up to date with now. It first
// tops up each active repeating definition's rolling window, then in one transaction marks past
// due occurrences and missed occurrences. Every change is conditional on the status it expects,
// so running it again, including after a restart, changes nothing that is already done.
func RunSchedule(ctx context.Context, conn *sql.DB, now time.Time) (ScheduleResult, error) {
	var result ScheduleResult

	defs, err := recurringDefinitions(ctx, conn)
	if err != nil {
		return result, err
	}
	for _, def := range defs {
		created, err := generateOne(ctx, conn, def, now)
		if err != nil {
			return result, err
		}
		result.Generated += created
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()

	result.PastDue, err = MarkPastDue(ctx, tx, now)
	if err != nil {
		return result, err
	}
	result.Missed, result.Penalties, err = MarkMissed(ctx, tx, now)
	if err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

// recurringDefinitions lists the active repeating definitions across all families.
func recurringDefinitions(ctx context.Context, conn *sql.DB) ([]recurringRef, error) {
	rows, err := conn.QueryContext(ctx,
		`SELECT family_id, id FROM task_definitions
		 WHERE recurrence_rule IS NOT NULL AND status = 'active'
		 ORDER BY family_id, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var defs []recurringRef
	for rows.Next() {
		var def recurringRef
		if err := rows.Scan(&def.familyID, &def.id); err != nil {
			return nil, err
		}
		defs = append(defs, def)
	}
	return defs, rows.Err()
}

// generateOne tops up one definition's window in its own transaction.
func generateOne(ctx context.Context, conn *sql.DB, def recurringRef, now time.Time) (int, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	created, err := GenerateWindow(ctx, tx, def.familyID, def.id, now, DefaultWindowDays)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return created, nil
}

// MarkPastDue sets every scheduled occurrence whose due time is at or before now to past_due. The
// occurrence stays open for completion. It returns how many occurrences changed.
func MarkPastDue(ctx context.Context, tx *sql.Tx, now time.Time) (int, error) {
	stamp := dbTime(now)
	res, err := tx.ExecContext(ctx,
		`UPDATE task_occurrences
		 SET status = 'past_due', version = version + 1, updated_at = ?
		 WHERE status = 'scheduled' AND due_at <= ?`,
		stamp, stamp,
	)
	if err != nil {
		return 0, err
	}
	affected, err := res.RowsAffected()
	return int(affected), err
}

// expiredOccurrence is an occurrence whose expiry has passed, with the penalty of its task.
type expiredOccurrence struct {
	id           string
	familyID     string
	penaltyValue sql.NullInt64
	penaltyUnit  sql.NullString
	points       int
}

// MarkMissed sets every open occurrence whose expiry is at or before now to missed. Occurrences
// without an expiry never miss. For each one that changed, every assignee still to_do gets a
// penalty intent when the task has a penalty. The penalty is resolved to points once, here. It
// returns how many occurrences were missed and how many intents were recorded.
//
// A status change and its intents are written in the same transaction as the caller, and the
// intent insert ignores repeats, so a rerun cannot penalise anyone twice.
func MarkMissed(ctx context.Context, tx *sql.Tx, now time.Time) (int, int, error) {
	stamp := dbTime(now)
	rows, err := tx.QueryContext(ctx,
		`SELECT o.id, o.family_id, d.penalty_value, d.penalty_unit, d.points
		 FROM task_occurrences o
		 JOIN task_definitions d ON d.family_id = o.family_id AND d.id = o.task_definition_id
		 WHERE o.status IN ('scheduled', 'past_due') AND o.expires_at IS NOT NULL AND o.expires_at <= ?
		 ORDER BY o.expires_at, o.id`,
		stamp,
	)
	if err != nil {
		return 0, 0, err
	}
	var expired []expiredOccurrence
	for rows.Next() {
		var o expiredOccurrence
		if err := rows.Scan(&o.id, &o.familyID, &o.penaltyValue, &o.penaltyUnit, &o.points); err != nil {
			rows.Close()
			return 0, 0, err
		}
		expired = append(expired, o)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, 0, err
	}
	rows.Close()

	missed, penalties := 0, 0
	for _, o := range expired {
		res, err := tx.ExecContext(ctx,
			`UPDATE task_occurrences
			 SET status = 'missed', version = version + 1, updated_at = ?
			 WHERE family_id = ? AND id = ? AND status IN ('scheduled', 'past_due')`,
			stamp, o.familyID, o.id,
		)
		if err != nil {
			return missed, penalties, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return missed, penalties, err
		}
		if affected == 0 {
			continue
		}
		missed++

		if !o.penaltyValue.Valid || !o.penaltyUnit.Valid {
			continue
		}
		recorded, err := recordPenaltyIntents(ctx, tx, o, stamp)
		if err != nil {
			return missed, penalties, err
		}
		penalties += recorded
	}
	return missed, penalties, nil
}

// recordPenaltyIntents writes one penalty intent for each assignee who is still to_do. The
// percentage is resolved with PenaltyPoints. Rows that already exist are skipped.
func recordPenaltyIntents(ctx context.Context, tx *sql.Tx, o expiredOccurrence, stamp string) (int, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT family_member_id FROM task_occurrence_assignees
		 WHERE family_id = ? AND task_occurrence_id = ? AND status = 'to_do'
		 ORDER BY family_member_id`,
		o.familyID, o.id,
	)
	if err != nil {
		return 0, err
	}
	var members []string
	for rows.Next() {
		var memberID string
		if err := rows.Scan(&memberID); err != nil {
			rows.Close()
			return 0, err
		}
		members = append(members, memberID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	value := int(o.penaltyValue.Int64)
	unit := o.penaltyUnit.String
	points := PenaltyPoints(value, unit, o.points)

	recorded := 0
	for _, memberID := range members {
		res, err := tx.ExecContext(ctx,
			`INSERT INTO task_penalty_intents
				(id, family_id, task_occurrence_id, family_member_id, penalty_value, penalty_unit, penalty_points, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (task_occurrence_id, family_member_id) DO NOTHING`,
			ids.New(), o.familyID, o.id, memberID, value, unit, points, stamp,
		)
		if err != nil {
			return recorded, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return recorded, err
		}
		recorded += int(affected)
	}
	return recorded, nil
}
