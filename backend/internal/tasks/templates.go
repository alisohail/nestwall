package tasks

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/platform/web"
)

// Task templates are reusable presets for new tasks (PBI M3-03). A template is not a task: it
// only supplies prefill values, and changing it never changes a task created from it.

const (
	maxTemplateName        = 80
	maxTemplateTitle       = 200
	maxTemplateDescription = 2000
	defaultPageSize        = 50
	maxPageSize            = 200
)

var (
	dueTimePattern = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
	partsOfDay     = map[string]bool{"anytime": true, "morning": true, "after_school": true, "evening": true}
	priorities     = map[string]bool{"low": true, "normal": true, "high": true}
)

// Template kinds. A bonus template prefills a bonus task (PBI M4-10). It never carries a penalty.
const (
	kindRegular = "regular"
	kindBonus   = "bonus"
)

// templateColumns lists the columns that scanTemplate reads, in order. The last column is the
// lower-cased name, which the list cursor sorts on.
const templateColumns = `id, kind, name, title, description, category_id, part_of_day, priority, due_time,
	reminder_minutes_before, late_points_percent, default_points, approval_required,
	expires_after_minutes, penalty_value, penalty_unit, version, lower(name)`

// template is the API view of a task template. Its defaults are flattened into the object.
type template struct {
	ID string `json:"id"`
	templateFields
	Version int `json:"version"`
}

// templateFields are the editable fields of a template. A nil field means "do not prefill" and
// is stored as NULL. On PATCH an absent field keeps its value and an explicit null clears it.
type templateFields struct {
	Kind                  *string  `json:"kind"`
	Name                  *string  `json:"name"`
	CategoryID            *string  `json:"category_id"`
	Title                 *string  `json:"title"`
	Description           *string  `json:"description"`
	PartOfDay             *string  `json:"part_of_day"`
	Priority              *string  `json:"priority"`
	DueTime               *string  `json:"due_time"`
	ReminderMinutesBefore *int     `json:"reminder_minutes_before"`
	LatePointsPercent     *int     `json:"late_points_percent"`
	DefaultAssigneeIDs    []string `json:"default_assignee_ids"`
	Defaults
}

// normalise trims the text fields, checks every field and drops duplicate assignees. It runs
// before any database work. Blank optional text is stored as NULL.
func (f *templateFields) normalise() error {
	if err := f.normaliseKind(); err != nil {
		return err
	}
	if f.Name == nil {
		return &FieldError{Field: "name", Reason: "required"}
	}
	name := strings.TrimSpace(*f.Name)
	switch {
	case name == "":
		return &FieldError{Field: "name", Reason: "required"}
	case utf8.RuneCountInString(name) > maxTemplateName:
		return &FieldError{Field: "name", Reason: "too_long"}
	}
	f.Name = &name

	if err := trimText(&f.Title, "title", maxTemplateTitle); err != nil {
		return err
	}
	if err := trimText(&f.Description, "description", maxTemplateDescription); err != nil {
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
	seen := make(map[string]bool, len(f.DefaultAssigneeIDs))
	for _, id := range f.DefaultAssigneeIDs {
		id = strings.ToLower(id)
		if !isUUID(id) {
			return &FieldError{Field: "default_assignee_ids", Reason: "invalid"}
		}
		if !seen[id] {
			seen[id] = true
			members = append(members, id)
		}
	}
	f.DefaultAssigneeIDs = members

	return f.Defaults.Validate()
}

// normaliseKind defaults the kind to regular and checks it. A bonus template cannot carry a
// penalty, because a bonus task never penalises (PBI M4-10).
func (f *templateFields) normaliseKind() error {
	if f.Kind == nil {
		kind := kindRegular
		f.Kind = &kind
	}
	switch *f.Kind {
	case kindRegular:
		return nil
	case kindBonus:
		if f.PenaltyValue != nil || f.PenaltyUnit != nil {
			return &FieldError{Field: "penalty_value", Reason: "not_allowed_for_bonus"}
		}
		return nil
	}
	return &FieldError{Field: "kind", Reason: "invalid"}
}

// columnArgs returns the values of the editable columns, in the order the INSERT and UPDATE
// statements use.
func (f templateFields) columnArgs() []any {
	return []any{f.Name, f.Title, f.Description, f.CategoryID, f.PartOfDay, f.Priority, f.DueTime,
		f.ReminderMinutesBefore, f.LatePointsPercent, f.Points, f.ApprovalRequired,
		f.ExpiresAfterMinutes, f.PenaltyValue, f.PenaltyUnit}
}

// trimText trims an optional text field in place. Blank text becomes nil.
func trimText(field **string, name string, max int) error {
	if *field == nil {
		return nil
	}
	text := strings.TrimSpace(**field)
	if utf8.RuneCountInString(text) > max {
		return &FieldError{Field: name, Reason: "too_long"}
	}
	if text == "" {
		*field = nil
		return nil
	}
	*field = &text
	return nil
}

// checkReferences confirms that the category belongs to the family and that every default
// assignee is an active member of it. A reference outside the family returns a FieldError.
func checkReferences(ctx context.Context, q querier, familyID string, f templateFields) error {
	if f.CategoryID != nil {
		_, err := loadCategory(ctx, q, familyID, *f.CategoryID)
		if errors.Is(err, sql.ErrNoRows) {
			return &FieldError{Field: "category_id", Reason: "not_found"}
		}
		if err != nil {
			return err
		}
	}
	for _, memberID := range f.DefaultAssigneeIDs {
		var n int
		if err := q.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM family_members WHERE id = ? AND family_id = ? AND status = 'active'`,
			memberID, familyID,
		).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return &FieldError{Field: "default_assignee_ids", Reason: "not_found"}
		}
	}
	return nil
}

// scanTemplate reads a row in templateColumns order. key receives the lower-cased name.
func scanTemplate(s rowScanner, t *template, key *string) error {
	return s.Scan(&t.ID, &t.Kind, &t.Name, &t.Title, &t.Description, &t.CategoryID, &t.PartOfDay, &t.Priority, &t.DueTime,
		&t.ReminderMinutesBefore, &t.LatePointsPercent, &t.Points, &t.ApprovalRequired,
		&t.ExpiresAfterMinutes, &t.PenaltyValue, &t.PenaltyUnit, &t.Version, key)
}

// loadTemplate reads one template of the family with its default assignees. It returns
// sql.ErrNoRows when the template is not in that family.
func loadTemplate(ctx context.Context, q querier, familyID, id string) (template, error) {
	var t template
	var key string
	row := q.QueryRowContext(ctx,
		`SELECT `+templateColumns+` FROM task_templates WHERE id = ? AND family_id = ?`, id, familyID)
	if err := scanTemplate(row, &t, &key); err != nil {
		return template{}, err
	}
	members, err := loadAssignees(ctx, q, familyID, id)
	if err != nil {
		return template{}, err
	}
	t.DefaultAssigneeIDs = members
	return t, nil
}

// loadAssignees returns the default assignee IDs of a template, sorted.
func loadAssignees(ctx context.Context, q querier, familyID, templateID string) ([]string, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT family_member_id FROM task_template_assignees
		 WHERE family_id = ? AND task_template_id = ? ORDER BY family_member_id`,
		familyID, templateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	list := []string{}
	for rows.Next() {
		var memberID string
		if err := rows.Scan(&memberID); err != nil {
			return nil, err
		}
		list = append(list, memberID)
	}
	return list, rows.Err()
}

// saveAssignees replaces the default assignees of a template.
func saveAssignees(ctx context.Context, tx *sql.Tx, familyID, templateID string, memberIDs []string) error {
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM task_template_assignees WHERE family_id = ? AND task_template_id = ?`,
		familyID, templateID,
	); err != nil {
		return err
	}
	for _, memberID := range memberIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO task_template_assignees (id, family_id, task_template_id, family_member_id)
			 VALUES (?, ?, ?, ?)`,
			ids.New(), familyID, templateID, memberID,
		); err != nil {
			return err
		}
	}
	return nil
}

// listTemplateRows reads templates and their sort keys, and closes the rows before it returns.
func listTemplateRows(ctx context.Context, q querier, query string, args ...any) ([]template, []string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	list := []template{}
	var keys []string
	for rows.Next() {
		var t template
		var key string
		if err := scanTemplate(rows, &t, &key); err != nil {
			return nil, nil, err
		}
		list = append(list, t)
		keys = append(keys, key)
	}
	return list, keys, rows.Err()
}

// parsePageSize reads the limit query parameter. It defaults to defaultPageSize.
func parsePageSize(raw string) (int, error) {
	if raw == "" {
		return defaultPageSize, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxPageSize {
		return 0, &FieldError{Field: "limit", Reason: "invalid"}
	}
	return n, nil
}

// encodeCursor builds the opaque cursor that points just after a template in name order.
func encodeCursor(key, id string) string {
	raw, _ := json.Marshal([]string{key, id})
	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeCursor reverses encodeCursor. It returns a FieldError for a cursor it did not issue.
func decodeCursor(raw string) (key, id string, err error) {
	bad := &FieldError{Field: "cursor", Reason: "invalid"}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return "", "", bad
	}
	var parts []string
	if err := json.Unmarshal(data, &parts); err != nil || len(parts) != 2 || !isUUID(parts[1]) {
		return "", "", bad
	}
	return parts[0], parts[1], nil
}

// respondFailure writes a 400 for a FieldError, and a 500 for any other error.
func (h *Handlers) respondFailure(w http.ResponseWriter, what string, err error) {
	var fieldErr *FieldError
	if errors.As(err, &fieldErr) {
		respondValidation(w, err)
		return
	}
	h.internalError(w, what, err)
}

// ListTemplates returns one page of the family's templates in name order. The response has
// next_cursor, which is null on the last page.
func (h *Handlers) ListTemplates(w http.ResponseWriter, r *http.Request) {
	familyID, _, ok := h.memberFamily(w, r)
	if !ok {
		return
	}

	query := r.URL.Query()
	limit, err := parsePageSize(query.Get("limit"))
	if err != nil {
		respondValidation(w, err)
		return
	}

	kind := query.Get("kind")
	if kind == "" {
		kind = kindRegular
	}
	if kind != kindRegular && kind != kindBonus {
		respondValidation(w, &FieldError{Field: "kind", Reason: "invalid"})
		return
	}

	args := []any{familyID, kind}
	where := ""
	if raw := query.Get("cursor"); raw != "" {
		key, id, err := decodeCursor(raw)
		if err != nil {
			respondValidation(w, err)
			return
		}
		where = ` AND (lower(name), id) > (?, ?)`
		args = append(args, key, id)
	}
	// One extra row tells whether another page exists.
	args = append(args, limit+1)

	ctx := r.Context()
	list, keys, err := listTemplateRows(ctx, h.db,
		`SELECT `+templateColumns+` FROM task_templates WHERE family_id = ? AND kind = ?`+where+
			` ORDER BY lower(name), id LIMIT ?`,
		args...)
	if err != nil {
		h.internalError(w, "list templates", err)
		return
	}

	var next any
	if len(list) > limit {
		list = list[:limit]
		next = encodeCursor(keys[limit-1], list[limit-1].ID)
	}
	for i := range list {
		members, err := loadAssignees(ctx, h.db, familyID, list[i].ID)
		if err != nil {
			h.internalError(w, "load template assignees", err)
			return
		}
		list[i].DefaultAssigneeIDs = members
	}
	web.RespondJSON(w, http.StatusOK, map[string]any{"templates": list, "next_cursor": next})
}

// GetTemplate returns one template with its ETag.
func (h *Handlers) GetTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	familyID, _, ok := h.memberFamily(w, r)
	if !ok {
		return
	}
	if !isUUID(id) {
		notFound(w, "Template not found")
		return
	}

	t, err := loadTemplate(r.Context(), h.db, familyID, id)
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w, "Template not found")
		return
	}
	if err != nil {
		h.internalError(w, "load template", err)
		return
	}
	setETag(w, t.Version)
	web.RespondJSON(w, http.StatusOK, t)
}

// CreateTemplate adds a template. Only name is required. It returns 201 with the new template and its ETag.
func (h *Handlers) CreateTemplate(w http.ResponseWriter, r *http.Request) {
	familyID, userID, ok := h.memberFamily(w, r)
	if !ok {
		return
	}

	var req templateFields
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
		h.internalError(w, "begin create template", err)
		return
	}
	defer tx.Rollback()

	if err := checkReferences(ctx, tx, familyID, req); err != nil {
		h.respondFailure(w, "check template references", err)
		return
	}

	id := ids.New()
	args := append([]any{id, familyID, *req.Kind}, req.columnArgs()...)
	args = append(args, userID)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO task_templates (
			id, family_id, kind, name, title, description, category_id, part_of_day, priority, due_time,
			reminder_minutes_before, late_points_percent, default_points, approval_required,
			expires_after_minutes, penalty_value, penalty_unit, created_by
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		args...,
	); err != nil {
		h.internalError(w, "insert template", err)
		return
	}
	if err := saveAssignees(ctx, tx, familyID, id, req.DefaultAssigneeIDs); err != nil {
		h.internalError(w, "save template assignees", err)
		return
	}

	if err := audit.Record(ctx, tx, familyID, userID, "task_template.created", "task_template", &id,
		map[string]any{"name": *req.Name}); err != nil {
		h.internalError(w, "audit create template", err)
		return
	}

	created, err := loadTemplate(ctx, tx, familyID, id)
	if err != nil {
		h.internalError(w, "reload template", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit create template", err)
		return
	}

	setETag(w, created.Version)
	web.RespondJSON(w, http.StatusCreated, created)
}

// UpdateTemplate changes some fields of a template (PATCH). If-Match must match the current
// version when sent. Tasks created earlier from the template keep their values.
func (h *Handlers) UpdateTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	familyID, userID, ok := h.memberFamily(w, r)
	if !ok {
		return
	}
	if !isUUID(id) {
		notFound(w, "Template not found")
		return
	}

	ctx := r.Context()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "begin update template", err)
		return
	}
	defer tx.Rollback()

	current, err := loadTemplate(ctx, tx, familyID, id)
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w, "Template not found")
		return
	}
	if err != nil {
		h.internalError(w, "load template", err)
		return
	}
	if !checkIfMatch(w, r, current.Version) {
		return
	}

	// Start from the stored values, so that fields absent from the body keep them. The kind is
	// copied first: decoding into the shared pointer would overwrite the stored value.
	currentKind := *current.Kind
	req := current.templateFields
	if !decodeBody(w, r, &req) {
		return
	}
	if err := req.normalise(); err != nil {
		respondValidation(w, err)
		return
	}
	if *req.Kind != currentKind {
		respondValidation(w, &FieldError{Field: "kind", Reason: "immutable"})
		return
	}
	if err := checkReferences(ctx, tx, familyID, req); err != nil {
		h.respondFailure(w, "check template references", err)
		return
	}

	args := append(req.columnArgs(), id, familyID, current.Version)
	res, err := tx.ExecContext(ctx,
		`UPDATE task_templates SET
			name = ?, title = ?, description = ?, category_id = ?, part_of_day = ?, priority = ?, due_time = ?,
			reminder_minutes_before = ?, late_points_percent = ?, default_points = ?, approval_required = ?,
			expires_after_minutes = ?, penalty_value = ?, penalty_unit = ?,
			version = version + 1, updated_at = strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')
		WHERE id = ? AND family_id = ? AND version = ?`,
		args...,
	)
	if err != nil {
		h.internalError(w, "update template", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// The version changed between the read and the write.
		web.RespondError(w, http.StatusPreconditionFailed, "version_conflict",
			"This changed since you loaded it. Reload and try again.", nil)
		return
	}
	if err := saveAssignees(ctx, tx, familyID, id, req.DefaultAssigneeIDs); err != nil {
		h.internalError(w, "save template assignees", err)
		return
	}

	if err := audit.Record(ctx, tx, familyID, userID, "task_template.updated", "task_template", &id,
		map[string]any{"name": *req.Name}); err != nil {
		h.internalError(w, "audit update template", err)
		return
	}

	updated, err := loadTemplate(ctx, tx, familyID, id)
	if err != nil {
		h.internalError(w, "reload template", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit update template", err)
		return
	}

	setETag(w, updated.Version)
	web.RespondJSON(w, http.StatusOK, updated)
}

// DeleteTemplate removes a template that no task was created from. When tasks exist it returns
// 409 template_in_use with the count, because the tasks refer to the template.
func (h *Handlers) DeleteTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	familyID, userID, ok := h.memberFamily(w, r)
	if !ok {
		return
	}
	if !isUUID(id) {
		notFound(w, "Template not found")
		return
	}

	ctx := r.Context()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "begin delete template", err)
		return
	}
	defer tx.Rollback()

	current, err := loadTemplate(ctx, tx, familyID, id)
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w, "Template not found")
		return
	}
	if err != nil {
		h.internalError(w, "load template", err)
		return
	}
	if !checkIfMatch(w, r, current.Version) {
		return
	}

	var tasks int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM task_definitions WHERE family_id = ? AND template_id = ?`, familyID, id,
	).Scan(&tasks); err != nil {
		h.internalError(w, "count template uses", err)
		return
	}
	if tasks > 0 {
		web.RespondError(w, http.StatusConflict, "template_in_use",
			"Tasks were created from this template, so it cannot be deleted.",
			map[string]any{"tasks": tasks})
		return
	}

	res, err := tx.ExecContext(ctx,
		`DELETE FROM task_templates WHERE id = ? AND family_id = ? AND version = ?`,
		id, familyID, current.Version)
	if err != nil {
		h.internalError(w, "delete template", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		web.RespondError(w, http.StatusPreconditionFailed, "version_conflict",
			"This changed since you loaded it. Reload and try again.", nil)
		return
	}

	if err := audit.Record(ctx, tx, familyID, userID, "task_template.deleted", "task_template", &id,
		map[string]any{"name": *current.Name}); err != nil {
		h.internalError(w, "audit delete template", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit delete template", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
