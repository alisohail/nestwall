package tasks

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/tasks/seed"
	"nestwall/backend/internal/testhelper"
)

// actor is the signed-in member a request runs as. The handlers read the IDs from the context
// that the permission middleware would set.
type actor struct {
	memberID string
	userID   string
}

// fixture holds a database with two families, each with the default categories, and the
// routes under test. Family A is the one the tests act as.
type fixture struct {
	db     *sql.DB
	mux    *http.ServeMux
	h      *Handlers
	clk    *testClock
	a      testhelper.Family
	b      testhelper.Family
	parent actor
	other  actor
}

// fixtureNow is the time the fixture's clock starts at. The family zone is UTC.
var fixtureNow = time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)

func newFixture(t *testing.T) fixture {
	t.Helper()
	conn := testhelper.OpenDB(t)
	a := testhelper.OneFamily(t, conn)
	b := testhelper.OneFamily(t, conn)
	seedCategories(t, conn, a.FamilyID)
	seedCategories(t, conn, b.FamilyID)

	clk := &testClock{now: fixtureNow}
	h := NewHandlers(conn, clk, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/family/task-categories", h.ListCategories)
	mux.HandleFunc("POST /api/v1/family/task-categories", h.CreateCategory)
	mux.HandleFunc("PUT /api/v1/family/task-categories/{id}", h.UpdateCategory)
	mux.HandleFunc("DELETE /api/v1/family/task-categories/{id}", h.DeleteCategory)
	mux.HandleFunc("GET /api/v1/family/task-settings", h.GetSettings)
	mux.HandleFunc("PUT /api/v1/family/task-settings", h.UpdateSettings)
	mux.HandleFunc("GET /api/v1/task-templates", h.ListTemplates)
	mux.HandleFunc("POST /api/v1/task-templates", h.CreateTemplate)
	mux.HandleFunc("GET /api/v1/task-templates/{id}", h.GetTemplate)
	mux.HandleFunc("PATCH /api/v1/task-templates/{id}", h.UpdateTemplate)
	mux.HandleFunc("DELETE /api/v1/task-templates/{id}", h.DeleteTemplate)
	mux.HandleFunc("GET /api/v1/tasks", h.ListTasks)
	mux.HandleFunc("GET /api/v1/me/tasks", h.MyTasks)
	mux.HandleFunc("GET /api/v1/me/tasks/coming", h.MyComingUp)
	mux.HandleFunc("GET /api/v1/me/tasks/done", h.MyDone)
	mux.HandleFunc("POST /api/v1/tasks", h.CreateTask)
	mux.HandleFunc("POST /api/v1/task-definitions", h.CreateRepeat)
	mux.HandleFunc("GET /api/v1/tasks/{id}", h.GetTask)
	mux.HandleFunc("PATCH /api/v1/tasks/{id}", h.UpdateTask)
	mux.HandleFunc("POST /api/v1/tasks/{id}/archive", h.ArchiveTask)
	mux.HandleFunc("POST /api/v1/tasks/{id}/unarchive", h.UnarchiveTask)
	mux.HandleFunc("POST /api/v1/tasks/{id}/remove-person", h.RemovePerson)
	mux.HandleFunc("DELETE /api/v1/tasks/{id}", h.DeleteTask)
	mux.HandleFunc("POST /api/v1/tasks/{id}/complete", h.Complete)
	mux.HandleFunc("POST /api/v1/tasks/{id}/approve", h.Approve)
	mux.HandleFunc("POST /api/v1/tasks/{id}/reject", h.Reject)
	mux.HandleFunc("POST /api/v1/tasks/{id}/skip", h.Skip)
	mux.HandleFunc("POST /api/v1/tasks/{id}/reopen", h.Reopen)
	mux.HandleFunc("POST /api/v1/tasks/{id}/withdraw", h.Withdraw)
	mux.HandleFunc("PUT /api/v1/tasks/{id}/checklist/{item}", h.TickChecklist)
	mux.HandleFunc("POST /api/v1/tasks/{id}/checklist", h.AddChecklistItem)
	mux.HandleFunc("PATCH /api/v1/tasks/{id}/checklist/{item}", h.RenameChecklistItem)
	mux.HandleFunc("DELETE /api/v1/tasks/{id}/checklist/{item}", h.RemoveChecklistItem)

	return fixture{
		db:     conn,
		mux:    mux,
		h:      h,
		clk:    clk,
		a:      a,
		b:      b,
		parent: actor{memberID: a.MemberID, userID: a.ParentUserID},
		other:  actor{memberID: b.MemberID, userID: b.ParentUserID},
	}
}

// seedCategories inserts the built-in categories for a family, as family creation does.
func seedCategories(t *testing.T, conn *sql.DB, familyID string) {
	t.Helper()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	if err := seed.SeedDefaultCategories(context.Background(), tx, familyID); err != nil {
		t.Fatalf("seed categories: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit seed: %v", err)
	}
}

// do sends a request as the given actor and returns the recorded response.
func (f fixture) do(t *testing.T, a actor, method, target string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	ctx := context.WithValue(req.Context(), permissions.ContextKeyFamilyMemberID, a.memberID)
	ctx = context.WithValue(ctx, permissions.ContextKeyUserID, a.userID)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

// categoryID returns the ID of a seeded category of a family, found by its seed key.
func (f fixture) categoryID(t *testing.T, familyID, seedKey string) string {
	t.Helper()
	var id string
	if err := f.db.QueryRow(
		`SELECT id FROM task_categories WHERE family_id = ? AND seed_key = ?`, familyID, seedKey,
	).Scan(&id); err != nil {
		t.Fatalf("find category %s: %v", seedKey, err)
	}
	return id
}

// expectStatus fails the test when the response status is not want.
func expectStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, want, rec.Body.String())
	}
}

// errorBody decodes the error envelope of a response.
func errorBody(t *testing.T, rec *httptest.ResponseRecorder) (code string, params map[string]any) {
	t.Helper()
	var env struct {
		Error struct {
			Code   string         `json:"code"`
			Params map[string]any `json:"params"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return env.Error.Code, env.Error.Params
}

// expectError checks the status, error code and, when given, the validation field and reason.
func expectError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) map[string]any {
	t.Helper()
	expectStatus(t, rec, status)
	got, params := errorBody(t, rec)
	if got != code {
		t.Fatalf("error code = %q, want %q; body: %s", got, code, rec.Body.String())
	}
	return params
}

// expectValidation checks a 400 validation_failed response for one field and reason.
func expectValidation(t *testing.T, rec *httptest.ResponseRecorder, field, reason string) {
	t.Helper()
	params := expectError(t, rec, http.StatusBadRequest, "validation_failed")
	if params["field"] != field || params["reason"] != reason {
		t.Fatalf("validation params = %v, want field %s reason %s", params, field, reason)
	}
}
