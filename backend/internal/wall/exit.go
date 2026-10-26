package wall

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net/http"
	"time"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/auth"
	"nestwall/backend/internal/platform/web"
)

const (
	// exitMaxAttempts wrong codes in a row lock a device's exit keypad for exitLockout.
	exitMaxAttempts = 5
	exitLockout     = 5 * time.Minute
)

type exitCodeRequest struct {
	ExitCode string `json:"exit_code"`
}

type exitOutcome int

const (
	exitAllowed exitOutcome = iota
	exitWrong
	exitLocked
)

// exitCheck is the result of one exit code attempt. attemptsLeft is set for exitWrong, lockedUntil for exitLocked.
type exitCheck struct {
	outcome      exitOutcome
	attemptsLeft int
	lockedUntil  time.Time
}

// checkExitCode compares a code with the device's stored hash and updates the failure counter. It must run in a
// transaction that is committed even when the outcome is refused, or the failed attempt would not be counted.
func checkExitCode(ctx context.Context, tx *sql.Tx, deviceID, code string, now time.Time) (exitCheck, error) {
	var hash string
	var failed int
	var lockedUntil sql.NullTime
	if err := tx.QueryRowContext(ctx,
		`SELECT exit_code_hash, failed_exit_attempts, exit_locked_until FROM wall_devices WHERE id = ?`,
		deviceID,
	).Scan(&hash, &failed, &lockedUntil); err != nil {
		return exitCheck{}, err
	}

	if lockedUntil.Valid && lockedUntil.Time.After(now) {
		return exitCheck{outcome: exitLocked, lockedUntil: lockedUntil.Time}, nil
	}
	if auth.VerifyPassword(code, hash) {
		_, err := tx.ExecContext(ctx,
			`UPDATE wall_devices SET failed_exit_attempts = 0, exit_locked_until = NULL WHERE id = ?`, deviceID)
		return exitCheck{outcome: exitAllowed}, err
	}

	failed++
	if failed < exitMaxAttempts {
		_, err := tx.ExecContext(ctx,
			`UPDATE wall_devices SET failed_exit_attempts = ? WHERE id = ?`, failed, deviceID)
		return exitCheck{outcome: exitWrong, attemptsLeft: exitMaxAttempts - failed}, err
	}
	until := now.Add(exitLockout)
	_, err := tx.ExecContext(ctx,
		`UPDATE wall_devices SET failed_exit_attempts = 0, exit_locked_until = ? WHERE id = ?`, until, deviceID)
	return exitCheck{outcome: exitLocked, lockedUntil: until}, err
}

// decodeExitCode reads and validates the exit code a device sends. A malformed code is rejected before any
// attempt is counted.
func decodeExitCode(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req exitCodeRequest
	if !decodeJSON(w, r, &req) {
		return "", false
	}
	if !exitCodePattern.MatchString(req.ExitCode) {
		respondInvalid(w, &auth.ValidationError{Field: "exit_code", Reason: "invalid_format"})
		return "", false
	}
	return req.ExitCode, true
}

// respondExitRefused writes the response for an attempt that was not allowed.
func respondExitRefused(w http.ResponseWriter, c exitCheck, now time.Time) {
	if c.outcome == exitLocked {
		retryAfter := int(math.Ceil(c.lockedUntil.Sub(now).Seconds()))
		web.RespondError(w, http.StatusTooManyRequests, "exit_locked",
			"Too many attempts. Try again later.", map[string]any{"retry_after": retryAfter})
		return
	}
	web.RespondError(w, http.StatusForbidden, "exit_code_wrong",
		"Wrong exit code", map[string]any{"attempts_left": c.attemptsLeft})
}

// VerifyExitCode checks the exit code for the calling wall device. It changes nothing except the failure counter,
// and it never returns the code or its hash. Route it through RequireDevice.
func (h *Handlers) VerifyExitCode(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dev, ok := DeviceFromContext(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "Device authentication required", nil)
		return
	}
	code, ok := decodeExitCode(w, r)
	if !ok {
		return
	}
	now := h.clk.Now().UTC()

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "failed to begin transaction", err)
		return
	}
	defer tx.Rollback()

	check, err := checkExitCode(ctx, tx, dev.ID, code, now)
	if err != nil {
		h.internalError(w, "failed to check exit code", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "failed to commit transaction", err)
		return
	}
	if check.outcome != exitAllowed {
		respondExitRefused(w, check, now)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// UnpairDevice lets a wall device unpair itself after its exit code is entered. The device is revoked the same
// way a parent revokes it. The audit row names the parent who created the device, because audit actors are users,
// and it records that the device started the change.
func (h *Handlers) UnpairDevice(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dev, ok := DeviceFromContext(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "Device authentication required", nil)
		return
	}
	code, ok := decodeExitCode(w, r)
	if !ok {
		return
	}
	now := h.clk.Now().UTC()

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "failed to begin transaction", err)
		return
	}
	defer tx.Rollback()

	check, err := checkExitCode(ctx, tx, dev.ID, code, now)
	if err != nil {
		h.internalError(w, "failed to check exit code", err)
		return
	}
	if check.outcome != exitAllowed {
		if err := tx.Commit(); err != nil {
			h.internalError(w, "failed to commit transaction", err)
			return
		}
		respondExitRefused(w, check, now)
		return
	}

	var createdBy string
	err = tx.QueryRowContext(ctx,
		`SELECT created_by FROM wall_devices WHERE id = ? AND family_id = ? AND revoked_at IS NULL`,
		dev.ID, dev.FamilyID,
	).Scan(&createdBy)
	if errors.Is(err, sql.ErrNoRows) {
		web.RespondError(w, http.StatusUnauthorized, "device_invalid", "This wall device is not paired or was revoked", nil)
		return
	}
	if err != nil {
		h.internalError(w, "failed to find wall device", err)
		return
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE wall_devices SET revoked_at = ?, updated_at = ? WHERE family_id = ? AND id = ?`,
		now, now, dev.FamilyID, dev.ID,
	); err != nil {
		h.internalError(w, "failed to unpair wall device", err)
		return
	}
	if err := audit.Record(ctx, tx, dev.FamilyID, createdBy, "wall_device.unpaired", "wall_device", &dev.ID,
		map[string]any{"name": dev.Name, "initiated_by": "wall_device"}); err != nil {
		h.internalError(w, "failed to record audit event", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "failed to commit transaction", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
