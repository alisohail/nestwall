package tasks

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/platform/web"
)

// A one-off task (M3-04) is a task definition with no recurrence rule, one occurrence on its due
// date, and one assignee row per person. Each person has their own copy and completes and is
// approved on it separately. Recurring tasks are added in M3-05.

const (
	maxTaskTitle       = 200
	maxTaskDescription = 2000
	dateLayout         = "2006-01-02"
	dbTimeLayout       = "2006-01-02 15:04:05.000"
	statusActive       = "active"
	statusArchived     = "archived"
	taskStatusToDo     = "to_do"
)

// taskStatuses are the values an assignee's status may take.
var taskStatuses = map[string]bool{
	"to_do": true, "submitted": true, "approved": true, "rejected": true, "skipped": true,
}

// task is the API view of one task occurrence, whose ID is the task ID. It holds the shared fields
// and every assignee. The list view adds the one person's copy (see listItem).
type task struct {
	ID string `json:"id"`
	taskFields
	TemplateID  *string         `json:"template_id"`
	DueAt       string          `json:"due_at"`
	ExpiresAt   *string         `json:"expires_at"`
	Archived    bool            `json:"archived"`
	ArchivedAt  *string         `json:"archived_at,omitempty"`
	Repeats     bool            `json:"repeats"`
	AssigneeIDs []string        `json:"assignee_ids,omitempty"`
	Assignees   []assignment    `json:"assignees,omitempty"`
	Checklist   []checklistItem `json:"checklist,omitempty"`
	Version     int             `json:"version"`

	definitionID string
}

// assignment is one person on a task and where they are with it.
type assignment struct {
	MemberID string `json:"member_id"`
	Status   string `json:"status"`
}

// checklistItem is one step of a task occurrence's checklist.
type checklistItem struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	Done bool   `json:"done"`
}

// listItem is one person's copy of a task in the list. It carries that person's status.
type listItem struct {
	task
	MemberID string `json:"member_id"`
	Status   string `json:"status"`
}

// taskFields are the editable fields of a one-off task. On create a nil field takes the
// template, category or family default (see applyPrefill). On PATCH an absent field keeps its
// current value.
type taskFields struct {
	Title                 *string  `json:"title"`
	Description           *string  `json:"description"`
	CategoryID            *string  `json:"category_id"`
	PartOfDay             *string  `json:"part_of_day"`
	Priority              *string  `json:"priority"`
	DueDate               *string  `json:"due_date"`
	DueTime               *string  `json:"due_time"`
	ReminderMinutesBefore *int     `json:"reminder_minutes_before"`
	LatePointsPercent     *int     `json:"late_points_percent"`
	AssigneeIDs           []string `json:"assignee_ids"`
	Defaults
}

// createTaskRequest is the body of POST /api/v1/tasks.
type createTaskRequest struct {
	TemplateID *string `json:"template_id"`
	taskFields
}

// normalise trims the text, checks each field that is present, and drops duplicate assignees.
// It does not check required fields, because a template or default may still supply them.
func (f *taskFields) normalise() error {
	if err := trimText(&f.Title, "title", maxTaskTitle); err != nil {
		return err
	}
	if err := trimText(&f.Description, "description", maxTaskDescription); err != nil {
		return err
	}
	if f.CategoryID != nil {
		id := strings.ToLower(strings.TrimSpace(*f.CategoryID))
		if !isUUID(id) {
			return &FieldError{Field: "category_id", Reason: "invalid"}
		}
		f.CategoryID = &id
	}
	if f.PartOfDay != nil && !partsOfDay[*f.PartOfDay] {
		return &FieldError{Field: "part_of_day", Reason: "invalid"}
	}
	if f.Priority != nil && !priorities[*f.Priority] {
		return &FieldError{Field: "priority", Reason: "invalid"}
	}
	if f.DueDate != nil {
		date, err := parseDate("due_date", *f.DueDate)
		if err != nil {
			return err
		}
		f.DueDate = &date
	}
	if err := trimText(&f.DueTime, "due_time", 5); err != nil {
		return err
	}
	if f.DueTime != nil && !dueTimePattern.MatchString(*f.DueTime) {
		return &FieldError{Field: "due_time", Reason: "invalid"}
	}
	if f.ReminderMinutesBefore != nil && *f.ReminderMinutesBefore < 0 {
		return &FieldError{Field: "reminder_minutes_before", Reason: "negative"}
	}
	if f.LatePointsPercent != nil && (*f.LatePointsPercent < 0 || *f.LatePointsPercent > 100) {
		return &FieldError{Field: "late_points_percent", Reason: "out_of_range"}
	}

	members := []string{}
	seen := make(map[string]bool, len(f.AssigneeIDs))
	for _, id := range f.AssigneeIDs {
		id = strings.ToLower(strings.TrimSpace(id))
		if !isUUID(id) {
			return &FieldError{Field: "assignee_ids", Reason: "invalid"}
		}
		if !seen[id] {
			seen[id] = true
			members = append(members, id)
		}
	}
	f.AssigneeIDs = members
	return nil
}

// normalise checks the template reference and then the shared fields.
func (req *createTaskRequest) normalise() error {
	if req.TemplateID != nil {
		id := strings.ToLower(strings.TrimSpace(*req.TemplateID))
		if !isUUID(id) {
			return &FieldError{Field: "template_id", Reason: "invalid"}
		}
		req.TemplateID = &id
	}
	return req.taskFields.normalise()
}

// require runs after prefill. It checks the fields every task needs and fills priority and
// approval with their defaults. It then validates the defaults.
func (f *taskFields) require() error {
	switch {
	case f.Title == nil:
		return &FieldError{Field: "title", Reason: "required"}
	case f.DueDate == nil:
		return &FieldError{Field: "due_date", Reason: "required"}
	case f.Points == nil:
		return &FieldError{Field: "points", Reason: "required"}
	case len(f.AssigneeIDs) == 0:
		return &FieldError{Field: "assignee_ids", Reason: "required"}
	}
	if f.Priority == nil {
		normal := "normal"
		f.Priority = &normal
	}
	if f.ApprovalRequired == nil {
		required := true
		f.ApprovalRequired = &required
	}
	return f.Defaults.Validate()
}

// applyPrefill fills the fields the request left out. The order is template, then category,
// then family default. The request always wins over a default. A template's assignees are used
// only when the request gave none.
func applyPrefill(ctx context.Context, q querier, familyID string, req *createTaskRequest) error {
	f := &req.taskFields
	var fromTemplate Defaults
	if req.TemplateID != nil {
		tpl, err := loadTemplate(ctx, q, familyID, *req.TemplateID)
		if errors.Is(err, sql.ErrNoRows) {
			return &FieldError{Field: "template_id", Reason: "not_found"}
		}
		if err != nil {
			return err
		}
		// Bonus templates prefill bonus tasks only, never regular one-off tasks (PBI M4-10).
		if tpl.Kind != nil && *tpl.Kind == kindBonus {
			return &FieldError{Field: "template_id", Reason: "not_regular"}
		}
		f.Title = first(f.Title, tpl.Title)
		f.Description = first(f.Description, tpl.Description)
		f.CategoryID = first(f.CategoryID, tpl.CategoryID)
		f.PartOfDay = first(f.PartOfDay, tpl.PartOfDay)
		f.Priority = first(f.Priority, tpl.Priority)
		f.DueTime = first(f.DueTime, tpl.DueTime)
		f.ReminderMinutesBefore = first(f.ReminderMinutesBefore, tpl.ReminderMinutesBefore)
		f.LatePointsPercent = first(f.LatePointsPercent, tpl.LatePointsPercent)
		if len(f.AssigneeIDs) == 0 {
			f.AssigneeIDs = tpl.DefaultAssigneeIDs
		}
		fromTemplate = tpl.Defaults
	}

	var fromCategory Defaults
	if f.CategoryID != nil {
		c, err := loadCategory(ctx, q, familyID, *f.CategoryID)
		if errors.Is(err, sql.ErrNoRows) {
			return &FieldError{Field: "category_id", Reason: "not_found"}
		}
		if err != nil {
			return err
		}
		fromCategory = c.Defaults
	}

	family, _, err := loadSettings(ctx, q, familyID)
	if err != nil {
		return err
	}
	fillDefaults(&req.Defaults, ResolvePrefill(fromTemplate, fromCategory, family.defaults()))
	return nil
}

// fillDefaults sets each field the request left nil from the prefill. The penalty value and unit
// are taken together, and only when the request set neither of them.
func fillDefaults(d *Defaults, p Defaults) {
	d.Points = first(d.Points, p.Points)
	d.ApprovalRequired = first(d.ApprovalRequired, p.ApprovalRequired)
	d.ExpiresAfterMinutes = first(d.ExpiresAfterMinutes, p.ExpiresAfterMinutes)
	if d.PenaltyValue == nil && d.PenaltyUnit == nil {
		d.PenaltyValue = p.PenaltyValue
		d.PenaltyUnit = p.PenaltyUnit
	}
}

// checkTaskRefs confirms that the category belongs to the family and that every assignee is an
// active member of it.
func checkTaskRefs(ctx context.Context, q querier, familyID string, f taskFields) error {
	if f.CategoryID != nil {
		_, err := loadCategory(ctx, q, familyID, *f.CategoryID)
		if errors.Is(err, sql.ErrNoRows) {
			return &FieldError{Field: "category_id", Reason: "not_found"}
		}
		if err != nil {
			return err
		}
	}
	for _, memberID := range f.AssigneeIDs {
		var n int
		if err := q.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM family_members WHERE id = ? AND family_id = ? AND status = 'active'`,
			memberID, familyID,
		).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return &FieldError{Field: "assignee_ids", Reason: "not_found"}
		}
	}
	return nil
}

// dueInstant returns when a task is due, in UTC. The date and optional time are local to the
// family's zone. A task with no due time is due at the end of its day, 23:59:59 local.
func dueInstant(date string, dueTime *string, zone *time.Location) (time.Time, error) {
	day, err := time.ParseInLocation(dateLayout, date, zone)
	if err != nil {
		return time.Time{}, err
	}
	hour, minute, second := 23, 59, 59
	if dueTime != nil {
		clock, err := time.Parse("15:04", *dueTime)
		if err != nil {
			return time.Time{}, err
		}
		hour, minute, second = clock.Hour(), clock.Minute(), 0
	}
	return time.Date(day.Year(), day.Month(), day.Day(), hour, minute, second, 0, zone).UTC(), nil
}

// dbTime formats a UTC instant the way the schema stores timestamps, so that text order is time
// order.
func dbTime(t time.Time) string {
	return t.UTC().Format(dbTimeLayout) + "+00:00"
}

// occurrenceTimes returns the stored due and expiry text of an occurrence. A nil expiry means the
// occurrence never expires.
func occurrenceTimes(due time.Time, expires *time.Time) (string, *string) {
	dueText := dbTime(due)
	if expires == nil {
		return dueText, nil
	}
	expiresText := dbTime(*expires)
	return dueText, &expiresText
}

// fieldsOf turns a stored task back into request fields, so that a PATCH keeps every field it
// leaves out.
func fieldsOf(t task) taskFields {
	members := make([]string, 0, len(t.Assignees))
	for _, a := range t.Assignees {
		members = append(members, a.MemberID)
	}
	return taskFields{
		Title:                 t.Title,
		Description:           t.Description,
		CategoryID:            t.CategoryID,
		PartOfDay:             t.PartOfDay,
		Priority:              t.Priority,
		DueDate:               t.DueDate,
		DueTime:               t.DueTime,
		ReminderMinutesBefore: t.ReminderMinutesBefore,
		LatePointsPercent:     t.LatePointsPercent,
		AssigneeIDs:           members,
		Defaults: Defaults{
			Points:              t.Points,
			ApprovalRequired:    t.ApprovalRequired,
			ExpiresAfterMinutes: t.ExpiresAfterMinutes,
			PenaltyValue:        t.PenaltyValue,
			PenaltyUnit:         t.PenaltyUnit,
		},
	}
}

// insertDefinition writes a task definition. A nil rule makes a one-off definition. A rule makes
// a repeating one, and its occurrences are generated separately.
func insertDefinition(ctx context.Context, tx *sql.Tx, familyID, userID, zoneName, definitionID string, templateID *string, f taskFields, rule *string) error {
	var ruleArg any
	if rule != nil {
		ruleArg = *rule
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO task_definitions (id, family_id, title, description, category_id, part_of_day, priority,
			time_zone, starts_on, due_time, points, approval_required, expires_after_minutes,
			penalty_value, penalty_unit, late_points_percent, reminder_minutes_before, template_id, created_by,
			recurrence_rule)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		definitionID, familyID, *f.Title, f.Description, f.CategoryID, f.PartOfDay, *f.Priority,
		zoneName, *f.DueDate, f.DueTime, *f.Points, *f.ApprovalRequired, f.ExpiresAfterMinutes,
		f.PenaltyValue, f.PenaltyUnit, f.LatePointsPercent, f.ReminderMinutesBefore, templateID, userID,
		ruleArg,
	)
	return err
}

// insertDefinitionAssignees gives a definition its people. Generated occurrences copy them.
func insertDefinitionAssignees(ctx context.Context, tx *sql.Tx, familyID, definitionID string, members []string) error {
	for _, memberID := range members {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO task_definition_assignees (id, family_id, task_definition_id, family_member_id)
			 VALUES (?, ?, ?, ?)`,
			ids.New(), familyID, definitionID, memberID,
		); err != nil {
			return err
		}
	}
	return nil
}

// insertOneOff writes the definition, its occurrence and one to_do row for each assignee.
func insertOneOff(ctx context.Context, tx *sql.Tx, familyID, userID, zoneName, taskID, occurrenceID string, templateID *string, f taskFields, due time.Time, expires *time.Time) error {
	dueText, expiresText := occurrenceTimes(due, expires)
	if err := insertDefinition(ctx, tx, familyID, userID, zoneName, taskID, templateID, f, nil); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO task_occurrences (id, family_id, task_definition_id, due_date, due_at, expires_at, status)
		 VALUES (?, ?, ?, ?, ?, ?, 'scheduled')`,
		occurrenceID, familyID, taskID, *f.DueDate, dueText, expiresText,
	); err != nil {
		return err
	}

	for _, memberID := range f.AssigneeIDs {
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

// ListTasks returns one page of task copies, one per person, in due-time order. Filters narrow
// the list. A caller with tasks.view at own level sees only its own copies. The response has
// next_cursor, which is null on the last page.
func (h *Handlers) ListTasks(w http.ResponseWriter, r *http.Request) {
	familyID, scope, ok := h.readScope(w, r)
	if !ok {
		return
	}

	query := r.URL.Query()
	limit, err := parsePageSize(query.Get("limit"))
	if err != nil {
		respondValidation(w, err)
		return
	}
	where, filterArgs, err := listConditions(query)
	if err != nil {
		respondValidation(w, err)
		return
	}

	args := append([]any{familyID}, filterArgs...)
	if scope != "" {
		where += ` AND oa.family_member_id = ?`
		args = append(args, scope)
	}
	// The Missed list shows the most recent misses first; every other list runs earliest first.
	newestFirst := query.Get("expired") == "true"
	order, after := "ASC", ">"
	if newestFirst {
		order, after = "DESC", "<"
	}
	if raw := query.Get("cursor"); raw != "" {
		key, id, err := decodeCursor(raw)
		if err != nil {
			respondValidation(w, err)
			return
		}
		where += ` AND (CAST(o.due_at AS TEXT), oa.id) ` + after + ` (?, ?)`
		args = append(args, key, id)
	}
	// One extra row tells whether another page exists.
	args = append(args, limit+1)

	rows, err := queryTaskRows(r.Context(), h.db, where+` ORDER BY CAST(o.due_at AS TEXT) `+order+`, oa.id `+order+` LIMIT ?`, args...)
	if err != nil {
		h.internalError(w, "list tasks", err)
		return
	}

	var next any
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		next = encodeCursor(last.dueKey, last.assigneeID)
	}
	assigneeIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		assigneeIDs = append(assigneeIDs, row.assigneeID)
	}
	checklists, err := loadChecklists(r.Context(), h.db, familyID, assigneeIDs)
	if err != nil {
		h.internalError(w, "load checklists for list", err)
		return
	}
	items := make([]listItem, 0, len(rows))
	for _, row := range rows {
		t := row.task
		t.Checklist = checklists[row.assigneeID]
		items = append(items, listItem{task: t, MemberID: row.memberID, Status: row.status})
	}
	web.RespondJSON(w, http.StatusOK, map[string]any{"tasks": items, "next_cursor": next})
}

// GetTask returns one task with all its assignees and an ETag. The checklist is the copy of the
// member named in the member_id query, or the first copy when none is named. A caller with only
// tasks.view at own level gets 404 for a task that has no copy for it.
func (h *Handlers) GetTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	familyID, scope, ok := h.readScope(w, r)
	if !ok {
		return
	}
	if !isUUID(id) {
		notFound(w, "Task not found")
		return
	}
	member := strings.ToLower(r.URL.Query().Get("member_id"))
	if member != "" && !isUUID(member) {
		respondValidation(w, &FieldError{Field: "member_id", Reason: "invalid"})
		return
	}

	t, err := loadTaskCopy(r.Context(), h.db, familyID, id, scope, member)
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w, "Task not found")
		return
	}
	if err != nil {
		h.internalError(w, "load task", err)
		return
	}
	setETag(w, t.Version)
	web.RespondJSON(w, http.StatusOK, t)
}

// CreateTask creates a one-off task for the listed people. Template and category defaults fill
// the fields the request left out. Each assignee gets their own copy, to do.
func (h *Handlers) CreateTask(w http.ResponseWriter, r *http.Request) {
	familyID, userID, ok := h.memberFamily(w, r)
	if !ok {
		return
	}

	var req createTaskRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if err := req.normalise(); err != nil {
		respondValidation(w, err)
		return
	}

	ctx := r.Context()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "begin create task", err)
		return
	}
	defer tx.Rollback()

	if err := applyPrefill(ctx, tx, familyID, &req); err != nil {
		h.respondFailure(w, "prefill task", err)
		return
	}
	if err := req.require(); err != nil {
		respondValidation(w, err)
		return
	}
	if err := checkTaskRefs(ctx, tx, familyID, req.taskFields); err != nil {
		h.respondFailure(w, "check task references", err)
		return
	}

	zone, zoneName, err := familyZone(ctx, tx, familyID)
	if err != nil {
		h.internalError(w, "load family time zone", err)
		return
	}
	due, err := dueInstant(*req.DueDate, req.DueTime, zone)
	if err != nil {
		h.internalError(w, "compute due time", err)
		return
	}

	family, _, err := loadSettings(ctx, tx, familyID)
	if err != nil {
		h.internalError(w, "load family settings", err)
		return
	}
	expires := occurrenceExpiry(due, zone, req.ExpiresAfterMinutes, family.MissedAfter)

	taskID := ids.New()
	occurrenceID := ids.New()
	if err := insertOneOff(ctx, tx, familyID, userID, zoneName, taskID, occurrenceID, req.TemplateID, req.taskFields, due, expires); err != nil {
		h.internalError(w, "insert task", err)
		return
	}
	if err := audit.Record(ctx, tx, familyID, userID, "task.created", "task", &occurrenceID,
		map[string]any{"title": *req.Title, "assignees": len(req.AssigneeIDs)}); err != nil {
		h.internalError(w, "audit task created", err)
		return
	}

	created, err := loadTask(ctx, tx, familyID, occurrenceID, "")
	if err != nil {
		h.internalError(w, "reload task", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit create task", err)
		return
	}
	setETag(w, created.Version)
	web.RespondJSON(w, http.StatusCreated, created)
}

// UpdateTask changes some fields of a one-off task (PATCH). If-Match must match the current
// version when it is sent. Absent fields keep their value. When assignee_ids is sent the list is
// replaced, and a removed person's copy is deleted even if they already acted on it.
func (h *Handlers) UpdateTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	familyID, userID, ok := h.memberFamily(w, r)
	if !ok {
		return
	}
	if !isUUID(id) {
		notFound(w, "Task not found")
		return
	}

	ctx := r.Context()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "begin update task", err)
		return
	}
	defer tx.Rollback()

	current, err := loadTask(ctx, tx, familyID, id, "")
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w, "Task not found")
		return
	}
	if err != nil {
		h.internalError(w, "load task for update", err)
		return
	}
	if current.Archived {
		web.RespondError(w, http.StatusConflict, "task_archived", "This task is archived and cannot be changed", nil)
		return
	}
	if current.Repeats {
		web.RespondError(w, http.StatusConflict, "repeat_edit_unsupported", "A repeating task cannot be edited yet", nil)
		return
	}
	if !checkIfMatch(w, r, current.Version) {
		return
	}

	req := fieldsOf(current)
	if !decodeBody(w, r, &req) {
		return
	}
	if err := req.normalise(); err != nil {
		respondValidation(w, err)
		return
	}
	if err := req.require(); err != nil {
		respondValidation(w, err)
		return
	}
	if err := checkTaskRefs(ctx, tx, familyID, req); err != nil {
		h.respondFailure(w, "check task references", err)
		return
	}

	zone, zoneName, err := familyZone(ctx, tx, familyID)
	if err != nil {
		h.internalError(w, "load family time zone", err)
		return
	}
	due, err := dueInstant(*req.DueDate, req.DueTime, zone)
	if err != nil {
		h.internalError(w, "compute due time", err)
		return
	}
	family, _, err := loadSettings(ctx, tx, familyID)
	if err != nil {
		h.internalError(w, "load family settings", err)
		return
	}
	expires := occurrenceExpiry(due, zone, req.ExpiresAfterMinutes, family.MissedAfter)
	dueText, expiresText := occurrenceTimes(due, expires)

	res, err := tx.ExecContext(ctx,
		`UPDATE task_definitions SET title = ?, description = ?, category_id = ?, part_of_day = ?,
			priority = ?, time_zone = ?, starts_on = ?, due_time = ?, points = ?, approval_required = ?,
			expires_after_minutes = ?, penalty_value = ?, penalty_unit = ?, late_points_percent = ?,
			reminder_minutes_before = ?, version = version + 1,
			updated_at = strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')
		 WHERE id = ? AND family_id = ? AND version = ?`,
		*req.Title, req.Description, req.CategoryID, req.PartOfDay,
		*req.Priority, zoneName, *req.DueDate, req.DueTime, *req.Points, *req.ApprovalRequired,
		req.ExpiresAfterMinutes, req.PenaltyValue, req.PenaltyUnit, req.LatePointsPercent,
		req.ReminderMinutesBefore,
		current.definitionID, familyID, current.Version,
	)
	if err != nil {
		h.internalError(w, "update task", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		web.RespondError(w, http.StatusPreconditionFailed, "version_conflict", "The task changed since you loaded it", nil)
		return
	}

	if err := setAssignees(ctx, tx, familyID, current.definitionID, req.AssigneeIDs); err != nil {
		h.internalError(w, "update task assignees", err)
		return
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE task_occurrences SET due_date = ?, due_at = ?, expires_at = ?, version = version + 1,
			updated_at = strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')
		 WHERE family_id = ? AND id = ?`,
		*req.DueDate, dueText, expiresText, familyID, id,
	); err != nil {
		h.internalError(w, "update task occurrence", err)
		return
	}
	if err := audit.Record(ctx, tx, familyID, userID, "task.updated", "task", &id,
		map[string]any{"assignees": len(req.AssigneeIDs)}); err != nil {
		h.internalError(w, "audit task updated", err)
		return
	}

	updated, err := loadTask(ctx, tx, familyID, id, "")
	if err != nil {
		h.internalError(w, "reload task", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit update task", err)
		return
	}
	setETag(w, updated.Version)
	web.RespondJSON(w, http.StatusOK, updated)
}

// ArchiveTask archives a one-off task. An archived task is hidden from the default list and is
// read-only. Archiving an archived task returns 409.
func (h *Handlers) ArchiveTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	familyID, userID, ok := h.memberFamily(w, r)
	if !ok {
		return
	}
	if !isUUID(id) {
		notFound(w, "Task not found")
		return
	}

	ctx := r.Context()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "begin archive task", err)
		return
	}
	defer tx.Rollback()

	current, err := loadTask(ctx, tx, familyID, id, "")
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w, "Task not found")
		return
	}
	if err != nil {
		h.internalError(w, "load task for archive", err)
		return
	}
	if current.Archived {
		web.RespondError(w, http.StatusConflict, "task_archived", "This task is already archived", nil)
		return
	}
	if !checkIfMatch(w, r, current.Version) {
		return
	}

	if err := archiveDefinition(ctx, tx, familyID, userID, current); errors.Is(err, errVersionChanged) {
		web.RespondError(w, http.StatusPreconditionFailed, "version_conflict", "The task changed since you loaded it", nil)
		return
	} else if err != nil {
		h.internalError(w, "archive task", err)
		return
	}

	archived, err := loadTask(ctx, tx, familyID, id, "")
	if err != nil {
		h.internalError(w, "reload archived task", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit archive task", err)
		return
	}
	setETag(w, archived.Version)
	web.RespondJSON(w, http.StatusOK, archived)
}

// errVersionChanged means the task's version moved on between the load and the write.
var errVersionChanged = errors.New("task version changed")

// archiveDefinition archives the task's definition, which hides every copy from everyone, and
// records the audit entry. The caller has checked that the task is not archived and that its
// If-Match version is current.
func archiveDefinition(ctx context.Context, tx *sql.Tx, familyID, userID string, current task) error {
	res, err := tx.ExecContext(ctx,
		`UPDATE task_definitions SET status = 'archived', version = version + 1,
			updated_at = strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')
		 WHERE id = ? AND family_id = ? AND version = ?`,
		current.definitionID, familyID, current.Version,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errVersionChanged
	}
	return audit.Record(ctx, tx, familyID, userID, "task.archived", "task", &current.ID, nil)
}
