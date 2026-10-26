package badges

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"

	"nestwall/backend/internal/testhelper"
)

func strPtr(s string) *string { return &s }

// builtInID returns the definition id of a built-in badge in the family.
func builtInID(t *testing.T, conn *sql.DB, familyID, key string) string {
	t.Helper()
	var id string
	if err := conn.QueryRow(
		`SELECT id FROM badge_definitions WHERE family_id = ? AND badge_key = ?`, familyID, key,
	).Scan(&id); err != nil {
		t.Fatalf("built-in id %s: %v", key, err)
	}
	return id
}

func TestSettingsStartOnAndSaveBumpsVersion(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f, _ := fixture(t, conn)
	ctx := context.Background()

	cfg, err := LoadConfig(ctx, conn, f.FamilyID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.Settings.Enabled || cfg.Version != 1 {
		t.Fatalf("defaults = enabled %v version %d, want on and 1", cfg.Settings.Enabled, cfg.Version)
	}
	if len(cfg.Groups) != 6 {
		t.Fatalf("groups = %d, want 6", len(cfg.Groups))
	}
	for _, g := range cfg.Groups {
		if !g.Enabled || len(g.Badges) == 0 {
			t.Fatalf("group %s enabled=%v badges=%d, want on with badges", g.Group, g.Enabled, len(g.Badges))
		}
	}

	s := Settings{Enabled: true, ShowOnKidDashboard: true, ShowOnWall: true, NotifyChild: false}
	saved, err := SaveSettings(ctx, conn, testNow, f.FamilyID, f.ParentUserID, s, 1)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if saved.Version != 2 || !saved.Settings.ShowOnWall || saved.Settings.NotifyChild {
		t.Fatalf("saved = %+v, want version 2 with the new switches", saved)
	}

	_, err = SaveSettings(ctx, conn, testNow, f.FamilyID, f.ParentUserID, s, 1)
	var stale staleVersionError
	if !errors.As(err, &stale) || stale.Current != 2 {
		t.Fatalf("stale save err = %v, want staleVersionError current 2", err)
	}

	if _, err := SaveSettings(ctx, conn, testNow, f.FamilyID, f.ParentUserID, s, -1); err != nil {
		t.Fatalf("unconditional save: %v", err)
	}
}

func TestMasterSwitchStopsAutoAwards(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f, child := fixture(t, conn)
	ctx := context.Background()
	addApproved(t, conn, f, child, "2026-10-12")

	off := Settings{Enabled: false, ShowOnKidDashboard: true, NotifyChild: true}
	if _, err := SaveSettings(ctx, conn, testNow, f.FamilyID, f.ParentUserID, off, -1); err != nil {
		t.Fatalf("switch off: %v", err)
	}
	if keys := evaluate(t, conn, f, child); len(keys) != 0 {
		t.Fatalf("awarded %v with badges off, want none", keys)
	}

	on := off
	on.Enabled = true
	if _, err := SaveSettings(ctx, conn, testNow, f.FamilyID, f.ParentUserID, on, -1); err != nil {
		t.Fatalf("switch on: %v", err)
	}
	if keys := evaluate(t, conn, f, child); !slices.Contains(keys, "t1") {
		t.Fatalf("awarded %v after switching on, want t1", keys)
	}
}

func TestGroupOffStopsAwardsUntilOnAgain(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f, child := fixture(t, conn)
	ctx := context.Background()
	for _, d := range []string{"2026-10-10", "2026-10-11", "2026-10-12"} {
		addApproved(t, conn, f, child, d)
	}

	if _, err := SetGroup(ctx, conn, testNow, f.FamilyID, f.ParentUserID, "streaks", false, -1); err != nil {
		t.Fatalf("group off: %v", err)
	}
	if keys := evaluate(t, conn, f, child); slices.Contains(keys, "s3") {
		t.Fatalf("awarded %v with streaks off, want no s3", keys)
	}

	cfg, err := SetGroup(ctx, conn, testNow, f.FamilyID, f.ParentUserID, "streaks", true, -1)
	if err != nil {
		t.Fatalf("group on: %v", err)
	}
	if cfg.Groups[0].Group != "streaks" || !cfg.Groups[0].Enabled {
		t.Fatalf("first group = %+v, want streaks on", cfg.Groups[0])
	}
	if keys := evaluate(t, conn, f, child); !slices.Contains(keys, "s3") {
		t.Fatalf("awarded %v after streaks on, want s3", keys)
	}
}

func TestSetGroupRejectsUnknownGroup(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f, _ := fixture(t, conn)
	_, err := SetGroup(context.Background(), conn, testNow, f.FamilyID, f.ParentUserID, "nope", false, -1)
	if !errors.Is(err, ErrUnknownGroup) {
		t.Fatalf("err = %v, want ErrUnknownGroup", err)
	}
}

func TestCustomBadgeCreateEditAndStale(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f, _ := fixture(t, conn)
	ctx := context.Background()

	c, err := CreateCustom(ctx, conn, testNow, f.FamilyID, f.ParentUserID, CustomInput{
		Name:        "  Kindness star ",
		Emoji:       "⭐",
		Description: strPtr("   "),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if c.Name != "Kindness star" || c.Description != nil || c.Version != 1 || !c.Enabled {
		t.Fatalf("created = %+v, want trimmed name, no description, version 1, on", c)
	}

	u, err := UpdateCustom(ctx, conn, testNow, f.FamilyID, f.ParentUserID, c.ID, CustomUpdate{
		CustomInput: CustomInput{Name: "Kind", Emoji: "⭐", Description: strPtr("Helps out")},
		Enabled:     true,
	}, 1)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if u.Version != 2 || u.Description == nil || *u.Description != "Helps out" {
		t.Fatalf("updated = %+v, want version 2 and the new description", u)
	}

	_, err = UpdateCustom(ctx, conn, testNow, f.FamilyID, f.ParentUserID, c.ID, CustomUpdate{
		CustomInput: CustomInput{Name: "Old", Emoji: "⭐"}, Enabled: true,
	}, 1)
	var stale staleVersionError
	if !errors.As(err, &stale) || stale.Current != 2 {
		t.Fatalf("stale update err = %v, want staleVersionError current 2", err)
	}

	_, err = UpdateCustom(ctx, conn, testNow, f.FamilyID, f.ParentUserID, "missing", CustomUpdate{
		CustomInput: CustomInput{Name: "x", Emoji: "⭐"}, Enabled: true,
	}, -1)
	if !errors.Is(err, ErrBadgeNotFound) {
		t.Fatalf("missing update err = %v, want ErrBadgeNotFound", err)
	}

	builtIn := builtInID(t, conn, f.FamilyID, "t1")
	_, err = UpdateCustom(ctx, conn, testNow, f.FamilyID, f.ParentUserID, builtIn, CustomUpdate{
		CustomInput: CustomInput{Name: "x", Emoji: "⭐"}, Enabled: true,
	}, -1)
	if !errors.Is(err, ErrBadgeNotFound) {
		t.Fatalf("built-in update err = %v, want ErrBadgeNotFound", err)
	}
}

func TestAwardReplayConflictAndRefusals(t *testing.T) {
	conn := testhelper.OpenDB(t)
	f, child := fixture(t, conn)
	ctx := context.Background()

	c, err := CreateCustom(ctx, conn, testNow, f.FamilyID, f.ParentUserID, CustomInput{Name: "Kindness star", Emoji: "⭐"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	award := func(member, badge, note, key string) (AwardResult, error) {
		return Award(ctx, conn, testNow, f.FamilyID, f.ParentUserID, f.MemberID, AwardInput{
			MemberID: member, BadgeID: badge, Note: note, IdempotencyKey: key,
		})
	}

	first, err := award(child, c.ID, "Helped a sibling", "key-1")
	if err != nil || first.Replayed || first.ID == "" {
		t.Fatalf("first award = %+v, %v; want a new award", first, err)
	}
	replay, err := award(child, c.ID, "Helped a sibling", "key-1")
	if err != nil || !replay.Replayed || replay.ID != first.ID {
		t.Fatalf("replay = %+v, %v; want the stored award with Replayed", replay, err)
	}
	if _, err := award(child, c.ID, "Something else", "key-1"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("reused key err = %v, want ErrIdempotencyConflict", err)
	}

	cfg, err := LoadConfig(ctx, conn, f.FamilyID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Custom) != 1 || cfg.Custom[0].AwardedCount != 1 {
		t.Fatalf("custom = %+v, want one badge with one award", cfg.Custom)
	}

	if _, err := award(child, builtInID(t, conn, f.FamilyID, "t1"), "Nope", "key-2"); !errors.Is(err, ErrNotCustom) {
		t.Fatalf("built-in award err = %v, want ErrNotCustom", err)
	}
	if _, err := award(f.MemberID, c.ID, "Nope", "key-3"); !errors.Is(err, ErrMemberNotFound) {
		t.Fatalf("parent award err = %v, want ErrMemberNotFound", err)
	}
	if _, err := award(child, "missing", "Nope", "key-4"); !errors.Is(err, ErrBadgeNotFound) {
		t.Fatalf("missing badge err = %v, want ErrBadgeNotFound", err)
	}
	if _, err := award(child, c.ID, "   ", "key-5"); err == nil {
		t.Fatal("blank note accepted, want a field error")
	}

	if _, err := UpdateCustom(ctx, conn, testNow, f.FamilyID, f.ParentUserID, c.ID, CustomUpdate{
		CustomInput: CustomInput{Name: "Kindness star", Emoji: "⭐"}, Enabled: false,
	}, -1); err != nil {
		t.Fatalf("disable badge: %v", err)
	}
	if _, err := award(child, c.ID, "Off now", "key-6"); !errors.Is(err, ErrBadgeDisabled) {
		t.Fatalf("disabled award err = %v, want ErrBadgeDisabled", err)
	}

	if _, err := SaveSettings(ctx, conn, testNow, f.FamilyID, f.ParentUserID,
		Settings{Enabled: false, ShowOnKidDashboard: true, NotifyChild: true}, -1); err != nil {
		t.Fatalf("switch badges off: %v", err)
	}
	if _, err := UpdateCustom(ctx, conn, testNow, f.FamilyID, f.ParentUserID, c.ID, CustomUpdate{
		CustomInput: CustomInput{Name: "Kindness star", Emoji: "⭐"}, Enabled: true,
	}, -1); err != nil {
		t.Fatalf("enable badge: %v", err)
	}
	if _, err := award(child, c.ID, "Badges off", "key-7"); !errors.Is(err, ErrBadgesOff) {
		t.Fatalf("award with master switch off err = %v, want ErrBadgesOff", err)
	}
}

func TestCleanCustomRejectsBadInput(t *testing.T) {
	cases := []struct {
		name  string
		in    CustomInput
		field string
	}{
		{"blank name", CustomInput{Name: "  ", Emoji: "⭐"}, "name"},
		{"name too long", CustomInput{Name: strings.Repeat("a", 61), Emoji: "⭐"}, "name"},
		{"no picture", CustomInput{Name: "Star", Emoji: " "}, "emoji"},
		{"two emoji", CustomInput{Name: "Star", Emoji: "⭐⭐"}, "emoji"},
		{"plain text", CustomInput{Name: "Star", Emoji: "star"}, "emoji"},
		{"description too long", CustomInput{Name: "Star", Emoji: "⭐", Description: strPtr(strings.Repeat("b", 201))}, "description"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cleanCustom(tc.in)
			var fe *FieldError
			if !errors.As(err, &fe) || fe.Field != tc.field {
				t.Fatalf("err = %v, want field error on %s", err, tc.field)
			}
		})
	}
	clean, err := cleanCustom(CustomInput{Name: "Star", Emoji: "⭐", Description: strPtr("  ")})
	if err != nil || clean.Description != nil {
		t.Fatalf("blank description = %+v, %v; want NULL description", clean, err)
	}
	for _, emoji := range []string{"👍🏽", "🇬🇧", "❤️", "👨‍👩‍👧"} {
		if _, err := cleanCustom(CustomInput{Name: "Star", Emoji: emoji}); err != nil {
			t.Fatalf("single emoji %q rejected: %v", emoji, err)
		}
	}
}
