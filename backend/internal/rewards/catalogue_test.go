package rewards

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/testhelper"
)

// catChildRoleID is the seeded Child role from migration 000001.
const catChildRoleID = "c3d4e5f6-a7b8-9012-cdef-123456789012"

// catNow is a Saturday, 10 October 2026, 09:00 UTC. The test families use the UTC zone.
var catNow = time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)

// catActor is the member a request runs as. The handlers read the IDs from the context that the
// permission middleware would set.
type catActor struct {
	memberID string
	userID   string
}

type catFixture struct {
	t      *testing.T
	db     *sql.DB
	mux    *http.ServeMux
	fam    testhelper.Family
	other  testhelper.Family
	parent catActor
	rival  catActor
}

func newCatFixture(t *testing.T) catFixture {
	t.Helper()
	conn := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, conn)
	other := testhelper.OneFamily(t, conn)
	h := NewHandlers(conn, clock.FakeClock{Time: catNow}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/rewards", h.ListRewards)
	mux.HandleFunc("POST /api/v1/rewards", h.CreateReward)
	mux.HandleFunc("GET /api/v1/rewards/{id}", h.GetReward)
	mux.HandleFunc("PATCH /api/v1/rewards/{id}", h.UpdateReward)
	mux.HandleFunc("DELETE /api/v1/rewards/{id}", h.ArchiveReward)

	return catFixture{
		t:      t,
		db:     conn,
		mux:    mux,
		fam:    fam,
		other:  other,
		parent: catActor{memberID: fam.MemberID, userID: fam.ParentUserID},
		rival:  catActor{memberID: other.MemberID, userID: other.ParentUserID},
	}
}

// do sends a request as the given member and returns the recorded response.
func (f catFixture) do(a catActor, method, target, body string, headers map[string]string) *httptest.ResponseRecorder {
	f.t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
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

// create posts a reward and returns it. The test fails if the request does not return 201.
func (f catFixture) create(a catActor, body string) Reward {
	f.t.Helper()
	rec := f.do(a, http.MethodPost, "/api/v1/rewards", body, nil)
	if rec.Code != http.StatusCreated {
		f.t.Fatalf("create: status %d: %s", rec.Code, rec.Body.String())
	}
	return decodeCatReward(f.t, rec)
}

// addChild inserts an active child member into a family and returns the child as an actor.
func (f catFixture) addChild(familyID, name string) catActor {
	f.t.Helper()
	ctx := context.Background()
	userID := ids.New()
	memberID := ids.New()
	if _, err := f.db.ExecContext(ctx,
		`INSERT INTO users (id, user_id, password_hash, display_name, is_child) VALUES (?, ?, 'unused', ?, 1)`,
		userID, "child_"+userID[:8], name,
	); err != nil {
		f.t.Fatalf("insert child user: %v", err)
	}
	if _, err := f.db.ExecContext(ctx,
		`INSERT INTO family_members (id, family_id, user_id, role_id, status, colour) VALUES (?, ?, ?, ?, 'active', '#F59E0B')`,
		memberID, familyID, userID, catChildRoleID,
	); err != nil {
		f.t.Fatalf("insert child member: %v", err)
	}
	return catActor{memberID: memberID, userID: userID}
}

func decodeCatReward(t *testing.T, rec *httptest.ResponseRecorder) Reward {
	t.Helper()
	var rw Reward
	if err := json.Unmarshal(rec.Body.Bytes(), &rw); err != nil {
		t.Fatalf("decode reward: %v: %s", err, rec.Body.String())
	}
	return rw
}

// decodeCatList returns the IDs in a list response.
func decodeCatList(t *testing.T, rec *httptest.ResponseRecorder) map[string]bool {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Rewards []Reward `json:"rewards"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	got := map[string]bool{}
	for _, rw := range body.Rewards {
		got[rw.ID] = true
	}
	return got
}

// errorCode returns the error code in an error response.
func errorCode(rec *httptest.ResponseRecorder) string {
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return body.Error.Code
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status %d, want %d: %s", rec.Code, status, rec.Body.String())
	}
	if code != "" {
		if got := errorCode(rec); got != code {
			t.Fatalf("error code %q, want %q: %s", got, code, rec.Body.String())
		}
	}
}

func TestCatalogueCRUD(t *testing.T) {
	f := newCatFixture(t)
	parent := f.parent

	created := f.create(parent, `{"name":"  Pick dessert  ","cost":40,"description":"   "}`)
	if created.Name != "Pick dessert" || created.Description != nil || created.Icon != defaultIcon ||
		created.Status != "active" || created.Version != 1 || created.Limit != nil || created.ApprovalRequired {
		t.Fatalf("created = %+v", created)
	}
	if !created.InWindow || created.AvailableDays != nil || len(created.EligibleMemberIDs) != 0 {
		t.Fatalf("new reward should be open every day to everyone: %+v", created)
	}
	path := "/api/v1/rewards/" + created.ID

	rec := f.do(parent, http.MethodGet, path, "", nil)
	wantStatus(t, rec, http.StatusOK, "")
	if got := rec.Header().Get("ETag"); got != `"1"` {
		t.Fatalf("ETag = %q, want \"1\"", got)
	}

	// A stale If-Match is refused, and a malformed one is a bad request.
	rec = f.do(parent, http.MethodPatch, path, `{"cost":50}`, map[string]string{"If-Match": `"9"`})
	wantStatus(t, rec, http.StatusPreconditionFailed, "version_conflict")
	rec = f.do(parent, http.MethodPatch, path, `{}`, map[string]string{"If-Match": "abc"})
	wantStatus(t, rec, http.StatusBadRequest, "bad_request")

	// A matching If-Match updates the reward and bumps the version.
	rec = f.do(parent, http.MethodPatch, path, `{"cost":50,"limit_period":"week","limit_count":2}`,
		map[string]string{"If-Match": `"1"`})
	wantStatus(t, rec, http.StatusOK, "")
	updated := decodeCatReward(t, rec)
	if updated.Version != 2 || updated.Cost != 50 || updated.Limit == nil || updated.Limit.Period != "week" || updated.Limit.Count != 2 {
		t.Fatalf("updated = %+v", updated)
	}
	if got := rec.Header().Get("ETag"); got != `"2"` {
		t.Fatalf("ETag after PATCH = %q, want \"2\"", got)
	}

	// Absent fields keep their value. Null clears the limit.
	rec = f.do(parent, http.MethodPatch, path, `{"description":"Any dessert"}`, nil)
	wantStatus(t, rec, http.StatusOK, "")
	kept := decodeCatReward(t, rec)
	if kept.Version != 3 || kept.Limit == nil || kept.Limit.Count != 2 || kept.Description == nil || *kept.Description != "Any dessert" {
		t.Fatalf("PATCH should keep the limit: %+v", kept)
	}
	rec = f.do(parent, http.MethodPatch, path, `{"limit_period":null,"limit_count":null}`, nil)
	wantStatus(t, rec, http.StatusOK, "")
	if cleared := decodeCatReward(t, rec); cleared.Limit != nil {
		t.Fatalf("null should clear the limit: %+v", cleared)
	}

	// Archive. The reward leaves the list but a parent can still open it.
	rec = f.do(parent, http.MethodDelete, path, "", map[string]string{"If-Match": `"4"`})
	wantStatus(t, rec, http.StatusOK, "")
	if archived := decodeCatReward(t, rec); !archived.Archived || archived.Version != 5 {
		t.Fatalf("archived = %+v", archived)
	}
	if list := decodeCatList(t, f.do(parent, http.MethodGet, "/api/v1/rewards", "", nil)); len(list) != 0 {
		t.Fatalf("archived reward listed: %v", list)
	}
	rec = f.do(parent, http.MethodGet, path, "", nil)
	wantStatus(t, rec, http.StatusOK, "")
	if got := decodeCatReward(t, rec); !got.Archived {
		t.Fatalf("parent should see archived reward flagged: %+v", got)
	}
	wantStatus(t, f.do(parent, http.MethodPatch, path, `{"cost":60}`, nil), http.StatusConflict, "reward_archived")
	wantStatus(t, f.do(parent, http.MethodDelete, path, "", nil), http.StatusConflict, "reward_archived")
}

func TestCatalogueValidation(t *testing.T) {
	f := newCatFixture(t)
	otherChild := f.addChild(f.other.FamilyID, "Zed")

	cases := []struct {
		name string
		body string
		code string
	}{
		{"name missing", `{"cost":10}`, "validation_failed"},
		{"cost zero", `{"name":"x","cost":0}`, "validation_failed"},
		{"cost too large", `{"name":"x","cost":100001}`, "validation_failed"},
		{"unknown status", `{"name":"x","cost":10,"status":"paused"}`, "validation_failed"},
		{"limit without count", `{"name":"x","cost":10,"limit_period":"day"}`, "validation_failed"},
		{"limit without period", `{"name":"x","cost":10,"limit_count":2}`, "validation_failed"},
		{"unknown limit period", `{"name":"x","cost":10,"limit_period":"year","limit_count":1}`, "validation_failed"},
		{"limit count zero", `{"name":"x","cost":10,"limit_period":"day","limit_count":0}`, "validation_failed"},
		{"bad date", `{"name":"x","cost":10,"available_from":"2026-13-01"}`, "validation_failed"},
		{"window reversed", `{"name":"x","cost":10,"available_from":"2026-10-12","available_until":"2026-10-10"}`, "validation_failed"},
		{"unknown weekday", `{"name":"x","cost":10,"available_days":["funday"]}`, "validation_failed"},
		{"eligible is a parent", `{"name":"x","cost":10,"eligible_member_ids":["` + f.parent.memberID + `"]}`, "validation_failed"},
		{"eligible from another family", `{"name":"x","cost":10,"eligible_member_ids":["` + otherChild.memberID + `"]}`, "validation_failed"},
		{"unknown field", `{"name":"x","cost":10,"in_window":true}`, "bad_request"},
		{"malformed json", `{"name":`, "bad_request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.do(f.parent, http.MethodPost, "/api/v1/rewards", tc.body, nil)
			wantStatus(t, rec, http.StatusBadRequest, tc.code)
		})
	}
	if list := decodeCatList(t, f.do(f.parent, http.MethodGet, "/api/v1/rewards", "", nil)); len(list) != 0 {
		t.Fatalf("rejected rewards were stored: %v", list)
	}
}

func TestCatalogueFamilyIsolation(t *testing.T) {
	f := newCatFixture(t)
	ownChild := f.addChild(f.fam.FamilyID, "Ava")
	created := f.create(f.parent, `{"name":"Pick dessert","cost":40}`)
	path := "/api/v1/rewards/" + created.ID

	wantStatus(t, f.do(f.rival, http.MethodGet, path, "", nil), http.StatusNotFound, "reward_not_found")
	wantStatus(t, f.do(f.rival, http.MethodPatch, path, `{"cost":1}`, nil), http.StatusNotFound, "reward_not_found")
	wantStatus(t, f.do(f.rival, http.MethodDelete, path, "", nil), http.StatusNotFound, "reward_not_found")
	if list := decodeCatList(t, f.do(f.rival, http.MethodGet, "/api/v1/rewards", "", nil)); len(list) != 0 {
		t.Fatalf("another family sees the reward: %v", list)
	}

	// Another family's member cannot be made eligible for this family's reward.
	body := `{"name":"Movie","cost":10,"eligible_member_ids":["` + ownChild.memberID + `"]}`
	wantStatus(t, f.do(f.rival, http.MethodPost, "/api/v1/rewards", body, nil), http.StatusBadRequest, "validation_failed")
}

func TestChildSeesOnlyOpenRewards(t *testing.T) {
	f := newCatFixture(t)
	ava := f.addChild(f.fam.FamilyID, "Ava")
	noah := f.addChild(f.fam.FamilyID, "Noah")

	open := f.create(f.parent, `{"name":"Pick dessert","cost":40}`)
	noahOnly := f.create(f.parent, `{"name":"Family outing","cost":500,"eligible_member_ids":["`+noah.memberID+`"]}`)
	paused := f.create(f.parent, `{"name":"Paused treat","cost":60,"status":"inactive"}`)
	gone := f.create(f.parent, `{"name":"Old reward","cost":5}`)
	wantStatus(t, f.do(f.parent, http.MethodDelete, "/api/v1/rewards/"+gone.ID, "", nil), http.StatusOK, "")

	avaList := decodeCatList(t, f.do(ava, http.MethodGet, "/api/v1/rewards", "", nil))
	if len(avaList) != 1 || !avaList[open.ID] {
		t.Fatalf("Ava sees %v, want only %s", avaList, open.ID)
	}
	noahList := decodeCatList(t, f.do(noah, http.MethodGet, "/api/v1/rewards", "", nil))
	if len(noahList) != 2 || !noahList[open.ID] || !noahList[noahOnly.ID] {
		t.Fatalf("Noah sees %v", noahList)
	}
	parentList := decodeCatList(t, f.do(f.parent, http.MethodGet, "/api/v1/rewards", "", nil))
	if len(parentList) != 3 || !parentList[paused.ID] {
		t.Fatalf("parent sees %v, want three non-archived rewards", parentList)
	}

	wantStatus(t, f.do(ava, http.MethodGet, "/api/v1/rewards/"+noahOnly.ID, "", nil), http.StatusNotFound, "reward_not_found")
	wantStatus(t, f.do(ava, http.MethodGet, "/api/v1/rewards/"+paused.ID, "", nil), http.StatusNotFound, "reward_not_found")
	wantStatus(t, f.do(ava, http.MethodGet, "/api/v1/rewards/"+gone.ID, "", nil), http.StatusNotFound, "reward_not_found")
	wantStatus(t, f.do(ava, http.MethodGet, "/api/v1/rewards/"+open.ID, "", nil), http.StatusOK, "")
	wantStatus(t, f.do(noah, http.MethodGet, "/api/v1/rewards/"+noahOnly.ID, "", nil), http.StatusOK, "")
}

func TestWindowAndWeekdays(t *testing.T) {
	// catNow is a Saturday, so the day-of-week cases below are checked against it.
	f := newCatFixture(t)
	cases := []struct {
		name string
		frag string
		want bool
	}{
		{"open", `"status":"active"`, true},
		{"starts tomorrow", `"available_from":"2026-10-11"`, false},
		{"starts today", `"available_from":"2026-10-10"`, true},
		{"ended yesterday", `"available_until":"2026-10-09"`, false},
		{"ends today", `"available_until":"2026-10-10"`, true},
		{"monday only", `"available_days":["mon"]`, false},
		{"weekend", `"available_days":["Sun","sat"]`, true},
		{"every day listed", `"available_days":["mon","tue","wed","thu","fri","sat","sun"]`, true},
		{"inactive", `"status":"inactive"`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sub := f
			sub.t = t
			rw := sub.create(sub.parent, `{"name":"x","cost":1,`+tc.frag+`}`)
			if rw.InWindow != tc.want {
				t.Fatalf("in_window = %v, want %v: %+v", rw.InWindow, tc.want, rw)
			}
		})
	}

	weekend := f.create(f.parent, `{"name":"Weekend","cost":1,"available_days":["Sun","sat"]}`)
	if len(weekend.AvailableDays) != 2 || weekend.AvailableDays[0] != "sat" || weekend.AvailableDays[1] != "sun" {
		t.Fatalf("available_days = %v, want [sat sun]", weekend.AvailableDays)
	}
}
