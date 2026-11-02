package tasks

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
)

type tabCounts struct {
	Overdue   int `json:"overdue"`
	Today     int `json:"today"`
	Upcoming  int `json:"upcoming"`
	Submitted int `json:"submitted"`
	Approved  int `json:"approved"`
	Expired   int `json:"expired"`
	Archived  int `json:"archived"`
}

func fetchCounts(t *testing.T, f fixture) tabCounts {
	t.Helper()
	rec := f.do(t, f.parent, http.MethodGet, "/api/v1/tasks/counts", nil, nil)
	expectStatus(t, rec, http.StatusOK)
	var c tabCounts
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
		t.Fatalf("decode counts: %v", err)
	}
	return c
}

func listLen(t *testing.T, f fixture, query url.Values) int {
	t.Helper()
	rec := f.do(t, f.parent, http.MethodGet, "/api/v1/tasks?"+query.Encode(), nil, nil)
	expectStatus(t, rec, http.StatusOK)
	var body struct {
		Tasks []json.RawMessage `json:"tasks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return len(body.Tasks)
}

func TestTaskTabCountsMatchListedTasks(t *testing.T) {
	f := newFixture(t)
	child := addChildMember(t, f, f.a.FamilyID)
	tk := newActionTask(t, f, true, child.memberID)
	runAction(t, f, child, tk.ID, "complete", nil, tk.Version)

	c := fetchCounts(t, f)
	if want := listLen(t, f, url.Values{"status": {"submitted"}}); c.Submitted != want {
		t.Errorf("submitted count = %d, list has %d", c.Submitted, want)
	}
	if want := listLen(t, f, url.Values{"expired": {"true"}}); c.Expired != want {
		t.Errorf("expired count = %d, list has %d", c.Expired, want)
	}
	if want := listLen(t, f, url.Values{"archived": {"true"}}); c.Archived != want {
		t.Errorf("archived count = %d, list has %d", c.Archived, want)
	}
}
