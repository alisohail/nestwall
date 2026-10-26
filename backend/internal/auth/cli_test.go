package auth_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"nestwall/backend/internal/auth"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/testhelper"
)

func TestResetPasswordByLoginIDRejectsUnknownUser(t *testing.T) {
	db := testhelper.OpenDB(t)
	testhelper.OneFamily(t, db)
	ctx := context.Background()

	for _, id := range []string{"nobody", "   ", ""} {
		_, err := auth.ResetPasswordByLoginID(ctx, db, clock.RealClock{}, id)
		if !errors.Is(err, auth.ErrUnknownUser) {
			t.Errorf("ResetPasswordByLoginID(%q) err = %v, want ErrUnknownUser", id, err)
		}
	}
}

// The command-line reset only needs the database file. This test opens the
// database directly with no server running and checks the result is usable.
func TestResetPasswordByLoginIDWorksWithoutServer(t *testing.T) {
	db := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, db)
	ctx := context.Background()

	temp, err := auth.ResetPasswordByLoginID(ctx, db, clock.RealClock{}, fam.ParentUserIDTx)
	if err != nil {
		t.Fatalf("ResetPasswordByLoginID: %v", err)
	}
	if temp == "" {
		t.Fatal("temporary password is empty")
	}

	if n := countRows(t, db, `SELECT must_change_password FROM users WHERE id = ?`, fam.ParentUserID); n != 1 {
		t.Errorf("must_change_password = %d, want 1", n)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM sessions WHERE user_id = ? AND revoked_at IS NULL`, fam.ParentUserID); n != 0 {
		t.Errorf("active sessions after CLI reset = %d, want 0", n)
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM audit_logs WHERE action = 'password.reset_cli' AND target_id = ?`, fam.ParentUserID); n != 1 {
		t.Errorf("audit rows for CLI reset = %d, want 1", n)
	}

	// The temporary password must sign in.
	h := newHandlers(db)
	rec := postJSON(t, h.Login, auth.LoginRequest{UserID: fam.ParentUserIDTx, Password: temp}, nil)
	expectStatus(t, rec, http.StatusOK)
}
