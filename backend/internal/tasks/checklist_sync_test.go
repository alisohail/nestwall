package tasks

import (
	"net/http"
	"reflect"
	"testing"
)

// checklistTexts reads the texts an owner has, in display order.
func checklistTexts(t *testing.T, f fixture, column, ownerID string) []string {
	t.Helper()
	rows, err := f.db.Query(`SELECT item_text FROM task_checklist_items WHERE `+column+` = ? ORDER BY sort_order`, ownerID)
	if err != nil {
		t.Fatalf("load checklist: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			t.Fatalf("scan checklist: %v", err)
		}
		out = append(out, text)
	}
	return out
}

func TestOneOffChecklistEditKeepsKnownStepsAndIDs(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, true, child.memberID)

	rec := f.do(t, f.parent, http.MethodPost, checklistRoot(tk.ID), map[string]any{"text": "Bed made"}, ifMatch(tk.Version))
	expectStatus(t, rec, http.StatusCreated)
	added := decodeTask(t, rec.Body.Bytes())
	rec = f.do(t, f.parent, http.MethodPost, checklistRoot(tk.ID), map[string]any{"text": "Teeth"}, ifMatch(added.Version))
	expectStatus(t, rec, http.StatusCreated)
	both := decodeTask(t, rec.Body.Bytes())
	keepID := both.Checklist[0].ID

	rec = f.do(t, f.parent, http.MethodPatch, tasksPath+"/"+tk.ID, map[string]any{
		"checklist": []map[string]any{
			{"id": keepID, "text": "Bed tidy"},
			{"id": nil, "text": "Shoes"},
		},
	}, ifMatch(both.Version))
	expectStatus(t, rec, http.StatusOK)
	got := decodeTask(t, rec.Body.Bytes())
	if len(got.Checklist) != 2 {
		t.Fatalf("checklist = %+v, want two steps", got.Checklist)
	}
	if got.Checklist[0].ID != keepID || got.Checklist[0].Text != "Bed tidy" {
		t.Fatalf("first step = %+v, want the kept id with the new text", got.Checklist[0])
	}
	if got.Checklist[1].Text != "Shoes" {
		t.Fatalf("second step = %+v, want Shoes", got.Checklist[1])
	}
}

func TestChecklistEditIsAllowedAfterSubmission(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, true, child.memberID)
	if _, err := f.db.Exec(`UPDATE task_occurrence_assignees SET status = 'submitted', completed_at = '2026-10-09 09:00:00.000+00:00' WHERE task_occurrence_id = ?`, tk.ID); err != nil {
		t.Fatalf("mark submitted: %v", err)
	}

	rec := f.do(t, f.parent, http.MethodPatch, tasksPath+"/"+tk.ID, map[string]any{
		"checklist": []map[string]any{{"id": nil, "text": "Late step"}},
	}, ifMatch(tk.Version))
	expectStatus(t, rec, http.StatusOK)
	if got := decodeTask(t, rec.Body.Bytes()); len(got.Checklist) != 1 || got.Checklist[0].Text != "Late step" {
		t.Fatalf("checklist = %+v, want one step %q", got.Checklist, "Late step")
	}
}

func TestRepeatSeriesChecklistEditUpdatesDefinitionAndOpenOccurrences(t *testing.T) {
	f := newFixture(t)
	rep := createRepeat(t, f, f.parent, "Feed cat", "2026-10-09", f.parent.memberID)
	first := rep.FirstTask
	def := definitionOf(t, f, first.ID)
	var other string
	if err := f.db.QueryRow(`SELECT id FROM task_occurrences WHERE task_definition_id = ? AND id <> ? LIMIT 1`, def, first.ID).
		Scan(&other); err != nil {
		t.Fatalf("find another occurrence: %v", err)
	}

	rec := f.do(t, f.parent, http.MethodPatch, tasksPath+"/"+first.ID+"?scope=series", map[string]any{
		"checklist": []map[string]any{{"id": nil, "text": "Feed"}, {"id": nil, "text": "Water"}},
	}, ifMatch(first.Version))
	expectStatus(t, rec, http.StatusOK)

	want := []string{"Feed", "Water"}
	if got := checklistTexts(t, f, "task_definition_id", def); !reflect.DeepEqual(got, want) {
		t.Fatalf("definition checklist = %v, want %v", got, want)
	}
	if got := checklistTexts(t, f, "task_occurrence_id", first.ID); !reflect.DeepEqual(got, want) {
		t.Fatalf("viewed occurrence checklist = %v, want %v", got, want)
	}
	if got := checklistTexts(t, f, "task_occurrence_id", other); !reflect.DeepEqual(got, want) {
		t.Fatalf("open future occurrence checklist = %v, want %v", got, want)
	}
}

func TestRepeatSeriesChecklistEditLeavesStartedOccurrenceAlone(t *testing.T) {
	f := newFixture(t)
	rep := createRepeat(t, f, f.parent, "Feed cat", "2026-10-09", f.parent.memberID)
	first := rep.FirstTask
	def := definitionOf(t, f, first.ID)
	var other string
	if err := f.db.QueryRow(`SELECT id FROM task_occurrences WHERE task_definition_id = ? AND id <> ? LIMIT 1`, def, first.ID).
		Scan(&other); err != nil {
		t.Fatalf("find another occurrence: %v", err)
	}
	if _, err := f.db.Exec(`UPDATE task_occurrence_assignees SET status = 'submitted', completed_at = '2026-10-09 09:00:00.000+00:00' WHERE task_occurrence_id = ?`, other); err != nil {
		t.Fatalf("mark other submitted: %v", err)
	}
	if _, err := f.db.Exec(`INSERT INTO task_checklist_items (id, family_id, task_occurrence_id, sort_order, item_text)
		SELECT 'started-step', family_id, ?, 0, 'Old step' FROM task_occurrences WHERE id = ?`, other, other); err != nil {
		t.Fatalf("seed started occurrence: %v", err)
	}

	rec := f.do(t, f.parent, http.MethodPatch, tasksPath+"/"+first.ID+"?scope=series", map[string]any{
		"checklist": []map[string]any{{"id": nil, "text": "New step"}},
	}, ifMatch(first.Version))
	expectStatus(t, rec, http.StatusOK)

	if got := checklistTexts(t, f, "task_occurrence_id", other); !reflect.DeepEqual(got, []string{"Old step"}) {
		t.Fatalf("started occurrence checklist = %v, want it unchanged", got)
	}
}
