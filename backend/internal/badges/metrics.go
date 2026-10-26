package badges

import (
	"context"
	"fmt"

	"nestwall/backend/internal/badges/catalogue"
)

// Metrics are the figures a child's built-in badges are measured against. They come from the
// child's records only: approved copies, the points ledger, approved redemptions and completed shares.
type Metrics struct {
	TasksDone       int            // approved copies
	Early           int            // approved copies completed before the due time
	Late            int            // approved copies completed after the due time
	Streak          int            // longest run of complete days
	PointsEarned    int            // net points from task, bonus-task and bonus awards
	BonusFromParent int            // points from parent bonuses
	Redemptions     int            // approved redemptions
	SharesDone      int            // completed shares the child sent
	BySkill         map[string]int // approved copies per task category badge key
}

// tasksDoneQuery counts approved copies, split into early (completed before the due time) and late.
const tasksDoneQuery = `SELECT COUNT(*),
	COALESCE(SUM(CASE WHEN julianday(oa.completed_at) < julianday(o.due_at) THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(oa.completed_late), 0)
FROM task_occurrence_assignees oa
JOIN task_occurrences o ON o.family_id = oa.family_id AND o.id = oa.task_occurrence_id
WHERE oa.family_id = ? AND oa.family_member_id = ? AND oa.status = 'approved'`

// skillQuery counts approved copies per category badge key.
const skillQuery = `SELECT c.badge_key, COUNT(*)
FROM task_occurrence_assignees oa
JOIN task_occurrences o ON o.family_id = oa.family_id AND o.id = oa.task_occurrence_id
JOIN task_definitions d ON d.family_id = o.family_id AND d.id = o.task_definition_id
JOIN task_categories c ON c.family_id = d.family_id AND c.id = d.category_id
WHERE oa.family_id = ? AND oa.family_member_id = ? AND oa.status = 'approved' AND c.badge_key IS NOT NULL
GROUP BY c.badge_key`

// dayTotalsQuery totals required and approved copies per due date, oldest first. Skipped copies are
// not required. Inactive (archived) tasks are left out, as in the My tasks streak.
const dayTotalsQuery = `SELECT o.due_date, COUNT(*), SUM(CASE WHEN oa.status = 'approved' THEN 1 ELSE 0 END)
FROM task_occurrence_assignees oa
JOIN task_occurrences o ON o.family_id = oa.family_id AND o.id = oa.task_occurrence_id
JOIN task_definitions d ON d.family_id = o.family_id AND d.id = o.task_definition_id
WHERE oa.family_id = ? AND oa.family_member_id = ? AND d.status = 'active' AND oa.status <> 'skipped'
GROUP BY o.due_date
ORDER BY o.due_date ASC`

// pointsQuery returns the net points earned and the points from parent bonuses. A reversal of a task
// award is negative and is netted off; a reversal of a penalty is positive and is not counted.
const pointsQuery = `SELECT
	COALESCE(SUM(CASE
		WHEN entry_type IN ('task_award', 'bonus_task_award', 'bonus') THEN amount
		WHEN entry_type = 'reversal' AND amount < 0 THEN amount
		ELSE 0 END), 0),
	COALESCE(SUM(CASE WHEN entry_type = 'bonus' THEN amount ELSE 0 END), 0)
FROM points_ledger
WHERE family_id = ? AND family_member_id = ?`

const redemptionsQuery = `SELECT COUNT(*) FROM redemptions
WHERE family_id = ? AND family_member_id = ? AND status = 'approved'`

const sharesQuery = `SELECT COUNT(*) FROM point_shares
WHERE family_id = ? AND from_member_id = ? AND status = 'completed'`

// dayTotals is one family date in the streak window.
type dayTotals struct {
	date  string
	total int
	done  int
}

// loadMetrics reads a child's figures.
func loadMetrics(ctx context.Context, q querier, familyID, memberID string) (Metrics, error) {
	m := Metrics{BySkill: map[string]int{}}

	if err := q.QueryRowContext(ctx, tasksDoneQuery, familyID, memberID).Scan(&m.TasksDone, &m.Early, &m.Late); err != nil {
		return m, fmt.Errorf("count approved tasks: %w", err)
	}

	skills, err := q.QueryContext(ctx, skillQuery, familyID, memberID)
	if err != nil {
		return m, fmt.Errorf("count skill tasks: %w", err)
	}
	for skills.Next() {
		var key string
		var n int
		if err := skills.Scan(&key, &n); err != nil {
			skills.Close()
			return m, fmt.Errorf("scan skill tasks: %w", err)
		}
		m.BySkill[key] = n
	}
	skills.Close()
	if err := skills.Err(); err != nil {
		return m, fmt.Errorf("read skill tasks: %w", err)
	}

	days, err := q.QueryContext(ctx, dayTotalsQuery, familyID, memberID)
	if err != nil {
		return m, fmt.Errorf("load streak days: %w", err)
	}
	var list []dayTotals
	for days.Next() {
		var d dayTotals
		if err := days.Scan(&d.date, &d.total, &d.done); err != nil {
			days.Close()
			return m, fmt.Errorf("scan streak day: %w", err)
		}
		list = append(list, d)
	}
	days.Close()
	if err := days.Err(); err != nil {
		return m, fmt.Errorf("read streak days: %w", err)
	}
	m.Streak = longestRun(list)

	if err := q.QueryRowContext(ctx, pointsQuery, familyID, memberID).Scan(&m.PointsEarned, &m.BonusFromParent); err != nil {
		return m, fmt.Errorf("sum points: %w", err)
	}
	if err := q.QueryRowContext(ctx, redemptionsQuery, familyID, memberID).Scan(&m.Redemptions); err != nil {
		return m, fmt.Errorf("count redemptions: %w", err)
	}
	if err := q.QueryRowContext(ctx, sharesQuery, familyID, memberID).Scan(&m.SharesDone); err != nil {
		return m, fmt.Errorf("count shares: %w", err)
	}
	return m, nil
}

// longestRun returns the longest run of complete days. Days are oldest first. A day with no required
// copies is neutral and does not break a run. Any other day that is not complete ends the run. Using
// the longest run, not the current one, keeps an award permanent once it is earned.
func longestRun(days []dayTotals) int {
	run, best := 0, 0
	for _, d := range days {
		if d.total == 0 {
			continue
		}
		if d.done == d.total {
			run++
			if run > best {
				best = run
			}
			continue
		}
		run = 0
	}
	return best
}

// valueOf returns the figure a built-in badge is measured against. Every catalogue key must be
// handled here; a key that is not is never awarded (the catalogue test checks this).
func (m Metrics) valueOf(b catalogue.Badge) int {
	if b.Category != "" {
		return m.BySkill[b.Category]
	}
	switch b.Key {
	case "s3", "s7", "s14", "s30", "s100", "e_week", "e_month":
		return m.Streak
	case "p100", "p250", "p500", "p1000":
		return m.PointsEarned
	case "p_reward":
		return m.Redemptions
	case "t1", "t10", "t50", "t100":
		return m.TasksDone
	case "e_early":
		return m.Early
	case "e_back":
		return m.Late
	case "k_share":
		return m.SharesDone
	case "k_help":
		return m.BonusFromParent
	}
	return 0
}
