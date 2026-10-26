package tasks

import "testing"

// TestMyTasksCarriesOwnChecklistAndVersion checks that each copy in /me/tasks lists its steps with the
// child's own done state, and the version the tick call needs. The version moves after a tick.
func TestMyTasksCarriesOwnChecklistAndVersion(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)

	tk := newActionTask(t, f, false, child.memberID)
	setOccurrence(t, f, tk.ID, utc(10, 12), nil)
	first := addChecklistItem(t, f, tk.ID, "Clothes in the hamper", 0)
	addChecklistItem(t, f, tk.ID, "Clear the desk", 1)

	item := todayItem(t, myTasksGet(t, f, child), tk.ID)
	if len(item.Checklist) != 2 || item.Checklist[0].Text != "Clothes in the hamper" || item.Checklist[0].Done || item.Checklist[1].Done {
		t.Fatalf("checklist = %+v, want two open steps in order", item.Checklist)
	}

	ticked := tickItem(t, f, child, tk.ID, first, true, item.Version)
	after := todayItem(t, myTasksGet(t, f, child), tk.ID)
	if !after.Checklist[0].Done || after.Checklist[1].Done {
		t.Fatalf("checklist after tick = %+v, want only the first step done", after.Checklist)
	}
	if after.Version != ticked.Version {
		t.Fatalf("version = %d, want %d after the tick", after.Version, ticked.Version)
	}
}

// TestMyTasksNoChecklistForStepFreeTask checks that a task without steps has no checklist.
func TestMyTasksNoChecklistForStepFreeTask(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)

	tk := createTask(t, f, f.parent, oneOffBody("Feed the cat", "2026-10-10", child.memberID))
	setOccurrence(t, f, tk.ID, utc(10, 12), nil)

	if item := todayItem(t, myTasksGet(t, f, child), tk.ID); len(item.Checklist) != 0 {
		t.Fatalf("checklist = %+v, want none", item.Checklist)
	}
}

// todayItem returns the copy with the given task ID from the Today list, failing the test if absent.
func todayItem(t *testing.T, resp MyTasksResponse, taskID string) MyTaskItem {
	t.Helper()
	for _, it := range resp.Today {
		if it.TaskID == taskID {
			return it
		}
	}
	t.Fatalf("task %s not in today list %v", taskID, itemIDs(resp.Today))
	return MyTaskItem{}
}
