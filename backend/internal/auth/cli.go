package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/ids"
)

// ErrUnknownUser is returned when no account has the given user ID.
var ErrUnknownUser = errors.New("unknown user")

// ResetPasswordByLoginID resets the password of the account with the given user
// ID. It backs the command-line tool for a parent who is locked out. It needs
// no session and no running server, only the database. It returns the temporary
// password, which the caller shows once.
func ResetPasswordByLoginID(ctx context.Context, db *sql.DB, clk clock.Clock, loginID string) (string, error) {
	loginID = strings.TrimSpace(loginID)
	if loginID == "" {
		return "", ErrUnknownUser
	}

	var userUUID string
	err := db.QueryRowContext(ctx, `SELECT id FROM users WHERE user_id = ?`, loginID).Scan(&userUUID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrUnknownUser
	}
	if err != nil {
		return "", err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	temporary, err := ResetPassword(ctx, tx, clk, userUUID)
	if err != nil {
		return "", err
	}

	// The audit row needs a family and an actor. The command has no signed-in
	// user, so the actor is the account itself.
	membership, err := membershipOf(ctx, tx, userUUID)
	if err != nil {
		return "", err
	}
	if membership != nil {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO audit_logs (id, family_id, actor_id, action, target_type, target_id) VALUES (?, ?, ?, ?, ?, ?)`,
			ids.New(), membership.FamilyID, userUUID, "password.reset_cli", "user", userUUID,
		); err != nil {
			return "", err
		}
	}

	if err := tx.Commit(); err != nil {
		return "", err
	}
	return temporary, nil
}
