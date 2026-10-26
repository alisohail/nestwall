package tasks

import (
	"context"
	"sort"
	"strings"
	"time"
)

// CalendarItem is one task occurrence shown on the family calendar. MemberIDs lists the assignees
// whose copy is not skipped, sorted. OccurrenceID is the ID the task pages use.
type CalendarItem struct {
	OccurrenceID string
	Title        string
	DueDate      string
	DueAt        time.Time
	Status       string
	MemberIDs    []string
}

// CalendarQuery selects the occurrences for the calendar. From and To are family-local dates
// (YYYY-MM-DD) and both are inclusive. ScopeMember, when set, limits the result to occurrences that
// member is assigned to. MemberID, when set, does the same for the member filter.
type CalendarQuery struct {
	FamilyID    string
	From        string
	To          string
	ScopeMember string
	MemberID    string
}

// CalendarOccurrences returns the occurrences of active tasks due in the window, earliest first.
// An occurrence whose copies were all skipped is left out, as it is on the child's list.
func CalendarOccurrences(ctx context.Context, q querier, query CalendarQuery) ([]CalendarItem, error) {
	conds := []string{
		"o.family_id = ?",
		"d.status = 'active'",
		"o.due_date >= ?",
		"o.due_date <= ?",
		`EXISTS (SELECT 1 FROM task_occurrence_assignees live
			WHERE live.family_id = o.family_id AND live.task_occurrence_id = o.id AND live.status != 'skipped')`,
	}
	args := []any{query.FamilyID, query.From, query.To}
	for _, member := range []string{query.ScopeMember, query.MemberID} {
		if member == "" {
			continue
		}
		conds = append(conds, `EXISTS (SELECT 1 FROM task_occurrence_assignees m
			WHERE m.family_id = o.family_id AND m.task_occurrence_id = o.id
			AND m.family_member_id = ? AND m.status != 'skipped')`)
		args = append(args, member)
	}

	text := `SELECT o.id, d.title, o.due_date,
		strftime('%Y-%m-%dT%H:%M:%SZ', o.due_at), o.status,
		(SELECT GROUP_CONCAT(a.family_member_id) FROM task_occurrence_assignees a
			WHERE a.family_id = o.family_id AND a.task_occurrence_id = o.id AND a.status != 'skipped')
	FROM task_occurrences o
	JOIN task_definitions d ON d.family_id = o.family_id AND d.id = o.task_definition_id
	WHERE ` + strings.Join(conds, " AND ") + `
	ORDER BY o.due_at, o.id`

	rows, err := q.QueryContext(ctx, text, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []CalendarItem{}
	for rows.Next() {
		var it CalendarItem
		var dueAt, members string
		if err := rows.Scan(&it.OccurrenceID, &it.Title, &it.DueDate, &dueAt, &it.Status, &members); err != nil {
			return nil, err
		}
		if it.DueAt, err = time.Parse(time.RFC3339, dueAt); err != nil {
			return nil, err
		}
		it.MemberIDs = splitMembers(members)
		out = append(out, it)
	}
	return out, rows.Err()
}

// splitMembers turns a GROUP_CONCAT list of member IDs into a sorted slice.
func splitMembers(list string) []string {
	out := []string{}
	if list == "" {
		return out
	}
	out = append(out, strings.Split(list, ",")...)
	sort.Strings(out)
	return out
}
