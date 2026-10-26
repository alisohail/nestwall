package tasks

import (
	"encoding/json"
	"net/http"
	"testing"
)

// unarchivePath is the restore route for a task.
func unarchivePath(taskID string) string { return tasksPath + "/" + taskID + "/unarchive" }

func archiveFor(t *testing.T, f fixture, taskID string, version string) {
	t.Helper()
	expectStatus(t, f.do(t, f.parent, http.MethodPost, tasksPath+"/"+taskID+"/archive", nil, map[string]string{"If-Match": version}), http.StatusOK)
}

func TestUnarchiveRestoresArchivedTask(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	created := createTask(t, f, f.parent, oneOffBody("Tidy room", "2026-10-09", f.parent.memberID, child.memberID))
	defID := definitionOf(t, f, created.ID)
	archiveFor(t, f, created.ID, `"1"`)

	rec := f.do(t, f.parent, http.MethodPost, unarchivePath(created.ID), nil, ifMatch(2))
	expectStatus(t, rec, http.StatusOK)

	var restored task
	if err := json.Unmarshal(rec.Body.Bytes(), &restored); err != nil {
		t.Fatalf("decode restored task: %v", err)
	}
	if restored.Archived {
		t.Fatalf("restored task is still archived")
	}
	if restored.ArchivedAt != nil {
		t.Fatalf("restored task has archived_at %q, want none", *restored.ArchivedAt)
	}
	if restored.Version != 3 {
		t.Fatalf("restored version = %d, want 3", restored.Version)
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM task_definitions WHERE id = ? AND status = 'active'`, defID); n != 1 {
		t.Fatalf("active definition rows = %d after restore, want 1", n)
	}
	if n := countAudit(t, f, "task.restored", created.ID); n != 1 {
		t.Fatalf("task.restored audit rows = %d, want 1", n)
	}

	rec = f.do(t, f.parent, http.MethodGet, tasksPath+"/"+created.ID, nil, nil)
	expectStatus(t, rec, http.StatusOK)
	var got task
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	if got.Archived {
		t.Fatalf("task is still archived after restore")
	}
}

func TestArchivedTaskReportsArchivedAt(t *testing.T) {
	f := newFixture(t)
	created := createTask(t, f, f.parent, oneOffBody("Tidy room", "2026-10-09", f.parent.memberID))
	archiveFor(t, f, created.ID, `"1"`)

	rec := f.do(t, f.parent, http.MethodGet, tasksPath+"/"+created.ID, nil, nil)
	expectStatus(t, rec, http.StatusOK)
	var got task
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	if got.ArchivedAt == nil || *got.ArchivedAt == "" {
		t.Fatalf("archived task has no archived_at")
	}
}

func TestUnarchiveActiveTaskIsRefused(t *testing.T) {
	f := newFixture(t)
	created := createTask(t, f, f.parent, oneOffBody("Tidy room", "2026-10-09", f.parent.memberID))

	expectError(t, f.do(t, f.parent, http.MethodPost, unarchivePath(created.ID), nil, ifMatch(1)), http.StatusConflict, "task_not_archived")
}

func TestUnarchiveNeedsCurrentVersion(t *testing.T) {
	f := newFixture(t)
	created := createTask(t, f, f.parent, oneOffBody("Tidy room", "2026-10-09", f.parent.memberID))
	archiveFor(t, f, created.ID, `"1"`)

	expectStatus(t, f.do(t, f.parent, http.MethodPost, unarchivePath(created.ID), nil, ifMatch(1)), http.StatusPreconditionFailed)
	if n := countRows(t, f, `SELECT COUNT(*) FROM task_definitions WHERE status = 'archived'`); n != 1 {
		t.Fatalf("archived definitions = %d after stale restore, want 1", n)
	}
}

func TestUnarchiveRefusesTaskSplitByRecurrenceEdit(t *testing.T) {
	f := newFixture(t)
	created := createTask(t, f, f.parent, oneOffBody("Tidy room", "2026-10-09", f.parent.memberID))
	archiveFor(t, f, created.ID, `"1"`)
	if _, err := f.db.Exec(
		`INSERT INTO audit_logs (id, family_id, actor_id, action, target_type, target_id)
		 VALUES ('audit-split-1', ?, ?, 'task.series_split', 'task', ?)`,
		f.a.FamilyID, f.parent.userID, created.ID,
	); err != nil {
		t.Fatalf("add series split audit: %v", err)
	}

	expectError(t, f.do(t, f.parent, http.MethodPost, unarchivePath(created.ID), nil, ifMatch(2)), http.StatusConflict, "task_not_restorable")
}

func TestUnarchiveRefusesTaskArchivedWithoutAudit(t *testing.T) {
	f := newFixture(t)
	created := createTask(t, f, f.parent, oneOffBody("Tidy room", "2026-10-09", f.parent.memberID))
	archiveFor(t, f, created.ID, `"1"`)
	restore := f.do(t, f.parent, http.MethodPost, unarchivePath(created.ID), nil, ifMatch(2))
	expectStatus(t, restore, http.StatusOK)

	// A recurrence edit archives the definition without a task.archived entry, after the restore.
	if _, err := f.db.Exec(`UPDATE task_definitions SET status = 'archived', version = version + 1 WHERE id = ?`, definitionOf(t, f, created.ID)); err != nil {
		t.Fatalf("archive without audit: %v", err)
	}

	expectError(t, f.do(t, f.parent, http.MethodPost, unarchivePath(created.ID), nil, ifMatch(4)), http.StatusConflict, "task_not_restorable")
}

func TestUnarchiveUnknownTaskIsNotFound(t *testing.T) {
	f := newFixture(t)
	expectStatus(t, f.do(t, f.parent, http.MethodPost, unarchivePath("00000000-0000-4000-8000-000000000000"), nil, ifMatch(1)), http.StatusNotFound)
}
