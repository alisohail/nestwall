package auth_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"nestwall/backend/internal/auth"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/testhelper"
)

func newSetupHandlers(t *testing.T) (*auth.SetupHandlers, func(email string) auth.SetupParentRequest) {
	t.Helper()
	db := testhelper.OpenDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := auth.NewSetupHandlers(db, clock.RealClock{}, logger)
	base := func(email string) auth.SetupParentRequest {
		return auth.SetupParentRequest{
			UserID:      "alice",
			Password:    currentPassword,
			DisplayName: "Alice Rivera",
			Email:       email,
			FamilyName:  "Rivera Family",
			Timezone:    "UTC",
			Locale:      "en",
		}
	}
	return h, base
}

func TestSetupParentRequiresEmail(t *testing.T) {
	cases := map[string]string{
		"missing":       "",
		"blank":         "   ",
		"invalid":       "not-an-email",
		"invalid space": "alice @example.com",
	}
	for name, email := range cases {
		t.Run(name, func(t *testing.T) {
			h, base := newSetupHandlers(t)
			rec := postJSON(t, h.Parent, base(email), nil)
			expectStatus(t, rec, http.StatusBadRequest)
		})
	}
}

func TestSetupParentStoresLowerCasedEmail(t *testing.T) {
	db := testhelper.OpenDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := auth.NewSetupHandlers(db, clock.RealClock{}, logger)

	req := auth.SetupParentRequest{
		UserID:      "alice",
		Password:    currentPassword,
		DisplayName: "Alice Rivera",
		Email:       "  Alice@Example.COM ",
		FamilyName:  "Rivera Family",
	}
	expectStatus(t, postJSON(t, h.Parent, req, nil), http.StatusCreated)

	var email string
	if err := db.QueryRowContext(context.Background(),
		`SELECT email FROM users WHERE user_id = 'alice'`,
	).Scan(&email); err != nil {
		t.Fatalf("read stored email: %v", err)
	}
	if email != "alice@example.com" {
		t.Errorf("stored email = %q, want %q", email, "alice@example.com")
	}
	if n := countRows(t, db, `SELECT COUNT(*) FROM family_members WHERE role_id = ?`, auth.ParentRoleID); n != 1 {
		t.Errorf("parent memberships = %d, want 1", n)
	}
}

func TestSetupParentRejectsTakenEmail(t *testing.T) {
	db := testhelper.OpenDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := auth.NewSetupHandlers(db, clock.RealClock{}, logger)

	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO users (id, user_id, password_hash, display_name, email, is_child)
		 VALUES (?, 'existing', 'unused', 'Existing', 'taken@example.com', 0)`,
		ids.New(),
	); err != nil {
		t.Fatalf("insert existing user: %v", err)
	}

	req := auth.SetupParentRequest{
		UserID:      "alice",
		Password:    currentPassword,
		DisplayName: "Alice Rivera",
		Email:       "Taken@Example.com",
		FamilyName:  "Rivera Family",
	}
	rec := postJSON(t, h.Parent, req, nil)
	expectStatus(t, rec, http.StatusConflict)
	if n := countRows(t, db, `SELECT COUNT(*) FROM families`); n != 0 {
		t.Errorf("families = %d, want 0 after rejected setup", n)
	}
}
