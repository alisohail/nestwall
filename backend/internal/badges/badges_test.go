package badges

import (
	"context"
	"database/sql"
	"slices"
	"testing"
	"time"

	"nestwall/backend/internal/badges/catalogue"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/rewards"
	"nestwall/backend/internal/tasks"
	"nestwall/backend/internal/tasks/seed"
	"nestwall/backend/internal/testhelper"
)

var testNow = time.Date(2026, 10, 12, 20, 0, 0, 0, time.UTC)

// childRoleID is the seeded Child role (migration 000001).
const childRoleID = "c3d4e5f6-a7b8-9012-cdef-123456789012"

// copyOf describes one task copy assigned to the child.
type copyOf struct {
	category string // category badge key such as "tidy_pro"; empty for none
	dueDate  string // YYYY-MM-DD
	status   string // approved, to_do, skipped, ...
	late     bool   // approved after the due time
}

// fixture makes a family with the default categories and badges, plus one child.
func fixture(t *testing.T, conn *sql.DB) (testhelper.Family, string) {
	t.Helper()
	f := testhelper.OneFamily(t, conn)
	ctx := context.Background()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	if err := seed.SeedDefaultCategories(ctx, tx, f.FamilyID); err != nil {
		t.Fatalf("seed categories: %v", err)
	}
	if err := catalogue.SeedDefaults(ctx, tx, f.FamilyID); err != nil {
		t.Fatalf("seed badges: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit seed: %v", err)
	}
	return f, newChild(t, conn, f)
}

// newChild adds an active child member to the family.
func newChild(t *testing.T, conn *sql.DB, f testhelper.Family) string {
	t.Helper()
	userID, memberID := ids.New(), ids.New()
	mustExec(t, conn,
		`INSERT INTO users (id, user_id, password_hash, display_name, is_child) VALUES (?, ?, 'unused', 'Kid', 1)`,
		userID, "kid_"+userID)
	mustExec(t, conn,
		`INSERT INTO family_members (id, family_id, user_id, role_id, status, colour) VALUES (?, ?, ?, ?, 'active', '#F59E0B')`,
		memberID, f.FamilyID, userID, childRoleID)
	return memberID
}

func mustExec(t *testing.T, conn *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := conn.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// categoryID returns the family's category with the badge key.
func categoryID(t *testing.T, conn *sql.DB, familyID, badgeKey string) string {
	t.Helper()
	var id string
	if err := conn.QueryRow(
		`SELECT id FROM task_categories WHERE family_id = ? AND badge_key = ?`, familyID, badgeKey,
	).Scan(&id); err != nil {
		t.Fatalf("category %s: %v", badgeKey, err)
	}
	return id
}

// addCopy adds one task definition, occurrence and child assignee row. An approved copy is completed
// at its due time, unless it is late.
func addCopy(t *testing.T, conn *sql.DB, f testhelper.Family, memberID string, c copyOf) {
	t.Helper()
	var categoryArg any
	if c.category != "" {
		categoryArg = categoryID(t, conn, f.FamilyID, c.category)
	}
	dueAt := c.dueDate + " 18:00:00.000+00:00"
	defID, occID := ids.New(), ids.New()

	mustExec(t, conn,
		`INSERT INTO task_definitions (id, family_id, title, time_zone, starts_on, points, created_by, category_id)
		 VALUES (?, ?, 'Task', 'UTC', ?, 1, ?, ?)`,
		defID, f.FamilyID, c.dueDate, f.ParentUserID, categoryArg)
	mustExec(t, conn,
		`INSERT INTO task_occurrences (id, family_id, task_definition_id, due_date, due_at) VALUES (?, ?, ?, ?, ?)`,
		occID, f.FamilyID, defID, c.dueDate, dueAt)

	var completedAt, decidedAt, decidedBy any
	lateFlag := 0
	if c.late {
		lateFlag = 1
	}
	if c.status == "approved" {
		completedAt, decidedAt, decidedBy = dueAt, dueAt, f.MemberID
	}
	mustExec(t, conn,
		`INSERT INTO task_occurrence_assignees
			(id, family_id, task_occurrence_id, family_member_id, status, completed_at, completed_late, decided_at, decided_by_member_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ids.New(), f.FamilyID, occID, memberID, c.status, completedAt, lateFlag, decidedAt, decidedBy)
}

// addApproved adds one approved copy with no category.
func addApproved(t *testing.T, conn *sql.DB, f testhelper.Family, memberID, date string) {
	t.Helper()
	addCopy(t, conn, f, memberID, copyOf{dueDate: date, status: "approved"})
}

// post writes one ledger row with the parent as actor.
func post(t *testing.T, conn *sql.DB, f testhelper.Family, memberID string, typ rewards.EntryType, amount int, key string) {
	t.Helper()
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	if _, err := rewards.Post(context.Background(), tx, testNow, rewards.Entry{
		FamilyID: f.FamilyID, MemberID: memberID, Type: typ, Amount: amount,
		ActorMemberID: f.MemberID, IdempotencyKey: key,
	}); err != nil {
		t.Fatalf("post %s: %v", key, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit %s: %v", key, err)
	}
}

// evaluate runs Evaluate for the child and returns the keys awarded now.
func evaluate(t *testing.T, conn *sql.DB, f testhelper.Family, memberID string) []string {
	t.Helper()
	keys, err := Evaluate(context.Background(), conn, f.FamilyID, memberID, testNow)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	return keys
}

// awardCount returns how many member_badges rows the child has for the built-in key.
func awardCount(t *testing.T, conn *sql.DB, memberID, key string) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(
		`SELECT COUNT(*) FROM member_badges mb
		 JOIN badge_definitions d ON d.id = mb.badge_definition_id
		 WHERE mb.family_member_id = ? AND d.badge_key = ?`,
		memberID, key,
	).Scan(&n); err != nil {
		t.Fatalf("count awards %s: %v", key, err)
	}
	return n
}

// runApply runs the approval participant in its own transaction, as the approval does.
func runApply(t *testing.T, conn *sql.DB, a *Awards, e tasks.Event) {
	t.Helper()
	ctx := context.Background()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()
	if err := a.Apply(ctx, tx, e); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func TestSeedCreatesOneDefinitionPerBuiltIn(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f, _ := fixture(t, conn)
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM badge_definitions WHERE family_id = ? AND enabled = 1`, f.FamilyID).Scan(&n); err != nil {
		t.Fatalf("count definitions: %v", err)
	}
	if n != len(catalogue.BuiltIn) {
		t.Fatalf("definitions = %d, want %d", n, len(catalogue.BuiltIn))
	}
}

func TestFirstApprovalAwardsT1Once(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f, child := fixture(t, conn)
	addApproved(t, conn, f, child, "2026-10-12")

	a := NewAwards(clock.FakeClock{Time: testNow})
	runApply(t, conn, a, tasks.Event{Type: tasks.EventApproved, FamilyID: f.FamilyID, MemberID: child})
	if n := awardCount(t, conn, child, "t1"); n != 1 {
		t.Fatalf("t1 awards after approval = %d, want 1", n)
	}

	runApply(t, conn, a, tasks.Event{Type: tasks.EventApproved, FamilyID: f.FamilyID, MemberID: child})
	if n := awardCount(t, conn, child, "t1"); n != 1 {
		t.Fatalf("t1 awards after replay = %d, want 1", n)
	}
	if n := awardCount(t, conn, child, "s3"); n != 0 {
		t.Fatalf("s3 awarded with one day = %d, want 0", n)
	}
}

func TestApplyIgnoresOtherEvents(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f, child := fixture(t, conn)
	addApproved(t, conn, f, child, "2026-10-12")

	a := NewAwards(clock.FakeClock{Time: testNow})
	runApply(t, conn, a, tasks.Event{Type: tasks.EventReopened, FamilyID: f.FamilyID, MemberID: child})
	if n := awardCount(t, conn, child, "t1"); n != 0 {
		t.Fatalf("t1 awarded on reopen = %d, want 0", n)
	}
}

func TestThreeDayStreakAwardsS3NotS7(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f, child := fixture(t, conn)
	for _, d := range []string{"2026-10-10", "2026-10-11", "2026-10-12"} {
		addApproved(t, conn, f, child, d)
	}
	keys := evaluate(t, conn, f, child)
	if !slices.Contains(keys, "s3") {
		t.Fatalf("awarded %v, want s3", keys)
	}
	if n := awardCount(t, conn, child, "s7"); n != 0 {
		t.Fatalf("s7 awarded after three days = %d, want 0", n)
	}
}

func TestBrokenStreakDoesNotAwardS3(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f, child := fixture(t, conn)
	addApproved(t, conn, f, child, "2026-10-10")
	addApproved(t, conn, f, child, "2026-10-11")
	addCopy(t, conn, f, child, copyOf{dueDate: "2026-10-12", status: "to_do"})
	addApproved(t, conn, f, child, "2026-10-13")
	evaluate(t, conn, f, child)
	if n := awardCount(t, conn, child, "s3"); n != 0 {
		t.Fatalf("s3 awarded across an incomplete day = %d, want 0", n)
	}
}

func TestSkipAndArchivedDoNotBreakStreak(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f, child := fixture(t, conn)
	addApproved(t, conn, f, child, "2026-10-10")
	addCopy(t, conn, f, child, copyOf{dueDate: "2026-10-11", status: "skipped"})
	addApproved(t, conn, f, child, "2026-10-12")
	addApproved(t, conn, f, child, "2026-10-13")
	evaluate(t, conn, f, child)
	if n := awardCount(t, conn, child, "s3"); n != 1 {
		t.Fatalf("s3 with a skipped day = %d, want 1", n)
	}
}

func TestSkillBadgeNeedsTwentyCopies(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f, child := fixture(t, conn)
	for i := 0; i < 19; i++ {
		addCopy(t, conn, f, child, copyOf{category: "tidy_pro", dueDate: "2026-10-12", status: "approved"})
	}
	evaluate(t, conn, f, child)
	if n := awardCount(t, conn, child, "k_tidy"); n != 0 {
		t.Fatalf("k_tidy at 19 chores = %d, want 0", n)
	}

	addCopy(t, conn, f, child, copyOf{category: "tidy_pro", dueDate: "2026-10-12", status: "approved"})
	evaluate(t, conn, f, child)
	if n := awardCount(t, conn, child, "k_tidy"); n != 1 {
		t.Fatalf("k_tidy at 20 chores = %d, want 1", n)
	}
}

func TestPointsBadgeNeedsOneHundred(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f, child := fixture(t, conn)
	post(t, conn, f, child, rewards.Bonus, 99, "bonus-a")
	evaluate(t, conn, f, child)
	if n := awardCount(t, conn, child, "p100"); n != 0 {
		t.Fatalf("p100 at 99 points = %d, want 0", n)
	}

	post(t, conn, f, child, rewards.Bonus, 1, "bonus-b")
	evaluate(t, conn, f, child)
	if n := awardCount(t, conn, child, "p100"); n != 1 {
		t.Fatalf("p100 at 100 points = %d, want 1", n)
	}
}

func TestDisabledGroupAwardsNothing(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f, child := fixture(t, conn)
	mustExec(t, conn,
		`UPDATE badge_definitions SET enabled = 0 WHERE family_id = ? AND group_key = 'tasks'`, f.FamilyID)
	addApproved(t, conn, f, child, "2026-10-12")
	evaluate(t, conn, f, child)
	if n := awardCount(t, conn, child, "t1"); n != 0 {
		t.Fatalf("t1 awarded with tasks group off = %d, want 0", n)
	}
}

func TestFiveLateCopiesAwardEBack(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f, child := fixture(t, conn)
	for i := 0; i < 4; i++ {
		addCopy(t, conn, f, child, copyOf{dueDate: "2026-10-12", status: "approved", late: true})
	}
	evaluate(t, conn, f, child)
	if n := awardCount(t, conn, child, "e_back"); n != 0 {
		t.Fatalf("e_back at 4 late copies = %d, want 0", n)
	}

	addCopy(t, conn, f, child, copyOf{dueDate: "2026-10-12", status: "approved", late: true})
	evaluate(t, conn, f, child)
	if n := awardCount(t, conn, child, "e_back"); n != 1 {
		t.Fatalf("e_back at 5 late copies = %d, want 1", n)
	}
}

func TestCompletedShareAwardsKShare(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f, child := fixture(t, conn)
	mustExec(t, conn,
		`INSERT INTO point_shares (id, family_id, from_member_id, to_member_id, amount, status, approval_required,
			transfer_id, idempotency_key, requested_at, decided_at, decided_by_member_id)
		 VALUES (?, ?, ?, ?, 5, 'completed', 0, ?, 'share-1', '2026-10-12 19:00:00.000+00:00', '2026-10-12 19:00:00.000+00:00', ?)`,
		ids.New(), f.FamilyID, child, f.MemberID, ids.New(), child)
	keys := evaluate(t, conn, f, child)
	if !slices.Contains(keys, "k_share") {
		t.Fatalf("awarded %v, want k_share", keys)
	}
}

func TestEvaluateTwiceAwardsNothingNew(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f, child := fixture(t, conn)
	addApproved(t, conn, f, child, "2026-10-12")
	if keys := evaluate(t, conn, f, child); len(keys) == 0 {
		t.Fatalf("first evaluate awarded nothing")
	}
	if keys := evaluate(t, conn, f, child); len(keys) != 0 {
		t.Fatalf("second evaluate awarded %v, want none", keys)
	}
}

func TestEvaluateAllSweepsChildrenOnce(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f, child := fixture(t, conn)
	addApproved(t, conn, f, child, "2026-10-12")

	ctx := context.Background()
	n, err := EvaluateAll(ctx, conn, testNow)
	if err != nil {
		t.Fatalf("evaluate all: %v", err)
	}
	if n != 1 {
		t.Fatalf("first sweep awarded %d, want 1 (t1)", n)
	}
	n, err = EvaluateAll(ctx, conn, testNow)
	if err != nil {
		t.Fatalf("evaluate all again: %v", err)
	}
	if n != 0 {
		t.Fatalf("second sweep awarded %d, want 0", n)
	}
}

func TestEvaluateAllSkipsInactiveChildren(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f, child := fixture(t, conn)
	addApproved(t, conn, f, child, "2026-10-12")
	mustExec(t, conn, `UPDATE family_members SET status = 'inactive' WHERE id = ?`, child)

	n, err := EvaluateAll(context.Background(), conn, testNow)
	if err != nil {
		t.Fatalf("evaluate all: %v", err)
	}
	if n != 0 {
		t.Fatalf("sweep awarded %d for an inactive child, want 0", n)
	}
	if c := awardCount(t, conn, child, "t1"); c != 0 {
		t.Fatalf("t1 awarded to an inactive child = %d, want 0", c)
	}
}

func TestEveryBuiltInKeyIsMeasured(t *testing.T) {
	m := Metrics{
		TasksDone: 1000, Early: 1000, Late: 1000, Streak: 1000, PointsEarned: 1000,
		BonusFromParent: 1000, Redemptions: 1000, SharesDone: 1000,
		BySkill: map[string]int{"tidy_pro": 1000, "homework_hero": 1000, "pet_pal": 1000, "green_thumb": 1000},
	}
	for _, b := range catalogue.BuiltIn {
		if got := m.valueOf(b); got < b.Target {
			t.Errorf("%s: valueOf = %d, want at least %d (key not handled in valueOf?)", b.Key, got, b.Target)
		}
	}
}

func TestLongestRun(t *testing.T) {
	tests := []struct {
		name string
		days []dayTotals
		want int
	}{
		{"no days", nil, 0},
		{"empty day is neutral", []dayTotals{{"d1", 2, 2}, {"d2", 0, 0}, {"d3", 1, 1}}, 2},
		{"incomplete day breaks the run", []dayTotals{{"d1", 1, 1}, {"d2", 1, 0}, {"d3", 1, 1}}, 1},
		{"empty day between runs", []dayTotals{{"d1", 2, 2}, {"d2", 0, 0}, {"d3", 2, 2}}, 2},
		{"longest run wins", []dayTotals{{"d1", 1, 1}, {"d2", 1, 1}, {"d3", 1, 0}, {"d4", 1, 1}}, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := longestRun(tc.days); got != tc.want {
				t.Fatalf("longestRun = %d, want %d", got, tc.want)
			}
		})
	}
}
