package tasks

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"strings"
	"time"

	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/platform/web"
)

// taskRowSelect reads one row per assignee of each occurrence: the occurrence, its definition and
// that person's assignee row. One-off and repeating tasks are both read here. The WHERE clause is
// completed by the caller.
//
// Columns, in the order scanTaskRow reads them. The due and expiry times are returned in ISO
// form for the API. CAST keeps the stored text, which the list cursor sorts on.
const taskRowSelect = `SELECT o.id, d.id, d.title, d.description, d.category_id, d.part_of_day, d.priority,
	o.due_date, d.due_time,
	strftime('%Y-%m-%dT%H:%M:%SZ', o.due_at), strftime('%Y-%m-%dT%H:%M:%SZ', o.expires_at),
	d.reminder_minutes_before, d.late_points_percent, d.points, d.approval_required,
	d.expires_after_minutes, d.penalty_value, d.penalty_unit, d.template_id, d.status, d.version,
	d.recurrence_rule IS NOT NULL, oa.family_member_id, oa.status, oa.id, CAST(o.due_at AS TEXT),
	CASE WHEN d.status = 'archived' THEN strftime('%Y-%m-%dT%H:%M:%SZ', d.updated_at) END
FROM task_occurrence_assignees oa
JOIN task_occurrences o ON o.family_id = oa.family_id AND o.id = oa.task_occurrence_id
JOIN task_definitions d ON d.family_id = o.family_id AND d.id = o.task_definition_id
WHERE oa.family_id = ?`

// taskRow is one row of taskRowSelect: a task, plus one person's copy of it.
type taskRow struct {
	task
	definitionID string
	memberID     string
	status       string // the person's status on the task
	assigneeID   string // ID of the assignee row; breaks ties in the list cursor
	dueKey       string // stored due_at text; the list cursor sorts on it
	defStatus    string // active or archived
}

// scanTaskRow reads one row in taskRowSelect order.
func scanTaskRow(s rowScanner, r *taskRow) error {
	if err := s.Scan(&r.ID, &r.definitionID, &r.Title, &r.Description, &r.CategoryID, &r.PartOfDay, &r.Priority,
		&r.DueDate, &r.DueTime, &r.DueAt, &r.ExpiresAt,
		&r.ReminderMinutesBefore, &r.LatePointsPercent, &r.Points, &r.ApprovalRequired,
		&r.ExpiresAfterMinutes, &r.PenaltyValue, &r.PenaltyUnit, &r.TemplateID, &r.defStatus, &r.Version,
		&r.Repeats, &r.memberID, &r.status, &r.assigneeID, &r.dueKey, &r.ArchivedAt); err != nil {
		return err
	}
	r.Archived = r.defStatus == statusArchived
	return nil
}

// queryTaskRows runs taskRowSelect with the extra conditions and suffix (ORDER BY, LIMIT) that
// the caller adds after the WHERE clause.
func queryTaskRows(ctx context.Context, q querier, suffix string, args ...any) ([]taskRow, error) {
	rows, err := q.QueryContext(ctx, taskRowSelect+suffix, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []taskRow
	for rows.Next() {
		var r taskRow
		if err := scanTaskRow(rows, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// loadTask reads one task occurrence with its assignees, ordered by member. When scope is a member
// ID only that member's copy is read, so a child sees its own copy and nothing else. The checklist
// is that copy's. It returns sql.ErrNoRows when no visible copy exists, which includes tasks of
// other families.
func loadTask(ctx context.Context, q querier, familyID, taskID, scope string) (task, error) {
	return loadTaskCopy(ctx, q, familyID, taskID, scope, "")
}

// loadTaskCopy is loadTask with the checklist read from one member's copy. When that member has no
// visible copy, or member is empty, the checklist comes from the first copy in member order.
func loadTaskCopy(ctx context.Context, q querier, familyID, taskID, scope, member string) (task, error) {
	args := []any{familyID}
	suffix := ` AND o.id = ?`
	args = append(args, taskID)
	if scope != "" {
		suffix += ` AND oa.family_member_id = ?`
		args = append(args, scope)
	}

	rows, err := queryTaskRows(ctx, q, suffix+` ORDER BY oa.family_member_id`, args...)
	if err != nil {
		return task{}, err
	}
	if len(rows) == 0 {
		return task{}, sql.ErrNoRows
	}

	t := rows[0].task
	t.definitionID = rows[0].definitionID
	t.Assignees = make([]assignment, 0, len(rows))
	t.AssigneeIDs = make([]string, 0, len(rows))
	shown := rows[0]
	for _, r := range rows {
		t.Assignees = append(t.Assignees, assignment{MemberID: r.memberID, Status: r.status})
		t.AssigneeIDs = append(t.AssigneeIDs, r.memberID)
		if member != "" && r.memberID == member {
			shown = r
		}
	}
	checklists, err := loadChecklists(ctx, q, familyID, []string{shown.assigneeID})
	if err != nil {
		return task{}, err
	}
	t.Checklist = checklists[shown.assigneeID]
	return t, nil
}

// loadChecklists reads the checklists of several copies (assignee rows) in one query, keyed by
// assignee row ID. The step text is shared by the task, and each copy has its own done state. Each
// checklist is in sort order. A copy whose task has no steps has no entry.
func loadChecklists(ctx context.Context, q querier, familyID string, assigneeIDs []string) (map[string][]checklistItem, error) {
	out := map[string][]checklistItem{}
	args := []any{familyID}
	marks := make([]string, 0, len(assigneeIDs))
	seen := make(map[string]bool, len(assigneeIDs))
	for _, id := range assigneeIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		marks = append(marks, "?")
		args = append(args, id)
	}
	if len(marks) == 0 {
		return out, nil
	}

	rows, err := q.QueryContext(ctx,
		`SELECT oa.id, i.id, i.item_text, c.task_checklist_item_id IS NOT NULL
		 FROM task_occurrence_assignees oa
		 JOIN task_checklist_items i ON i.family_id = oa.family_id AND i.task_occurrence_id = oa.task_occurrence_id
		 LEFT JOIN task_checklist_completions c ON c.family_id = oa.family_id
			AND c.task_occurrence_assignee_id = oa.id AND c.task_checklist_item_id = i.id
		 WHERE oa.family_id = ? AND oa.id IN (`+strings.Join(marks, ", ")+`)
		 ORDER BY oa.id, i.sort_order`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var assigneeID string
		var it checklistItem
		if err := rows.Scan(&assigneeID, &it.ID, &it.Text, &it.Done); err != nil {
			return nil, err
		}
		out[assigneeID] = append(out[assigneeID], it)
	}
	return out, rows.Err()
}

// copyNotDue reports whether a copy's due day is still ahead of today in the family's time zone.
func copyNotDue(ctx context.Context, q querier, familyID, dueDate string, now time.Time) (bool, error) {
	zone, _, err := familyZone(ctx, q, familyID)
	if err != nil {
		return false, err
	}
	return dueDate > now.In(zone).Format(time.DateOnly), nil
}

// familyZone returns the family's time zone as a location and as its IANA name.
func familyZone(ctx context.Context, q querier, familyID string) (*time.Location, string, error) {
	var name string
	if err := q.QueryRowContext(ctx,
		`SELECT timezone FROM families WHERE id = ?`, familyID,
	).Scan(&name); err != nil {
		return nil, "", err
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, "", err
	}
	return loc, name, nil
}

// loadAssigneeStatuses maps each assignee of an occurrence to that person's status.
func loadAssigneeStatuses(ctx context.Context, q querier, familyID, occurrenceID string) (map[string]string, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT family_member_id, status FROM task_occurrence_assignees
		 WHERE family_id = ? AND task_occurrence_id = ?`,
		familyID, occurrenceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	statuses := map[string]string{}
	for rows.Next() {
		var memberID, status string
		if err := rows.Scan(&memberID, &status); err != nil {
			return nil, err
		}
		statuses[memberID] = status
	}
	return statuses, rows.Err()
}

// setAssignees makes the assignee rows of a one-off task match want. A removed person's row is
// deleted even if they already acted on the task; points they earned stay in the ledger.
func setAssignees(ctx context.Context, tx *sql.Tx, familyID, definitionID string, want []string) error {
	var occurrenceID string
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM task_occurrences WHERE family_id = ? AND task_definition_id = ?`,
		familyID, definitionID,
	).Scan(&occurrenceID); err != nil {
		return err
	}

	current, err := loadAssigneeStatuses(ctx, tx, familyID, occurrenceID)
	if err != nil {
		return err
	}
	wanted := make(map[string]bool, len(want))
	for _, memberID := range want {
		wanted[memberID] = true
	}

	for memberID := range current {
		if wanted[memberID] {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM task_occurrence_assignees
			 WHERE family_id = ? AND task_occurrence_id = ? AND family_member_id = ?`,
			familyID, occurrenceID, memberID,
		); err != nil {
			return err
		}
	}
	for _, memberID := range want {
		if _, ok := current[memberID]; ok {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO task_occurrence_assignees (id, family_id, task_occurrence_id, family_member_id, status, version)
			 VALUES (?, ?, ?, ?, 'to_do', 1)`,
			ids.New(), familyID, occurrenceID, memberID,
		); err != nil {
			return err
		}
	}
	return nil
}

// readScope checks that the caller may read tasks. It returns the family, and the member whose
// copies the caller may see. An empty member means every copy (tasks.view at full or view level).
// A caller with tasks.view at own level sees just its own copies. A caller without tasks.view gets 403.
func (h *Handlers) readScope(w http.ResponseWriter, r *http.Request) (familyID, scope string, ok bool) {
	familyID, _, ok = h.memberFamily(w, r)
	if !ok {
		return "", "", false
	}
	ctx := r.Context()
	memberID, _ := permissions.GetFamilyMemberID(ctx)

	policy := permissions.NewPolicy(h.db)
	all, err := policy.Check(ctx, memberID, permissions.PermTasksView)
	if err != nil {
		h.internalError(w, "check tasks.view", err)
		return "", "", false
	}
	if !all.Allowed {
		web.RespondError(w, http.StatusForbidden, "forbidden", "You do not have permission for this action", nil)
		return "", "", false
	}
	if all.AccessLevel != permissions.AccessOwn {
		return familyID, "", true
	}
	return familyID, memberID, true
}

// listConditions turns the list filters of the query string into SQL conditions. Each
// condition is appended after the WHERE clause of taskRowSelect. An unknown value is a
// FieldError, so a typo is reported and not ignored.
func listConditions(q url.Values) (string, []any, error) {
	var where strings.Builder
	var args []any
	add := func(condition string, arg any) {
		where.WriteString(" AND " + condition)
		args = append(args, arg)
	}

	if raw := q.Get("member"); raw != "" {
		id := strings.ToLower(strings.TrimSpace(raw))
		if !isUUID(id) {
			return "", nil, &FieldError{Field: "member", Reason: "invalid"}
		}
		add("oa.family_member_id = ?", id)
	}
	if raw := q.Get("status"); raw != "" {
		if !taskStatuses[raw] {
			return "", nil, &FieldError{Field: "status", Reason: "invalid"}
		}
		add("oa.status = ?", raw)
	}
	if raw := q.Get("category"); raw != "" {
		id := strings.ToLower(strings.TrimSpace(raw))
		if !isUUID(id) {
			return "", nil, &FieldError{Field: "category", Reason: "invalid"}
		}
		add("d.category_id = ?", id)
	}
	if raw := q.Get("priority"); raw != "" {
		if !priorities[raw] {
			return "", nil, &FieldError{Field: "priority", Reason: "invalid"}
		}
		add("d.priority = ?", raw)
	}
	if raw := q.Get("due_from"); raw != "" {
		date, err := parseDate("due_from", raw)
		if err != nil {
			return "", nil, err
		}
		add("o.due_date >= ?", date)
	}
	if raw := q.Get("due_to"); raw != "" {
		date, err := parseDate("due_to", raw)
		if err != nil {
			return "", nil, err
		}
		add("o.due_date <= ?", date)
	}
	switch q.Get("archived") {
	case "", "false":
		add("d.status = ?", statusActive)
	case "true":
		add("d.status = ?", statusArchived)
	default:
		return "", nil, &FieldError{Field: "archived", Reason: "invalid"}
	}
	switch q.Get("expired") {
	case "", "false":
		// Open copies whose occurrence was missed only appear in the expired list.
		where.WriteString(" AND NOT (o.status = 'missed' AND oa.status IN ('to_do', 'rejected'))")
	case "true":
		// An expired copy is open and its occurrence was missed. Other filters still apply.
		where.WriteString(" AND o.status = 'missed' AND oa.status IN ('to_do', 'rejected')")
	default:
		return "", nil, &FieldError{Field: "expired", Reason: "invalid"}
	}
	return where.String(), args, nil
}

// parseDate checks a YYYY-MM-DD date and returns it trimmed.
func parseDate(field, raw string) (string, error) {
	date := strings.TrimSpace(raw)
	if _, err := time.Parse(dateLayout, date); err != nil {
		return "", &FieldError{Field: field, Reason: "invalid"}
	}
	return date, nil
}
