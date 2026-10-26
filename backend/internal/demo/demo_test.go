package demo

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"nestwall/backend/internal/auth"
	"nestwall/backend/internal/testhelper"
)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

var countedTables = []string{
	"families", "users", "family_members", "task_categories", "task_templates",
	"task_definitions", "task_definition_assignees", "task_occurrences",
	"task_occurrence_assignees", "task_checklist_items", "audit_logs",
}

func rowCounts(t *testing.T, conn *sql.DB) map[string]int {
	t.Helper()
	counts := make(map[string]int, len(countedTables))
	for _, table := range countedTables {
		var n int
		if err := conn.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		counts[table] = n
	}
	return counts
}

// Mid-morning in Toronto, so some of today's tasks are already due.
var testNow = time.Date(2026, 10, 12, 14, 0, 0, 0, time.UTC)

func TestSeedCreatesDemoFamilyOnce(t *testing.T) {
	conn := testhelper.OpenDB(t)
	ctx := context.Background()

	first, err := Seed(ctx, conn, fixedClock{now: testNow})
	if err != nil {
		t.Fatalf("first seed: %v", err)
	}
	if !first.Created || first.Password == "" {
		t.Fatalf("first run: created=%v, password empty=%v", first.Created, first.Password == "")
	}

	before := rowCounts(t, conn)
	if before["families"] != 1 {
		t.Errorf("families = %d, want 1", before["families"])
	}
	if before["users"] != len(people) || before["family_members"] != len(people) {
		t.Errorf("users = %d, family_members = %d, want %d each", before["users"], before["family_members"], len(people))
	}
	if want := len(repeats) + len(oneOffs); before["task_definitions"] != want {
		t.Errorf("task_definitions = %d, want %d", before["task_definitions"], want)
	}

	second, err := Seed(ctx, conn, fixedClock{now: testNow.Add(time.Hour)})
	if err != nil {
		t.Fatalf("second seed: %v", err)
	}
	if second.Created || second.Password != "" {
		t.Fatalf("second run: created=%v, password empty=%v; want no-op", second.Created, second.Password == "")
	}

	after := rowCounts(t, conn)
	for _, table := range countedTables {
		if after[table] != before[table] {
			t.Errorf("%s: %d rows after second run, want %d", table, after[table], before[table])
		}
	}
}

func TestSeedPasswordWorksForParent(t *testing.T) {
	conn := testhelper.OpenDB(t)
	result, err := Seed(context.Background(), conn, fixedClock{now: testNow})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	var hash string
	if err := conn.QueryRow(`SELECT password_hash FROM users WHERE user_id = 'sam'`).Scan(&hash); err != nil {
		t.Fatalf("load hash: %v", err)
	}
	if !auth.VerifyPassword(result.Password, hash) {
		t.Error("the printed password does not verify against the demo account")
	}
}

func TestSeedWithPasswordUsesGivenPassword(t *testing.T) {
	conn := testhelper.OpenDB(t)
	const want = "demo-test-password-123"
	if _, err := SeedWithPassword(context.Background(), conn, fixedClock{now: testNow}, want); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var hash string
	if err := conn.QueryRow(`SELECT password_hash FROM users WHERE user_id = 'sam'`).Scan(&hash); err != nil {
		t.Fatalf("load hash: %v", err)
	}
	if !auth.VerifyPassword(want, hash) {
		t.Error("the given password does not verify against the demo account")
	}
}

func TestSeedWithPasswordRejectsShortPassword(t *testing.T) {
	conn := testhelper.OpenDB(t)
	if _, err := SeedWithPassword(context.Background(), conn, fixedClock{now: testNow}, "short"); err == nil {
		t.Fatal("seed accepted a password shorter than the minimum length")
	}
	if n := rowCounts(t, conn)["families"]; n != 0 {
		t.Errorf("families = %d after rejected seed, want 0", n)
	}
}

func TestSeedStatesMatchMock(t *testing.T) {
	conn := testhelper.OpenDB(t)
	if _, err := Seed(context.Background(), conn, fixedClock{now: testNow}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var status string
	err := conn.QueryRow(`
		SELECT a.status FROM task_occurrence_assignees a
		JOIN task_occurrences o ON o.id = a.task_occurrence_id
		JOIN task_definitions d ON d.id = o.task_definition_id
		JOIN family_members m ON m.id = a.family_member_id
		JOIN users u ON u.id = m.user_id
		WHERE d.title = 'Homework' AND u.user_id = 'ava'`).Scan(&status)
	if err != nil {
		t.Fatalf("load Homework state: %v", err)
	}
	if status != stateSubmitted {
		t.Errorf("Ava's Homework status = %q, want %q", status, stateSubmitted)
	}

	var piano string
	err = conn.QueryRow(`
		SELECT o.status FROM task_occurrences o
		JOIN task_definitions d ON d.id = o.task_definition_id
		WHERE d.title = 'Practise piano'`).Scan(&piano)
	if err != nil {
		t.Fatalf("load piano occurrence: %v", err)
	}
	if piano != "past_due" {
		t.Errorf("yesterday's piano status = %q, want past_due", piano)
	}

	var tidyChecklist int
	err = conn.QueryRow(`
		SELECT COUNT(*) FROM task_checklist_completions cc
		JOIN task_occurrence_assignees oa ON oa.id = cc.task_occurrence_assignee_id
		JOIN task_occurrences o ON o.id = oa.task_occurrence_id
		JOIN task_definitions d ON d.id = o.task_definition_id
		WHERE d.title = 'Tidy room'`).Scan(&tidyChecklist)
	if err != nil {
		t.Fatalf("count checked items: %v", err)
	}
	if tidyChecklist != 1 {
		t.Errorf("completed Tidy room steps = %d, want 1", tidyChecklist)
	}
}

func TestSeedAddsRewardsAndPoints(t *testing.T) {
	conn := testhelper.OpenDB(t)
	if _, err := Seed(context.Background(), conn, fixedClock{now: testNow}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var rewards int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM rewards`).Scan(&rewards); err != nil {
		t.Fatalf("count rewards: %v", err)
	}
	if rewards != len(rewardSpecs) {
		t.Errorf("rewards = %d, want %d", rewards, len(rewardSpecs))
	}

	for _, status := range []string{"pending", "approved", "rejected"} {
		var n int
		if err := conn.QueryRow(`SELECT COUNT(*) FROM redemptions WHERE status = ?`, status).Scan(&n); err != nil {
			t.Fatalf("count %s redemptions: %v", status, err)
		}
		if n == 0 {
			t.Errorf("no %s redemption seeded", status)
		}
	}

	// A held or spent redemption has one negative entry. A rejected one is refunded, so its
	// net is zero.
	rows, err := conn.Query(`
		SELECT r.id, r.status, r.cost, COALESCE(SUM(l.amount), 0)
		FROM redemptions r
		LEFT JOIN points_ledger l ON l.reference_type = 'redemption' AND l.reference_id = r.id
		GROUP BY r.id`)
	if err != nil {
		t.Fatalf("load redemption ledger: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, status string
		var cost, net int
		if err := rows.Scan(&id, &status, &cost, &net); err != nil {
			t.Fatalf("scan redemption: %v", err)
		}
		want := -cost
		if status == "rejected" {
			want = 0
		}
		if net != want {
			t.Errorf("redemption %s (%s): ledger net = %d, want %d", id, status, net, want)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate redemptions: %v", err)
	}

	balances, err := conn.Query(`
		SELECT u.user_id, COALESCE(SUM(l.amount), 0)
		FROM family_members m
		JOIN users u ON u.id = m.user_id
		LEFT JOIN points_ledger l ON l.family_member_id = m.id
		WHERE u.is_child = 1
		GROUP BY m.id, u.user_id`)
	if err != nil {
		t.Fatalf("load balances: %v", err)
	}
	defer balances.Close()
	children := 0
	for balances.Next() {
		var login string
		var balance int
		if err := balances.Scan(&login, &balance); err != nil {
			t.Fatalf("scan balance: %v", err)
		}
		children++
		if balance <= 0 {
			t.Errorf("%s balance = %d, want > 0", login, balance)
		}
	}
	if err := balances.Err(); err != nil {
		t.Fatalf("iterate balances: %v", err)
	}
	if children != 3 {
		t.Errorf("children with a ledger = %d, want 3", children)
	}
}

func TestSeedAddsBonusTasks(t *testing.T) {
	conn := testhelper.OpenDB(t)
	if _, err := Seed(context.Background(), conn, fixedClock{now: testNow}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var tasks int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM bonus_tasks`).Scan(&tasks); err != nil {
		t.Fatalf("count bonus tasks: %v", err)
	}
	if tasks != len(bonusSpecs) {
		t.Errorf("bonus tasks = %d, want %d", tasks, len(bonusSpecs))
	}

	var unoffered int
	if err := conn.QueryRow(`
		SELECT COUNT(*) FROM bonus_tasks b
		WHERE (SELECT COUNT(*) FROM bonus_task_members m WHERE m.bonus_task_id = b.id) <> 3`).Scan(&unoffered); err != nil {
		t.Fatalf("count offers: %v", err)
	}
	if unoffered != 0 {
		t.Errorf("%d bonus tasks are not offered to all three children", unoffered)
	}

	for _, status := range []string{"claimed", "submitted", "rejected", "released"} {
		var n int
		if err := conn.QueryRow(`SELECT COUNT(*) FROM bonus_claims WHERE status = ?`, status).Scan(&n); err != nil {
			t.Fatalf("count %s claims: %v", status, err)
		}
		if n == 0 {
			t.Errorf("no %s bonus claim seeded", status)
		}
	}

	var held string
	if err := conn.QueryRow(`
		SELECT u.user_id FROM bonus_claims c
		JOIN bonus_tasks b ON b.id = c.bonus_task_id
		JOIN family_members m ON m.id = c.family_member_id
		JOIN users u ON u.id = m.user_id
		WHERE b.title = 'Wash the car' AND c.status = 'claimed'`).Scan(&held); err != nil {
		t.Fatalf("load held claim: %v", err)
	}
	if held != "ava" {
		t.Errorf("Wash the car held by %q, want ava", held)
	}

	// Bonus tasks post awards only when approved, and the seed approves none.
	var awards int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM points_ledger WHERE entry_type = 'bonus_task_award'`).Scan(&awards); err != nil {
		t.Fatalf("count bonus awards: %v", err)
	}
	if awards != 0 {
		t.Errorf("bonus_task_award entries = %d, want 0", awards)
	}
}

func TestSeedAddsBonusTemplates(t *testing.T) {
	conn := testhelper.OpenDB(t)
	if _, err := Seed(context.Background(), conn, fixedClock{now: testNow}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM task_templates WHERE kind = 'bonus'`).Scan(&n); err != nil {
		t.Fatalf("count bonus templates: %v", err)
	}
	if n != len(bonusTemplateSpecs) {
		t.Errorf("bonus templates = %d, want %d", n, len(bonusTemplateSpecs))
	}

	var points int
	var description sql.NullString
	if err := conn.QueryRow(`
		SELECT default_points, description FROM task_templates
		WHERE kind = 'bonus' AND name = 'Wash the car'`).Scan(&points, &description); err != nil {
		t.Fatalf("load Wash the car: %v", err)
	}
	if points != 20 {
		t.Errorf("Wash the car points = %d, want 20", points)
	}
	if !description.Valid || description.String != "Hand wash and vacuum inside" {
		t.Errorf("Wash the car description = %v, want the mock text", description)
	}
}

func TestSeedRefusesAnotherFamily(t *testing.T) {
	conn := testhelper.OpenDB(t)
	testhelper.OneFamily(t, conn)

	_, err := Seed(context.Background(), conn, fixedClock{now: testNow})
	if !errors.Is(err, ErrFamilyExists) {
		t.Fatalf("seed error = %v, want ErrFamilyExists", err)
	}

	counts := rowCounts(t, conn)
	if counts["families"] != 1 || counts["task_templates"] != 0 {
		t.Errorf("families = %d, task_templates = %d; want 1 and 0", counts["families"], counts["task_templates"])
	}
}
