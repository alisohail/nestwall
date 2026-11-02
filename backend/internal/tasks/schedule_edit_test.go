package tasks

import (
	"net/http"
	"testing"
)

func TestOneOffPatchPersistsSchedule(t *testing.T) {
	f := newFixture(t)
	created := createTask(t, f, f.parent, oneOffBody("Tidy room", "2026-10-09", f.parent.memberID))

	rec := f.do(t, f.parent, http.MethodPatch, tasksPath+"/"+created.ID, map[string]any{
		"due_date":   "2026-10-11",
		"start_time": "08:00",
		"due_time":   "10:00",
	}, map[string]string{"If-Match": `"1"`})
	expectStatus(t, rec, http.StatusOK)
	got := decodeTask(t, rec.Body.Bytes())
	if got.DueDate == nil || *got.DueDate != "2026-10-11" {
		t.Fatalf("due_date = %v, want 2026-10-11", got.DueDate)
	}
	if got.StartTime == nil || *got.StartTime != "08:00" {
		t.Fatalf("start_time = %v, want 08:00", got.StartTime)
	}
	if got.DueTime == nil || *got.DueTime != "10:00" {
		t.Fatalf("due_time = %v, want 10:00", got.DueTime)
	}
	if got.DueAt != "2026-10-11T10:00:00Z" {
		t.Fatalf("due_at = %q, want 2026-10-11T10:00:00Z", got.DueAt)
	}
}

func TestOccurrencePatchPersistsSchedule(t *testing.T) {
	f := newFixture(t)
	rep := createRepeat(t, f, f.parent, "Feed cat", "2026-10-09", f.parent.memberID)
	first := rep.FirstTask

	rec := f.do(t, f.parent, http.MethodPatch, tasksPath+"/"+first.ID+"?scope=occurrence", map[string]any{
		"start_time": "07:30",
		"due_time":   "09:15",
	}, ifMatch(first.Version))
	expectStatus(t, rec, http.StatusOK)
	got := decodeTask(t, rec.Body.Bytes())
	if got.StartTime == nil || *got.StartTime != "07:30" {
		t.Fatalf("occurrence start_time = %v, want 07:30", got.StartTime)
	}
	if got.DueTime == nil || *got.DueTime != "09:15" {
		t.Fatalf("occurrence due_time = %v, want 09:15", got.DueTime)
	}
	if got.DueDate == nil {
		t.Fatalf("occurrence due_date missing")
	}
	if want := *got.DueDate + "T09:15:00Z"; got.DueAt != want {
		t.Fatalf("occurrence due_at = %q, want %q", got.DueAt, want)
	}
}

func TestSeriesPatchAppliesTimeToFutureOccurrences(t *testing.T) {
	f := newFixture(t)
	rep := createRepeat(t, f, f.parent, "Feed cat", "2026-10-09", f.parent.memberID)
	first := rep.FirstTask
	def := definitionOf(t, f, first.ID)
	var other, otherDate string
	if err := f.db.QueryRow(`SELECT id, due_date FROM task_occurrences WHERE task_definition_id = ? AND id <> ? LIMIT 1`, def, first.ID).
		Scan(&other, &otherDate); err != nil {
		t.Fatalf("find another occurrence: %v", err)
	}

	rec := f.do(t, f.parent, http.MethodPatch, tasksPath+"/"+first.ID+"?scope=series", map[string]any{
		"start_time": "06:45",
		"due_time":   "08:20",
	}, ifMatch(first.Version))
	expectStatus(t, rec, http.StatusOK)
	got := decodeTask(t, rec.Body.Bytes())
	if got.StartTime == nil || *got.StartTime != "06:45" || got.DueTime == nil || *got.DueTime != "08:20" {
		t.Fatalf("times = %v / %v, want 06:45 / 08:20", got.StartTime, got.DueTime)
	}

	var start, due string
	if err := f.db.QueryRow(`SELECT start_time, due_time FROM task_definitions WHERE id = ?`, def).Scan(&start, &due); err != nil {
		t.Fatalf("load definition: %v", err)
	}
	if start != "06:45" || due != "08:20" {
		t.Fatalf("definition times = %s / %s, want 06:45 / 08:20", start, due)
	}
	var otherDue string
	if err := f.db.QueryRow(`SELECT due_at FROM task_occurrences WHERE id = ?`, other).Scan(&otherDue); err != nil {
		t.Fatalf("load other occurrence: %v", err)
	}
	if want := otherDate + "T08:20:00Z"; otherDue != want {
		t.Fatalf("future occurrence due_at = %q, want %q", otherDue, want)
	}
}

func TestSeriesPatchKeepsEachOccurrenceDate(t *testing.T) {
	f := newFixture(t)
	rep := createRepeat(t, f, f.parent, "Feed cat", "2026-10-09", f.parent.memberID)
	first := rep.FirstTask
	before := *first.DueDate

	rec := f.do(t, f.parent, http.MethodPatch, tasksPath+"/"+first.ID+"?scope=series", map[string]any{
		"due_date": "2030-01-01",
	}, ifMatch(first.Version))
	expectStatus(t, rec, http.StatusOK)
	if got := decodeTask(t, rec.Body.Bytes()); got.DueDate == nil || *got.DueDate != before {
		t.Fatalf("due_date = %v, want the occurrence's own %s", got.DueDate, before)
	}
}
