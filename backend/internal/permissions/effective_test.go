package permissions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"nestwall/backend/internal/testhelper"
)

func TestEffectiveCodesForParent(t *testing.T) {
	db := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, db)

	codes, err := EffectiveCodes(context.Background(), db, fam.MemberID)
	if err != nil {
		t.Fatalf("EffectiveCodes returned error: %v", err)
	}
	for _, want := range []string{PermTasksManage, PermTasksComplete, PermMembersView, PermSettingsView} {
		if !slices.Contains(codes, want) {
			t.Errorf("parent should hold %s, got %v", want, codes)
		}
	}
	if slices.Contains(codes, "no.such.permission") {
		t.Error("unknown code must not be listed")
	}
}

func TestEffectiveCodesUnknownMemberIsEmpty(t *testing.T) {
	db := testhelper.OpenDB(t)
	testhelper.OneFamily(t, db)

	codes, err := EffectiveCodes(context.Background(), db, "no-such-member")
	if err != nil {
		t.Fatalf("EffectiveCodes returned error: %v", err)
	}
	if len(codes) != 0 {
		t.Errorf("unknown member should hold no codes, got %v", codes)
	}
}

func TestEffectiveHandlerNeedsMember(t *testing.T) {
	db := testhelper.OpenDB(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/permissions", nil)

	Effective(db).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}
