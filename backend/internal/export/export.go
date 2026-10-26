// Package export serves readable CSV copies of family data (milestone M7-09). The files are for
// people to open in a spreadsheet. They are not backups and cannot be imported. The calendar is
// exported separately as .ics by the calendar package.
package export

import (
	"context"
	"database/sql"
	"encoding/csv"
	"log/slog"
	"net/http"
	"strings"

	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/web"
)

// utf8BOM lets spreadsheet apps read accented names as UTF-8.
const utf8BOM = "\uFEFF"

// file describes one CSV export. Numeric columns are listed by index so their values, such as a
// negative amount, are not treated as formulas.
type file struct {
	name    string
	header  []string
	numeric map[int]bool
	query   string
}

var tasksFile = file{
	name:    "nestwall-tasks.csv",
	header:  []string{"due_utc", "task", "priority", "points", "person", "status", "completed_utc", "completed_late", "decided_utc", "decision_note"},
	numeric: map[int]bool{3: true},
	query: `SELECT strftime('%Y-%m-%dT%H:%M:%SZ', o.due_at), d.title, d.priority, CAST(d.points AS TEXT),
		       u.display_name, oa.status,
		       COALESCE(strftime('%Y-%m-%dT%H:%M:%SZ', oa.completed_at), ''),
		       CASE WHEN oa.completed_late = 1 THEN 'yes' ELSE 'no' END,
		       COALESCE(strftime('%Y-%m-%dT%H:%M:%SZ', oa.decided_at), ''),
		       COALESCE(oa.decision_note, '')
		FROM task_occurrence_assignees oa
		JOIN task_occurrences o ON o.id = oa.task_occurrence_id AND o.family_id = oa.family_id
		JOIN task_definitions d ON d.id = o.task_definition_id AND d.family_id = o.family_id
		JOIN family_members fm ON fm.id = oa.family_member_id AND fm.family_id = oa.family_id
		JOIN users u ON u.id = fm.user_id
		WHERE oa.family_id = ?
		ORDER BY o.due_at, d.title, u.display_name`,
}

var ledgerFile = file{
	name:    "nestwall-points-ledger.csv",
	header:  []string{"when_utc", "person", "entry_type", "amount", "reason", "recorded_by"},
	numeric: map[int]bool{3: true},
	query: `SELECT strftime('%Y-%m-%dT%H:%M:%SZ', l.created_at), u.display_name, l.entry_type,
		       CAST(l.amount AS TEXT), COALESCE(l.reason, ''), COALESCE(au.display_name, '')
		FROM points_ledger l
		JOIN family_members fm ON fm.id = l.family_member_id AND fm.family_id = l.family_id
		JOIN users u ON u.id = fm.user_id
		LEFT JOIN family_members am ON am.id = l.actor_member_id AND am.family_id = l.family_id
		LEFT JOIN users au ON au.id = am.user_id
		WHERE l.family_id = ?
		ORDER BY l.created_at, l.id`,
}

var membersFile = file{
	name:   "nestwall-members.csv",
	header: []string{"name", "role", "status", "kind", "colour", "date_of_birth"},
	query: `SELECT u.display_name, r.name, fm.status, CASE WHEN u.is_child = 1 THEN 'child' ELSE 'parent' END,
		       fm.colour, COALESCE(u.date_of_birth, '')
		FROM family_members fm
		JOIN users u ON u.id = fm.user_id
		JOIN roles r ON r.id = fm.role_id
		WHERE fm.family_id = ?
		ORDER BY u.display_name`,
}

// Handlers serves the CSV exports. Routes must be wrapped with the settings.manage permission.
type Handlers struct {
	db     *sql.DB
	logger *slog.Logger
}

// NewHandlers returns the export handlers.
func NewHandlers(db *sql.DB, logger *slog.Logger) *Handlers {
	return &Handlers{db: db, logger: logger}
}

// Tasks handles GET /api/v1/export/tasks.csv.
func (h *Handlers) Tasks(w http.ResponseWriter, r *http.Request) { h.serve(w, r, tasksFile) }

// Ledger handles GET /api/v1/export/ledger.csv.
func (h *Handlers) Ledger(w http.ResponseWriter, r *http.Request) { h.serve(w, r, ledgerFile) }

// Members handles GET /api/v1/export/members.csv.
func (h *Handlers) Members(w http.ResponseWriter, r *http.Request) { h.serve(w, r, membersFile) }

func (h *Handlers) serve(w http.ResponseWriter, r *http.Request, f file) {
	ctx := r.Context()
	callerID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}
	familyID, err := h.familyID(ctx, callerID)
	if err != nil {
		h.internalError(w, "export: family lookup", err)
		return
	}
	rows, err := h.rows(ctx, f, familyID)
	if err != nil {
		h.internalError(w, "export: "+f.name, err)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+f.name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(utf8BOM)); err != nil {
		h.logger.Warn("export: write failed", "file", f.name, "error", err)
		return
	}
	cw := csv.NewWriter(w)
	if err := cw.Write(f.header); err != nil {
		h.logger.Warn("export: write failed", "file", f.name, "error", err)
		return
	}
	for _, row := range rows {
		for i := range row {
			if !f.numeric[i] {
				row[i] = guardFormula(row[i])
			}
		}
		if err := cw.Write(row); err != nil {
			h.logger.Warn("export: write failed", "file", f.name, "error", err)
			return
		}
	}
	cw.Flush()
}

// familyID returns the family of the signed-in member. The export never reads across families.
func (h *Handlers) familyID(ctx context.Context, memberID string) (string, error) {
	var familyID string
	err := h.db.QueryRowContext(ctx, `SELECT family_id FROM family_members WHERE id = ?`, memberID).Scan(&familyID)
	return familyID, err
}

func (h *Handlers) rows(ctx context.Context, f file, familyID string) ([][]string, error) {
	rs, err := h.db.QueryContext(ctx, f.query, familyID)
	if err != nil {
		return nil, err
	}
	defer rs.Close()

	var out [][]string
	for rs.Next() {
		vals := make([]sql.NullString, len(f.header))
		ptrs := make([]any, len(vals))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rs.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make([]string, len(vals))
		for i, v := range vals {
			row[i] = v.String
		}
		out = append(out, row)
	}
	return out, rs.Err()
}

// guardFormula stops spreadsheet apps from running a cell that starts with a formula character.
// Names and notes come from users, so a leading apostrophe keeps them as plain text.
func guardFormula(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

func (h *Handlers) internalError(w http.ResponseWriter, what string, err error) {
	h.logger.Error(what, "error", err)
	web.RespondError(w, http.StatusInternalServerError, "internal_error", "Something went wrong", nil)
}
