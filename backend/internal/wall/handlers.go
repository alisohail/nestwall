package wall

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/auth"
	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/platform/web"
	"nestwall/backend/internal/weather"
)

const (
	pairingKeyTTL   = 10 * time.Minute
	pairMaxAttempts = 10
	pairWindow      = 15 * time.Minute
	maxNameRunes    = 60
	maxBodyBytes    = 8 << 10
	deviceCookie    = "nestwall-wall-device"
	deviceCookieAge = 365 * 24 * time.Hour
)

// exitCodePattern is the 4 to 6 digit exit code a parent sets when creating a device.
var exitCodePattern = regexp.MustCompile(`^[0-9]{4,6}$`)

// errKeyRejected is returned for any pairing key that cannot be used. The caller answers every case the same
// way, so a response does not reveal whether a key was unknown, expired, used or belonged to a revoked device.
var errKeyRejected = errors.New("pairing key rejected")

// Handlers serves the wall device routes.
type Handlers struct {
	db        *sql.DB
	limiter   *auth.RateLimiter
	clk       clock.Clock
	logger    *slog.Logger
	weather   *weather.Service
	avatarDir string
}

// NewHandlers creates the wall device handlers. The weather service fills the summary's weather and
// avatarDir is the folder that holds the picture files the wall shows.
func NewHandlers(db *sql.DB, limiter *auth.RateLimiter, clk clock.Clock, logger *slog.Logger, wx *weather.Service, avatarDir string) *Handlers {
	return &Handlers{db: db, limiter: limiter, clk: clk, logger: logger, weather: wx, avatarDir: avatarDir}
}

type createDeviceRequest struct {
	Name     string `json:"name"`
	ExitCode string `json:"exit_code"`
}

type createDeviceResponse struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	PairingKey string    `json:"pairing_key"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// CreateDevice registers a wall device for the family and returns its pairing key. The key is shown once.
func (h *Handlers) CreateDevice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	memberID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", nil)
		return
	}

	var req createDeviceRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if n := utf8.RuneCountInString(name); n < 1 || n > maxNameRunes {
		respondInvalid(w, &auth.ValidationError{Field: "name", Reason: "invalid_length"})
		return
	}
	if !exitCodePattern.MatchString(req.ExitCode) {
		respondInvalid(w, &auth.ValidationError{Field: "exit_code", Reason: "invalid_format"})
		return
	}

	familyID, userID, err := actorFamily(ctx, h.db, memberID)
	if err != nil {
		h.internalError(w, "failed to get actor family", err)
		return
	}
	exitHash, err := auth.HashPassword(req.ExitCode)
	if err != nil {
		h.internalError(w, "failed to hash exit code", err)
		return
	}
	key, err := newPairingKey()
	if err != nil {
		h.internalError(w, "failed to create pairing key", err)
		return
	}

	now := h.clk.Now().UTC()
	expiresAt := now.Add(pairingKeyTTL)
	deviceID := ids.New()

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "failed to begin transaction", err)
		return
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO wall_devices (id, family_id, name, exit_code_hash, created_by, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		deviceID, familyID, name, exitHash, userID, now, now,
	); err != nil {
		h.internalError(w, "failed to insert wall device", err)
		return
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO wall_pairing_codes (id, family_id, device_id, code_hash, expires_at, created_by, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ids.New(), familyID, deviceID, auth.HashToken(key), expiresAt, userID, now,
	); err != nil {
		h.internalError(w, "failed to insert pairing code", err)
		return
	}
	if err := audit.Record(ctx, tx, familyID, userID, "wall_device.created", "wall_device", &deviceID,
		map[string]any{"name": name}); err != nil {
		h.internalError(w, "failed to record audit event", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "failed to commit transaction", err)
		return
	}

	web.RespondJSON(w, http.StatusCreated, createDeviceResponse{
		ID:         deviceID,
		Name:       name,
		PairingKey: formatKey(key),
		ExpiresAt:  expiresAt,
	})
}

type pairRequest struct {
	Key string `json:"key"`
}

type pairResponse struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	PairedAt time.Time `json:"paired_at"`
}

// Pair exchanges a one-time pairing key for a device credential, which is set as a cookie. It is public
// because the tablet has no session yet. The key and the per-IP limit protect it.
func (h *Handlers) Pair(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limitKey := "wall-pair:" + auth.ClientIP(r)
	if err := h.limiter.CheckWindow(limitKey, pairMaxAttempts, pairWindow); err != nil {
		web.RespondError(w, http.StatusTooManyRequests, "rate_limited", "Too many attempts. Try again later.", nil)
		return
	}
	h.limiter.RecordWindow(limitKey, pairWindow)

	var req pairRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	key := normalizeKey(req.Key)
	if !validKey(key) {
		respondInvalidKey(w)
		return
	}

	credential, err := newCredential()
	if err != nil {
		h.internalError(w, "failed to create device credential", err)
		return
	}
	now := h.clk.Now().UTC()

	device, err := h.redeem(ctx, auth.HashToken(key), auth.HashToken(credential), now)
	if errors.Is(err, errKeyRejected) {
		respondInvalidKey(w)
		return
	}
	if err != nil {
		h.internalError(w, "failed to pair wall device", err)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     deviceCookie,
		Value:    credential,
		Path:     "/api/v1/wall",
		MaxAge:   int(deviceCookieAge.Seconds()),
		HttpOnly: true,
		Secure:   auth.IsSecureRequest(r),
		SameSite: http.SameSiteStrictMode,
	})
	web.RespondJSON(w, http.StatusOK, pairResponse{ID: device.id, Name: device.name, PairedAt: now})
}

type pairedDevice struct {
	id   string
	name string
}

// redeem marks the key as used and stores the credential hash on its device, in one transaction. Any failed
// check returns errKeyRejected and rolls back, so a rejected attempt never uses up a key. BEGIN IMMEDIATE holds
// the write lock from the start, so the checks and the updates cannot race another pairing.
func (h *Handlers) redeem(ctx context.Context, codeHash, credentialHash string, now time.Time) (pairedDevice, error) {
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return pairedDevice{}, err
	}
	defer tx.Rollback()

	var codeID, familyID, deviceID, createdBy, name string
	var expiresAt time.Time
	var usedAt, revokedAt sql.NullTime
	err = tx.QueryRowContext(ctx,
		`SELECT pc.id, pc.family_id, pc.device_id, pc.created_by, pc.expires_at, pc.used_at, d.name, d.revoked_at
		 FROM wall_pairing_codes pc
		 JOIN wall_devices d ON d.family_id = pc.family_id AND d.id = pc.device_id
		 WHERE pc.code_hash = ?`,
		codeHash,
	).Scan(&codeID, &familyID, &deviceID, &createdBy, &expiresAt, &usedAt, &name, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return pairedDevice{}, errKeyRejected
	}
	if err != nil {
		return pairedDevice{}, err
	}
	if usedAt.Valid || revokedAt.Valid || !now.Before(expiresAt) {
		return pairedDevice{}, errKeyRejected
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE wall_pairing_codes SET used_at = ? WHERE id = ? AND used_at IS NULL`,
		now, codeID,
	)
	if err != nil {
		return pairedDevice{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return pairedDevice{}, errKeyRejected
	}

	res, err = tx.ExecContext(ctx,
		`UPDATE wall_devices SET credential_hash = ?, paired_at = ?, last_seen_at = ?, updated_at = ?
		 WHERE family_id = ? AND id = ? AND paired_at IS NULL AND revoked_at IS NULL`,
		credentialHash, now, now, now, familyID, deviceID,
	)
	if err != nil {
		return pairedDevice{}, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return pairedDevice{}, errKeyRejected
	}

	if err := audit.Record(ctx, tx, familyID, createdBy, "wall_device.paired", "wall_device", &deviceID,
		map[string]any{"name": name}); err != nil {
		return pairedDevice{}, err
	}
	if err := tx.Commit(); err != nil {
		return pairedDevice{}, err
	}
	return pairedDevice{id: deviceID, name: name}, nil
}

// actorFamily returns the family ID and user ID of the calling family member.
func actorFamily(ctx context.Context, db *sql.DB, memberID string) (string, string, error) {
	var familyID, userID string
	err := db.QueryRowContext(ctx,
		`SELECT family_id, user_id FROM family_members WHERE id = ?`,
		memberID,
	).Scan(&familyID, &userID)
	return familyID, userID, err
}

// decodeJSON decodes a bounded JSON body that must not contain unknown fields. It writes a 400 and returns false on failure.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid request body", nil)
		return false
	}
	return true
}

// respondInvalid writes a 400 validation_failed response.
func respondInvalid(w http.ResponseWriter, err error) {
	var vErr *auth.ValidationError
	if errors.As(err, &vErr) {
		web.RespondError(w, http.StatusBadRequest, "validation_failed", "Invalid input", vErr.Params())
		return
	}
	web.RespondError(w, http.StatusBadRequest, "validation_failed", "Invalid input", nil)
}

// respondInvalidKey writes the one response every unusable pairing key gets.
func respondInvalidKey(w http.ResponseWriter) {
	web.RespondError(w, http.StatusBadRequest, "pairing_key_invalid",
		"That key is not valid or has expired. Check it, or ask a parent for a new one.", nil)
}

// internalError logs the error and writes a generic 500 response.
func (h *Handlers) internalError(w http.ResponseWriter, msg string, err error) {
	h.logger.Error(msg, slog.String("error", err.Error()))
	web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
}
