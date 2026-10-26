package auth

import (
	"context"
	"database/sql"
	"fmt"

	"nestwall/backend/internal/platform/clock"
)

// ResetPassword sets a temporary password for a user, marks the account so the
// user must replace it at the next sign-in, and signs out all of the user's
// sessions. It returns the temporary password, which the caller shows once.
//
// It runs inside the caller's transaction, so the reset and its audit entry
// succeed or fail together.
func ResetPassword(ctx context.Context, tx *sql.Tx, clk clock.Clock, userUUID string) (string, error) {
	temporary, err := GenerateTemporaryPassword()
	if err != nil {
		return "", err
	}
	hash, err := HashPassword(temporary)
	if err != nil {
		return "", fmt.Errorf("hash temporary password: %w", err)
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, must_change_password = 1, updated_at = ? WHERE id = ?`,
		hash, clk.Now(), userUUID,
	)
	if err != nil {
		return "", fmt.Errorf("update password: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", sql.ErrNoRows
	}

	// An empty current session ID revokes every session of the user.
	if err := RevokeAllUserSessions(ctx, tx, clk, userUUID, ""); err != nil {
		return "", err
	}
	return temporary, nil
}
