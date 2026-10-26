package rewards

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/ids"
)

// readFixture is a catalogue fixture whose mux also serves the M4-06 read endpoints.
func readFixture(t *testing.T) catFixture {
	t.Helper()
	f := newCatFixture(t)
	h := NewHandlers(f.db, clock.FakeClock{Time: catNow}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/rewards", h.CreateReward)
	mux.HandleFunc("GET /api/v1/me/points", h.MyPoints)
	mux.HandleFunc("GET /api/v1/points/entries", h.ListEntries)
	mux.HandleFunc("GET /api/v1/points/balances", h.MemberBalances)
	mux.HandleFunc("GET /api/v1/redemptions", h.ListRedemptions)
	f.mux = mux
	return f
}

// postAt writes one ledger entry with the given timestamp.
func postAt(t *testing.T, conn *sql.DB, at time.Time, e Entry) {
	t.Helper()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	if _, err := Post(context.Background(), tx, at, e); err != nil {
		t.Fatalf("post at %s: %v", at, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// getJSON sends a GET as the actor and decodes the body into out. It returns the status code.
func getJSON(t *testing.T, f catFixture, a catActor, target string, out any) int {
	t.Helper()
	rec := f.do(a, http.MethodGet, target, "", nil)
	if out != nil && rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("decode %s: %v (%s)", target, err, rec.Body.String())
		}
	}
	return rec.Code
}

func TestMyPointsTasksAndBonusPerDay(t *testing.T) {
	f := readFixture(t)
	child := f.addChild(f.fam.FamilyID, "Ana")

	// Week of Monday 5 October 2026. Each occurrence has its own definition and due time.
	seed := func(due, occStatus, asgStatus, completed string) {
		defID, occID, asgID := ids.New(), ids.New(), ids.New()
		if _, err := f.db.Exec(
			`INSERT INTO task_definitions (id, family_id, title, time_zone, starts_on, points, created_by)
			 VALUES (?, ?, 'Tidy', 'UTC', ?, 5, ?)`, defID, f.fam.FamilyID, due, f.fam.ParentUserID); err != nil {
			t.Fatalf("insert definition: %v", err)
		}
		if _, err := f.db.Exec(
			`INSERT INTO task_occurrences (id, family_id, task_definition_id, due_date, due_at, status)
			 VALUES (?, ?, ?, ?, ?, ?)`, occID, f.fam.FamilyID, defID, due, due+" 18:00:00.000+00:00", occStatus); err != nil {
			t.Fatalf("insert occurrence: %v", err)
		}
		var done any
		if completed != "" {
			done = completed
		}
		if _, err := f.db.Exec(
			`INSERT INTO task_occurrence_assignees (id, family_id, task_occurrence_id, family_member_id, status, completed_at, decided_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`, asgID, f.fam.FamilyID, occID, child.memberID, asgStatus, done, done); err != nil {
			t.Fatalf("insert assignee: %v", err)
		}
	}
	seed("2026-10-06", "scheduled", "approved", "2026-10-06 10:00:00.000+00:00") // Tue, done
	seed("2026-10-11", "scheduled", "approved", "2026-10-11 09:00:00.000+00:00") // Sun, done
	seed("2026-10-08", "missed", "to_do", "")                                    // Thu, missed
	seed("2026-10-04", "missed", "to_do", "")                                    // previous week, ignored
	seed("2026-10-05", "scheduled", "approved", "2026-10-03 09:00:00.000+00:00") // completed before the week, ignored

	postAt(t, f.db, time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC), Entry{FamilyID: f.fam.FamilyID, MemberID: child.memberID, Type: Bonus, Amount: 4, IdempotencyKey: "b1"})
	postAt(t, f.db, time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC), Entry{FamilyID: f.fam.FamilyID, MemberID: child.memberID, Type: Bonus, Amount: 9, IdempotencyKey: "b2"})

	var got MyPointsResponse
	if code := getJSON(t, f, child, "/api/v1/me/points", &got); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if want := [7]int{0, 1, 0, 0, 0, 0, 1}; got.TasksDone != want {
		t.Errorf("tasks_done = %v, want %v", got.TasksDone, want)
	}
	if want := [7]int{0, 0, 0, 1, 0, 0, 0}; got.TasksMissed != want {
		t.Errorf("tasks_missed = %v, want %v", got.TasksMissed, want)
	}
	if want := [7]int{0, 0, 4, 0, 0, 0, 0}; got.Bonus != want {
		t.Errorf("bonus = %v, want %v", got.Bonus, want)
	}
}

func TestMyPointsBalanceReservedAndWeek(t *testing.T) {
	f := readFixture(t)
	child := f.addChild(f.fam.FamilyID, "Ana")

	// Week of Monday 5 October 2026 (UTC). Sunday 4 October belongs to the previous week.
	postAt(t, f.db, time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC), Entry{FamilyID: f.fam.FamilyID, MemberID: child.memberID, Type: TaskAward, Amount: 10, IdempotencyKey: "w1"})
	postAt(t, f.db, time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC), Entry{FamilyID: f.fam.FamilyID, MemberID: child.memberID, Type: Bonus, Amount: 7, IdempotencyKey: "w2"})
	postAt(t, f.db, time.Date(2026, 10, 4, 23, 0, 0, 0, time.UTC), Entry{FamilyID: f.fam.FamilyID, MemberID: child.memberID, Type: TaskAward, Amount: 50, IdempotencyKey: "w3"})

	rw := f.create(f.parent, `{"name":"Movie","cost":20,"approval_required":true}`)
	if _, err := f.redeemAt(catNow, child, rw.ID, "hold1"); err != nil {
		t.Fatalf("redeem: %v", err)
	}

	var got MyPointsResponse
	if code := getJSON(t, f, child, "/api/v1/me/points", &got); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if got.Balance != 47 { // 10 + 7 + 50 - 20 held
		t.Errorf("balance = %d, want 47", got.Balance)
	}
	if got.Reserved != 20 {
		t.Errorf("reserved = %d, want 20", got.Reserved)
	}
	if got.WeekStart != "2026-10-05" || got.Today != 5 {
		t.Errorf("week_start=%s today=%d, want 2026-10-05 and 5", got.WeekStart, got.Today)
	}
	if want := [7]int{10, 0, 0, 0, 0, 7, 0}; got.Week != want {
		t.Errorf("week = %v, want %v", got.Week, want)
	}
	if got.MemberID != child.memberID {
		t.Errorf("member_id = %s, want the caller", got.MemberID)
	}
}

func TestEarnedThisWeekSplitsByMemberFromWeekStart(t *testing.T) {
	f := readFixture(t)
	ana := f.addChild(f.fam.FamilyID, "Ana")
	ben := f.addChild(f.fam.FamilyID, "Ben")
	idle := f.addChild(f.fam.FamilyID, "Cal")

	// Monday 5 October starts the week. Sunday 4 October does not count.
	postAt(t, f.db, time.Date(2026, 10, 4, 23, 0, 0, 0, time.UTC), Entry{FamilyID: f.fam.FamilyID, MemberID: ana.memberID, Type: TaskAward, Amount: 50, IdempotencyKey: "e0"})
	postAt(t, f.db, time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC), Entry{FamilyID: f.fam.FamilyID, MemberID: ana.memberID, Type: TaskAward, Amount: 10, IdempotencyKey: "e1"})
	postAt(t, f.db, time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC), Entry{FamilyID: f.fam.FamilyID, MemberID: ana.memberID, Type: Bonus, Amount: 7, IdempotencyKey: "e2"})
	postAt(t, f.db, time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC), Entry{FamilyID: f.fam.FamilyID, MemberID: ben.memberID, Type: TaskAward, Amount: 3, IdempotencyKey: "e3"})

	got, err := EarnedThisWeek(context.Background(), f.db, f.fam.FamilyID, time.UTC, time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("EarnedThisWeek: %v", err)
	}
	if got[ana.memberID] != 17 || got[ben.memberID] != 3 {
		t.Errorf("earned = %v, want Ana 17 and Ben 3", got)
	}
	if _, ok := got[idle.memberID]; ok {
		t.Errorf("earned lists a member with no earnings: %v", got)
	}
}

func TestMemberBalancesParentOnlyAndActiveChildren(t *testing.T) {
	f := readFixture(t)
	ana := f.addChild(f.fam.FamilyID, "Ana")
	ben := f.addChild(f.fam.FamilyID, "Ben")
	gone := f.addChild(f.fam.FamilyID, "Cal")
	if _, err := f.db.Exec(`UPDATE family_members SET status = 'removed' WHERE id = ?`, gone.memberID); err != nil {
		t.Fatalf("remove member: %v", err)
	}

	postAt(t, f.db, catNow, Entry{FamilyID: f.fam.FamilyID, MemberID: ana.memberID, Type: TaskAward, Amount: 30, IdempotencyKey: "b1"})
	postAt(t, f.db, catNow, Entry{FamilyID: f.fam.FamilyID, MemberID: ben.memberID, Type: TaskAward, Amount: 10, IdempotencyKey: "b2"})
	postAt(t, f.db, catNow, Entry{FamilyID: f.fam.FamilyID, MemberID: gone.memberID, Type: TaskAward, Amount: 99, IdempotencyKey: "b3"})
	rw := f.create(f.parent, `{"name":"Movie","cost":20,"approval_required":true}`)
	if _, err := f.redeemAt(catNow, ana, rw.ID, "hold-b"); err != nil {
		t.Fatalf("redeem: %v", err)
	}

	var got MemberBalancesResponse
	if code := getJSON(t, f, f.parent, "/api/v1/points/balances", &got); code != http.StatusOK {
		t.Fatalf("parent: status %d", code)
	}
	want := []MemberBalance{
		{MemberID: ana.memberID, Balance: 10, Reserved: 20},
		{MemberID: ben.memberID, Balance: 10, Reserved: 0},
	}
	if len(got.Members) != len(want) {
		t.Fatalf("members = %+v, want %+v", got.Members, want)
	}
	for i := range want {
		if got.Members[i] != want[i] {
			t.Errorf("member %d = %+v, want %+v", i, got.Members[i], want[i])
		}
	}

	if code := getJSON(t, f, ana, "/api/v1/points/balances", nil); code != http.StatusForbidden {
		t.Errorf("child: status %d, want 403", code)
	}
}

func TestMyPointsUsesFamilyZone(t *testing.T) {
	f := readFixture(t)
	child := f.addChild(f.fam.FamilyID, "Ana")
	if _, err := f.db.Exec(`UPDATE families SET timezone = 'Pacific/Auckland' WHERE id = ?`, f.fam.FamilyID); err != nil {
		t.Fatalf("set zone: %v", err)
	}
	// 14:00 UTC on Sunday is 03:00 on Monday in Auckland (UTC+13 in October). It counts for Monday.
	postAt(t, f.db, time.Date(2026, 10, 4, 14, 0, 0, 0, time.UTC), Entry{FamilyID: f.fam.FamilyID, MemberID: child.memberID, Type: TaskAward, Amount: 10, IdempotencyKey: "z1"})

	var got MyPointsResponse
	if code := getJSON(t, f, child, "/api/v1/me/points", &got); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if got.Week[0] != 10 {
		t.Errorf("week = %v, want Monday to hold 10 in the family zone", got.Week)
	}
	if got.Timezone != "Pacific/Auckland" {
		t.Errorf("timezone = %s", got.Timezone)
	}
}

func TestMyPointsRequiresMembership(t *testing.T) {
	f := readFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/me/points", nil)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
}

func TestEntriesChildSeesOnlyOwnAndPages(t *testing.T) {
	f := readFixture(t)
	child := f.addChild(f.fam.FamilyID, "Ana")
	other := f.addChild(f.fam.FamilyID, "Ben")
	base := time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC)
	for i, key := range []string{"e1", "e2", "e3"} {
		postAt(t, f.db, base.Add(time.Duration(i)*time.Hour), Entry{FamilyID: f.fam.FamilyID, MemberID: child.memberID, Type: Bonus, Amount: 1 + i, IdempotencyKey: key})
	}
	postAt(t, f.db, base, Entry{FamilyID: f.fam.FamilyID, MemberID: other.memberID, Type: Bonus, Amount: 99, IdempotencyKey: "b1"})

	var first PointsEntryPage
	if code := getJSON(t, f, child, "/api/v1/points/entries?limit=2", &first); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if len(first.Entries) != 2 || first.NextCursor == nil {
		t.Fatalf("first page = %d entries, next=%v; want 2 and a cursor", len(first.Entries), first.NextCursor)
	}
	if first.Entries[0].Amount != 3 {
		t.Errorf("newest first: amount = %d, want 3", first.Entries[0].Amount)
	}

	var second PointsEntryPage
	if code := getJSON(t, f, child, "/api/v1/points/entries?limit=2&cursor="+*first.NextCursor, &second); code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if len(second.Entries) != 1 || second.NextCursor != nil || second.Entries[0].Amount != 1 {
		t.Fatalf("second page = %+v, want the oldest entry and no cursor", second)
	}
	for _, e := range append(first.Entries, second.Entries...) {
		if e.MemberID != child.memberID {
			t.Errorf("child saw an entry of member %s", e.MemberID)
		}
	}

	var typed PointsEntryPage
	getJSON(t, f, child, "/api/v1/points/entries?type=task_award", &typed)
	if len(typed.Entries) != 0 {
		t.Errorf("type filter returned %d entries, want 0", len(typed.Entries))
	}
}

func TestEntriesParentFiltersAndChildIsolation(t *testing.T) {
	f := readFixture(t)
	child := f.addChild(f.fam.FamilyID, "Ana")
	postAt(t, f.db, time.Date(2026, 10, 9, 9, 0, 0, 0, time.UTC), Entry{FamilyID: f.fam.FamilyID, MemberID: child.memberID, Type: Bonus, Amount: 5, IdempotencyKey: "p1"})
	postAt(t, f.db, time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC), Entry{FamilyID: f.fam.FamilyID, MemberID: child.memberID, Type: Bonus, Amount: 6, IdempotencyKey: "p2"})

	var byMember PointsEntryPage
	getJSON(t, f, f.parent, "/api/v1/points/entries?member_id="+child.memberID, &byMember)
	if len(byMember.Entries) != 2 {
		t.Errorf("parent member filter = %d entries, want 2", len(byMember.Entries))
	}

	var byDay PointsEntryPage
	getJSON(t, f, f.parent, "/api/v1/points/entries?member_id="+child.memberID+"&from=2026-10-09&to=2026-10-09", &byDay)
	if len(byDay.Entries) != 1 || byDay.Entries[0].Amount != 5 {
		t.Errorf("date filter = %+v, want the 9 October entry only", byDay.Entries)
	}

	rec := f.do(child, http.MethodGet, "/api/v1/points/entries?member_id="+f.parent.memberID, "", nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("child reads a parent: status %d, want 403", rec.Code)
	}
}

func TestEntriesRejectBadQueries(t *testing.T) {
	f := readFixture(t)
	child := f.addChild(f.fam.FamilyID, "Ana")
	for _, target := range []string{
		"/api/v1/points/entries?type=nope",
		"/api/v1/points/entries?limit=0",
		"/api/v1/points/entries?limit=201",
		"/api/v1/points/entries?limit=abc",
		"/api/v1/points/entries?cursor=bogus",
		"/api/v1/points/entries?from=2026-13-01",
		"/api/v1/points/entries?to=tomorrow",
	} {
		rec := f.do(child, http.MethodGet, target, "", nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", target, rec.Code)
		}
	}
}

func TestRedemptionsListScopedAndFiltered(t *testing.T) {
	f := readFixture(t)
	ana := f.addChild(f.fam.FamilyID, "Ana")
	ben := f.addChild(f.fam.FamilyID, "Ben")
	fund(t, f, ana.memberID, 100)
	fund(t, f, ben.memberID, 100)

	pending := f.create(f.parent, `{"name":"Movie","cost":30,"approval_required":true}`)
	instant := f.create(f.parent, `{"name":"Sticker","cost":5}`)
	if _, err := f.redeemAt(catNow, ana, pending.ID, "a1"); err != nil {
		t.Fatalf("ana pending: %v", err)
	}
	if _, err := f.redeemAt(catNow, ana, instant.ID, "a2"); err != nil {
		t.Fatalf("ana instant: %v", err)
	}
	if _, err := f.redeemAt(catNow, ben, pending.ID, "b1"); err != nil {
		t.Fatalf("ben pending: %v", err)
	}

	var all RedemptionPage
	if code := getJSON(t, f, f.parent, "/api/v1/redemptions", &all); code != http.StatusOK {
		t.Fatalf("parent list: status %d", code)
	}
	if len(all.Redemptions) != 3 {
		t.Fatalf("parent sees %d rows, want 3", len(all.Redemptions))
	}
	for _, r := range all.Redemptions {
		if r.RewardName == "" || r.MemberName == "" {
			t.Errorf("row %s lacks reward or member name", r.ID)
		}
	}

	var waiting RedemptionPage
	getJSON(t, f, f.parent, "/api/v1/redemptions?status=pending", &waiting)
	if len(waiting.Redemptions) != 2 {
		t.Errorf("pending filter = %d rows, want 2", len(waiting.Redemptions))
	}
	var mixed RedemptionPage
	getJSON(t, f, f.parent, "/api/v1/redemptions?status=approved,rejected", &mixed)
	if len(mixed.Redemptions) != 1 || mixed.Redemptions[0].Status != "approved" {
		t.Errorf("approved,rejected filter = %+v, want the instant approval", mixed.Redemptions)
	}

	var own RedemptionPage
	getJSON(t, f, ana, "/api/v1/redemptions", &own)
	if len(own.Redemptions) != 2 {
		t.Fatalf("child sees %d rows, want their 2", len(own.Redemptions))
	}
	for _, r := range own.Redemptions {
		if r.MemberID != ana.memberID {
			t.Errorf("child saw a request of member %s", r.MemberID)
		}
	}
	if rec := f.do(ana, http.MethodGet, "/api/v1/redemptions?member_id="+ben.memberID, "", nil); rec.Code != http.StatusForbidden {
		t.Errorf("child reads another child: status %d, want 403", rec.Code)
	}
}

func TestRedemptionsRejectBadQueries(t *testing.T) {
	f := readFixture(t)
	child := f.addChild(f.fam.FamilyID, "Ana")
	for _, target := range []string{
		"/api/v1/redemptions?status=lost",
		"/api/v1/redemptions?status=pending,lost",
		"/api/v1/redemptions?limit=500",
		"/api/v1/redemptions?cursor=bogus",
	} {
		rec := f.do(child, http.MethodGet, target, "", nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", target, rec.Code)
		}
	}
}
