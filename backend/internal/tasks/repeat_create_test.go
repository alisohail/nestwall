package tasks

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const definitionsPath = "/api/v1/task-definitions"

// TestCreateRepeatGeneratesSeries checks that a repeat creates one occurrence per date, that the
// first occurrence is returned, and that generating again creates nothing.
func TestCreateRepeatGeneratesSeries(t *testing.T) {
	f := newFixture(t)
	body := oneOffBody("Walk dog", "2026-10-12", f.parent.memberID)
	body["recurrence_rule"] = "FREQ=DAILY;COUNT=3"

	rec := f.do(t, f.parent, http.MethodPost, definitionsPath, body, nil)
	expectStatus(t, rec, http.StatusCreated)
	var got repeatCreated
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode repeat %q: %v", rec.Body.String(), err)
	}

	if got.Occurrences != 3 || got.RecurrenceRule != "FREQ=DAILY;COUNT=3" {
		t.Fatalf("response = %+v, want 3 occurrences of FREQ=DAILY;COUNT=3", got)
	}
	if got.FirstTask == nil || !got.FirstTask.Repeats || !strings.HasPrefix(got.FirstTask.DueAt, "2026-10-12") {
		t.Fatalf("first task = %+v, want a repeating task due 2026-10-12", got.FirstTask)
	}
	if len(got.FirstTask.Assignees) != 1 || got.FirstTask.Assignees[0].MemberID != f.parent.memberID {
		t.Fatalf("first task assignees = %+v, want the parent", got.FirstTask.Assignees)
	}

	var stored int
	if err := f.db.QueryRow(
		`SELECT COUNT(*) FROM task_occurrences WHERE task_definition_id = ?`, got.DefinitionID,
	).Scan(&stored); err != nil {
		t.Fatalf("count occurrences: %v", err)
	}
	if stored != 3 {
		t.Fatalf("stored occurrences = %d, want 3", stored)
	}

	tx, err := f.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	again, err := GenerateWindow(context.Background(), tx, f.a.FamilyID, got.DefinitionID, fixtureNow, DefaultWindowDays)
	if err != nil {
		t.Fatalf("generate again: %v", err)
	}
	if again != 0 {
		t.Fatalf("second generation created %d occurrences, want 0", again)
	}
}

// TestCreateRepeatRejectsUnsupportedRule checks that a rule outside the Repeat options is refused
// before anything is written.
func TestCreateRepeatRejectsUnsupportedRule(t *testing.T) {
	f := newFixture(t)
	body := oneOffBody("Walk dog", "2026-10-12", f.parent.memberID)
	body["recurrence_rule"] = "FREQ=HOURLY"

	rec := f.do(t, f.parent, http.MethodPost, definitionsPath, body, nil)
	expectError(t, rec, http.StatusBadRequest, "validation_failed")

	var n int
	if err := f.db.QueryRow(
		`SELECT COUNT(*) FROM task_definitions WHERE family_id = ? AND recurrence_rule IS NOT NULL`,
		f.a.FamilyID,
	).Scan(&n); err != nil {
		t.Fatalf("count definitions: %v", err)
	}
	if n != 0 {
		t.Fatalf("repeat definitions after refused rule = %d, want 0", n)
	}
}
