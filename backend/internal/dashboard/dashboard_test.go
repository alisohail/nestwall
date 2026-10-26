package dashboard

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	"nestwall/backend/internal/auth"
	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/rewards"
	"nestwall/backend/internal/tasks"
	"nestwall/backend/internal/testhelper"
)

// childRoleID is the seeded Child role from migration 000001.
const childRoleID = "c3d4e5f6-a7b8-9012-cdef-123456789012"

// fixedNow is a Wednesday in the family's time zone (UTC in the default fixture).
var fixedNow = time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)

// env wires the dashboard and the task actions behind the real permission middleware.
type env struct {
	t           *testing.T
	conn        *sql.DB
	mux         *http.ServeMux
	family      testhelper.Family
	child       string
	childCookie *http.Cookie
}

func newEnv(t *testing.T, now time.Time) *env {
	t.Helper()
	conn := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, conn)
	clk := clock.FakeClock{Time: now}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	childID, childCookie := addChild(t, conn, clk, fam.FamilyID)

	permMW := permissions.Middleware(conn, clk)
	tasksH := tasks.NewHandlers(conn, clk, logger)
	tasksH.Participate(rewards.NewTaskAwards(clk).Apply)
	dashH := NewHandlers(conn, clk, logger)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/dashboard/parent", permissions.RequireParent(conn, clk)(dashH.Parent))
	mux.Handle("POST /api/v1/tasks", permMW(permissions.PermTasksManage, tasksH.CreateTask))
	mux.Handle("POST /api/v1/tasks/{id}/complete", permMW(permissions.PermTasksComplete, tasksH.Complete))
	mux.Handle("POST /api/v1/tasks/{id}/approve", permMW(permissions.PermTasksApprove, tasksH.Approve))
	mux.Handle("POST /api/v1/tasks/{id}/skip", permMW(permissions.PermTasksManage, tasksH.Skip))

	rewardsH := rewards.NewHandlers(conn, clk, logger)
	mux.Handle("POST /api/v1/rewards", permMW(permissions.PermRewardsManage, rewardsH.CreateReward))
	mux.Handle("POST /api/v1/rewards/{id}/redeem", permMW(permissions.PermRewardsRedeem, rewardsH.RedeemReward))

	return &env{t: t, conn: conn, mux: mux, family: fam, child: childID, childCookie: childCookie}
}

// addChild creates an active child member with a valid session and returns the member ID and cookie.
func addChild(t *testing.T, conn *sql.DB, clk clock.Clock, familyID string) (string, *http.Cookie) {
	t.Helper()
	ctx := context.Background()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()

	userID := ids.New()
	hash, err := auth.HashPassword("TestPassword1!")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO users (id, user_id, password_hash, display_name, is_child) VALUES (?, ?, ?, 'Ava', 1)`,
		userID, "child_"+userID[:8], hash,
	); err != nil {
		t.Fatalf("insert child user: %v", err)
	}

	memberID := ids.New()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO family_members (id, family_id, user_id, role_id, status, colour)
		 VALUES (?, ?, ?, ?, 'active', '#F59E0B')`,
		memberID, familyID, userID, childRoleID,
	); err != nil {
		t.Fatalf("insert child member: %v", err)
	}

	sess, err := auth.CreateSession(ctx, tx, clk, userID, "dashboard-test")
	if err != nil {
		t.Fatalf("create child session: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit child: %v", err)
	}
	return memberID, &http.Cookie{Name: auth.SessionCookieName, Value: sess.Token}
}

func (e *env) do(method, path string, cookie *http.Cookie, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			e.t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func (e *env) exec(query string, args ...any) {
	e.t.Helper()
	if _, err := e.conn.ExecContext(context.Background(), query, args...); err != nil {
		e.t.Fatalf("exec %q: %v", query, err)
	}
}

// createTask creates a one-off task for assignee and returns its occurrence ID, which is the task ID.
func (e *env) createTask(title, dueDate, assignee string, cookie *http.Cookie) string {
	e.t.Helper()
	rec := e.do("POST", "/api/v1/tasks", cookie, map[string]any{
		"title":        title,
		"due_date":     dueDate,
		"points":       5,
		"assignee_ids": []string{assignee},
	})
	if rec.Code != http.StatusCreated {
		e.t.Fatalf("create %q: status %d, body %s", title, rec.Code, rec.Body.String())
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.ID == "" {
		e.t.Fatalf("create %q: no id in %s", title, rec.Body.String())
	}
	return out.ID
}

func (e *env) dashboard(cookie *http.Cookie) Response {
	e.t.Helper()
	rec := e.do("GET", "/api/v1/dashboard/parent", cookie, nil)
	if rec.Code != http.StatusOK {
		e.t.Fatalf("dashboard: status %d, body %s", rec.Code, rec.Body.String())
	}
	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		e.t.Fatalf("decode dashboard: %v", err)
	}
	return resp
}

func titlesOf(items []Item) string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Title)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

func TestParentDashboardCountsBandsAndActivity(t *testing.T) {
	e := newEnv(t, fixedNow)
	parent := e.family.SessionCookie

	makeBed := e.createTask("Make bed", "2026-10-10", e.child, parent)
	walkDog := e.createTask("Walk dog", "2026-10-10", e.child, parent)
	tidyDesk := e.createTask("Tidy desk", "2026-10-10", e.child, parent)
	feedFish := e.createTask("Feed fish", "2026-10-10", e.child, parent)
	e.createTask("Read book", "2026-10-11", e.child, parent) // tomorrow: not due today
	e.createTask("Mow lawn", "2026-10-12", e.child, parent) // two days out: not due today
	e.createTask("Water plants", "2026-10-20", e.child, parent) // not due today

	approveBody := map[string]string{"member_id": e.child}
	skipBody := map[string]string{"member_id": e.child}

	if rec := e.do("POST", "/api/v1/tasks/"+makeBed+"/complete", e.childCookie, nil); rec.Code != http.StatusOK {
		t.Fatalf("complete make bed: status %d, body %s", rec.Code, rec.Body.String())
	}
	if rec := e.do("POST", "/api/v1/tasks/"+walkDog+"/complete", e.childCookie, nil); rec.Code != http.StatusOK {
		t.Fatalf("complete walk dog: status %d, body %s", rec.Code, rec.Body.String())
	}
	if rec := e.do("POST", "/api/v1/tasks/"+walkDog+"/approve", parent, approveBody); rec.Code != http.StatusOK {
		t.Fatalf("approve walk dog: status %d, body %s", rec.Code, rec.Body.String())
	}
	if rec := e.do("POST", "/api/v1/tasks/"+tidyDesk+"/skip", parent, skipBody); rec.Code != http.StatusOK {
		t.Fatalf("skip tidy desk: status %d, body %s", rec.Code, rec.Body.String())
	}
	e.exec(`UPDATE task_occurrences SET status = 'past_due' WHERE id = ?`, feedFish)

	resp := e.dashboard(parent)

	want := Summary{TodayTotal: 3, TodayDone: 1, Overdue: 1, WaitingApproval: 1, PointsWeek: 5}
	if resp.Summary != want {
		t.Errorf("summary = %+v, want %+v", resp.Summary, want)
	}
	if got := titlesOf(resp.Today); got != "Feed fish, Make bed, Walk dog" {
		t.Errorf("today band = %q", got)
	}

	var child *Person
	for i := range resp.People {
		if resp.People[i].MemberID == e.child {
			child = &resp.People[i]
		}
	}
	if child == nil {
		t.Fatalf("people has no child: %+v", resp.People)
	}
	if child.Total != 3 || child.Done != 1 || child.MemberName != "Ava" || child.PointsWeek != 5 || child.Overdue != 1 {
		t.Errorf("child progress = %+v, want total 3, done 1, overdue 1, 5 points this week", child)
	}

	overdueSum := 0
	for _, p := range resp.People {
		overdueSum += p.Overdue
	}
	if overdueSum != resp.Summary.Overdue {
		t.Errorf("per-person overdue sums to %d, summary overdue is %d", overdueSum, resp.Summary.Overdue)
	}
	if child.WeekTotal != 4 || child.WeekDone != 1 {
		t.Errorf("child week progress = %d of %d, want 1 of 4 (skipped copy and next week excluded)", child.WeekDone, child.WeekTotal)
	}

	if len(resp.Approvals) != 1 || resp.Approvals[0].TaskID != makeBed || resp.Approvals[0].MemberID != e.child {
		t.Errorf("approvals = %+v, want one entry for make bed", resp.Approvals)
	}

	counts := map[string]int{}
	for _, a := range resp.Activity {
		counts[a.Action]++
	}
	if len(resp.Activity) != 4 || counts["task.completed"] != 2 || counts["task.approved"] != 1 || counts["task.skipped"] != 1 {
		t.Errorf("activity = %+v", resp.Activity)
	}
}

func TestParentDashboardWeekCountsStayInsideTheWeek(t *testing.T) {
	e := newEnv(t, fixedNow)
	parent := e.family.SessionCookie

	// fixedNow is Saturday 2026-10-10, so the week runs Monday 2026-10-05 to Sunday 2026-10-11.
	e.createTask("Last Sunday", "2026-10-04", e.child, parent)
	e.createTask("This Monday", "2026-10-05", e.child, parent)
	e.createTask("This Sunday", "2026-10-11", e.child, parent)
	e.createTask("Next Monday", "2026-10-12", e.child, parent)

	resp := e.dashboard(parent)

	var child *Person
	for i := range resp.People {
		if resp.People[i].MemberID == e.child {
			child = &resp.People[i]
		}
	}
	if child == nil {
		t.Fatalf("people has no child: %+v", resp.People)
	}
	if child.Total != 0 || child.WeekTotal != 2 || child.WeekDone != 0 {
		t.Errorf("child = %+v, want today 0, week 0 of 2", child)
	}
}

func TestParentDashboardPointsAndRewardRequests(t *testing.T) {
	e := newEnv(t, fixedNow)
	parent := e.family.SessionCookie

	rec := e.do("POST", "/api/v1/rewards", parent, map[string]any{"name": "Movie", "cost": 5, "approval_required": true})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create reward: status %d, body %s", rec.Code, rec.Body.String())
	}
	var reward struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reward); err != nil || reward.ID == "" {
		t.Fatalf("create reward: no id in %s", rec.Body.String())
	}

	for _, title := range []string{"Make bed", "Walk dog"} {
		id := e.createTask(title, "2026-10-10", e.child, parent)
		if rec := e.do("POST", "/api/v1/tasks/"+id+"/complete", e.childCookie, nil); rec.Code != http.StatusOK {
			t.Fatalf("complete %s: status %d, body %s", title, rec.Code, rec.Body.String())
		}
		if rec := e.do("POST", "/api/v1/tasks/"+id+"/approve", parent, map[string]string{"member_id": e.child}); rec.Code != http.StatusOK {
			t.Fatalf("approve %s: status %d, body %s", title, rec.Code, rec.Body.String())
		}
	}

	if rec := e.redeem(reward.ID, "dash-1", e.childCookie); rec.Code != http.StatusCreated {
		t.Fatalf("redeem: status %d, body %s", rec.Code, rec.Body.String())
	}

	resp := e.dashboard(parent)

	if resp.Summary.PointsWeek != 10 || resp.Summary.WaitingRewards != 1 || resp.Summary.WaitingApproval != 0 {
		t.Errorf("summary = %+v, want 10 points, 1 waiting reward, 0 waiting task approval", resp.Summary)
	}
	var child *Person
	for i := range resp.People {
		if resp.People[i].MemberID == e.child {
			child = &resp.People[i]
		}
	}
	if child == nil || child.PointsWeek != 10 {
		t.Errorf("child = %+v, want 10 points this week", child)
	}
	if len(resp.RewardRequests) != 1 {
		t.Fatalf("reward requests = %+v, want one", resp.RewardRequests)
	}
	got := resp.RewardRequests[0]
	if got.RewardName != "Movie" || got.Cost != 5 || got.MemberID != e.child || got.RedemptionID == "" || got.RequestedAt == "" {
		t.Errorf("reward request = %+v", got)
	}
}

// redeem sends a redemption request with the Idempotency-Key header the endpoint requires.
func (e *env) redeem(rewardID, key string, cookie *http.Cookie) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/rewards/"+rewardID+"/redeem", nil)
	req.Header.Set("Idempotency-Key", key)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func TestParentDashboardReturnsCurrentAvatarForPeople(t *testing.T) {
	e := newEnv(t, fixedNow)
	avatarID := ids.New()
	e.exec(`INSERT INTO member_avatars (id, family_id, family_member_id) SELECT ?, family_id, ? FROM family_members WHERE id = ?`,
		avatarID, e.child, e.child)

	resp := e.dashboard(e.family.SessionCookie)
	for _, p := range resp.People {
		if p.MemberID != e.child {
			continue
		}
		if p.AvatarID == nil || *p.AvatarID != avatarID {
			t.Fatalf("avatar_id = %v, want %s", p.AvatarID, avatarID)
		}
		return
	}
	t.Fatalf("child %s missing from people: %+v", e.child, resp.People)
}

func TestParentDashboardEmptyFamilyReturnsArrays(t *testing.T) {
	e := newEnv(t, fixedNow)

	rec := e.do("GET", "/api/v1/dashboard/parent", e.family.SessionCookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{"today", "approvals", "activity"} {
		if string(raw[key]) != "[]" {
			t.Errorf("%s = %s, want []", key, raw[key])
		}
	}
	resp := e.dashboard(e.family.SessionCookie)
	if resp.Summary != (Summary{}) {
		t.Errorf("summary = %+v, want zeros", resp.Summary)
	}
}

func TestParentDashboardUsesFamilyTimeZoneForToday(t *testing.T) {
	// 23:30 UTC on 10 October is already 11 October in Auckland (UTC+13 in October).
	e := newEnv(t, time.Date(2026, 10, 10, 23, 30, 0, 0, time.UTC))
	e.exec(`UPDATE families SET timezone = 'Pacific/Auckland' WHERE id = ?`, e.family.FamilyID)

	resp := e.dashboard(e.family.SessionCookie)
	if resp.Date != "2026-10-11" || resp.Timezone != "Pacific/Auckland" {
		t.Errorf("date = %q, timezone = %q; want 2026-10-11 and Pacific/Auckland", resp.Date, resp.Timezone)
	}
}

func TestParentDashboardIsScopedToFamily(t *testing.T) {
	e := newEnv(t, fixedNow)
	other := testhelper.OneFamily(t, e.conn)

	e.createTask("Other family task", "2026-10-10", other.MemberID, other.SessionCookie)

	if got := e.dashboard(e.family.SessionCookie); got.Summary.TodayTotal != 0 || len(got.Today) != 0 {
		t.Errorf("family A sees family B's task: %+v", got)
	}
	if got := e.dashboard(other.SessionCookie); got.Summary.TodayTotal != 1 {
		t.Errorf("family B summary = %+v, want one task today", got.Summary)
	}
}

func TestParentDashboardRequiresSignInAndParentPermission(t *testing.T) {
	e := newEnv(t, fixedNow)

	if rec := e.do("GET", "/api/v1/dashboard/parent", nil, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("no session: status %d, want 401", rec.Code)
	}
	if rec := e.do("GET", "/api/v1/dashboard/parent", e.childCookie, nil); rec.Code != http.StatusForbidden {
		t.Errorf("child: status %d, want 403", rec.Code)
	}
}
