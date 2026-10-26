// Package badges awards the built-in badges (PBI M4-09, batch 09a). A badge is earned from a child's
// records. It is awarded in the same transaction as the approval that earns it, and by a sweep that
// catches the rest (a share, a redemption, a day that closes). A built-in badge is awarded once per
// child and is never revoked.
package badges

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"nestwall/backend/internal/badges/catalogue"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/tasks"
)

// querier is satisfied by *sql.DB and *sql.Tx.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Evaluate awards each enabled built-in badge that the child has reached and does not yet have. It
// returns the keys it awarded now. Running it again awards nothing new.
func Evaluate(ctx context.Context, q querier, familyID, memberID string, now time.Time) ([]string, error) {
	m, err := loadMetrics(ctx, q, familyID, memberID)
	if err != nil {
		return nil, err
	}

	rows, err := q.QueryContext(ctx,
		`SELECT d.id, d.badge_key FROM badge_definitions d
		 WHERE d.family_id = ? AND d.badge_key IS NOT NULL AND d.enabled = 1
		   AND EXISTS (SELECT 1 FROM badge_settings s WHERE s.family_id = d.family_id AND s.enabled = 1)
		   AND NOT EXISTS (
		       SELECT 1 FROM member_badges mb
		       WHERE mb.family_id = d.family_id AND mb.family_member_id = ?
		         AND mb.badge_definition_id = d.id AND mb.source = 'auto')
		 ORDER BY d.badge_key`,
		familyID, memberID)
	if err != nil {
		return nil, fmt.Errorf("load badge definitions: %w", err)
	}
	type pending struct{ definitionID, key string }
	var todo []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.definitionID, &p.key); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan badge definition: %w", err)
		}
		todo = append(todo, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read badge definitions: %w", err)
	}

	var awarded []string
	for _, p := range todo {
		b, ok := catalogue.ByKey(p.key)
		if !ok || m.valueOf(b) < b.Target {
			continue
		}
		res, err := q.ExecContext(ctx,
			`INSERT INTO member_badges (id, family_id, family_member_id, badge_definition_id, source, idempotency_key, awarded_at)
			 VALUES (?, ?, ?, ?, 'auto', ?, ?)
			 ON CONFLICT (family_id, idempotency_key) DO NOTHING`,
			ids.New(), familyID, memberID, p.definitionID, "auto:"+memberID+":"+p.key, formatTime(now))
		if err != nil {
			return nil, fmt.Errorf("award badge %s: %w", p.key, err)
		}
		if n, _ := res.RowsAffected(); n == 1 {
			awarded = append(awarded, p.key)
			if err := notifyChild(ctx, q, familyID, memberID, kindBadgeEarned, "You earned a new badge.", now); err != nil {
				return nil, err
			}
		}
	}
	return awarded, nil
}

// EvaluateAll runs Evaluate for every child in every family, one transaction per child. The scheduler
// calls it each tick, so awards for changes that are not approvals (shares, redemptions, a streak that
// closes) appear within a minute.
func EvaluateAll(ctx context.Context, db *sql.DB, now time.Time) (int, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT m.family_id, m.id FROM family_members m
		 JOIN users u ON u.id = m.user_id
		 WHERE u.is_child = 1 AND m.status = 'active'`)
	if err != nil {
		return 0, fmt.Errorf("list children: %w", err)
	}
	type child struct{ familyID, memberID string }
	var children []child
	for rows.Next() {
		var c child
		if err := rows.Scan(&c.familyID, &c.memberID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan child: %w", err)
		}
		children = append(children, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("read children: %w", err)
	}

	total := 0
	for _, c := range children {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return total, fmt.Errorf("begin badge sweep: %w", err)
		}
		keys, err := Evaluate(ctx, tx, c.familyID, c.memberID, now)
		if err != nil {
			_ = tx.Rollback()
			return total, fmt.Errorf("evaluate badges for %s: %w", c.memberID, err)
		}
		if err := tx.Commit(); err != nil {
			return total, fmt.Errorf("commit badge sweep: %w", err)
		}
		total += len(keys)
	}
	return total, nil
}

// Awards is the approval participant. It runs inside the approval transaction, after the points
// participant, so a badge that depends on the new points sees them.
type Awards struct {
	clock clock.Clock
}

// NewAwards returns the participant. Register Apply with tasks.Handlers.Participate at startup.
func NewAwards(clk clock.Clock) *Awards {
	return &Awards{clock: clk}
}

// Apply evaluates the child's badges when a copy is approved. Other events are ignored. An error
// rolls the approval back, as it does for points.
func (a *Awards) Apply(ctx context.Context, tx *sql.Tx, e tasks.Event) error {
	if e.Type != tasks.EventApproved {
		return nil
	}
	_, err := Evaluate(ctx, tx, e.FamilyID, e.MemberID, a.clock.Now())
	return err
}

// formatTime writes a time in the database's DATETIME text form (UTC, +00:00).
func formatTime(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05.000") + "+00:00"
}
