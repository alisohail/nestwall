package rewards

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

// The parent's range sets the least and most a child may share in one go.
func TestShareAmountRangeFromPolicy(t *testing.T) {
	f := newCatFixture(t)
	s := defaultShareSettings()
	s.MinPerShare = 8
	enableSharing(t, f, s)
	ana := f.addChild(f.fam.FamilyID, "Ana")
	bo := f.addChild(f.fam.FamilyID, "Bo")
	fund(t, f, ana.memberID, 100)

	_, err := f.shareAt(catNow, ana, bo, 7, "below")
	var amountErr *ShareAmountError
	if !errors.As(err, &amountErr) || amountErr.Min != 8 || amountErr.Max != 30 {
		t.Fatalf("share 7: err = %v, want ShareAmountError {8, 30}", err)
	}
	if _, err := f.shareAt(catNow, ana, bo, 8, "at-min"); err != nil {
		t.Fatalf("share 8 at the minimum: %v", err)
	}
	if _, err := f.shareAt(catNow, ana, bo, 30, "at-max"); err != nil {
		t.Fatalf("share 30 at the maximum: %v", err)
	}
	if _, err := f.shareAt(catNow, ana, bo, 31, "above"); !errors.As(err, &amountErr) {
		t.Fatalf("share 31: err = %v, want ShareAmountError", err)
	}
}

// The default range is 5 to 30, so an amount of 4 is refused with a 400 that names the range.
func TestShareHTTPAmountBelowMinimum(t *testing.T) {
	f := withShareRoutes(t, newCatFixture(t))
	enableSharing(t, f, defaultShareSettings())
	ana := f.addChild(f.fam.FamilyID, "Ana")
	bo := f.addChild(f.fam.FamilyID, "Bo")
	fund(t, f, ana.memberID, 100)

	const path = "/api/v1/points/share"
	rec := f.do(ana, http.MethodPost, path, fmt.Sprintf(`{"to_member_id":%q,"amount":4}`, bo.memberID),
		map[string]string{"Idempotency-Key": "lo4"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("amount 4: status %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if got := balanceOf(t, f, ana.memberID); got != 100 {
		t.Fatalf("sender balance = %d after refused share, want 100", got)
	}
}

// A parent's saved range is stored and returned on the policy.
func TestShareSettingsMinPerShareSaved(t *testing.T) {
	f := newCatFixture(t)
	s := defaultShareSettings()
	s.MinPerShare = 12
	if saved := enableSharing(t, f, s); saved.MinPerShare != 12 {
		t.Fatalf("saved min_per_share = %d, want 12", saved.MinPerShare)
	}
}
