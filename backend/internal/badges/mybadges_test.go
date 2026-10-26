package badges

import (
	"context"
	"database/sql"
	"testing"

	"nestwall/backend/internal/rewards"
	"nestwall/backend/internal/testhelper"
)

// findMine returns the child's badge with the key in the group. It fails the test when the badge is
// not shown.
func findMine(t *testing.T, out MyBadges, group, key string) MyBadge {
	t.Helper()
	for _, g := range out.Groups {
		if g.Group != group {
			continue
		}
		for _, b := range g.Badges {
			if b.Key == key {
				return b
			}
		}
	}
	t.Fatalf("badge %s not shown in group %s", key, group)
	return MyBadge{}
}

// hasGroup reports whether the child's view shows the group.
func hasGroup(out MyBadges, group string) bool {
	for _, g := range out.Groups {
		if g.Group == group {
			return true
		}
	}
	return false
}

// countNotices returns how many notifications the member received with the kind.
func countNotices(t *testing.T, conn *sql.DB, memberID, kind string) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(
		`SELECT COUNT(*) FROM notifications WHERE recipient_id = ? AND kind = ?`, memberID, kind,
	).Scan(&n); err != nil {
		t.Fatalf("count notices %s: %v", kind, err)
	}
	return n
}

func loadMine(t *testing.T, conn *sql.DB, familyID, memberID string) MyBadges {
	t.Helper()
	out, err := loadMyBadges(context.Background(), conn, familyID, memberID)
	if err != nil {
		t.Fatalf("load my badges: %v", err)
	}
	return out
}

func TestMyBadgesShowsProgressOfUnearned(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f, child := fixture(t, conn)
	post(t, conn, f, child, rewards.Bonus, 40, "bonus-a")
	evaluate(t, conn, f, child)

	out := loadMine(t, conn, f.FamilyID, child)
	b := findMine(t, out, "points", "p100")
	if b.Earned || b.EarnedAt != nil {
		t.Fatalf("p100 earned at 40 points: %+v", b)
	}
	if b.Progress != 40 || b.Target != 100 {
		t.Fatalf("p100 progress = %d of %d, want 40 of 100", b.Progress, b.Target)
	}
	// 40 bonus points from parents also earn k_help (3 needed), so one badge is earned.
	if !findMine(t, out, "kindness", "k_help").Earned {
		t.Fatal("k_help not earned from 40 bonus points")
	}
	if out.TotalCount != 24 || out.EarnedCount != 1 {
		t.Fatalf("totals = %d earned of %d, want 1 of 24", out.EarnedCount, out.TotalCount)
	}
}

func TestMyBadgesEarnedHasTimeAndFullProgress(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f, child := fixture(t, conn)
	addApproved(t, conn, f, child, "2026-10-10")
	addApproved(t, conn, f, child, "2026-10-11")
	addApproved(t, conn, f, child, "2026-10-12")
	evaluate(t, conn, f, child)

	out := loadMine(t, conn, f.FamilyID, child)
	b := findMine(t, out, "streaks", "s3")
	if !b.Earned || b.EarnedAt == nil {
		t.Fatalf("s3 not shown as earned: %+v", b)
	}
	if b.Progress != 3 || b.Target != 3 {
		t.Fatalf("s3 progress = %d of %d, want 3 of 3", b.Progress, b.Target)
	}
	// Three tasks in a row also earn t1 (first task).
	if out.EarnedCount != 2 {
		t.Fatalf("earned = %d, want 2 (t1 and s3)", out.EarnedCount)
	}
}

func TestMyBadgesIsScopedToTheChild(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f, child := fixture(t, conn)
	other := newChild(t, conn, f)
	addApproved(t, conn, f, child, "2026-10-10")
	addApproved(t, conn, f, child, "2026-10-11")
	addApproved(t, conn, f, child, "2026-10-12")
	evaluate(t, conn, f, child)

	out := loadMine(t, conn, f.FamilyID, other)
	if findMine(t, out, "streaks", "s3").Earned {
		t.Fatal("other child sees the first child's s3 as earned")
	}
	if out.EarnedCount != 0 {
		t.Fatalf("other child earned = %d, want 0", out.EarnedCount)
	}
}

func TestMyBadgesHidesDisabledUnearnedButKeepsEarned(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f, child := fixture(t, conn)
	addApproved(t, conn, f, child, "2026-10-10")
	addApproved(t, conn, f, child, "2026-10-11")
	addApproved(t, conn, f, child, "2026-10-12")
	evaluate(t, conn, f, child)

	if _, err := SetGroup(context.Background(), conn, testNow, f.FamilyID, f.ParentUserID, "streaks", false, -1); err != nil {
		t.Fatalf("switch streaks off: %v", err)
	}
	out := loadMine(t, conn, f.FamilyID, child)
	if !findMine(t, out, "streaks", "s3").Earned {
		t.Fatal("earned s3 hidden after its group was switched off")
	}
	for _, g := range out.Groups {
		if g.Group != "streaks" {
			continue
		}
		if len(g.Badges) != 1 || g.Total != 1 {
			t.Fatalf("streaks shows %d badges (total %d), want only the earned s3", len(g.Badges), g.Total)
		}
	}
	// 24 built-in, less the four unearned streaks (s7, s14, s30, s100) that are now off.
	if out.TotalCount != 20 {
		t.Fatalf("total shown = %d, want 20", out.TotalCount)
	}

	if _, err := SetGroup(context.Background(), conn, testNow, f.FamilyID, f.ParentUserID, "points", false, -1); err != nil {
		t.Fatalf("switch points off: %v", err)
	}
	if hasGroup(loadMine(t, conn, f.FamilyID, child), "points") {
		t.Fatal("points group shown with nothing earned after it was switched off")
	}
}

func TestMyBadgesFromParentsShowsOnlyOwnAwards(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f, child := fixture(t, conn)
	other := newChild(t, conn, f)
	c, err := CreateCustom(context.Background(), conn, testNow, f.FamilyID, f.ParentUserID, CustomInput{
		Name: "Tidy star", Emoji: "\u2b50",
	})
	if err != nil {
		t.Fatalf("create custom: %v", err)
	}
	if _, err := Award(context.Background(), conn, testNow, f.FamilyID, f.ParentUserID, f.MemberID, AwardInput{
		MemberID: child, BadgeID: c.ID, Note: "Great week", IdempotencyKey: "award-own",
	}); err != nil {
		t.Fatalf("award: %v", err)
	}

	mine := loadMine(t, conn, f.FamilyID, child)
	if len(mine.FromParents) != 1 {
		t.Fatalf("from parents = %d, want 1", len(mine.FromParents))
	}
	if a := mine.FromParents[0]; a.Name != "Tidy star" || a.Note != "Great week" {
		t.Fatalf("award = %+v", a)
	}
	if mine.EarnedCount != 1 {
		t.Fatalf("earned = %d, want 1 (custom award counts)", mine.EarnedCount)
	}

	theirs := loadMine(t, conn, f.FamilyID, other)
	if len(theirs.FromParents) != 0 || theirs.EarnedCount != 0 {
		t.Fatalf("other child sees awards %+v", theirs.FromParents)
	}
}

func TestEarnedNoticeWrittenOncePerAward(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f, child := fixture(t, conn)
	addApproved(t, conn, f, child, "2026-10-10")
	addApproved(t, conn, f, child, "2026-10-11")
	addApproved(t, conn, f, child, "2026-10-12")
	evaluate(t, conn, f, child)
	evaluate(t, conn, f, child)

	// Two awards (t1 and s3), each noticed once, even after a second evaluation.
	if n := countNotices(t, conn, child, kindBadgeEarned); n != 2 {
		t.Fatalf("earned notices = %d, want 2", n)
	}
}

func TestNoEarnedNoticeWhenChildNoticesOff(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f, child := fixture(t, conn)
	mustExec(t, conn, `UPDATE badge_settings SET notify_child = 0 WHERE family_id = ?`, f.FamilyID)
	addApproved(t, conn, f, child, "2026-10-10")
	addApproved(t, conn, f, child, "2026-10-11")
	addApproved(t, conn, f, child, "2026-10-12")
	evaluate(t, conn, f, child)

	if awardCount(t, conn, child, "s3") != 1 {
		t.Fatal("s3 not awarded with notices off")
	}
	if n := countNotices(t, conn, child, kindBadgeEarned); n != 0 {
		t.Fatalf("earned notices = %d, want 0", n)
	}
}

func TestManualAwardNoticesChildOnce(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f, child := fixture(t, conn)
	c, err := CreateCustom(context.Background(), conn, testNow, f.FamilyID, f.ParentUserID, CustomInput{
		Name: "Tidy star", Emoji: "\u2b50",
	})
	if err != nil {
		t.Fatalf("create custom: %v", err)
	}
	in := AwardInput{MemberID: child, BadgeID: c.ID, Note: "Great week", IdempotencyKey: "award-notice"}
	if _, err := Award(context.Background(), conn, testNow, f.FamilyID, f.ParentUserID, f.MemberID, in); err != nil {
		t.Fatalf("award: %v", err)
	}
	res, err := Award(context.Background(), conn, testNow, f.FamilyID, f.ParentUserID, f.MemberID, in)
	if err != nil {
		t.Fatalf("replay award: %v", err)
	}
	if !res.Replayed {
		t.Fatal("replay not marked as replayed")
	}
	if n := countNotices(t, conn, child, kindBadgeAwarded); n != 1 {
		t.Fatalf("award notices = %d, want 1", n)
	}
	var body string
	if err := conn.QueryRow(
		`SELECT body FROM notifications WHERE recipient_id = ? AND kind = ?`, child, kindBadgeAwarded,
	).Scan(&body); err != nil {
		t.Fatalf("load notice: %v", err)
	}
	if body == "" {
		t.Fatal("award notice has an empty body")
	}
}
