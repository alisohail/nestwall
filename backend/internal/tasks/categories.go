package tasks

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/platform/web"
)

const (
	maxCategoryName  = 80
	maxCategoryEmoji = 16
)

var (
	colourPattern   = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	badgeKeyPattern = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)
	// singleEmojiPattern is one emoji: a flag pair, or a symbol with an optional skin tone or
	// variation selector, joined by zero-width joiners. It mirrors isSingleEmoji in the frontend.
	singleEmojiPattern = regexp.MustCompile(`^(?:[\x{1F1E6}-\x{1F1FF}]{2}|\p{So}(?:\x{FE0F}|[\x{1F3FB}-\x{1F3FF}])?(?:\x{200D}\p{So}(?:\x{FE0F}|[\x{1F3FB}-\x{1F3FF}])?)*)$`)
)

// category is the API view of a task category. Its defaults are flattened into the object.
type category struct {
	ID        string  `json:"id"`
	SeedKey   *string `json:"seed_key"`
	Name      string  `json:"name"`
	Emoji     string  `json:"emoji"`
	Colour    string  `json:"colour"`
	SortOrder int     `json:"sort_order"`
	BadgeKey  *string `json:"badge_key"`
	Defaults
	Version int `json:"version"`
}

// categoryRequest is the body for create and update (PUT replaces the whole category).
// A nil sort_order on update keeps the current order; on create it appends.
type categoryRequest struct {
	Name      string  `json:"name"`
	Emoji     string  `json:"emoji"`
	Colour    string  `json:"colour"`
	SortOrder *int    `json:"sort_order"`
	BadgeKey  *string `json:"badge_key"`
	Defaults
}

// normalise trims the text fields and checks them. It must run before any database work.
func (req *categoryRequest) normalise() error {
	req.Name = strings.TrimSpace(req.Name)
	req.Emoji = strings.TrimSpace(req.Emoji)
	req.Colour = strings.TrimSpace(req.Colour)

	switch {
	case req.Name == "":
		return &FieldError{Field: "name", Reason: "required"}
	case utf8.RuneCountInString(req.Name) > maxCategoryName:
		return &FieldError{Field: "name", Reason: "too_long"}
	case req.Emoji == "":
		return &FieldError{Field: "emoji", Reason: "required"}
	case utf8.RuneCountInString(req.Emoji) > maxCategoryEmoji:
		return &FieldError{Field: "emoji", Reason: "too_long"}
	case !singleEmojiPattern.MatchString(req.Emoji):
		return &FieldError{Field: "emoji", Reason: "invalid"}
	case !colourPattern.MatchString(req.Colour):
		return &FieldError{Field: "colour", Reason: "invalid"}
	case req.SortOrder != nil && *req.SortOrder < 0:
		return &FieldError{Field: "sort_order", Reason: "negative"}
	case req.BadgeKey != nil && !badgeKeyPattern.MatchString(*req.BadgeKey):
		return &FieldError{Field: "badge_key", Reason: "invalid"}
	}
	return req.Defaults.Validate()
}

// categoryColumns lists the columns that scanCategory reads, in order.
const categoryColumns = `id, seed_key, name, emoji, colour, sort_order, badge_key,
	default_points, default_approval_required, default_expires_after_minutes,
	default_penalty_value, default_penalty_unit, version`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanCategory(s rowScanner, c *category) error {
	return s.Scan(&c.ID, &c.SeedKey, &c.Name, &c.Emoji, &c.Colour, &c.SortOrder, &c.BadgeKey,
		&c.Points, &c.ApprovalRequired, &c.ExpiresAfterMinutes,
		&c.PenaltyValue, &c.PenaltyUnit, &c.Version)
}

// loadCategory reads one category of the family. It returns sql.ErrNoRows when the category
// does not exist in that family, so another family's category is never visible.
func loadCategory(ctx context.Context, q querier, familyID, id string) (category, error) {
	var c category
	row := q.QueryRowContext(ctx,
		`SELECT `+categoryColumns+` FROM task_categories WHERE id = ? AND family_id = ?`, id, familyID)
	err := scanCategory(row, &c)
	return c, err
}

// nameTaken reports whether another category of the family already uses the name, ignoring case.
// exceptID is the category being updated (empty on create).
func nameTaken(ctx context.Context, q querier, familyID, exceptID, name string) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM task_categories WHERE family_id = ? AND lower(name) = lower(?) AND id <> ?`,
		familyID, name, exceptID,
	).Scan(&n)
	return n > 0, err
}

// ListCategories returns the family's categories in display order.
func (h *Handlers) ListCategories(w http.ResponseWriter, r *http.Request) {
	familyID, _, ok := h.memberFamily(w, r)
	if !ok {
		return
	}

	rows, err := h.db.QueryContext(r.Context(),
		`SELECT `+categoryColumns+` FROM task_categories WHERE family_id = ? ORDER BY sort_order, lower(name)`,
		familyID)
	if err != nil {
		h.internalError(w, "list categories", err)
		return
	}
	defer rows.Close()

	list := []category{}
	for rows.Next() {
		var c category
		if err := scanCategory(rows, &c); err != nil {
			h.internalError(w, "scan category", err)
			return
		}
		list = append(list, c)
	}
	if err := rows.Err(); err != nil {
		h.internalError(w, "list categories", err)
		return
	}
	web.RespondJSON(w, http.StatusOK, map[string]any{"categories": list})
}

// CreateCategory adds a family category. It returns 201 with the new category and its ETag.
func (h *Handlers) CreateCategory(w http.ResponseWriter, r *http.Request) {
	familyID, userID, ok := h.memberFamily(w, r)
	if !ok {
		return
	}

	var req categoryRequest
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
		h.internalError(w, "begin create category", err)
		return
	}
	defer tx.Rollback()

	taken, err := nameTaken(ctx, tx, familyID, "", req.Name)
	if err != nil {
		h.internalError(w, "check category name", err)
		return
	}
	if taken {
		web.RespondError(w, http.StatusConflict, "name_taken", "A category with this name already exists",
			map[string]any{"field": "name"})
		return
	}

	sortOrder := 0
	if req.SortOrder != nil {
		sortOrder = *req.SortOrder
	} else if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(sort_order), 0) + 1 FROM task_categories WHERE family_id = ?`, familyID,
	).Scan(&sortOrder); err != nil {
		h.internalError(w, "next category sort order", err)
		return
	}

	id := ids.New()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO task_categories (
			id, family_id, name, emoji, colour, sort_order, badge_key,
			default_points, default_approval_required, default_expires_after_minutes,
			default_penalty_value, default_penalty_unit
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, familyID, req.Name, req.Emoji, req.Colour, sortOrder, req.BadgeKey,
		req.Points, req.ApprovalRequired, req.ExpiresAfterMinutes, req.PenaltyValue, req.PenaltyUnit,
	)
	if err != nil {
		h.internalError(w, "insert category", err)
		return
	}

	if err := audit.Record(ctx, tx, familyID, userID, "task_category.created", "task_category", &id,
		map[string]any{"name": req.Name}); err != nil {
		h.internalError(w, "audit create category", err)
		return
	}

	created, err := loadCategory(ctx, tx, familyID, id)
	if err != nil {
		h.internalError(w, "reload category", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit create category", err)
		return
	}

	setETag(w, created.Version)
	web.RespondJSON(w, http.StatusCreated, created)
}

// UpdateCategory replaces a category. If-Match must match the current version when sent.
func (h *Handlers) UpdateCategory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	familyID, userID, ok := h.memberFamily(w, r)
	if !ok {
		return
	}
	if !isUUID(id) {
		notFound(w, "Category not found")
		return
	}

	var req categoryRequest
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
		h.internalError(w, "begin update category", err)
		return
	}
	defer tx.Rollback()

	current, err := loadCategory(ctx, tx, familyID, id)
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w, "Category not found")
		return
	}
	if err != nil {
		h.internalError(w, "load category", err)
		return
	}
	if !checkIfMatch(w, r, current.Version) {
		return
	}

	taken, err := nameTaken(ctx, tx, familyID, id, req.Name)
	if err != nil {
		h.internalError(w, "check category name", err)
		return
	}
	if taken {
		web.RespondError(w, http.StatusConflict, "name_taken", "A category with this name already exists",
			map[string]any{"field": "name"})
		return
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE task_categories SET
			name = ?, emoji = ?, colour = ?, sort_order = COALESCE(?, sort_order), badge_key = ?,
			default_points = ?, default_approval_required = ?, default_expires_after_minutes = ?,
			default_penalty_value = ?, default_penalty_unit = ?,
			version = version + 1, updated_at = strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')
		WHERE id = ? AND family_id = ? AND version = ?`,
		req.Name, req.Emoji, req.Colour, req.SortOrder, req.BadgeKey,
		req.Points, req.ApprovalRequired, req.ExpiresAfterMinutes,
		req.PenaltyValue, req.PenaltyUnit,
		id, familyID, current.Version,
	)
	if err != nil {
		h.internalError(w, "update category", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// The version changed between the read and the write.
		web.RespondError(w, http.StatusPreconditionFailed, "version_conflict",
			"This changed since you loaded it. Reload and try again.", nil)
		return
	}

	if err := audit.Record(ctx, tx, familyID, userID, "task_category.updated", "task_category", &id,
		map[string]any{"name": req.Name}); err != nil {
		h.internalError(w, "audit update category", err)
		return
	}

	updated, err := loadCategory(ctx, tx, familyID, id)
	if err != nil {
		h.internalError(w, "reload category", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit update category", err)
		return
	}

	setETag(w, updated.Version)
	web.RespondJSON(w, http.StatusOK, updated)
}

// DeleteCategory removes a category. A category that a template or task uses cannot be deleted
// until its items are moved: the request must pass move_to with another category of the family.
// Without it the response is 409 category_in_use with the counts.
func (h *Handlers) DeleteCategory(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	familyID, userID, ok := h.memberFamily(w, r)
	if !ok {
		return
	}
	if !isUUID(id) {
		notFound(w, "Category not found")
		return
	}

	ctx := r.Context()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "begin delete category", err)
		return
	}
	defer tx.Rollback()

	current, err := loadCategory(ctx, tx, familyID, id)
	if errors.Is(err, sql.ErrNoRows) {
		notFound(w, "Category not found")
		return
	}
	if err != nil {
		h.internalError(w, "load category", err)
		return
	}
	if !checkIfMatch(w, r, current.Version) {
		return
	}

	var templates, tasks int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM task_templates WHERE family_id = ? AND category_id = ?`, familyID, id,
	).Scan(&templates); err != nil {
		h.internalError(w, "count template uses", err)
		return
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM task_definitions WHERE family_id = ? AND category_id = ?`, familyID, id,
	).Scan(&tasks); err != nil {
		h.internalError(w, "count task uses", err)
		return
	}

	moveTo := ""
	if templates+tasks > 0 {
		moveTo = r.URL.Query().Get("move_to")
		if moveTo == "" {
			web.RespondError(w, http.StatusConflict, "category_in_use",
				"This category is used by templates or tasks. Choose another category to move them to.",
				map[string]any{"templates": templates, "tasks": tasks})
			return
		}
		if !isUUID(moveTo) || moveTo == id {
			respondValidation(w, &FieldError{Field: "move_to", Reason: "invalid"})
			return
		}

		var exists int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM task_categories WHERE id = ? AND family_id = ?`, moveTo, familyID,
		).Scan(&exists); err != nil {
			h.internalError(w, "check move target", err)
			return
		}
		if exists == 0 {
			respondValidation(w, &FieldError{Field: "move_to", Reason: "not_found"})
			return
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE task_templates SET category_id = ? WHERE family_id = ? AND category_id = ?`,
			moveTo, familyID, id); err != nil {
			h.internalError(w, "move template categories", err)
			return
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE task_definitions SET category_id = ? WHERE family_id = ? AND category_id = ?`,
			moveTo, familyID, id); err != nil {
			h.internalError(w, "move task categories", err)
			return
		}
	}

	res, err := tx.ExecContext(ctx,
		`DELETE FROM task_categories WHERE id = ? AND family_id = ?`, id, familyID)
	if err != nil {
		h.internalError(w, "delete category", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		notFound(w, "Category not found")
		return
	}

	if err := audit.Record(ctx, tx, familyID, userID, "task_category.deleted", "task_category", &id,
		map[string]any{"name": current.Name, "templates_moved": templates, "tasks_moved": tasks, "move_to": moveTo},
	); err != nil {
		h.internalError(w, "audit delete category", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit delete category", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// notFound writes a 404 response with the given message.
func notFound(w http.ResponseWriter, message string) {
	web.RespondError(w, http.StatusNotFound, "not_found", message, nil)
}
