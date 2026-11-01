package rewards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
)

// fund credits a child (or parent) with points through the ledger.
func fund(t *testing.T, f catFixture, memberID string, amount int) {
	t.Helper()
	if _, err := postEntry(t, f.db, Entry{
		FamilyID:       f.fam.FamilyID,
		MemberID:       memberID,
		Type:           Bonus,
		Amount:         amount,
		IdempotencyKey: "fund:" + ids.New(),
	}); err != nil {
		t.Fatalf("fund: %v", err)
	}
}

func balanceOf(t *testing.T, f catFixture, memberID string) int {
	t.Helper()
	bal, err := Balance(context.Background(), f.db, f.fam.FamilyID, memberID)
	if err != nil {
		t.Fatalf("Balance: %v", err)
	}
	return bal
}

func countRedemptions(t *testing.T, f catFixture) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM redemptions WHERE family_id = ?`, f.fam.FamilyID).Scan(&n); err != nil {
		t.Fatalf("count redemptions: %v", err)
	}
	return n
}

func countLedgerType(t *testing.T, f catFixture, memberID string, entryType EntryType) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(
		`SELECT COUNT(*) FROM points_ledger WHERE family_id = ? AND family_member_id = ? AND entry_type = ?`,
		f.fam.FamilyID, memberID, string(entryType),
	).Scan(&n); err != nil {
		t.Fatalf("count ledger: %v", err)
	}
	return n
}

// redeemAt requests a reward as the given actor at the given time.
func (f catFixture) redeemAt(at time.Time, a catActor, rewardID, key string) (RedemptionResult, error) {
	return Redeem(context.Background(), f.db, clock.FakeClock{Time: at}, f.fam.FamilyID, a.memberID, a.userID, rewardID, key)
}

func (f catFixture) approve(a catActor, id string, expected int) (RewardRedemption, error) {
	res, err := Approve(context.Background(), f.db, clock.FakeClock{Time: catNow}, f.fam.FamilyID, id, a.userID, a.memberID, expected)
	return res.RewardRedemption, err
}

func (f catFixture) reject(a catActor, id, reason string, expected int) (RedemptionResult, error) {
	return Reject(context.Background(), f.db, clock.FakeClock{Time: catNow}, f.fam.FamilyID, id, a.userID, a.memberID, reason, expected)
}

func (f catFixture) cancel(a catActor, id string, expected int) (RedemptionResult, error) {
	return Cancel(context.Background(), f.db, clock.FakeClock{Time: catNow}, f.fam.FamilyID, id, a.userID, a.memberID, expected)
}

func TestRedeemCreatesPendingAndHoldsPoints(t *testing.T) {
	f := newCatFixture(t)
	child := f.addChild(f.fam.FamilyID, "Ana")
	fund(t, f, child.memberID, 100)
	rw := f.create(f.parent, `{"name":"Movie","cost":30,"approval_required":true}`)

	res, err := f.redeemAt(catNow, child, rw.ID, "k1")
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if res.Status != statusPending || res.DecidedAt != nil || res.Version != 1 {
		t.Fatalf("got status=%s decided=%v version=%d, want pending/nil/1", res.Status, res.DecidedAt, res.Version)
	}
	if res.Replayed {
		t.Fatal("first request must not be a replay")
	}
	if res.Balance != 70 {
		t.Fatalf("balance = %d, want 70", res.Balance)
	}
	if got := balanceOf(t, f, child.memberID); got != 70 {
		t.Fatalf("stored balance = %d, want 70", got)
	}
}

func TestRedeemAutoApprovesWithoutApprovalRule(t *testing.T) {
	f := newCatFixture(t)
	child := f.addChild(f.fam.FamilyID, "Ana")
	fund(t, f, child.memberID, 100)
	rw := f.create(f.parent, `{"name":"Stickers","cost":10}`)

	res, err := f.redeemAt(catNow, child, rw.ID, "k1")
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if res.Status != statusApproved || res.DecidedAt == nil {
		t.Fatalf("status=%s decided=%v, want approved with decided_at", res.Status, res.DecidedAt)
	}
	if got := balanceOf(t, f, child.memberID); got != 90 {
		t.Fatalf("balance = %d, want 90", got)
	}
}

func TestRedeemReplayChangesNothing(t *testing.T) {
	f := newCatFixture(t)
	child := f.addChild(f.fam.FamilyID, "Ana")
	fund(t, f, child.memberID, 100)
	rw := f.create(f.parent, `{"name":"Movie","cost":30,"approval_required":true}`)

	first, err := f.redeemAt(catNow, child, rw.ID, "same")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := f.redeemAt(catNow, child, rw.ID, "same")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !second.Replayed || second.ID != first.ID {
		t.Fatalf("replay = %+v, want Replayed with id %s", second, first.ID)
	}
	if second.Balance != 70 {
		t.Fatalf("replay balance = %d, want 70", second.Balance)
	}
	if n := countRedemptions(t, f); n != 1 {
		t.Fatalf("redemption rows = %d, want 1", n)
	}
	if got := balanceOf(t, f, child.memberID); got != 70 {
		t.Fatalf("stored balance = %d, want 70", got)
	}
}

func TestRedeemKeyForAnotherRewardConflicts(t *testing.T) {
	f := newCatFixture(t)
	child := f.addChild(f.fam.FamilyID, "Ana")
	fund(t, f, child.memberID, 100)
	movie := f.create(f.parent, `{"name":"Movie","cost":10}`)
	park := f.create(f.parent, `{"name":"Park","cost":10}`)

	if _, err := f.redeemAt(catNow, child, movie.ID, "shared"); err != nil {
		t.Fatalf("first: %v", err)
	}
	_, err := f.redeemAt(catNow, child, park.ID, "shared")
	if !errors.Is(err, ErrRedemptionIdemConflict) {
		t.Fatalf("err = %v, want ErrRedemptionIdemConflict", err)
	}
	if n := countRedemptions(t, f); n != 1 {
		t.Fatalf("redemption rows = %d, want 1", n)
	}
}

func TestRedeemInsufficientPointsRefused(t *testing.T) {
	f := newCatFixture(t)
	child := f.addChild(f.fam.FamilyID, "Ana")
	fund(t, f, child.memberID, 10)
	rw := f.create(f.parent, `{"name":"Movie","cost":30}`)

	_, err := f.redeemAt(catNow, child, rw.ID, "k1")
	var ins *InsufficientPointsError
	if !errors.As(err, &ins) {
		t.Fatalf("err = %v, want InsufficientPointsError", err)
	}
	if ins.Balance != 10 || ins.Cost != 30 {
		t.Fatalf("got balance=%d cost=%d, want 10 and 30", ins.Balance, ins.Cost)
	}
	if n := countRedemptions(t, f); n != 0 {
		t.Fatalf("redemption rows = %d, want 0", n)
	}
	if got := balanceOf(t, f, child.memberID); got != 10 {
		t.Fatalf("balance = %d, want 10", got)
	}
}

// Eight concurrent requests for 60 points from a 100-point balance must
// produce exactly one success. The write lock taken at BEGIN serialises the
// balance check and the insert.
func TestRedeemConcurrentNoDoubleSpend(t *testing.T) {
	f := newCatFixture(t)
	child := f.addChild(f.fam.FamilyID, "Ana")
	fund(t, f, child.memberID, 100)
	rw := f.create(f.parent, `{"name":"Cinema","cost":60}`)

	const attempts = 8
	var wg sync.WaitGroup
	errs := make(chan error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := f.redeemAt(catNow, child, rw.ID, fmt.Sprintf("race-%d", i))
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)

	var ok, short int
	for err := range errs {
		var ins *InsufficientPointsError
		switch {
		case err == nil:
			ok++
		case errors.As(err, &ins):
			short++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != 1 || short != attempts-1 {
		t.Fatalf("successes=%d insufficient=%d, want 1 and %d", ok, short, attempts-1)
	}
	if got := balanceOf(t, f, child.memberID); got != 40 {
		t.Fatalf("balance = %d, want 40", got)
	}
	if n := countRedemptions(t, f); n != 1 {
		t.Fatalf("redemption rows = %d, want 1", n)
	}
}

func TestRedeemOutsideAvailableDaysRefused(t *testing.T) {
	f := newCatFixture(t)
	child := f.addChild(f.fam.FamilyID, "Ana")
	fund(t, f, child.memberID, 100)
	// catNow is a Saturday.
	rw := f.create(f.parent, `{"name":"Weekday treat","cost":5,"available_days":["mon"]}`)

	_, err := f.redeemAt(catNow, child, rw.ID, "k1")
	if !errors.Is(err, ErrOutsideWindow) {
		t.Fatalf("err = %v, want ErrOutsideWindow", err)
	}
	if n := countRedemptions(t, f); n != 0 {
		t.Fatalf("redemption rows = %d, want 0", n)
	}
}

func TestRedeemDailyLimitCountsOnlyLiveRequests(t *testing.T) {
	f := newCatFixture(t)
	child := f.addChild(f.fam.FamilyID, "Ana")
	fund(t, f, child.memberID, 100)
	// Approval is required so the first request is still pending and can be cancelled.
	rw := f.create(f.parent, `{"name":"Treat","cost":5,"limit_period":"day","limit_count":1,"approval_required":true}`)

	first, err := f.redeemAt(catNow, child, rw.ID, "d1")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := f.redeemAt(catNow.Add(time.Hour), child, rw.ID, "d2"); !errors.Is(err, ErrLimitReached) {
		t.Fatalf("second same day: err = %v, want ErrLimitReached", err)
	}

	// A cancelled request does not count towards the limit.
	if _, err := f.cancel(child, first.ID, 1); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := f.redeemAt(catNow.Add(2*time.Hour), child, rw.ID, "d3"); err != nil {
		t.Fatalf("after cancel: %v", err)
	}
	if _, err := f.redeemAt(catNow.Add(3*time.Hour), child, rw.ID, "d4"); !errors.Is(err, ErrLimitReached) {
		t.Fatalf("again same day: err = %v, want ErrLimitReached", err)
	}

	// The next day the limit resets.
	if _, err := f.redeemAt(catNow.Add(24*time.Hour), child, rw.ID, "d5"); err != nil {
		t.Fatalf("next day: %v", err)
	}
}

func TestRedeemMonthlyLimitResetsOnFirstOfMonth(t *testing.T) {
	f := newCatFixture(t)
	child := f.addChild(f.fam.FamilyID, "Ana")
	fund(t, f, child.memberID, 100)
	rw := f.create(f.parent, `{"name":"Trip treat","cost":5,"limit_period":"month","limit_count":1}`)

	if _, err := f.redeemAt(time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC), child, rw.ID, "m1"); err != nil {
		t.Fatalf("first in October: %v", err)
	}
	if _, err := f.redeemAt(time.Date(2026, 10, 31, 23, 0, 0, 0, time.UTC), child, rw.ID, "m2"); !errors.Is(err, ErrLimitReached) {
		t.Fatalf("last evening of October: err = %v, want ErrLimitReached", err)
	}
	if _, err := f.redeemAt(time.Date(2026, 11, 1, 0, 30, 0, 0, time.UTC), child, rw.ID, "m3"); err != nil {
		t.Fatalf("first minute of November: %v", err)
	}
	if _, err := f.redeemAt(time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC), child, rw.ID, "m4"); !errors.Is(err, ErrLimitReached) {
		t.Fatalf("mid November: err = %v, want ErrLimitReached", err)
	}
}

func TestPeriodStartMonthUsesFamilyZone(t *testing.T) {
	auckland, err := time.LoadLocation("Pacific/Auckland")
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}
	// 2026-11-01 00:30 UTC is already 1 November in Auckland, so the month starts 1 November there.
	local := time.Date(2026, 11, 1, 0, 30, 0, 0, time.UTC).In(auckland)
	want := time.Date(2026, 11, 1, 0, 0, 0, 0, auckland)
	if got := periodStart("month", local); !got.Equal(want) {
		t.Fatalf("periodStart(month) = %v, want %v", got, want)
	}
	// Mid-month days map back to the first of the same month.
	mid := time.Date(2026, 2, 17, 9, 0, 0, 0, time.UTC)
	if got := periodStart("month", mid); !got.Equal(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("periodStart(month, mid-Feb) = %v", got)
	}
}

func TestApproveMovesPendingToApproved(t *testing.T) {
	f := newCatFixture(t)
	child := f.addChild(f.fam.FamilyID, "Ana")
	fund(t, f, child.memberID, 100)
	rw := f.create(f.parent, `{"name":"Trip","cost":40,"approval_required":true}`)

	req, err := f.redeemAt(catNow, child, rw.ID, "k1")
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	got, err := f.approve(f.parent, req.ID, 1)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if got.Status != statusApproved || got.Version != 2 || got.DecidedAt == nil {
		t.Fatalf("approved = %+v, want approved, version 2, decided_at set", got)
	}
	if got.DecidedByMemberID == nil || *got.DecidedByMemberID != f.parent.memberID {
		t.Fatalf("decided_by = %v, want parent", got.DecidedByMemberID)
	}
	// Points were already held at request time; approval does not charge again.
	if bal := balanceOf(t, f, child.memberID); bal != 60 {
		t.Fatalf("balance = %d, want 60", bal)
	}
	if _, err := f.approve(f.parent, req.ID, 2); !errors.Is(err, ErrNotPending) {
		t.Fatalf("second approve: err = %v, want ErrNotPending", err)
	}
}

func TestRejectRequiresReasonAndRefunds(t *testing.T) {
	f := newCatFixture(t)
	child := f.addChild(f.fam.FamilyID, "Ana")
	fund(t, f, child.memberID, 100)
	rw := f.create(f.parent, `{"name":"Trip","cost":40,"approval_required":true}`)

	req, err := f.redeemAt(catNow, child, rw.ID, "k1")
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	res, err := f.reject(f.parent, req.ID, "Not this week", 1)
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if res.Status != statusRejected || res.DecisionReason == nil || *res.DecisionReason != "Not this week" {
		t.Fatalf("rejected = %+v, want rejected with reason", res.RewardRedemption)
	}
	if res.Balance != 100 {
		t.Fatalf("balance after reject = %d, want 100", res.Balance)
	}
	if n := countLedgerType(t, f, child.memberID, RedemptionReversal); n != 1 {
		t.Fatalf("reversal entries = %d, want 1", n)
	}
}

func TestParentCannotDecideOwnRedemption(t *testing.T) {
	f := newCatFixture(t)
	fund(t, f, f.parent.memberID, 100)
	rw := f.create(f.parent, `{"name":"Dinner out","cost":20,"approval_required":true}`)

	req, err := f.redeemAt(catNow, f.parent, rw.ID, "p1")
	if err != nil {
		t.Fatalf("parent redeem: %v", err)
	}
	if _, err := f.approve(f.parent, req.ID, 1); !errors.Is(err, ErrOwnRedemption) {
		t.Fatalf("approve own: err = %v, want ErrOwnRedemption", err)
	}
	if _, err := f.reject(f.parent, req.ID, "no", 1); !errors.Is(err, ErrOwnRedemption) {
		t.Fatalf("reject own: err = %v, want ErrOwnRedemption", err)
	}
}

func TestCancelOwnPendingOnly(t *testing.T) {
	f := newCatFixture(t)
	ana := f.addChild(f.fam.FamilyID, "Ana")
	ben := f.addChild(f.fam.FamilyID, "Ben")
	fund(t, f, ana.memberID, 100)
	rw := f.create(f.parent, `{"name":"Trip","cost":40,"approval_required":true}`)

	req, err := f.redeemAt(catNow, ana, rw.ID, "k1")
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if _, err := f.cancel(ben, req.ID, 1); !errors.Is(err, ErrRedemptionNotFound) {
		t.Fatalf("other child cancel: err = %v, want ErrRedemptionNotFound", err)
	}
	res, err := f.cancel(ana, req.ID, 1)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if res.Status != statusCancelled || res.Balance != 100 {
		t.Fatalf("cancelled = status %s balance %d, want cancelled and 100", res.Status, res.Balance)
	}
	if _, err := f.cancel(ana, req.ID, 2); !errors.Is(err, ErrNotPending) {
		t.Fatalf("cancel twice: err = %v, want ErrNotPending", err)
	}

	// Once approved, the request can no longer be cancelled by the child.
	again, err := f.redeemAt(catNow, ana, rw.ID, "k2")
	if err != nil {
		t.Fatalf("redeem again: %v", err)
	}
	if _, err := f.approve(f.parent, again.ID, 1); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := f.cancel(ana, again.ID, 2); !errors.Is(err, ErrNotPending) {
		t.Fatalf("cancel approved: err = %v, want ErrNotPending", err)
	}
}

func TestDecisionWithStaleVersionRefused(t *testing.T) {
	f := newCatFixture(t)
	child := f.addChild(f.fam.FamilyID, "Ana")
	fund(t, f, child.memberID, 100)
	rw := f.create(f.parent, `{"name":"Trip","cost":40,"approval_required":true}`)

	req, err := f.redeemAt(catNow, child, rw.ID, "k1")
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	got, err := f.approve(f.parent, req.ID, 5)
	if !errors.Is(err, errStaleVersion) {
		t.Fatalf("err = %v, want errStaleVersion", err)
	}
	if got.Version != 1 || got.Status != statusPending {
		t.Fatalf("current row = version %d status %s, want 1 pending", got.Version, got.Status)
	}
}

func TestIneligibleChildCannotSeeReward(t *testing.T) {
	f := newCatFixture(t)
	ana := f.addChild(f.fam.FamilyID, "Ana")
	ben := f.addChild(f.fam.FamilyID, "Ben")
	fund(t, f, ana.memberID, 100)
	fund(t, f, ben.memberID, 100)
	rw := f.create(f.parent, fmt.Sprintf(`{"name":"Solo","cost":5,"eligible_member_ids":["%s"]}`, ana.memberID))

	if _, err := f.redeemAt(catNow, ben, rw.ID, "k1"); !errors.Is(err, ErrRewardNotFound) {
		t.Fatalf("ben: err = %v, want ErrRewardNotFound", err)
	}
	if _, err := f.redeemAt(catNow, ana, rw.ID, "k2"); err != nil {
		t.Fatalf("ana: %v", err)
	}
}

// newRedeemMux wires the redemption routes against the fixture's database.
func newRedeemMux(f catFixture) *http.ServeMux {
	h := NewHandlers(f.db, clock.FakeClock{Time: catNow}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/rewards/{id}/redeem", h.RedeemReward)
	mux.HandleFunc("POST /api/v1/redemptions/{id}/approve", h.ApproveRedemption)
	mux.HandleFunc("POST /api/v1/redemptions/{id}/reject", h.RejectRedemption)
	mux.HandleFunc("POST /api/v1/redemptions/{id}/cancel", h.CancelRedemption)
	return mux
}

func sendAs(t *testing.T, mux *http.ServeMux, a catActor, method, target, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	ctx := context.WithValue(req.Context(), permissions.ContextKeyFamilyMemberID, a.memberID)
	ctx = context.WithValue(ctx, permissions.ContextKeyUserID, a.userID)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func TestRedemptionHTTPStatusCodes(t *testing.T) {
	f := newCatFixture(t)
	mux := newRedeemMux(f)
	child := f.addChild(f.fam.FamilyID, "Ana")
	fund(t, f, child.memberID, 50)
	rw := f.create(f.parent, `{"name":"Park","cost":30,"approval_required":true}`)
	redeemPath := "/api/v1/rewards/" + rw.ID + "/redeem"

	// Idempotency-Key is required.
	rec := sendAs(t, mux, child, http.MethodPost, redeemPath, "", nil)
	if rec.Code != http.StatusBadRequest || errorCode(rec) != "validation_failed" {
		t.Fatalf("missing key: %d %s, want 400 validation_failed", rec.Code, errorCode(rec))
	}

	// First request creates the redemption.
	rec = sendAs(t, mux, child, http.MethodPost, redeemPath, "", map[string]string{"Idempotency-Key": "h1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d, want 201", rec.Code)
	}
	var created RedemptionResult
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Replay returns 200 with the same resource.
	rec = sendAs(t, mux, child, http.MethodPost, redeemPath, "", map[string]string{"Idempotency-Key": "h1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("replay: %d, want 200", rec.Code)
	}

	// Only 20 points remain, so a second 30-point request is refused.
	rec = sendAs(t, mux, child, http.MethodPost, redeemPath, "", map[string]string{"Idempotency-Key": "h2"})
	if rec.Code != http.StatusConflict || errorCode(rec) != "insufficient_points" {
		t.Fatalf("insufficient: %d %s, want 409 insufficient_points", rec.Code, errorCode(rec))
	}

	approvePath := "/api/v1/redemptions/" + created.ID + "/approve"
	rejectPath := "/api/v1/redemptions/" + created.ID + "/reject"

	// A stale If-Match gives 412.
	rec = sendAs(t, mux, f.parent, http.MethodPost, approvePath, "", map[string]string{"If-Match": `"9"`})
	if rec.Code != http.StatusPreconditionFailed || errorCode(rec) != "version_conflict" {
		t.Fatalf("stale: %d %s, want 412 version_conflict", rec.Code, errorCode(rec))
	}

	// Reject needs a reason.
	rec = sendAs(t, mux, f.parent, http.MethodPost, rejectPath, `{"reason":"  "}`, map[string]string{"If-Match": `"1"`})
	if rec.Code != http.StatusBadRequest || errorCode(rec) != "validation_failed" {
		t.Fatalf("empty reason: %d %s, want 400 validation_failed", rec.Code, errorCode(rec))
	}

	// Parent approves with the current version.
	rec = sendAs(t, mux, f.parent, http.MethodPost, approvePath, "", map[string]string{"If-Match": `"1"`})
	if rec.Code != http.StatusOK {
		t.Fatalf("approve: %d, want 200", rec.Code)
	}

	// Child cannot cancel an approved request.
	rec = sendAs(t, mux, child, http.MethodPost, "/api/v1/redemptions/"+created.ID+"/cancel", "", map[string]string{"If-Match": `"2"`})
	if rec.Code != http.StatusConflict || errorCode(rec) != "not_pending" {
		t.Fatalf("cancel approved: %d %s, want 409 not_pending", rec.Code, errorCode(rec))
	}
}
