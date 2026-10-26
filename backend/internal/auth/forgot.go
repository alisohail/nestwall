package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"nestwall/backend/internal/notifypolicy"
	"nestwall/backend/internal/platform/web"
)

// Limits for the forgot-password endpoints. They apply per account and per IP.
const (
	forgotWindow         = time.Hour
	forgotMaxPerUser     = 5
	forgotMaxPerIP       = 20
	notifyMaxPerUser     = 3
	notifyMaxPerIP       = 10
	maxUserIDLength      = 64
	passwordResetKind    = "password_reset_request"
	forgotAcceptedStatus = "accepted"
	forgotChildStatus    = "child"
	// forgotEmailNotSetUpStatus means email is not set up on this server, so no link can be sent.
	forgotEmailNotSetUpStatus = "email_not_set_up"
)

// ForgotPasswordRequest is the body for the forgot-password endpoints.
type ForgotPasswordRequest struct {
	UserID string `json:"user_id"`
}

// ForgotPasswordEmailRequest is the body for the email forgot-password endpoint.
type ForgotPasswordEmailRequest struct {
	Email string `json:"email"`
}

// ForgotPasswordResponse is the answer to a forgot-password request. Status is
// "child" when the user ID belongs to an active child, who must ask a parent.
// Otherwise it is "accepted", which is the same for parents and unknown IDs.
type ForgotPasswordResponse struct {
	Status string `json:"status"`
}

// ForgotPassword handles POST /api/v1/auth/forgot-password.
//
// A child is told that only a parent can reset their password. Every other
// user ID, parent or unknown, gets the same "accepted" answer, so the API does
// not reveal whether a parent account exists. The exception is a parent whose
// family has no ready email: they get "email_not_set_up", which reveals that
// the account exists (approved trade-off). A parent with a saved email address
// and ready email gets the reset link by email.
func (h *Handlers) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeForgotRequest(w, r)
	if !ok {
		return
	}
	if !h.allowForgot(w, ClientIP(r), req.UserID, "forgot", forgotMaxPerUser, forgotMaxPerIP) {
		return
	}
	ctx := r.Context()
	child, err := h.isActiveChild(ctx, req.UserID)
	if err != nil {
		h.logger.Error("forgot password lookup", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}
	if child {
		web.RespondJSON(w, http.StatusAccepted, ForgotPasswordResponse{Status: forgotChildStatus})
		return
	}
	web.RespondJSON(w, http.StatusAccepted, ForgotPasswordResponse{Status: h.emailParentByUserID(ctx, req.UserID)})
}

// emailParentByUserID sends the reset link to a parent's saved address, when there is one.
// It returns "email_not_set_up" when the parent's family has no ready email, so the
// dialog can say so; otherwise "accepted". Send failures are logged only.
func (h *Handlers) emailParentByUserID(ctx context.Context, userID string) string {
	p, err := h.findParentByUserID(ctx, userID)
	if err != nil {
		h.logger.Error("forgot password parent lookup", slog.String("error", err.Error()))
		return forgotAcceptedStatus
	}
	if p == nil {
		return forgotAcceptedStatus
	}
	ready, err := h.familyEmailReady(ctx, p.familyID)
	if err != nil {
		h.logger.Error("forgot password readiness", slog.String("error", err.Error()))
		return forgotAcceptedStatus
	}
	if !ready {
		return forgotEmailNotSetUpStatus
	}
	if err := h.sendResetLink(ctx, *p); err != nil {
		h.logger.Error("forgot password email", slog.String("error", err.Error()))
	}
	return forgotAcceptedStatus
}

// isActiveChild reports whether the user ID belongs to a child in an active family membership.
func (h *Handlers) isActiveChild(ctx context.Context, userID string) (bool, error) {
	var n int
	err := h.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users u
		 JOIN family_members fm ON fm.user_id = u.id AND fm.status = 'active'
		 WHERE u.user_id = ? AND u.is_child = 1`,
		userID,
	).Scan(&n)
	return n > 0, err
}

// NotifyParents handles POST /api/v1/auth/forgot-password/notify-parents.
//
// When the user ID belongs to an active child, it creates a notification for
// every active parent in that family. Any other request gets the same response,
// so the caller learns nothing about the account.
func (h *Handlers) NotifyParents(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeForgotRequest(w, r)
	if !ok {
		return
	}
	if !h.allowForgot(w, ClientIP(r), req.UserID, "notify", notifyMaxPerUser, notifyMaxPerIP) {
		return
	}

	ctx := r.Context()
	if err := h.notifyParentsOfChild(ctx, req.UserID); err != nil {
		h.logger.Error("notify parents", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}
	web.RespondJSON(w, http.StatusAccepted, ForgotPasswordResponse{Status: forgotAcceptedStatus})
}

// notifyParentsOfChild writes the notifications in one transaction.
func (h *Handlers) notifyParentsOfChild(ctx context.Context, userID string) error {
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var childMemberID, familyID, displayName string
	err = tx.QueryRowContext(ctx,
		`SELECT fm.id, fm.family_id, u.display_name
		 FROM users u
		 JOIN family_members fm ON fm.user_id = u.id AND fm.status = 'active'
		 WHERE u.user_id = ? AND u.is_child = 1
		 LIMIT 1`,
		userID,
	).Scan(&childMemberID, &familyID, &displayName)
	if err == sql.ErrNoRows {
		// Unknown or non-child account: nothing to do, same response as usual.
		return nil
	}
	if err != nil {
		return err
	}

	rows, err := tx.QueryContext(ctx,
		`SELECT fm.id FROM family_members fm
		 JOIN roles r ON r.id = fm.role_id
		 WHERE fm.family_id = ? AND fm.status = 'active' AND r.name = 'Parent'`,
		familyID,
	)
	if err != nil {
		return err
	}
	var parentIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		parentIDs = append(parentIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	now := h.clock.Now()
	body := displayName + " needs a password reset"
	for _, parentID := range parentIDs {
		if _, err := notifypolicy.Notify(ctx, tx, notifypolicy.Notice{
			FamilyID: familyID, RecipientID: parentID, Kind: passwordResetKind,
			SubjectID: childMemberID, Body: body,
		}, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ForgotPasswordByEmail handles POST /api/v1/auth/forgot-password/email.
//
// A parent enters the email address saved on their account. When email is not
// set up on this server, the answer is "email_not_set_up" for every address, so
// the screen can say so. Otherwise the answer is "accepted" for every address.
// The link is sent only when the address belongs to a parent, and nothing is
// recorded for other addresses.
func (h *Handlers) ForgotPasswordByEmail(w http.ResponseWriter, r *http.Request) {
	var req ForgotPasswordEmailRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody)).Decode(&req); err != nil {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid request body", nil)
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if err := ValidateEmail(email); err != nil {
		web.RespondError(w, http.StatusBadRequest, "validation_failed", "Enter a valid email address", nil)
		return
	}
	if !h.allowForgot(w, ClientIP(r), email, "forgot-email", forgotMaxPerUser, forgotMaxPerIP) {
		return
	}

	ctx := r.Context()
	ready, err := h.anyEmailReady(ctx)
	if err != nil {
		h.logger.Error("forgot password email readiness", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}
	if !ready {
		web.RespondJSON(w, http.StatusAccepted, ForgotPasswordResponse{Status: forgotEmailNotSetUpStatus})
		return
	}

	p, err := h.findParentByEmail(ctx, email)
	if err != nil {
		h.logger.Error("forgot password email lookup", slog.String("error", err.Error()))
		web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
		return
	}
	if p != nil {
		if err := h.sendResetLink(ctx, *p); err != nil {
			h.logger.Error("forgot password email", slog.String("error", err.Error()))
		}
	}
	web.RespondJSON(w, http.StatusAccepted, ForgotPasswordResponse{Status: forgotAcceptedStatus})
}

// decodeForgotRequest reads and checks the body. It writes the error response itself.
func decodeForgotRequest(w http.ResponseWriter, r *http.Request) (ForgotPasswordRequest, bool) {
	var req ForgotPasswordRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJSONBody)).Decode(&req); err != nil {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid request body", nil)
		return req, false
	}
	req.UserID = strings.TrimSpace(req.UserID)
	if req.UserID == "" || len(req.UserID) > maxUserIDLength {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "user_id is required", nil)
		return req, false
	}
	return req, true
}

// allowForgot applies the per-account and per-IP limits and records the request.
// It writes a 429 response and returns false when a limit is reached.
func (h *Handlers) allowForgot(w http.ResponseWriter, ip, userID, scope string, maxUser, maxIP int) bool {
	userKey := scope + ":user:" + userID
	ipKey := scope + ":ip:" + ip
	if err := h.limiter.CheckWindow(userKey, maxUser, forgotWindow); err != nil {
		web.RespondError(w, http.StatusTooManyRequests, "rate_limited", "Too many requests. Try again later.", nil)
		return false
	}
	if err := h.limiter.CheckWindow(ipKey, maxIP, forgotWindow); err != nil {
		web.RespondError(w, http.StatusTooManyRequests, "rate_limited", "Too many requests. Try again later.", nil)
		return false
	}
	h.limiter.RecordWindow(userKey, forgotWindow)
	h.limiter.RecordWindow(ipKey, forgotWindow)
	return true
}
