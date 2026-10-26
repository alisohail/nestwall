package notifications

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"nestwall/backend/internal/email"
	"nestwall/backend/internal/notifypolicy"
	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/web"
)

const (
	maxSettingsBody = 64 << 10
	maxTemplateLen  = 32 << 10
	bodyPlaceholder = "{{body}}"
)

// SettingsResponse is the family notification settings. EmailTemplate holds the template in use,
// which is DefaultTemplate when the family has not saved its own.
type SettingsResponse struct {
	Settings        notifypolicy.Settings `json:"settings"`
	DefaultTemplate string                `json:"default_template"`
}

// SettingsRequest is the body of PUT /api/v1/notifications/settings. Every field is required, so
// the form sends the whole state. An empty email_template restores the built-in template.
type SettingsRequest struct {
	InApp         *bool  `json:"in_app"`
	Browser       *bool  `json:"browser"`
	Email         *bool  `json:"email"`
	EmailDigest   *bool  `json:"email_digest"`
	DigestMinutes *int   `json:"digest_minutes"`
	EmailTemplate string `json:"email_template"`
}

// minDigestMinutes and maxDigestMinutes bound how often a digest may be sent: one minute to one day.
const (
	minDigestMinutes = 1
	maxDigestMinutes = 1440
)

// PreviewRequest is the body of POST /api/v1/notifications/settings/preview.
type PreviewRequest struct {
	EmailTemplate string `json:"email_template"`
}

// PreviewResponse is the sample email rendered with the template.
type PreviewResponse struct {
	HTML string `json:"html"`
}

func settingsResponse(s notifypolicy.Settings) SettingsResponse {
	if strings.TrimSpace(s.EmailTemplate) == "" {
		s.EmailTemplate = notifypolicy.DefaultTemplate
	}
	return SettingsResponse{Settings: s, DefaultTemplate: notifypolicy.DefaultTemplate}
}

// GetSettings handles GET /api/v1/notifications/settings. Parents read it to change it.
func (h *Handlers) GetSettings(w http.ResponseWriter, r *http.Request) {
	familyID, _, ok := h.actor(w, r)
	if !ok {
		return
	}
	s, err := notifypolicy.Load(r.Context(), h.db, familyID)
	if err != nil {
		h.internalError(w, "load notification settings", err)
		return
	}
	web.RespondJSON(w, http.StatusOK, settingsResponse(s))
}

// PutSettings handles PUT /api/v1/notifications/settings. Email can only be switched on once the
// family mail server is set up. The template must contain {{body}}, where the notices go.
func (h *Handlers) PutSettings(w http.ResponseWriter, r *http.Request) {
	familyID, _, ok := h.actor(w, r)
	if !ok {
		return
	}
	var req SettingsRequest
	if !decodeSettings(w, r, &req) {
		return
	}
	if req.InApp == nil || req.Browser == nil || req.Email == nil || req.EmailDigest == nil || req.DigestMinutes == nil {
		invalid(w, "settings", "All switches and the digest interval are required")
		return
	}
	if *req.DigestMinutes < minDigestMinutes || *req.DigestMinutes > maxDigestMinutes {
		invalid(w, "digest_minutes", "Digest interval must be between 1 and 1440 minutes")
		return
	}
	tmpl := strings.TrimSpace(req.EmailTemplate)
	if len(tmpl) > maxTemplateLen {
		invalid(w, "email_template", "Template is too long")
		return
	}
	if tmpl != "" && !strings.Contains(tmpl, bodyPlaceholder) {
		invalid(w, "email_template", "Template must include {{body}}")
		return
	}
	if tmpl == strings.TrimSpace(notifypolicy.DefaultTemplate) {
		tmpl = ""
	}

	if *req.Email {
		ready, err := h.emailReady(r.Context(), familyID)
		if err != nil {
			h.internalError(w, "load mail settings", err)
			return
		}
		if !ready {
			web.RespondError(w, http.StatusUnprocessableEntity, "smtp_not_ready",
				"Set up the mail server in Settings > Email before turning on email notifications", nil)
			return
		}
	}

	s := notifypolicy.Settings{
		InApp: *req.InApp, Browser: *req.Browser, Email: *req.Email,
		EmailDigest: *req.EmailDigest, DigestMinutes: *req.DigestMinutes, EmailTemplate: tmpl,
	}
	if err := notifypolicy.Save(r.Context(), h.db, familyID, s, h.clock.Now()); err != nil {
		h.internalError(w, "save notification settings", err)
		return
	}
	web.RespondJSON(w, http.StatusOK, settingsResponse(s))
}

// PreviewTemplate handles POST /api/v1/notifications/settings/preview. It renders a sample email
// with the given template so the parent can see it before saving.
func (h *Handlers) PreviewTemplate(w http.ResponseWriter, r *http.Request) {
	familyID, _, ok := h.actor(w, r)
	if !ok {
		return
	}
	var req PreviewRequest
	if !decodeSettings(w, r, &req) {
		return
	}
	if len(req.EmailTemplate) > maxTemplateLen {
		invalid(w, "email_template", "Template is too long")
		return
	}
	var familyName string
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT name FROM families WHERE id = ?`, familyID).Scan(&familyName); err != nil {
		h.internalError(w, "load family", err)
		return
	}
	html := notifypolicy.Render(req.EmailTemplate, notifypolicy.Fields{
		Subject:    notifypolicy.SampleSubject,
		Body:       notifypolicy.SampleBody(),
		LogoURL:    notifypolicy.LogoSrc,
		AppURL:     h.appURL,
		FamilyName: familyName,
	})
	// The preview is shown in the browser, which cannot resolve cid: references, so the logo is inlined here.
	// Sent mail keeps the attachment.
	html = strings.ReplaceAll(html, notifypolicy.LogoSrc, "data:image/png;base64,"+base64.StdEncoding.EncodeToString(email.LogoPNG()))
	web.RespondJSON(w, http.StatusOK, PreviewResponse{HTML: html})
}

// emailReady reports whether the family's mail server is switched on.
func (h *Handlers) emailReady(ctx context.Context, familyID string) (bool, error) {
	var enabled int
	err := h.db.QueryRowContext(ctx,
		`SELECT enabled FROM smtp_settings WHERE family_id = ?`, familyID).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return enabled == 1, nil
}

// actor returns the caller's family and member IDs for an active membership. It writes the error
// response and returns ok=false when the request may not continue.
func (h *Handlers) actor(w http.ResponseWriter, r *http.Request) (familyID, memberID string, ok bool) {
	ctx := r.Context()
	memberID, okMember := permissions.GetFamilyMemberID(ctx)
	if !okMember {
		web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "Authentication required", nil)
		return "", "", false
	}
	err := h.db.QueryRowContext(ctx,
		`SELECT family_id FROM family_members WHERE id = ? AND status = 'active'`, memberID,
	).Scan(&familyID)
	if errors.Is(err, sql.ErrNoRows) {
		web.RespondError(w, http.StatusForbidden, "no_membership", "No active family membership", nil)
		return "", "", false
	}
	if err != nil {
		h.internalError(w, "load membership", err)
		return "", "", false
	}
	return familyID, memberID, true
}

// decodeSettings decodes a bounded JSON body and rejects unknown fields. It writes a 400 response
// and returns false on failure.
func decodeSettings(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSettingsBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid request body", nil)
		return false
	}
	return true
}

// invalid writes a 400 validation_failed response with the field and reason.
func invalid(w http.ResponseWriter, field, reason string) {
	web.RespondError(w, http.StatusBadRequest, "validation_failed", "Invalid input",
		map[string]any{"field": field, "reason": reason})
}
