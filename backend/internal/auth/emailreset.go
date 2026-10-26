package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/platform/web"
)

const (
	// resetTokenTTL is how long an emailed reset link works.
	resetTokenTTL = 30 * time.Minute
	// resetMaxPerIP limits how often one IP can try to use a reset link.
	resetMaxPerIP = 20
	resetInvalid  = "reset_link_invalid"
)

// ResetMailer queues an email for delivery. email.Outbox satisfies it. auth cannot
// import the email package directly, because email depends on permissions, which depends on auth.
type ResetMailer interface {
	Enqueue(ctx context.Context, familyID, to, subject, body string) error
}

// CompletePasswordResetRequest is the body of POST /api/v1/auth/reset-password.
type CompletePasswordResetRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

// WithEmailReset turns on emailed reset links. appURL is the public address of
// the app, used to build the link in the email.
func (h *Handlers) WithEmailReset(mailer ResetMailer, appURL string) *Handlers {
	h.mailer = mailer
	h.appURL = strings.TrimRight(appURL, "/")
	return h
}

// parentAccount is an active parent who has a saved email address.
type parentAccount struct {
	userPK      string
	familyID    string
	displayName string
	email       string
}

const parentAccountQuery = `SELECT u.id, fm.family_id, u.display_name, u.email
	FROM users u
	JOIN family_members fm ON fm.user_id = u.id AND fm.status = 'active'
	JOIN roles r ON r.id = fm.role_id
	WHERE r.name = 'Parent' AND u.is_child = 0 AND u.email IS NOT NULL AND u.email <> ''`

// findParentByEmail returns the parent whose saved address matches email (already lower-cased).
// It returns nil when there is no match.
func (h *Handlers) findParentByEmail(ctx context.Context, email string) (*parentAccount, error) {
	return h.queryParent(ctx, parentAccountQuery+` AND lower(u.email) = ? LIMIT 1`, email)
}

// findParentByUserID returns the parent with this login ID, or nil when the account is not a parent with an email.
func (h *Handlers) findParentByUserID(ctx context.Context, userID string) (*parentAccount, error) {
	return h.queryParent(ctx, parentAccountQuery+` AND u.user_id = ? LIMIT 1`, userID)
}

func (h *Handlers) queryParent(ctx context.Context, query, arg string) (*parentAccount, error) {
	var p parentAccount
	err := h.db.QueryRowContext(ctx, query, arg).Scan(&p.userPK, &p.familyID, &p.displayName, &p.email)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// anyEmailReady reports whether email is set up for at least one family.
// The email route uses it to say "not set up" without checking any address.
func (h *Handlers) anyEmailReady(ctx context.Context) (bool, error) {
	var n int
	err := h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM smtp_settings WHERE enabled = 1`).Scan(&n)
	return n > 0, err
}

// familyEmailReady reports whether email is set up for this family.
func (h *Handlers) familyEmailReady(ctx context.Context, familyID string) (bool, error) {
	var n int
	err := h.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM smtp_settings WHERE family_id = ? AND enabled = 1`, familyID,
	).Scan(&n)
	return n > 0, err
}

// sendResetLink issues a single-use link for the parent and queues the email.
// Older unused links for the same account stop working. Nothing is stored when
// email is not set up for the family.
func (h *Handlers) sendResetLink(ctx context.Context, p parentAccount) error {
	if h.mailer == nil {
		return nil
	}
	ready, err := h.familyEmailReady(ctx, p.familyID)
	if err != nil {
		return err
	}
	if !ready {
		return nil
	}

	token, err := GenerateSessionToken()
	if err != nil {
		return fmt.Errorf("generate reset token: %w", err)
	}
	now := h.clock.Now()
	tokenID := ids.New()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM password_reset_tokens WHERE user_id = ? AND used_at IS NULL`, p.userPK,
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO password_reset_tokens (id, user_id, token_hash, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		tokenID, p.userPK, HashToken(token), now.Add(resetTokenTTL), now,
	); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	link := h.appURL + "/reset-password/" + token
	subject := "Reset your NestWall password"
	body := fmt.Sprintf(
		"Hi %s,\n\nUse this link to choose a new password. It works once and expires in 30 minutes.\n\n%s\n\n"+
			"If you did not ask for this, you can ignore this email. Your password stays the same.\n",
		p.displayName, link)
	if err := h.mailer.Enqueue(ctx, p.familyID, p.email, subject, body); err != nil {
		// Do not leave a link that was never emailed.
		if _, delErr := h.db.ExecContext(ctx, `DELETE FROM password_reset_tokens WHERE id = ?`, tokenID); delErr != nil {
			h.logger.Error("reset link cleanup", slog.String("error", delErr.Error()))
		}
		return err
	}
	return nil
}

// CompletePasswordReset handles POST /api/v1/auth/reset-password.
//
// The link is checked by its hash: it must be unused and not expired. A valid
// link sets the new password, clears the must-change flag, signs out every
// session and records an audit entry. The link is spent in the same transaction.
// The user then signs in again with the new password.
func (h *Handlers) CompletePasswordReset(w http.ResponseWriter, r *http.Request) {
	ip := ClientIP(r)
	key := "reset:ip:" + ip
	if err := h.limiter.CheckWindow(key, resetMaxPerIP, forgotWindow); err != nil {
		web.RespondError(w, http.StatusTooManyRequests, "rate_limited", "Too many requests. Try again later.", nil)
		return
	}
	h.limiter.RecordWindow(key, forgotWindow)

	var req CompletePasswordResetRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody)).Decode(&req); err != nil {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid request body", nil)
		return
	}
	if req.Token == "" {
		web.RespondError(w, http.StatusBadRequest, resetInvalid, "This reset link is invalid or has expired. Ask for a new one.", nil)
		return
	}
	if err := ValidatePassword(req.NewPassword); err != nil {
		respondValidation(w, err)
		return
	}
	newHash, err := HashPassword(req.NewPassword)
	if err != nil {
		h.logger.Error("reset password: hash", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}

	ctx := r.Context()
	now := h.clock.Now()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.logger.Error("reset password: begin", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}
	defer tx.Rollback()

	var tokenID, userPK string
	var expiresAt time.Time
	var usedAt sql.NullTime
	err = tx.QueryRowContext(ctx,
		`SELECT id, user_id, expires_at, used_at FROM password_reset_tokens WHERE token_hash = ?`,
		HashToken(req.Token),
	).Scan(&tokenID, &userPK, &expiresAt, &usedAt)
	if errors.Is(err, sql.ErrNoRows) {
		web.RespondError(w, http.StatusBadRequest, resetInvalid, "This reset link is invalid or has expired. Ask for a new one.", nil)
		return
	}
	if err != nil {
		h.logger.Error("reset password: lookup", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}
	if usedAt.Valid || !now.Before(expiresAt) {
		web.RespondError(w, http.StatusBadRequest, resetInvalid, "This reset link is invalid or has expired. Ask for a new one.", nil)
		return
	}

	// Spend the link first. If another request got there first, rows affected is 0 and nothing changes.
	res, err := tx.ExecContext(ctx,
		`UPDATE password_reset_tokens SET used_at = ? WHERE id = ? AND used_at IS NULL`, now, tokenID)
	if err != nil {
		h.logger.Error("reset password: spend link", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		web.RespondError(w, http.StatusBadRequest, resetInvalid, "This reset link is invalid or has expired. Ask for a new one.", nil)
		return
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, must_change_password = 0, updated_at = ? WHERE id = ?`,
		newHash, now, userPK,
	); err != nil {
		h.logger.Error("reset password: update", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}
	if err := RevokeAllUserSessions(ctx, tx, h.clock, userPK, ""); err != nil {
		h.logger.Error("reset password: revoke sessions", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}
	membership, err := membershipOf(ctx, tx, userPK)
	if err != nil {
		h.logger.Error("reset password: membership", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}
	if membership != nil {
		if err := recordPasswordAudit(ctx, tx, membership.FamilyID, userPK); err != nil {
			h.logger.Error("reset password: audit", slog.String("error", err.Error()))
			web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		h.logger.Error("reset password: commit", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
