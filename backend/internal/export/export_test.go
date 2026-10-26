package export

import (
	"context"
	"database/sql"
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/testhelper"
)

// get calls one export handler as the family's parent and returns the response and parsed CSV rows.
func get(t *testing.T, h http.HandlerFunc, familyMemberID string) (*httptest.ResponseRecorder, [][]string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/export/x.csv", nil)
	req = req.WithContext(context.WithValue(req.Context(), permissions.ContextKeyFamilyMemberID, familyMemberID))
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	body := strings.TrimPrefix(rec.Body.String(), utf8BOM)
	rows, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	return rec, rows
}

func TestMembersCSVHasNoSecrets(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f := testhelper.OneFamily(t, conn)
	var email, hash sql.NullString
	if err := conn.QueryRow(`SELECT email, password_hash FROM users WHERE id = ?`, f.ParentUserID).Scan(&email, &hash); err != nil {
		t.Fatalf("load parent: %v", err)
	}
	h := NewHandlers(conn, nil)

	rec, rows := get(t, h.Members, f.MemberID)
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/csv") {
		t.Fatalf("content type %q", got)
	}
	if !strings.Contains(rec.Header().Get("Content-Disposition"), `filename="nestwall-members.csv"`) {
		t.Fatalf("disposition %q", rec.Header().Get("Content-Disposition"))
	}
	if len(rows) != 2 || rows[0][0] != "name" {
		t.Fatalf("want header and one parent row, got %v", rows)
	}
	body := rec.Body.String()
	if (email.Valid && email.String != "" && strings.Contains(body, email.String)) ||
		(hash.Valid && hash.String != "" && strings.Contains(body, hash.String)) {
		t.Fatalf("members export leaks login details: %s", body)
	}
}

func TestLedgerCSVGuardsTextAndKeepsNumbers(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f := testhelper.OneFamily(t, conn)
	insert := `INSERT INTO points_ledger (id, family_id, family_member_id, entry_type, amount, reason, idempotency_key)
		VALUES (?, ?, ?, ?, ?, ?, ?)`
	if _, err := conn.Exec(insert, "l-1", f.FamilyID, f.MemberID, "bonus", 5, "=HYPERLINK(\"x\")", "k-1"); err != nil {
		t.Fatalf("insert bonus: %v", err)
	}
	if _, err := conn.Exec(insert, "l-2", f.FamilyID, f.MemberID, "penalty", -3, nil, "k-2"); err != nil {
		t.Fatalf("insert penalty: %v", err)
	}
	h := NewHandlers(conn, nil)

	_, rows := get(t, h.Ledger, f.MemberID)
	if len(rows) != 3 {
		t.Fatalf("want header and two rows, got %v", rows)
	}
	bonus, penalty := rows[1], rows[2]
	if bonus[4] != "'=HYPERLINK(\"x\")" {
		t.Fatalf("formula reason not guarded: %q", bonus[4])
	}
	if bonus[3] != "5" || penalty[3] != "-3" {
		t.Fatalf("amounts changed: %q %q", bonus[3], penalty[3])
	}
}

func TestTasksCSVListsAssignee(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f := testhelper.OneFamily(t, conn)
	stmts := []struct {
		q    string
		args []any
	}{
		{`INSERT INTO task_definitions (id, family_id, title, time_zone, starts_on, points, created_by)
		  VALUES ('d-1', ?, 'Feed the cat', 'UTC', '2025-01-01', 5, ?)`, []any{f.FamilyID, f.ParentUserID}},
		{`INSERT INTO task_occurrences (id, family_id, task_definition_id, due_date, due_at)
		  VALUES ('o-1', ?, 'd-1', '2025-01-02', '2025-01-02 09:00:00')`, []any{f.FamilyID}},
		{`INSERT INTO task_occurrence_assignees (id, family_id, task_occurrence_id, family_member_id, status)
		  VALUES ('a-1', ?, 'o-1', ?, 'to_do')`, []any{f.FamilyID, f.MemberID}},
	}
	for _, s := range stmts {
		if _, err := conn.Exec(s.q, s.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	h := NewHandlers(conn, nil)

	_, rows := get(t, h.Tasks, f.MemberID)
	if len(rows) != 2 {
		t.Fatalf("want header and one row, got %v", rows)
	}
	if rows[1][0] != "2025-01-02T09:00:00Z" || rows[1][1] != "Feed the cat" || rows[1][3] != "5" || rows[1][5] != "to_do" {
		t.Fatalf("unexpected row: %v", rows[1])
	}
}
