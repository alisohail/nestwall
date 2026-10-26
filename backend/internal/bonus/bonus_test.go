package bonus

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/rewards"
	"nestwall/backend/internal/testhelper"
)

// childRoleID is the seeded Child role (migration 000001).
const childRoleID = "c3d4e5f6-a7b8-9012-cdef-123456789012"

// base is the test clock's start. The family zone is UTC, so the day ends at 2026-10-11 00:00 UTC.
var base = time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)

// env is a database with one family and its parent.
type env struct {
	db     *sql.DB
	f      testhelper.Family
	parent Actor
}

func newEnv(t *testing.T) env {
	t.Helper()
	conn := testhelper.OpenDB(t)
	f := testhelper.OneFamily(t, conn)
	return env{
		db:     conn,
		f:      f,
		parent: Actor{FamilyID: f.FamilyID, UserID: f.ParentUserID, MemberID: f.MemberID},
	}
}

// child adds an active child to the test family.
func (e env) child(t *testing.T) Actor {
	t.Helper()
	return addChild(t, e.db, e.f.FamilyID)
}

// addChild adds an active child to a family.
func addChild(t *testing.T, conn *sql.DB, familyID string) Actor {
	t.Helper()
	userID, memberID := ids.New(), ids.New()
	mustExec(t, conn,
		`INSERT INTO users (id, user_id, password_hash, display_name, is_child) VALUES (?, ?, 'unused', 'Kid', 1)`,
		userID, "kid_"+userID)
	mustExec(t, conn,
		`INSERT INTO family_members (id, family_id, user_id, role_id, status, colour) VALUES (?, ?, ?, ?, 'active', '#F59E0B')`,
		memberID, familyID, userID, childRoleID)
	return Actor{FamilyID: familyID, UserID: userID, MemberID: memberID}
}

// insertTemplate adds a task template to the test family and returns its ID.
func insertTemplate(t *testing.T, e env, kind, name, title string, points, approval int) string {
	t.Helper()
	id := ids.New()
	mustExec(t, e.db,
		`INSERT INTO task_templates (id, family_id, kind, name, title, default_points, approval_required, created_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, e.f.FamilyID, kind, name, title, points, approval, e.f.ParentUserID)
	return id
}

func mustExec(t *testing.T, conn *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := conn.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func fixedClock(at time.Time) clock.Clock { return clock.FakeClock{Time: at} }

func ptr[T any](v T) *T { return &v }

// basicInput is a valid first_come task worth 10 points, approval required, for the given children.
func basicInput(eligible ...string) PublishInput {
	return PublishInput{
		Title:             ptr("Wash the car"),
		Points:            ptr(10),
		EligibleMemberIDs: eligible,
	}
}

func publishAt(t *testing.T, e env, at time.Time, in PublishInput) Task {
	t.Helper()
	task, err := Publish(context.Background(), e.db, fixedClock(at), e.parent, in)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	return task
}

func publish(t *testing.T, e env, in PublishInput) Task {
	t.Helper()
	return publishAt(t, e, base, in)
}

func balance(t *testing.T, e env, memberID string) int {
	t.Helper()
	n, err := rewards.Balance(context.Background(), e.db, e.f.FamilyID, memberID)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	return n
}

func wantErr(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want %v", err, target)
	}
}

func wantField(t *testing.T, err error, field, reason string) {
	t.Helper()
	var fe *FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("error = %v, want FieldError %s/%s", err, field, reason)
	}
	if fe.Field != field || fe.Reason != reason {
		t.Fatalf("field error = %s/%s, want %s/%s", fe.Field, fe.Reason, field, reason)
	}
}

func TestFirstComeOnlyOneWins(t *testing.T) {
	e := newEnv(t)
	kids := []Actor{e.child(t), e.child(t), e.child(t), e.child(t)}
	memberIDs := make([]string, 0, len(kids))
	for _, k := range kids {
		memberIDs = append(memberIDs, k.MemberID)
	}
	task := publish(t, e, basicInput(memberIDs...))

	var wg sync.WaitGroup
	results := make(chan error, len(kids))
	for _, k := range kids {
		wg.Add(1)
		go func(a Actor) {
			defer wg.Done()
			res, err := Grab(context.Background(), e.db, fixedClock(base), a, task.ID)
			if err == nil && res.Replayed {
				err = errors.New("unexpected replay")
			}
			results <- err
		}(k)
	}
	wg.Wait()
	close(results)

	wins, taken := 0, 0
	for err := range results {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, ErrTaken):
			taken++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if wins != 1 || taken != len(kids)-1 {
		t.Fatalf("wins = %d, taken = %d; want 1 and %d", wins, taken, len(kids)-1)
	}
}

func TestGrabReplayAndOtherChildTaken(t *testing.T) {
	e := newEnv(t)
	a, b := e.child(t), e.child(t)
	task := publish(t, e, basicInput(a.MemberID, b.MemberID))

	first, err := Grab(context.Background(), e.db, fixedClock(base), a, task.ID)
	if err != nil || first.Replayed {
		t.Fatalf("first grab: replayed=%v err=%v", first.Replayed, err)
	}
	again, err := Grab(context.Background(), e.db, fixedClock(base), a, task.ID)
	if err != nil || !again.Replayed || again.ID != first.ID {
		t.Fatalf("replay: replayed=%v same=%v err=%v", again.Replayed, again.ID == first.ID, err)
	}
	_, err = Grab(context.Background(), e.db, fixedClock(base), b, task.ID)
	wantErr(t, err, ErrTaken)
}

func TestEveryoneOnceLetsEachChildClaim(t *testing.T) {
	e := newEnv(t)
	a, b := e.child(t), e.child(t)
	in := basicInput(a.MemberID, b.MemberID)
	in.ClaimMode = ptr(ClaimEveryoneOnce)
	task := publish(t, e, in)

	if _, err := Grab(context.Background(), e.db, fixedClock(base), a, task.ID); err != nil {
		t.Fatalf("a grab: %v", err)
	}
	if _, err := Grab(context.Background(), e.db, fixedClock(base), b, task.ID); err != nil {
		t.Fatalf("b grab: %v", err)
	}
	again, err := Grab(context.Background(), e.db, fixedClock(base), b, task.ID)
	if err != nil || !again.Replayed {
		t.Fatalf("b replay: replayed=%v err=%v", again.Replayed, err)
	}
}

func TestCompleteApproveAwardsOnce(t *testing.T) {
	e := newEnv(t)
	kid := e.child(t)
	task := publish(t, e, basicInput(kid.MemberID))
	ctx := context.Background()

	c, err := Grab(ctx, e.db, fixedClock(base), kid, task.ID)
	if err != nil {
		t.Fatalf("grab: %v", err)
	}
	submitted, err := Complete(ctx, e.db, fixedClock(base), kid, task.ID, -1)
	if err != nil || submitted.Status != StatusSubmitted {
		t.Fatalf("complete: status=%q err=%v", submitted.Status, err)
	}

	approved, err := Approve(ctx, e.db, fixedClock(base), e.parent, c.ID, -1)
	if err != nil || approved.Replayed || approved.Status != StatusApproved {
		t.Fatalf("approve: replayed=%v status=%q err=%v", approved.Replayed, approved.Status, err)
	}
	again, err := Approve(ctx, e.db, fixedClock(base), e.parent, c.ID, -1)
	if err != nil || !again.Replayed {
		t.Fatalf("approve again: replayed=%v err=%v", again.Replayed, err)
	}
	if got := balance(t, e, kid.MemberID); got != 10 {
		t.Fatalf("balance = %d, want 10 (one award only)", got)
	}
}

func TestNoApprovalAutoAwards(t *testing.T) {
	e := newEnv(t)
	kid := e.child(t)
	in := basicInput(kid.MemberID)
	in.ApprovalRequired = ptr(false)
	task := publish(t, e, in)
	ctx := context.Background()

	if _, err := Grab(ctx, e.db, fixedClock(base), kid, task.ID); err != nil {
		t.Fatalf("grab: %v", err)
	}
	done, err := Complete(ctx, e.db, fixedClock(base), kid, task.ID, -1)
	if err != nil || done.Status != StatusApproved {
		t.Fatalf("complete: status=%q err=%v", done.Status, err)
	}
	if got := balance(t, e, kid.MemberID); got != 10 {
		t.Fatalf("balance = %d, want 10", got)
	}
	_, err = Complete(ctx, e.db, fixedClock(base), kid, task.ID, -1)
	wantErr(t, err, ErrWrongState)
	if got := balance(t, e, kid.MemberID); got != 10 {
		t.Fatalf("balance after repeat complete = %d, want 10", got)
	}
}

func TestRejectReplayAndApproveAfterRejectFails(t *testing.T) {
	e := newEnv(t)
	kid := e.child(t)
	task := publish(t, e, basicInput(kid.MemberID))
	ctx := context.Background()

	c, err := Grab(ctx, e.db, fixedClock(base), kid, task.ID)
	if err != nil {
		t.Fatalf("grab: %v", err)
	}
	if _, err := Complete(ctx, e.db, fixedClock(base), kid, task.ID, -1); err != nil {
		t.Fatalf("complete: %v", err)
	}
	rej, err := Reject(ctx, e.db, fixedClock(base), e.parent, c.ID, -1, "  not finished  ")
	if err != nil || rej.Status != StatusRejected {
		t.Fatalf("reject: status=%q err=%v", rej.Status, err)
	}
	if rej.DecisionNote == nil || *rej.DecisionNote != "not finished" {
		t.Fatalf("decision note = %v, want trimmed text", rej.DecisionNote)
	}
	again, err := Reject(ctx, e.db, fixedClock(base), e.parent, c.ID, -1, "")
	if err != nil || !again.Replayed {
		t.Fatalf("reject again: replayed=%v err=%v", again.Replayed, err)
	}
	_, err = Approve(ctx, e.db, fixedClock(base), e.parent, c.ID, -1)
	wantErr(t, err, ErrWrongState)
	if got := balance(t, e, kid.MemberID); got != 0 {
		t.Fatalf("balance = %d, want 0", got)
	}
}

func TestRejectedClaimFreesTask(t *testing.T) {
	e := newEnv(t)
	a, b := e.child(t), e.child(t)
	task := publish(t, e, basicInput(a.MemberID, b.MemberID))
	ctx := context.Background()

	c, err := Grab(ctx, e.db, fixedClock(base), a, task.ID)
	if err != nil {
		t.Fatalf("a grab: %v", err)
	}
	if _, err := Complete(ctx, e.db, fixedClock(base), a, task.ID, -1); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, err := Reject(ctx, e.db, fixedClock(base), e.parent, c.ID, -1, "try again"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	// A sent-back claim holds nothing: the same child can grab again, and the other child is not blocked.
	again, err := Grab(ctx, e.db, fixedClock(base), a, task.ID)
	if err != nil || again.Replayed {
		t.Fatalf("a grab after reject: replayed=%v err=%v", again.Replayed, err)
	}
	_, err = Grab(ctx, e.db, fixedClock(base), b, task.ID)
	wantErr(t, err, ErrTaken)
}

func TestRejectTooLongNote(t *testing.T) {
	e := newEnv(t)
	_, err := Reject(context.Background(), e.db, fixedClock(base), e.parent, ids.New(), -1, strings.Repeat("n", 201))
	wantField(t, err, "decision_note", "too_long")
}

func TestReleaseThenReclaim(t *testing.T) {
	e := newEnv(t)
	a, b := e.child(t), e.child(t)
	task := publish(t, e, basicInput(a.MemberID, b.MemberID))
	ctx := context.Background()

	if _, err := Grab(ctx, e.db, fixedClock(base), a, task.ID); err != nil {
		t.Fatalf("a grab: %v", err)
	}
	released, err := Release(ctx, e.db, fixedClock(base), a, task.ID, -1)
	if err != nil || released.Status != StatusReleased {
		t.Fatalf("release: status=%q err=%v", released.Status, err)
	}
	if _, err := Grab(ctx, e.db, fixedClock(base), b, task.ID); err != nil {
		t.Fatalf("b grab after release: %v", err)
	}
	_, err = Release(ctx, e.db, fixedClock(base), a, task.ID, -1)
	wantErr(t, err, ErrNotFound)
}

func TestVersionConflict(t *testing.T) {
	e := newEnv(t)
	kid := e.child(t)
	task := publish(t, e, basicInput(kid.MemberID))
	ctx := context.Background()

	if _, err := Grab(ctx, e.db, fixedClock(base), kid, task.ID); err != nil {
		t.Fatalf("grab: %v", err)
	}
	_, err := Complete(ctx, e.db, fixedClock(base), kid, task.ID, 2)
	var stale *StaleError
	if !errors.As(err, &stale) || stale.Current != 1 {
		t.Fatalf("error = %v, want StaleError current 1", err)
	}
	if _, err := Complete(ctx, e.db, fixedClock(base), kid, task.ID, 1); err != nil {
		t.Fatalf("complete with current version: %v", err)
	}
}

func TestExpiryAtEndOfDay(t *testing.T) {
	e := newEnv(t)
	a, b := e.child(t), e.child(t)
	late := time.Date(2026, 10, 10, 23, 59, 0, 0, time.UTC)
	nextDay := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	task := publishAt(t, e, late, basicInput(a.MemberID, b.MemberID))
	ctx := context.Background()

	if _, err := Grab(ctx, e.db, fixedClock(late), a, task.ID); err != nil {
		t.Fatalf("grab at 23:59: %v", err)
	}
	_, err := Grab(ctx, e.db, fixedClock(nextDay), b, task.ID)
	wantErr(t, err, ErrExpired)
	_, err = Complete(ctx, e.db, fixedClock(nextDay), a, task.ID, -1)
	wantErr(t, err, ErrExpired)
	if got := balance(t, e, b.MemberID); got != 0 {
		t.Fatalf("unclaimed balance = %d, want 0", got)
	}
}

func TestShowFromGate(t *testing.T) {
	e := newEnv(t)
	kid := e.child(t)
	in := basicInput(kid.MemberID)
	in.ShowFrom = ptr("2026-10-10T12:00:00Z")
	task := publish(t, e, in)
	ctx := context.Background()

	_, err := Grab(ctx, e.db, fixedClock(base), kid, task.ID)
	wantErr(t, err, ErrNotOpen)

	hidden, err := List(ctx, e.db, fixedClock(base), kid, false, ScopeToday)
	if err != nil || len(hidden) != 0 {
		t.Fatalf("child list before show_from: %d tasks, err=%v", len(hidden), err)
	}
	parentView, err := List(ctx, e.db, fixedClock(base), e.parent, true, ScopeToday)
	if err != nil || len(parentView) != 1 || parentView[0].State != StateScheduled {
		t.Fatalf("parent list before show_from: err=%v", err)
	}

	noon := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	if _, err := Grab(ctx, e.db, fixedClock(noon), kid, task.ID); err != nil {
		t.Fatalf("grab at noon: %v", err)
	}
}

func TestPublishValidation(t *testing.T) {
	e := newEnv(t)
	kid := e.child(t)
	other := testhelper.OneFamily(t, e.db)
	regular := insertTemplate(t, e, "regular", "Make bed", "Make bed", 5, 1)

	cases := []struct {
		name   string
		mod    func(*PublishInput)
		field  string
		reason string
	}{
		{"title required", func(p *PublishInput) { p.Title = ptr("   ") }, "title", "required"},
		{"title too long", func(p *PublishInput) { p.Title = ptr(strings.Repeat("a", 201)) }, "title", "too_long"},
		{"points zero", func(p *PublishInput) { p.Points = ptr(0) }, "points", "out_of_range"},
		{"points too high", func(p *PublishInput) { p.Points = ptr(100001) }, "points", "out_of_range"},
		{"bad claim mode", func(p *PublishInput) { p.ClaimMode = ptr("weekly") }, "claim_mode", "invalid"},
		{"no eligible", func(p *PublishInput) { p.EligibleMemberIDs = nil }, "eligible_member_ids", "required"},
		{"parent eligible", func(p *PublishInput) { p.EligibleMemberIDs = []string{e.parent.MemberID} }, "eligible_member_ids", "not_child"},
		{"other family member", func(p *PublishInput) { p.EligibleMemberIDs = []string{other.MemberID} }, "eligible_member_ids", "not_found"},
		{"ends after day", func(p *PublishInput) { p.EndsAt = ptr("2026-10-11T00:00:01Z") }, "ends_at", "after_day"},
		{"ends in past", func(p *PublishInput) { p.EndsAt = ptr("2026-10-10T08:00:00Z") }, "ends_at", "in_past"},
		{"show from after end", func(p *PublishInput) {
			p.ShowFrom = ptr("2026-10-10T18:00:00Z")
			p.EndsAt = ptr("2026-10-10T18:00:00Z")
		}, "show_from", "after_end"},
		{"show from invalid", func(p *PublishInput) { p.ShowFrom = ptr("tomorrow") }, "show_from", "invalid"},
		{"template not found", func(p *PublishInput) { p.TemplateID = ptr(ids.New()) }, "template_id", "not_found"},
		{"template not bonus", func(p *PublishInput) { p.TemplateID = ptr(regular) }, "template_id", "not_bonus"},
		{"day invalid", func(p *PublishInput) { p.Day = ptr("10/10/2026") }, "day", "invalid"},
		{"day in past", func(p *PublishInput) { p.Day = ptr("2026-10-09") }, "day", "in_past"},
		{"ends before day", func(p *PublishInput) {
			p.Day = ptr("2026-10-11")
			p.EndsAt = ptr("2026-10-10T18:00:00Z")
		}, "ends_at", "before_day"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := basicInput(kid.MemberID)
			tc.mod(&in)
			_, err := Publish(context.Background(), e.db, fixedClock(base), e.parent, in)
			wantField(t, err, tc.field, tc.reason)
		})
	}
}

func TestBonusTemplatePrefill(t *testing.T) {
	e := newEnv(t)
	kid := e.child(t)
	tpl := insertTemplate(t, e, "bonus", "Tidy", "Tidy room", 15, 0)
	mustExec(t, e.db, `UPDATE task_templates SET description = ? WHERE id = ?`, "Bonus for a tidy room", tpl)

	task := publish(t, e, PublishInput{TemplateID: ptr(tpl), EligibleMemberIDs: []string{kid.MemberID}})
	if task.Title != "Tidy room" || task.Points != 15 || task.ApprovalRequired {
		t.Fatalf("prefill: title=%q points=%d approval=%v", task.Title, task.Points, task.ApprovalRequired)
	}
	if task.Description == nil || *task.Description != "Bonus for a tidy room" {
		t.Fatalf("prefill description = %v", task.Description)
	}
	if task.TemplateID == nil || *task.TemplateID != tpl {
		t.Fatalf("template id = %v, want %s", task.TemplateID, tpl)
	}

	over := publish(t, e, PublishInput{
		TemplateID:        ptr(tpl),
		Title:             ptr("Tidy up"),
		Points:            ptr(20),
		EligibleMemberIDs: []string{kid.MemberID},
	})
	if over.Title != "Tidy up" || over.Points != 20 {
		t.Fatalf("body override: title=%q points=%d", over.Title, over.Points)
	}
}

func TestBonusTemplatePenaltyRefused(t *testing.T) {
	e := newEnv(t)
	_, err := e.db.Exec(
		`INSERT INTO task_templates (id, family_id, kind, name, title, default_points, approval_required, created_by, penalty_value, penalty_unit)
		 VALUES (?, ?, 'bonus', 'Bad', 'Bad', 5, 0, ?, 5, 'points')`,
		ids.New(), e.f.FamilyID, e.f.ParentUserID)
	if err == nil || !strings.Contains(err.Error(), "cannot carry a penalty") {
		t.Fatalf("insert with penalty: err = %v, want trigger refusal", err)
	}
}

// setBonusDefaults stores bonus defaults rows directly, as pairs of key and value.
func setBonusDefaults(t *testing.T, e env, pairs ...string) {
	t.Helper()
	for i := 0; i+1 < len(pairs); i += 2 {
		mustExec(t, e.db,
			`INSERT INTO app_settings (family_id, setting_key, setting_value) VALUES (?, ?, ?)`,
			e.f.FamilyID, pairs[i], pairs[i+1])
	}
}

func TestBonusDefaultsPrefillPublish(t *testing.T) {
	e := newEnv(t)
	kid := e.child(t)
	setBonusDefaults(t, e,
		"bonus_defaults.points", "25",
		"bonus_defaults.claim_mode", "everyone_once",
		"bonus_defaults.approval_required", "0")

	task := publish(t, e, PublishInput{Title: ptr("Feed the dog"), EligibleMemberIDs: []string{kid.MemberID}})
	if task.Points != 25 || task.ClaimMode != ClaimEveryoneOnce || task.ApprovalRequired {
		t.Fatalf("defaults: points=%d mode=%s approval=%v", task.Points, task.ClaimMode, task.ApprovalRequired)
	}

	over := publish(t, e, PublishInput{
		Title:             ptr("Feed the cat"),
		Points:            ptr(7),
		ClaimMode:         ptr(ClaimFirstCome),
		ApprovalRequired:  ptr(true),
		EligibleMemberIDs: []string{kid.MemberID},
	})
	if over.Points != 7 || over.ClaimMode != ClaimFirstCome || !over.ApprovalRequired {
		t.Fatalf("body override: points=%d mode=%s approval=%v", over.Points, over.ClaimMode, over.ApprovalRequired)
	}
}

func TestTemplateOverridesBonusDefaults(t *testing.T) {
	e := newEnv(t)
	kid := e.child(t)
	setBonusDefaults(t, e, "bonus_defaults.points", "25", "bonus_defaults.approval_required", "0")
	tpl := insertTemplate(t, e, "bonus", "Tidy", "Tidy room", 15, 1)

	task := publish(t, e, PublishInput{TemplateID: ptr(tpl), EligibleMemberIDs: []string{kid.MemberID}})
	if task.Points != 15 || !task.ApprovalRequired {
		t.Fatalf("template over defaults: points=%d approval=%v, want 15 and true", task.Points, task.ApprovalRequired)
	}
}

func TestBonusDefaultsTimes(t *testing.T) {
	dayEnd := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	at := func(h, m int) time.Time { return time.Date(2026, 10, 10, h, m, 0, 0, time.UTC) }

	cases := []struct {
		name     string
		defaults []string
		body     func(*PublishInput)
		wantEnds time.Time
		wantFrom *time.Time
	}{
		{
			name:     "default ends and show from apply",
			defaults: []string{"bonus_defaults.ends", "20:00", "bonus_defaults.show_from", "08:00"},
			wantEnds: at(20, 0),
			wantFrom: ptr(at(8, 0)),
		},
		{
			name:     "end of day default keeps the day end",
			defaults: []string{"bonus_defaults.ends", "end_of_day"},
			wantEnds: dayEnd,
		},
		{
			name:     "default ends already past is ignored",
			defaults: []string{"bonus_defaults.ends", "08:00"},
			wantEnds: dayEnd,
		},
		{
			name:     "default show from after the body end is ignored",
			defaults: []string{"bonus_defaults.show_from", "20:00"},
			body: func(p *PublishInput) {
				p.EndsAt = ptr("2026-10-10T18:00:00Z")
			},
			wantEnds: at(18, 0),
		},
		{
			name:     "body show from wins over the default",
			defaults: []string{"bonus_defaults.show_from", "20:00"},
			body: func(p *PublishInput) {
				p.ShowFrom = ptr("2026-10-10T10:00:00Z")
			},
			wantEnds: dayEnd,
			wantFrom: ptr(at(10, 0)),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			kid := e.child(t)
			setBonusDefaults(t, e, tc.defaults...)
			in := PublishInput{Title: ptr("Dishes"), Points: ptr(5), EligibleMemberIDs: []string{kid.MemberID}}
			if tc.body != nil {
				tc.body(&in)
			}
			task := publish(t, e, in)
			if !task.EndsAt.Equal(tc.wantEnds) {
				t.Errorf("ends = %s, want %s", task.EndsAt, tc.wantEnds)
			}
			switch {
			case tc.wantFrom == nil && task.ShowFrom != nil:
				t.Errorf("show from = %s, want none", task.ShowFrom)
			case tc.wantFrom != nil && (task.ShowFrom == nil || !task.ShowFrom.Equal(*tc.wantFrom)):
				t.Errorf("show from = %v, want %s", task.ShowFrom, *tc.wantFrom)
			}
		})
	}
}

func TestParentCannotGrab(t *testing.T) {
	e := newEnv(t)
	kid := e.child(t)
	task := publish(t, e, basicInput(kid.MemberID))
	_, err := Grab(context.Background(), e.db, fixedClock(base), e.parent, task.ID)
	wantErr(t, err, ErrChildrenOnly)
}

func TestIneligibleAndForeignChildNotFound(t *testing.T) {
	e := newEnv(t)
	eligible := e.child(t)
	outsider := e.child(t)
	task := publish(t, e, basicInput(eligible.MemberID))
	ctx := context.Background()

	_, err := Grab(ctx, e.db, fixedClock(base), outsider, task.ID)
	wantErr(t, err, ErrNotFound)

	foreignKid := addChild(t, e.db, testhelper.OneFamily(t, e.db).FamilyID)
	spoof := Actor{FamilyID: e.f.FamilyID, UserID: foreignKid.UserID, MemberID: foreignKid.MemberID}
	_, err = Grab(ctx, e.db, fixedClock(base), spoof, task.ID)
	wantErr(t, err, ErrNotFound)
}

func TestListViews(t *testing.T) {
	e := newEnv(t)
	a, b := e.child(t), e.child(t)
	task := publish(t, e, basicInput(a.MemberID, b.MemberID))
	ctx := context.Background()

	if _, err := Grab(ctx, e.db, fixedClock(base), a, task.ID); err != nil {
		t.Fatalf("grab: %v", err)
	}

	parentView, err := List(ctx, e.db, fixedClock(base), e.parent, true, ScopeToday)
	if err != nil || len(parentView) != 1 || len(parentView[0].Claims) != 1 {
		t.Fatalf("parent view: err=%v", err)
	}
	if parentView[0].Claims[0].Status != StatusClaimed {
		t.Fatalf("parent claim status = %q", parentView[0].Claims[0].Status)
	}

	aView, err := List(ctx, e.db, fixedClock(base), a, false, ScopeToday)
	if err != nil || len(aView) != 1 || aView[0].MyClaim == nil || aView[0].TakenByOther {
		t.Fatalf("holder view: err=%v", err)
	}

	bView, err := List(ctx, e.db, fixedClock(base), b, false, ScopeToday)
	if err != nil || len(bView) != 1 || bView[0].MyClaim != nil || !bView[0].TakenByOther {
		t.Fatalf("other child view: err=%v", err)
	}
}

func TestFutureDayPublishAndScopes(t *testing.T) {
	e := newEnv(t)
	kid := e.child(t)
	ctx := context.Background()
	in := basicInput(kid.MemberID)
	in.Day = ptr("2026-10-12")
	task := publish(t, e, in)
	if task.Day != "2026-10-12" || task.State != StateScheduled {
		t.Fatalf("day = %q state = %q, want 2026-10-12 scheduled", task.Day, task.State)
	}
	if want := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC); !task.EndsAt.Equal(want) {
		t.Fatalf("ends_at = %v, want %v", task.EndsAt, want)
	}

	_, err := Grab(ctx, e.db, fixedClock(base), kid, task.ID)
	wantErr(t, err, ErrNotOpen)

	today, err := List(ctx, e.db, fixedClock(base), e.parent, true, ScopeToday)
	if err != nil || len(today) != 0 {
		t.Fatalf("parent today: %d tasks, err=%v", len(today), err)
	}
	scheduled, err := List(ctx, e.db, fixedClock(base), e.parent, true, ScopeScheduled)
	if err != nil || len(scheduled) != 1 || scheduled[0].ID != task.ID {
		t.Fatalf("parent scheduled: %d tasks, err=%v", len(scheduled), err)
	}
	kidToday, err := List(ctx, e.db, fixedClock(base), kid, false, ScopeToday)
	if err != nil || len(kidToday) != 0 {
		t.Fatalf("child today: %d tasks, err=%v", len(kidToday), err)
	}
}

func TestPastScopeAfterDayEnds(t *testing.T) {
	e := newEnv(t)
	kid := e.child(t)
	task := publish(t, e, basicInput(kid.MemberID))
	ctx := context.Background()
	nextDay := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)

	past, err := List(ctx, e.db, fixedClock(nextDay), e.parent, true, ScopePast)
	if err != nil || len(past) != 1 || past[0].ID != task.ID || past[0].State != StateExpired {
		t.Fatalf("parent past: %d tasks, err=%v", len(past), err)
	}
	today, err := List(ctx, e.db, fixedClock(nextDay), e.parent, true, ScopeToday)
	if err != nil || len(today) != 0 {
		t.Fatalf("parent today after day end: %d tasks, err=%v", len(today), err)
	}
	_, err = Grab(ctx, e.db, fixedClock(nextDay), kid, task.ID)
	wantErr(t, err, ErrExpired)
}

func TestEditMovesDayAndGuards(t *testing.T) {
	e := newEnv(t)
	kid := e.child(t)
	task := publish(t, e, basicInput(kid.MemberID))
	ctx := context.Background()

	moved, err := Edit(ctx, e.db, fixedClock(base), e.parent, task.ID,
		EditInput{Day: ptr("2026-10-12"), Title: ptr("Wash the van")}, task.Version)
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if moved.Day != "2026-10-12" || moved.Title != "Wash the van" || moved.Version != 2 || moved.State != StateScheduled {
		t.Fatalf("moved: day=%q title=%q version=%d state=%q", moved.Day, moved.Title, moved.Version, moved.State)
	}
	if want := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC); !moved.EndsAt.Equal(want) {
		t.Fatalf("ends_at = %v, want %v", moved.EndsAt, want)
	}

	_, err = Edit(ctx, e.db, fixedClock(base), e.parent, task.ID, EditInput{Title: ptr("Old")}, 1)
	var stale *StaleError
	if !errors.As(err, &stale) || stale.Current != 2 {
		t.Fatalf("stale edit error = %v, want StaleError current 2", err)
	}
	_, err = Edit(ctx, e.db, fixedClock(base), e.parent, task.ID, EditInput{Day: ptr("2026-10-09")}, -1)
	wantField(t, err, "day", "in_past")

	day12 := time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)
	if _, err := Grab(ctx, e.db, fixedClock(day12), kid, task.ID); err != nil {
		t.Fatalf("grab on its day: %v", err)
	}
	_, err = Edit(ctx, e.db, fixedClock(day12), e.parent, task.ID, EditInput{Title: ptr("Late")}, -1)
	wantErr(t, err, ErrHasClaims)
	_, err = Cancel(ctx, e.db, fixedClock(day12), e.parent, task.ID, -1)
	wantErr(t, err, ErrHasClaims)
}

func TestCancelOnceAndBlockedByClaims(t *testing.T) {
	e := newEnv(t)
	kid := e.child(t)
	ctx := context.Background()

	task := publish(t, e, basicInput(kid.MemberID))
	cancelled, err := Cancel(ctx, e.db, fixedClock(base), e.parent, task.ID, 1)
	if err != nil || cancelled.State != StateCancelled || cancelled.Version != 2 || cancelled.CancelledAt == nil {
		t.Fatalf("cancel: state=%q version=%d err=%v", cancelled.State, cancelled.Version, err)
	}
	_, err = Cancel(ctx, e.db, fixedClock(base), e.parent, task.ID, -1)
	wantErr(t, err, ErrWrongState)
	_, err = Grab(ctx, e.db, fixedClock(base), kid, task.ID)
	wantErr(t, err, ErrCancelled)

	other := publish(t, e, basicInput(kid.MemberID))
	if _, err := Grab(ctx, e.db, fixedClock(base), kid, other.ID); err != nil {
		t.Fatalf("grab: %v", err)
	}
	_, err = Cancel(ctx, e.db, fixedClock(base), e.parent, other.ID, 7)
	var stale *StaleError
	if !errors.As(err, &stale) || stale.Current != 1 {
		t.Fatalf("stale cancel error = %v, want StaleError current 1", err)
	}
	_, err = Cancel(ctx, e.db, fixedClock(base), e.parent, other.ID, -1)
	wantErr(t, err, ErrHasClaims)
}

func TestCancelledLeavesTodayAndScheduledForPast(t *testing.T) {
	e := newEnv(t)
	kid := e.child(t)
	ctx := context.Background()

	sameDay := publish(t, e, basicInput(kid.MemberID))
	if _, err := Cancel(ctx, e.db, fixedClock(base), e.parent, sameDay.ID, 1); err != nil {
		t.Fatalf("cancel today: %v", err)
	}
	today, err := List(ctx, e.db, fixedClock(base), e.parent, true, ScopeToday)
	if err != nil || len(today) != 0 {
		t.Fatalf("parent today after cancel: %d tasks, err=%v", len(today), err)
	}
	past, err := List(ctx, e.db, fixedClock(base), e.parent, true, ScopePast)
	if err != nil || len(past) != 1 || past[0].ID != sameDay.ID || past[0].State != StateCancelled {
		t.Fatalf("parent past after cancel: %d tasks, err=%v", len(past), err)
	}

	in := basicInput(kid.MemberID)
	in.Day = ptr("2026-10-12")
	future := publish(t, e, in)
	if _, err := Cancel(ctx, e.db, fixedClock(base), e.parent, future.ID, 1); err != nil {
		t.Fatalf("cancel future: %v", err)
	}
	scheduled, err := List(ctx, e.db, fixedClock(base), e.parent, true, ScopeScheduled)
	if err != nil || len(scheduled) != 0 {
		t.Fatalf("parent scheduled after cancel: %d tasks, err=%v", len(scheduled), err)
	}
	past, err = List(ctx, e.db, fixedClock(base), e.parent, true, ScopePast)
	if err != nil || len(past) != 2 {
		t.Fatalf("parent past after future cancel: %d tasks, err=%v", len(past), err)
	}
}

func TestUnassignFreesTaskPostsNothing(t *testing.T) {
	e := newEnv(t)
	a, b := e.child(t), e.child(t)
	task := publish(t, e, basicInput(a.MemberID, b.MemberID))
	ctx := context.Background()

	c, err := Grab(ctx, e.db, fixedClock(base), a, task.ID)
	if err != nil {
		t.Fatalf("a grab: %v", err)
	}
	released, err := Unassign(ctx, e.db, fixedClock(base), e.parent, c.ID, -1)
	if err != nil || released.Status != StatusReleased {
		t.Fatalf("unassign: status=%q err=%v", released.Status, err)
	}
	if got := balance(t, e, a.MemberID); got != 0 {
		t.Fatalf("balance after unassign = %d, want 0", got)
	}
	if _, err := Grab(ctx, e.db, fixedClock(base), b, task.ID); err != nil {
		t.Fatalf("b grab after unassign: %v", err)
	}
	_, err = Unassign(ctx, e.db, fixedClock(base), e.parent, c.ID, -1)
	wantErr(t, err, ErrWrongState)
	_, err = Cancel(ctx, e.db, fixedClock(base), e.parent, task.ID, -1)
	wantErr(t, err, ErrHasClaims)
}

func TestUnassignRefusedAfterApproval(t *testing.T) {
	e := newEnv(t)
	kid := e.child(t)
	task := publish(t, e, basicInput(kid.MemberID))
	ctx := context.Background()

	c, err := Grab(ctx, e.db, fixedClock(base), kid, task.ID)
	if err != nil {
		t.Fatalf("grab: %v", err)
	}
	if _, err := Complete(ctx, e.db, fixedClock(base), kid, task.ID, -1); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if _, err := Approve(ctx, e.db, fixedClock(base), e.parent, c.ID, -1); err != nil {
		t.Fatalf("approve: %v", err)
	}
	_, err = Unassign(ctx, e.db, fixedClock(base), e.parent, c.ID, -1)
	wantErr(t, err, ErrWrongState)
	if got := balance(t, e, kid.MemberID); got != 10 {
		t.Fatalf("balance = %d, want 10", got)
	}
}

func TestHandlers(t *testing.T) {
	e := newEnv(t)
	kid1, kid2 := e.child(t), e.child(t)
	h := NewHandlers(e.db, fixedClock(base), slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/bonus-tasks", h.ListTasks)
	mux.HandleFunc("POST /api/v1/bonus-tasks", h.CreateTask)
	mux.HandleFunc("POST /api/v1/bonus-tasks/{id}/claim", h.ClaimTask)
	mux.HandleFunc("POST /api/v1/bonus-tasks/{id}/release", h.ReleaseTask)
	mux.HandleFunc("POST /api/v1/bonus-tasks/{id}/complete", h.CompleteTask)
	mux.HandleFunc("POST /api/v1/bonus-claims/{id}/approve", h.ApproveClaim)
	mux.HandleFunc("POST /api/v1/bonus-claims/{id}/reject", h.RejectClaim)
	mux.HandleFunc("PATCH /api/v1/bonus-tasks/{id}", h.EditTask)
	mux.HandleFunc("POST /api/v1/bonus-tasks/{id}/cancel", h.CancelTask)
	mux.HandleFunc("POST /api/v1/bonus-claims/{id}/unassign", h.UnassignClaim)

	do := func(a Actor, method, target, body string, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		ctx := context.WithValue(req.Context(), permissions.ContextKeyFamilyMemberID, a.MemberID)
		ctx = context.WithValue(ctx, permissions.ContextKeyUserID, a.UserID)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req.WithContext(ctx))
		return rec
	}
	expect := func(rec *httptest.ResponseRecorder, want int) {
		t.Helper()
		if rec.Code != want {
			t.Fatalf("status %d, want %d: %s", rec.Code, want, rec.Body.String())
		}
	}

	body := `{"title":"Wash the car","points":10,"eligible_member_ids":["` + kid1.MemberID + `","` + kid2.MemberID + `"]}`
	rec := do(e.parent, http.MethodPost, "/api/v1/bonus-tasks", body, nil)
	expect(rec, http.StatusCreated)
	var task Task
	if err := json.Unmarshal(rec.Body.Bytes(), &task); err != nil {
		t.Fatalf("decode task: %v", err)
	}

	expect(do(e.parent, http.MethodPost, "/api/v1/bonus-tasks",
		`{"title":"x","points":5,"colour":"red","eligible_member_ids":["`+kid1.MemberID+`"]}`, nil),
		http.StatusBadRequest)

	claimURL := "/api/v1/bonus-tasks/" + task.ID + "/claim"
	rec = do(kid1, http.MethodPost, claimURL, "", nil)
	expect(rec, http.StatusCreated)
	if got := rec.Header().Get("ETag"); got != `"1"` {
		t.Fatalf("claim ETag = %q, want \"1\"", got)
	}
	expect(do(kid1, http.MethodPost, claimURL, "", nil), http.StatusOK)
	expect(do(e.parent, http.MethodPost, claimURL, "", nil), http.StatusForbidden)
	expect(do(kid2, http.MethodPost, claimURL, "", nil), http.StatusConflict)
	expect(do(kid1, http.MethodPost, "/api/v1/bonus-tasks/"+ids.New()+"/claim", "", nil), http.StatusNotFound)

	expect(do(kid1, http.MethodPost, "/api/v1/bonus-tasks/"+task.ID+"/complete", "", map[string]string{"If-Match": "9"}),
		http.StatusPreconditionFailed)
	rec = do(kid1, http.MethodPost, "/api/v1/bonus-tasks/"+task.ID+"/complete", "", nil)
	expect(rec, http.StatusOK)
	var claim Claim
	if err := json.Unmarshal(rec.Body.Bytes(), &claim); err != nil {
		t.Fatalf("decode claim: %v", err)
	}
	if claim.Status != StatusSubmitted {
		t.Fatalf("claim status = %q, want submitted", claim.Status)
	}

	approveURL := "/api/v1/bonus-claims/" + claim.ID + "/approve"
	expect(do(e.parent, http.MethodPost, approveURL, "", map[string]string{"If-Match": "9"}), http.StatusPreconditionFailed)
	expect(do(e.parent, http.MethodPost, approveURL, "", nil), http.StatusOK)
	if got := balance(t, e, kid1.MemberID); got != 10 {
		t.Fatalf("balance after approve = %d, want 10", got)
	}

	expect(do(e.parent, http.MethodGet, "/api/v1/bonus-tasks", "", nil), http.StatusOK)
	expect(do(kid1, http.MethodGet, "/api/v1/bonus-tasks", "", nil), http.StatusOK)

	expect(do(kid1, http.MethodGet, "/api/v1/bonus-tasks?scope=scheduled", "", nil), http.StatusForbidden)
	expect(do(e.parent, http.MethodGet, "/api/v1/bonus-tasks?scope=weekly", "", nil), http.StatusBadRequest)
	expect(do(e.parent, http.MethodGet, "/api/v1/bonus-tasks?scope=past", "", nil), http.StatusOK)

	rec = do(e.parent, http.MethodPost, "/api/v1/bonus-tasks",
		`{"title":"Feed the cat","points":5,"day":"2026-10-12","eligible_member_ids":["`+kid1.MemberID+`"]}`, nil)
	expect(rec, http.StatusCreated)
	if got := rec.Header().Get("ETag"); got != `"1"` {
		t.Fatalf("create ETag = %q, want \"1\"", got)
	}
	var later Task
	if err := json.Unmarshal(rec.Body.Bytes(), &later); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	editURL := "/api/v1/bonus-tasks/" + later.ID
	expect(do(e.parent, http.MethodPatch, editURL, `{"title":"Feed the cat twice"}`,
		map[string]string{"If-Match": "9"}), http.StatusPreconditionFailed)
	expect(do(e.parent, http.MethodPatch, editURL, `{"colour":"red"}`, nil), http.StatusBadRequest)
	rec = do(e.parent, http.MethodPatch, editURL, `{"title":"Feed the cat twice"}`,
		map[string]string{"If-Match": "1"})
	expect(rec, http.StatusOK)
	if got := rec.Header().Get("ETag"); got != `"2"` {
		t.Fatalf("edit ETag = %q, want \"2\"", got)
	}

	cancelURL := editURL + "/cancel"
	expect(do(e.parent, http.MethodPost, cancelURL, "", map[string]string{"If-Match": "1"}), http.StatusPreconditionFailed)
	expect(do(e.parent, http.MethodPost, cancelURL, "", nil), http.StatusOK)
	expect(do(e.parent, http.MethodPost, cancelURL, "", nil), http.StatusConflict)
	expect(do(kid1, http.MethodPost, "/api/v1/bonus-tasks/"+later.ID+"/claim", "", nil), http.StatusConflict)
	expect(do(e.parent, http.MethodPost, "/api/v1/bonus-tasks/"+task.ID+"/cancel", "", nil), http.StatusConflict)

	expect(do(e.parent, http.MethodPost, "/api/v1/bonus-claims/"+claim.ID+"/unassign", "", nil), http.StatusConflict)
}
