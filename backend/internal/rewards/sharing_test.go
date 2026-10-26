package rewards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"nestwall/backend/internal/platform/clock"
)

// enableSharing saves a policy with sharing turned on and returns the stored policy.
func enableSharing(t *testing.T, f catFixture, s ShareSettings) ShareSettings {
	t.Helper()
	s.Enabled = true
	saved, err := SaveShareSettings(context.Background(), f.db, clock.FakeClock{Time: catNow}, f.fam.FamilyID, f.parent.userID, s, -1)
	if err != nil {
		t.Fatalf("save settings: %v", err)
	}
	return saved
}

// shareAt sends points as the given child at the given time.
func (f catFixture) shareAt(at time.Time, from, to catActor, amount int, key string) (ShareResult, error) {
	return SendShare(context.Background(), f.db, clock.FakeClock{Time: at}, f.fam.FamilyID, from.memberID, from.userID,
		ShareRequest{ToMemberID: to.memberID, Amount: amount}, key)
}

// countRows runs a COUNT query with the given arguments and returns the result.
func countRows(t *testing.T, f catFixture, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// withShareRoutes adds the sharing routes to the fixture's mux.
func withShareRoutes(t *testing.T, f catFixture) catFixture {
	t.Helper()
	h := NewHandlers(f.db, clock.FakeClock{Time: catNow}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/points/share", h.GetShare)
	mux.HandleFunc("PUT /api/v1/points/share/settings", h.PutShareSettings)
	mux.HandleFunc("POST /api/v1/points/share", h.PostShare)
	mux.HandleFunc("GET /api/v1/point-shares", h.ListPendingShares)
	mux.HandleFunc("POST /api/v1/point-shares/{id}/approve", h.ApprovePendingShare)
	mux.HandleFunc("POST /api/v1/point-shares/{id}/decline", h.DeclinePendingShare)
	f.mux = mux
	return f
}

func TestShareDisabledByDefault(t *testing.T) {
	f := newCatFixture(t)
	ana := f.addChild(f.fam.FamilyID, "Ana")
	bo := f.addChild(f.fam.FamilyID, "Bo")
	fund(t, f, ana.memberID, 100)

	if _, err := f.shareAt(catNow, ana, bo, 10, "s1"); !errors.Is(err, ErrSharingDisabled) {
		t.Fatalf("err = %v, want ErrSharingDisabled", err)
	}
	if got := balanceOf(t, f, ana.memberID); got != 100 {
		t.Fatalf("sender balance = %d, want 100", got)
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM point_shares WHERE family_id = ?`, f.fam.FamilyID); n != 0 {
		t.Fatalf("shares = %d, want 0", n)
	}
}

func TestShareCompletesAndPostsBothLegs(t *testing.T) {
	f := newCatFixture(t)
	enableSharing(t, f, defaultShareSettings())
	ana := f.addChild(f.fam.FamilyID, "Ana")
	bo := f.addChild(f.fam.FamilyID, "Bo")
	fund(t, f, ana.memberID, 100)

	res, err := f.shareAt(catNow, ana, bo, 20, "s1")
	if err != nil {
		t.Fatalf("share: %v", err)
	}
	if res.Status != shareCompleted || res.ApprovalRequired || res.Replayed || res.Balance != 80 {
		t.Fatalf("result = %+v, want completed, no approval, balance 80", res)
	}
	if got := balanceOf(t, f, bo.memberID); got != 20 {
		t.Fatalf("recipient balance = %d, want 20", got)
	}

	// The two ledger legs share one transfer_id.
	var legs, transfers int
	if err := f.db.QueryRow(
		`SELECT COUNT(*), COUNT(DISTINCT transfer_id) FROM points_ledger
		 WHERE family_id = ? AND reference_type = 'point_share' AND reference_id = ?`,
		f.fam.FamilyID, res.ID,
	).Scan(&legs, &transfers); err != nil {
		t.Fatalf("count legs: %v", err)
	}
	if legs != 2 || transfers != 1 {
		t.Fatalf("legs = %d, distinct transfer ids = %d; want 2 and 1", legs, transfers)
	}

	if n := countRows(t, f,
		`SELECT COUNT(*) FROM notifications WHERE family_id = ? AND recipient_id = ? AND kind = ?`,
		f.fam.FamilyID, bo.memberID, kindShareCompleted); n != 1 {
		t.Fatalf("recipient notifications = %d, want 1", n)
	}
	if n := countRows(t, f,
		`SELECT COUNT(*) FROM audit_logs WHERE family_id = ? AND action = 'points.shared' AND target_id = ?`,
		f.fam.FamilyID, res.ID); n != 1 {
		t.Fatalf("audit rows = %d, want 1", n)
	}
}

func TestShareParentCannotSend(t *testing.T) {
	f := newCatFixture(t)
	enableSharing(t, f, defaultShareSettings())
	bo := f.addChild(f.fam.FamilyID, "Bo")
	fund(t, f, f.parent.memberID, 100)

	if _, err := f.shareAt(catNow, f.parent, bo, 10, "s1"); !errors.Is(err, ErrShareChildrenOnly) {
		t.Fatalf("err = %v, want ErrShareChildrenOnly", err)
	}
}

func TestShareStatusListsOnlySiblings(t *testing.T) {
	f := newCatFixture(t)
	ana := f.addChild(f.fam.FamilyID, "Ana")
	bo := f.addChild(f.fam.FamilyID, "Bo")

	sum, err := ShareStatus(context.Background(), f.db, clock.FakeClock{Time: catNow}, f.fam.FamilyID, ana.memberID)
	if err != nil {
		t.Fatalf("share status: %v", err)
	}
	if len(sum.Recipients) != 1 || sum.Recipients[0].MemberID != bo.memberID || sum.Recipients[0].Name != "Bo" {
		t.Fatalf("recipients = %+v, want only Bo", sum.Recipients)
	}
}

func TestShareAmountLimits(t *testing.T) {
	f := newCatFixture(t)
	enableSharing(t, f, defaultShareSettings())
	ana := f.addChild(f.fam.FamilyID, "Ana")
	bo := f.addChild(f.fam.FamilyID, "Bo")
	fund(t, f, ana.memberID, 100)

	for _, amount := range []int{0, 31} {
		_, err := f.shareAt(catNow, ana, bo, amount, fmt.Sprintf("a%d", amount))
		var amountErr *ShareAmountError
		if !errors.As(err, &amountErr) || amountErr.Max != 30 {
			t.Fatalf("amount %d: err = %v, want ShareAmountError with max 30", amount, err)
		}
	}
}

func TestShareDailyLimitRollsOver(t *testing.T) {
	f := newCatFixture(t)
	enableSharing(t, f, defaultShareSettings()) // daily limit 50, max 30
	ana := f.addChild(f.fam.FamilyID, "Ana")
	bo := f.addChild(f.fam.FamilyID, "Bo")
	fund(t, f, ana.memberID, 200)

	if _, err := f.shareAt(catNow, ana, bo, 30, "d1"); err != nil {
		t.Fatalf("share 30: %v", err)
	}
	_, err := f.shareAt(catNow, ana, bo, 25, "d2")
	var dailyErr *ShareDailyLimitError
	if !errors.As(err, &dailyErr) || dailyErr.Remaining != 20 {
		t.Fatalf("share 25: err = %v, want ShareDailyLimitError with 20 left", err)
	}
	if _, err := f.shareAt(catNow, ana, bo, 20, "d3"); err != nil {
		t.Fatalf("share 20 up to the limit: %v", err)
	}
	// The next family day starts a fresh allowance.
	if _, err := f.shareAt(catNow.Add(24*time.Hour), ana, bo, 25, "d4"); err != nil {
		t.Fatalf("share on the next day: %v", err)
	}
}

func TestShareMinimumBalance(t *testing.T) {
	f := newCatFixture(t)
	enableSharing(t, f, defaultShareSettings()) // minimum balance 10
	ana := f.addChild(f.fam.FamilyID, "Ana")
	bo := f.addChild(f.fam.FamilyID, "Bo")
	fund(t, f, ana.memberID, 40)

	if _, err := f.shareAt(catNow, ana, bo, 30, "m1"); err != nil {
		t.Fatalf("share 30: %v", err)
	}
	_, err := f.shareAt(catNow, ana, bo, 5, "m2") // 5 is the least a share may be, so the balance rule is what refuses it
	var minErr *ShareBelowMinimumError
	if !errors.As(err, &minErr) || minErr.Balance != 10 || minErr.Minimum != 10 {
		t.Fatalf("share 5: err = %v, want ShareBelowMinimumError {10, 10}", err)
	}
	if got := balanceOf(t, f, ana.memberID); got != 10 {
		t.Fatalf("sender balance = %d, want 10", got)
	}
}

func TestShareReplayAndIdempotencyConflict(t *testing.T) {
	f := newCatFixture(t)
	enableSharing(t, f, defaultShareSettings())
	ana := f.addChild(f.fam.FamilyID, "Ana")
	bo := f.addChild(f.fam.FamilyID, "Bo")
	cy := f.addChild(f.fam.FamilyID, "Cy")
	fund(t, f, ana.memberID, 100)

	first, err := f.shareAt(catNow, ana, bo, 20, "r1")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := f.shareAt(catNow, ana, bo, 20, "r1")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !second.Replayed || second.ID != first.ID {
		t.Fatalf("replay = %+v, want Replayed with id %s", second, first.ID)
	}
	if got := balanceOf(t, f, ana.memberID); got != 80 {
		t.Fatalf("sender balance = %d after replay, want 80", got)
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM point_shares WHERE family_id = ?`, f.fam.FamilyID); n != 1 {
		t.Fatalf("shares = %d, want 1", n)
	}

	if _, err := f.shareAt(catNow, ana, bo, 21, "r1"); !errors.Is(err, ErrShareIdemConflict) {
		t.Fatalf("different amount: err = %v, want ErrShareIdemConflict", err)
	}
	if _, err := f.shareAt(catNow, ana, cy, 20, "r1"); !errors.Is(err, ErrShareIdemConflict) {
		t.Fatalf("different recipient: err = %v, want ErrShareIdemConflict", err)
	}
}

func TestShareRecipientRules(t *testing.T) {
	f := newCatFixture(t)
	enableSharing(t, f, defaultShareSettings())
	ana := f.addChild(f.fam.FamilyID, "Ana")
	fund(t, f, ana.memberID, 100)
	foreign := f.addChild(f.other.FamilyID, "Zed")

	cases := []struct {
		name string
		to   catActor
		want error
	}{
		{"self", ana, ErrShareRecipientInvalid},
		{"parent", f.parent, ErrShareRecipientInvalid},
		{"other family child", foreign, ErrShareRecipientMissing},
		{"unknown id", catActor{memberID: "nope"}, ErrShareRecipientMissing},
	}
	for i, c := range cases {
		_, err := f.shareAt(catNow, ana, c.to, 5, fmt.Sprintf("rc%d", i))
		if !errors.Is(err, c.want) {
			t.Fatalf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
	if got := balanceOf(t, f, ana.memberID); got != 100 {
		t.Fatalf("sender balance = %d after refused shares, want 100", got)
	}
}

func TestShareApprovalHoldAndApprove(t *testing.T) {
	f := newCatFixture(t)
	s := defaultShareSettings()
	s.ApprovalMode = "always"
	enableSharing(t, f, s)
	ana := f.addChild(f.fam.FamilyID, "Ana")
	bo := f.addChild(f.fam.FamilyID, "Bo")
	fund(t, f, ana.memberID, 100)

	res, err := f.shareAt(catNow, ana, bo, 10, "p1")
	if err != nil {
		t.Fatalf("share: %v", err)
	}
	if res.Status != statusPending || !res.ApprovalRequired || res.Balance != 90 || res.Version != 1 {
		t.Fatalf("result = %+v, want pending, approval required, balance 90, version 1", res)
	}
	if got := balanceOf(t, f, bo.memberID); got != 0 {
		t.Fatalf("recipient balance = %d before approval, want 0", got)
	}
	if n := countRows(t, f,
		`SELECT COUNT(*) FROM notifications WHERE family_id = ? AND recipient_id = ? AND kind = ?`,
		f.fam.FamilyID, f.parent.memberID, kindSharePending); n != 1 {
		t.Fatalf("parent pending notifications = %d, want 1", n)
	}

	if _, err := ApproveShare(context.Background(), f.db, clock.FakeClock{Time: catNow}, f.fam.FamilyID,
		res.ID, f.parent.userID, f.parent.memberID, 5); !errors.Is(err, errStaleVersion) {
		t.Fatalf("stale approve: err = %v, want errStaleVersion", err)
	}

	done, err := ApproveShare(context.Background(), f.db, clock.FakeClock{Time: catNow}, f.fam.FamilyID,
		res.ID, f.parent.userID, f.parent.memberID, 1)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if done.Status != shareCompleted || done.DecidedByMemberID == nil || *done.DecidedByMemberID != f.parent.memberID {
		t.Fatalf("approved = %+v, want completed and decided by the parent", done)
	}
	if got := balanceOf(t, f, bo.memberID); got != 10 {
		t.Fatalf("recipient balance = %d after approval, want 10", got)
	}
	if got := balanceOf(t, f, ana.memberID); got != 90 {
		t.Fatalf("sender balance = %d after approval, want 90", got)
	}
	if n := countRows(t, f,
		`SELECT COUNT(*) FROM notifications WHERE family_id = ? AND recipient_id = ? AND kind = ?`,
		f.fam.FamilyID, bo.memberID, kindShareCompleted); n != 1 {
		t.Fatalf("recipient notifications = %d, want 1", n)
	}

	if _, err := ApproveShare(context.Background(), f.db, clock.FakeClock{Time: catNow}, f.fam.FamilyID,
		res.ID, f.parent.userID, f.parent.memberID, -1); !errors.Is(err, ErrNotPending) {
		t.Fatalf("approve twice: err = %v, want ErrNotPending", err)
	}
	if _, err := DeclineShare(context.Background(), f.db, clock.FakeClock{Time: catNow}, f.fam.FamilyID,
		res.ID, f.parent.userID, f.parent.memberID, -1); !errors.Is(err, ErrNotPending) {
		t.Fatalf("decline after approve: err = %v, want ErrNotPending", err)
	}
}

func TestShareApprovalAboveThreshold(t *testing.T) {
	f := newCatFixture(t)
	s := defaultShareSettings()
	s.ApprovalMode = "above"
	s.ApprovalAbove = 20
	enableSharing(t, f, s)
	ana := f.addChild(f.fam.FamilyID, "Ana")
	bo := f.addChild(f.fam.FamilyID, "Bo")
	fund(t, f, ana.memberID, 100)

	at20, err := f.shareAt(catNow, ana, bo, 20, "t20")
	if err != nil {
		t.Fatalf("share 20: %v", err)
	}
	if at20.Status != shareCompleted {
		t.Fatalf("share 20 status = %s, want completed", at20.Status)
	}
	at21, err := f.shareAt(catNow, ana, bo, 21, "t21")
	if err != nil {
		t.Fatalf("share 21: %v", err)
	}
	if at21.Status != statusPending {
		t.Fatalf("share 21 status = %s, want pending", at21.Status)
	}
}

func TestShareDeclineRefundsSender(t *testing.T) {
	f := newCatFixture(t)
	s := defaultShareSettings()
	s.ApprovalMode = "always"
	enableSharing(t, f, s)
	ana := f.addChild(f.fam.FamilyID, "Ana")
	bo := f.addChild(f.fam.FamilyID, "Bo")
	fund(t, f, ana.memberID, 100)

	res, err := f.shareAt(catNow, ana, bo, 15, "x1")
	if err != nil {
		t.Fatalf("share: %v", err)
	}
	dec, err := DeclineShare(context.Background(), f.db, clock.FakeClock{Time: catNow}, f.fam.FamilyID,
		res.ID, f.parent.userID, f.parent.memberID, -1)
	if err != nil {
		t.Fatalf("decline: %v", err)
	}
	if dec.Status != shareDeclined || dec.Balance != 100 {
		t.Fatalf("declined = %+v, want declined with balance 100", dec)
	}
	if got := balanceOf(t, f, ana.memberID); got != 100 {
		t.Fatalf("sender balance = %d after decline, want 100", got)
	}
	if got := balanceOf(t, f, bo.memberID); got != 0 {
		t.Fatalf("recipient balance = %d after decline, want 0", got)
	}
	if n := countRows(t, f,
		`SELECT COUNT(*) FROM notifications WHERE family_id = ? AND recipient_id = ? AND kind = ?`,
		f.fam.FamilyID, ana.memberID, kindShareDeclined); n != 1 {
		t.Fatalf("sender notifications = %d, want 1", n)
	}
}

func TestShareSettingsVersion(t *testing.T) {
	f := newCatFixture(t)
	ctx := context.Background()
	clk := clock.FakeClock{Time: catNow}
	s := defaultShareSettings()
	s.Enabled = true

	if got := mustShareStatus(t, f).Version; got != 0 {
		t.Fatalf("version with no row = %d, want 0", got)
	}
	first, err := SaveShareSettings(ctx, f.db, clk, f.fam.FamilyID, f.parent.userID, s, 0)
	if err != nil || first.Version != 1 {
		t.Fatalf("first save = %+v, %v; want version 1", first, err)
	}
	stale, err := SaveShareSettings(ctx, f.db, clk, f.fam.FamilyID, f.parent.userID, s, 0)
	if !errors.Is(err, errStaleVersion) || stale.Version != 1 {
		t.Fatalf("stale save: err = %v, version = %d; want errStaleVersion and 1", err, stale.Version)
	}
	second, err := SaveShareSettings(ctx, f.db, clk, f.fam.FamilyID, f.parent.userID, s, 1)
	if err != nil || second.Version != 2 {
		t.Fatalf("second save = %+v, %v; want version 2", second, err)
	}
	if got := mustShareStatus(t, f).Version; got != 2 {
		t.Fatalf("status version = %d, want 2", got)
	}
}

func mustShareStatus(t *testing.T, f catFixture) ShareSummary {
	t.Helper()
	sum, err := ShareStatus(context.Background(), f.db, clock.FakeClock{Time: catNow}, f.fam.FamilyID, f.parent.memberID)
	if err != nil {
		t.Fatalf("share status: %v", err)
	}
	return sum
}

func TestShareSettingsCheck(t *testing.T) {
	if err := defaultShareSettings().check(); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	cases := []struct {
		name  string
		field string
		edit  func(*ShareSettings)
	}{
		{"max zero", "max_per_share", func(s *ShareSettings) { s.MaxPerShare = 0 }},
		{"min zero", "min_per_share", func(s *ShareSettings) { s.MinPerShare = 0 }},
		{"min above max", "min_per_share", func(s *ShareSettings) { s.MinPerShare = 31 }},
		{"daily over", "daily_limit", func(s *ShareSettings) { s.DailyLimit = maxPoints + 1 }},
		{"min negative", "min_balance", func(s *ShareSettings) { s.MinBalance = -1 }},
		{"unknown mode", "approval_mode", func(s *ShareSettings) { s.ApprovalMode = "maybe" }},
		{"above negative", "approval_above", func(s *ShareSettings) { s.ApprovalAbove = -1 }},
	}
	for _, c := range cases {
		s := defaultShareSettings()
		c.edit(&s)
		err := s.check()
		if err == nil || err.field != c.field {
			t.Fatalf("%s: check() = %+v, want field %s", c.name, err, c.field)
		}
	}
}

func TestShareConcurrencyCannotOverspend(t *testing.T) {
	f := newCatFixture(t)
	s := defaultShareSettings() // minimum balance 10, max 30
	s.DailyLimit = 1000
	enableSharing(t, f, s)
	ana := f.addChild(f.fam.FamilyID, "Ana")
	bo := f.addChild(f.fam.FamilyID, "Bo")
	fund(t, f, ana.memberID, 100)

	const n = 8
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = f.shareAt(catNow, ana, bo, 20, fmt.Sprintf("race-%d", i))
		}(i)
	}
	wg.Wait()

	succeeded, refused := 0, 0
	for _, err := range errs {
		var minErr *ShareBelowMinimumError
		switch {
		case err == nil:
			succeeded++
		case errors.As(err, &minErr):
			refused++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	// 100 points, minimum 10, 20 per share: four shares fit (100 to 20), a fifth would leave 0.
	if succeeded != 4 || refused != 4 {
		t.Fatalf("succeeded = %d, refused = %d; want 4 and 4", succeeded, refused)
	}
	if got := balanceOf(t, f, ana.memberID); got != 20 {
		t.Fatalf("sender balance = %d, want 20", got)
	}
	if got := balanceOf(t, f, bo.memberID); got != 80 {
		t.Fatalf("recipient balance = %d, want 80", got)
	}
}

func TestShareHTTP(t *testing.T) {
	f := withShareRoutes(t, newCatFixture(t))
	enableSharing(t, f, defaultShareSettings())
	ana := f.addChild(f.fam.FamilyID, "Ana")
	bo := f.addChild(f.fam.FamilyID, "Bo")
	fund(t, f, ana.memberID, 100)

	const path = "/api/v1/points/share"
	body := fmt.Sprintf(`{"to_member_id":%q,"amount":10}`, bo.memberID)

	rec := f.do(ana, http.MethodPost, path, body, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("no key: status %d, want 400", rec.Code)
	}
	rec = f.do(f.parent, http.MethodPost, path, body, map[string]string{"Idempotency-Key": "h0"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("parent sender: status %d, want 403", rec.Code)
	}
	rec = f.do(ana, http.MethodPost, path, fmt.Sprintf(`{"to_member_id":%q,"amount":31}`, bo.memberID),
		map[string]string{"Idempotency-Key": "h1"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("amount over max: status %d, want 400", rec.Code)
	}
	rec = f.do(ana, http.MethodPost, path, fmt.Sprintf(`{"to_member_id":%q,"amount":10,"note_key":"nope"}`, bo.memberID),
		map[string]string{"Idempotency-Key": "h2"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown note: status %d, want 400", rec.Code)
	}

	rec = f.do(ana, http.MethodPost, path, body, map[string]string{"Idempotency-Key": "h3"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("share: status %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var created ShareResult
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode share: %v", err)
	}
	if created.Balance != 90 || created.Status != shareCompleted {
		t.Fatalf("created = %+v, want balance 90 and completed", created)
	}
	rec = f.do(ana, http.MethodPost, path, body, map[string]string{"Idempotency-Key": "h3"})
	if rec.Code != http.StatusOK {
		t.Fatalf("replay: status %d, want 200", rec.Code)
	}
	rec = f.do(ana, http.MethodPost, path, fmt.Sprintf(`{"to_member_id":%q,"amount":11}`, bo.memberID),
		map[string]string{"Idempotency-Key": "h3"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("key reuse: status %d, want 409", rec.Code)
	}

	rec = f.do(ana, http.MethodGet, path, "", nil)
	if rec.Code != http.StatusOK || rec.Header().Get("ETag") != `"1"` {
		t.Fatalf("get status: %d, ETag %q; want 200 and \"1\"", rec.Code, rec.Header().Get("ETag"))
	}

	settings := `{"enabled":true,"min_per_share":5,"max_per_share":30,"daily_limit":50,"min_balance":10,"approval_mode":"never","approval_above":20,"notify_recipient":true,"notify_parents":false}`
	rec = f.do(f.parent, http.MethodPut, path+"/settings", settings, map[string]string{"If-Match": `"7"`})
	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale settings: status %d, want 412", rec.Code)
	}
	rec = f.do(f.parent, http.MethodPut, path+"/settings", settings, map[string]string{"If-Match": "abc"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed If-Match: status %d, want 400", rec.Code)
	}
	rec = f.do(f.parent, http.MethodPut, path+"/settings",
		`{"enabled":true,"max_per_share":0,"daily_limit":50,"min_balance":10,"approval_mode":"never","approval_above":20,"notify_recipient":true,"notify_parents":false}`,
		map[string]string{"If-Match": `"1"`})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid settings: status %d, want 400", rec.Code)
	}
	rec = f.do(f.parent, http.MethodPut, path+"/settings", settings, map[string]string{"If-Match": `"1"`})
	if rec.Code != http.StatusOK || rec.Header().Get("ETag") != `"2"` {
		t.Fatalf("settings: status %d, ETag %q; want 200 and \"2\"", rec.Code, rec.Header().Get("ETag"))
	}

	rec = f.do(f.parent, http.MethodPost, "/api/v1/point-shares/nope/approve", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("approve unknown: status %d, want 404", rec.Code)
	}
	rec = f.do(f.parent, http.MethodGet, "/api/v1/point-shares", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list pending: status %d, want 200", rec.Code)
	}
}
