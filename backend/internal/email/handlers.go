package email

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

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/platform/web"
)

const maxSettingsBody = 16 << 10

// Handlers serves the family SMTP settings and the test email.
type Handlers struct {
	db     *sql.DB
	clock  clock.Clock
	logger *slog.Logger
	dbKey  string
	Mailer Mailer
}

// NewHandlers returns the email handlers. The zero Mailer value is ready to use.
func NewHandlers(db *sql.DB, clk clock.Clock, logger *slog.Logger, dbKey string) *Handlers {
	return &Handlers{db: db, clock: clk, logger: logger, dbKey: dbKey}
}

// GetSettings returns the family's SMTP settings without the password.
func (h *Handlers) GetSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	memberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}
	familyID, err := familyOf(ctx, h.db, memberID)
	if errors.Is(err, sql.ErrNoRows) {
		web.RespondError(w, http.StatusForbidden, "no_membership", "No family membership", nil)
		return
	}
	if err != nil {
		h.internalError(w, "failed to find family", err)
		return
	}
	cur, err := loadStored(ctx, h.db, familyID)
	if err != nil {
		h.internalError(w, "failed to load email settings", err)
		return
	}
	web.RespondJSON(w, http.StatusOK, cur.Settings)
}

// UpdateSettings saves the family's SMTP settings. A password is sealed before it is stored.
func (h *Handlers) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	memberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}
	actorUserID, ok := permissions.GetUserID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}

	var body UpdateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSettingsBody)).Decode(&body); err != nil {
		web.RespondError(w, http.StatusBadRequest, "invalid_json", "Invalid request body", nil)
		return
	}
	req, err := normalize(body)
	if err != nil {
		var ve ValidationError
		if errors.As(err, &ve) {
			web.RespondError(w, http.StatusBadRequest, "validation_failed", ve.Message, nil)
			return
		}
		h.internalError(w, "failed to validate email settings", err)
		return
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "failed to begin transaction", err)
		return
	}
	defer tx.Rollback()

	familyID, err := familyOf(ctx, tx, memberID)
	if errors.Is(err, sql.ErrNoRows) {
		web.RespondError(w, http.StatusForbidden, "no_membership", "No family membership", nil)
		return
	}
	if err != nil {
		h.internalError(w, "failed to find family", err)
		return
	}

	// nil keeps the saved password, "" clears it, anything else replaces it.
	cur, err := loadStored(ctx, tx, familyID)
	if err != nil {
		h.internalError(w, "failed to load email settings", err)
		return
	}
	sealed := cur.Sealed
	if req.Password != nil {
		sealed = ""
		if *req.Password != "" {
			sealed, err = sealSecret(h.dbKey, familyID, *req.Password)
			if err != nil {
				h.internalError(w, "failed to seal smtp password", err)
				return
			}
		}
	}

	configured := ready(fromRequest(req))
	enabled := 0
	if configured {
		enabled = 1
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO smtp_settings (family_id, enabled, host, port, security, username, password_sealed,
		        from_address, from_name, send_limit_per_hour, updated_at, updated_by)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, strftime('%Y-%m-%d %H:%M:%f+00:00', 'now'), ?)
		 ON CONFLICT(family_id) DO UPDATE SET
		        enabled = excluded.enabled, host = excluded.host, port = excluded.port,
		        security = excluded.security, username = excluded.username,
		        password_sealed = excluded.password_sealed, from_address = excluded.from_address,
		        from_name = excluded.from_name, send_limit_per_hour = excluded.send_limit_per_hour,
		        updated_at = excluded.updated_at, updated_by = excluded.updated_by`,
		familyID, enabled, req.Host, req.Port, req.Security, req.Username, sealed,
		req.FromAddress, req.FromName, req.SendLimitPerHour, actorUserID); err != nil {
		h.internalError(w, "failed to save email settings", err)
		return
	}

	meta := map[string]any{"ready": configured, "security": req.Security, "password_changed": req.Password != nil}
	if err := audit.Record(ctx, tx, familyID, actorUserID, "smtp.settings_updated", "family", &familyID, meta); err != nil {
		h.internalError(w, "failed to record audit log", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "failed to commit transaction", err)
		return
	}

	saved := fromRequest(req)
	saved.PasswordSet = sealed != ""
	web.RespondJSON(w, http.StatusOK, saved)
}

// SendTest sends a test email to the signed-in user's address. It runs inside the request,
// bounded by the Mailer timeout, because the user asked for it and is waiting.
func (h *Handlers) SendTest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	memberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}
	actorUserID, ok := permissions.GetUserID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}
	familyID, err := familyOf(ctx, h.db, memberID)
	if errors.Is(err, sql.ErrNoRows) {
		web.RespondError(w, http.StatusForbidden, "no_membership", "No family membership", nil)
		return
	}
	if err != nil {
		h.internalError(w, "failed to find family", err)
		return
	}

	cur, err := loadStored(ctx, h.db, familyID)
	if err != nil {
		h.internalError(w, "failed to load email settings", err)
		return
	}
	if !cur.Ready {
		web.RespondError(w, http.StatusUnprocessableEntity, "smtp_not_ready", "Turn on email and enter the server and sender address first", nil)
		return
	}

	var to sql.NullString
	if err := h.db.QueryRowContext(ctx, `SELECT email FROM users WHERE id = ?`, actorUserID).Scan(&to); err != nil {
		h.internalError(w, "failed to load user email", err)
		return
	}
	if !to.Valid || strings.TrimSpace(to.String) == "" {
		web.RespondError(w, http.StatusUnprocessableEntity, "no_email", "Add an email address to your account to receive the test", nil)
		return
	}

	account := Account{
		Host:        cur.Host,
		Port:        cur.Port,
		Security:    cur.Security,
		Username:    cur.Username,
		FromAddress: cur.FromAddress,
		FromName:    cur.FromName,
	}
	if cur.Sealed != "" {
		account.Password, err = openSecret(h.dbKey, familyID, cur.Sealed)
		if err != nil {
			h.internalError(w, "failed to open smtp password", err)
			return
		}
	}
	msg := Message{
		To:      to.String,
		Subject: "NestWall test email",
		Body:    fmt.Sprintf("This is a test email from NestWall, sent at %s.\r\n\r\nIf you received it, email notifications are set up correctly.\r\n", time.Now().UTC().Format(time.RFC1123)),
	}
	sendErr := h.Mailer.Send(ctx, account, msg)
	h.recordTest(ctx, familyID, to.String, msg.Subject, sendErr)
	if sendErr != nil {
		h.logger.Warn("smtp test send failed", slog.String("error", sendErr.Error()))
		web.RespondError(w, http.StatusBadGateway, "send_failed", "The mail server did not accept the message. Check the server, port, security and password.", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// recordTest writes the test send to the outbox as sent or failed, so the diagnostics email counts
// include it. The body is not kept, as for other sent messages.
func (h *Handlers) recordTest(ctx context.Context, familyID, to, subject string, sendErr error) {
	status, lastErr := "sent", ""
	var sentAt any = formatTime(h.clock.Now())
	if sendErr != nil {
		status, lastErr, sentAt = "failed", "test send failed", nil
	}
	now := formatTime(h.clock.Now())
	if _, err := h.db.ExecContext(ctx,
		`INSERT INTO email_outbox (id, family_id, to_address, subject, body, status, attempts, next_attempt_at,
		        last_error, created_at, sent_at)
		 VALUES (?, ?, ?, ?, '', ?, 1, ?, ?, ?, ?)`,
		ids.New(), familyID, to, subject, status, now, lastErr, now, sentAt,
	); err != nil {
		h.logger.Warn("failed to record smtp test", slog.String("error", err.Error()))
	}
}

func (h *Handlers) internalError(w http.ResponseWriter, msg string, err error) {
	h.logger.Error(msg, slog.String("error", err.Error()))
	web.RespondError(w, http.StatusInternalServerError, "internal_error", "Something went wrong", nil)
}

// familyOf returns the family of an active member.
func familyOf(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, memberID string) (string, error) {
	var familyID string
	err := q.QueryRowContext(ctx,
		`SELECT family_id FROM family_members WHERE id = ? AND status = 'active'`, memberID).Scan(&familyID)
	return familyID, err
}
