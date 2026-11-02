package tasks

import (
	"net/http"
	"testing"
)

func TestRepeatEditNeedsAScope(t *testing.T) {
	f := newFixture(t)
	rep := createRepeat(t, f, f.parent, "Feed cat", "2026-10-09", f.parent.memberID)
	first := rep.FirstTask

	expectError(t, f.do(t, f.parent, http.MethodPatch, tasksPath+"/"+first.ID,
		map[string]any{"title": "Feed Biscuit"}, ifMatch(first.Version)), http.StatusConflict, "repeat_scope_required")
}

func TestRepeatSeriesEditChangesContentButKeepsSchedule(t *testing.T) {
	f := newFixture(t)
	rep := createRepeat(t, f, f.parent, "Feed cat", "2026-10-09", f.parent.memberID)
	first := rep.FirstTask
	def := definitionOf(t, f, first.ID)

	rec := f.do(t, f.parent, http.MethodPatch, tasksPath+"/"+first.ID+"?scope=series",
		map[string]any{"title": "Feed Biscuit", "points": 9, "due_date": "2026-12-01"}, ifMatch(first.Version))
	expectStatus(t, rec, http.StatusOK)
	got := decodeTask(t, rec.Body.Bytes())
	if got.Title == nil || *got.Title != "Feed Biscuit" {
		t.Fatalf("title = %v, want Feed Biscuit", got.Title)
	}
	if got.DueAt != first.DueAt {
		t.Fatalf("due_at moved from %q to %q; a series edit must keep each day's time", first.DueAt, got.DueAt)
	}

	var title, startsOn string
	var points int
	if err := f.db.QueryRow(`SELECT title, starts_on, points FROM task_definitions WHERE id = ?`, def).
		Scan(&title, &startsOn, &points); err != nil {
		t.Fatalf("load definition: %v", err)
	}
	if title != "Feed Biscuit" || points != 9 {
		t.Fatalf("definition title=%q points=%d, want Feed Biscuit and 9", title, points)
	}
	if startsOn != "2026-10-09" {
		t.Fatalf("starts_on = %s, want the series start 2026-10-09 kept", startsOn)
	}

	var other string
	if err := f.db.QueryRow(`SELECT id FROM task_occurrences WHERE task_definition_id = ? AND id <> ? LIMIT 1`, def, first.ID).
		Scan(&other); err != nil {
		t.Fatalf("find another occurrence: %v", err)
	}
	var otherTitle string
	if err := f.db.QueryRow(`SELECT title FROM task_definitions WHERE id = ?`, def).Scan(&otherTitle); err != nil {
		t.Fatalf("load other occurrence title: %v", err)
	}
	if otherTitle != "Feed Biscuit" {
		t.Fatalf("other occurrences show %q, want the series title", otherTitle)
	}
}

func TestRepeatOccurrenceEditDetachesOnlyThatDay(t *testing.T) {
	f := newFixture(t)
	rep := createRepeat(t, f, f.parent, "Feed cat", "2026-10-09", f.parent.memberID)
	first := rep.FirstTask
	series := definitionOf(t, f, first.ID)

	rec := f.do(t, f.parent, http.MethodPatch, tasksPath+"/"+first.ID+"?scope=occurrence",
		map[string]any{"title": "Feed once"}, ifMatch(first.Version))
	expectStatus(t, rec, http.StatusOK)
	got := decodeTask(t, rec.Body.Bytes())
	if got.Title == nil || *got.Title != "Feed once" {
		t.Fatalf("title = %v, want Feed once", got.Title)
	}

	detached := definitionOf(t, f, first.ID)
	if detached == series {
		t.Fatalf("occurrence still belongs to the series %s; want a detached one-off", series)
	}
	var rule string
	if err := f.db.QueryRow(`SELECT COALESCE(recurrence_rule, 'NULL') FROM task_definitions WHERE id = ?`, detached).
		Scan(&rule); err != nil {
		t.Fatalf("load detached definition: %v", err)
	}
	if rule != "NULL" {
		t.Fatalf("detached rule = %s, want NULL (one-off)", rule)
	}

	var seriesTitle string
	if err := f.db.QueryRow(`SELECT title FROM task_definitions WHERE id = ?`, series).Scan(&seriesTitle); err != nil {
		t.Fatalf("load series definition: %v", err)
	}
	if seriesTitle != "Feed cat" {
		t.Fatalf("series title = %q, want it unchanged", seriesTitle)
	}
}
