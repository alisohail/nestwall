package tasks

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strconv"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/platform/web"
)

// Keys in app_settings. A value is stored only when it is set; for defaults a missing row
// means NULL (do not prefill).
const (
	keyPoints       = "defaults.points"
	keyApproval     = "defaults.approval_required"
	keyMissedValue  = "defaults.missed_after_value"
	keyMissedUnit   = "defaults.missed_after_unit"
	keyPenaltyValue = "defaults.penalty_value"
	keyPenaltyUnit  = "defaults.penalty_unit"
	keyMorning      = "part_of_day.morning_start"
	keyAfterSchool  = "part_of_day.after_school_start"
	keyEvening      = "part_of_day.evening_start"
)

// PartOfDayTimes are the local start times of the windows. Morning runs from its start to
// the after-school start, After school to the evening start, and Evening to bedtime.
type PartOfDayTimes struct {
	MorningStart     string `json:"morning_start"`
	AfterSchoolStart string `json:"after_school_start"`
	EveningStart     string `json:"evening_start"`
}

// builtInPartOfDay matches the mock: Morning 5:00 am to 12:00 pm, After school 12:00 pm to
// 5:00 pm, Evening 5:00 pm to bedtime.
var builtInPartOfDay = PartOfDayTimes{MorningStart: "05:00", AfterSchoolStart: "12:00", EveningStart: "17:00"}

var clockPattern = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// validate checks that every time is HH:MM and that the windows are in order.
func (p PartOfDayTimes) validate() error {
	for _, v := range []string{p.MorningStart, p.AfterSchoolStart, p.EveningStart} {
		if !clockPattern.MatchString(v) {
			return &FieldError{Field: "part_of_day", Reason: "invalid_time"}
		}
	}
	if !(p.MorningStart < p.AfterSchoolStart && p.AfterSchoolStart < p.EveningStart) {
		return &FieldError{Field: "part_of_day", Reason: "out_of_order"}
	}
	return nil
}

// settingsResponse is the family task settings. Version is the value sent in ETag and If-Match.
type settingsResponse struct {
	Defaults      familyDefaults `json:"defaults"`
	BonusDefaults BonusDefaults  `json:"bonus_defaults"`
	PartOfDay     PartOfDayTimes `json:"part_of_day"`
	Version       int            `json:"version"`
}

// settingsRequest replaces the whole settings object on PUT. An omitted defaults object clears them.
type settingsRequest struct {
	Defaults      familyDefaults `json:"defaults"`
	BonusDefaults BonusDefaults  `json:"bonus_defaults"`
	PartOfDay     PartOfDayTimes `json:"part_of_day"`
}

// setting is one row of app_settings.
type setting struct{ key, value string }

// familyDefaults are the family defaults. MissedAfter is the cut-off for tasks with no grace period; nil means no expiry.
type familyDefaults struct {
	Points           *int         `json:"points"`
	ApprovalRequired *bool        `json:"approval_required"`
	MissedAfter      *MissedAfter `json:"missed_after"`
	PenaltyValue     *int         `json:"penalty_value"`
	PenaltyUnit      *string      `json:"penalty_unit"`
}

// defaults returns the prefill values for a new task. The cut-off is not prefilled.
func (f familyDefaults) defaults() Defaults {
	return Defaults{Points: f.Points, ApprovalRequired: f.ApprovalRequired, PenaltyValue: f.PenaltyValue, PenaltyUnit: f.PenaltyUnit}
}

// validate checks the prefill values and the cut-off.
func (f familyDefaults) validate() error {
	if err := f.defaults().Validate(); err != nil {
		return err
	}
	if f.MissedAfter != nil {
		return f.MissedAfter.validate()
	}
	return nil
}

// loadSettings reads the family defaults and part-of-day times. The times fall back to the
// built-in values when no row is stored.
func loadSettings(ctx context.Context, q querier, familyID string) (familyDefaults, PartOfDayTimes, error) {
	pod := builtInPartOfDay
	var d familyDefaults
	var missedValue, missedUnit string

	rows, err := q.QueryContext(ctx,
		`SELECT setting_key, setting_value FROM app_settings WHERE family_id = ?`, familyID)
	if err != nil {
		return d, pod, err
	}
	defer rows.Close()

	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return d, pod, err
		}
		switch key {
		case keyPoints:
			d.Points, err = parseIntSetting(value)
		case keyApproval:
			d.ApprovalRequired, err = parseBoolSetting(value)
		case keyMissedValue:
			missedValue = value
		case keyMissedUnit:
			missedUnit = value
		case keyPenaltyValue:
			d.PenaltyValue, err = parseIntSetting(value)
		case keyPenaltyUnit:
			d.PenaltyUnit = &value
		case keyMorning:
			pod.MorningStart = value
		case keyAfterSchool:
			pod.AfterSchoolStart = value
		case keyEvening:
			pod.EveningStart = value
		}
		if err != nil {
			return d, pod, err
		}
	}
	if err := rows.Err(); err != nil {
		return d, pod, err
	}
	if missedValue != "" && missedUnit != "" {
		n, err := strconv.Atoi(missedValue)
		if err != nil {
			return d, pod, err
		}
		d.MissedAfter = &MissedAfter{Value: n, Unit: missedUnit}
	}
	return d, pod, nil
}

// saveSettings replaces the stored settings of the family. It runs inside the caller's transaction.
func saveSettings(ctx context.Context, tx *sql.Tx, familyID string, d familyDefaults, b BonusDefaults, pod PartOfDayTimes) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM app_settings WHERE family_id = ?`, familyID); err != nil {
		return err
	}

	var pairs []setting
	if d.Points != nil {
		pairs = append(pairs, setting{keyPoints, strconv.Itoa(*d.Points)})
	}
	if d.ApprovalRequired != nil {
		pairs = append(pairs, setting{keyApproval, boolSetting(*d.ApprovalRequired)})
	}
	if d.MissedAfter != nil {
		pairs = append(pairs,
			setting{keyMissedValue, strconv.Itoa(d.MissedAfter.Value)},
			setting{keyMissedUnit, d.MissedAfter.Unit},
		)
	}
	if d.PenaltyValue != nil {
		pairs = append(pairs, setting{keyPenaltyValue, strconv.Itoa(*d.PenaltyValue)})
	}
	if d.PenaltyUnit != nil {
		pairs = append(pairs, setting{keyPenaltyUnit, *d.PenaltyUnit})
	}
	pairs = append(pairs, b.settingRows()...)
	pairs = append(pairs,
		setting{keyMorning, pod.MorningStart},
		setting{keyAfterSchool, pod.AfterSchoolStart},
		setting{keyEvening, pod.EveningStart},
	)

	for _, p := range pairs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO app_settings (family_id, setting_key, setting_value) VALUES (?, ?, ?)`,
			familyID, p.key, p.value,
		); err != nil {
			return err
		}
	}
	return nil
}

// familyVersion returns the settings version of the family.
func familyVersion(ctx context.Context, q querier, familyID string) (int, error) {
	var v int
	err := q.QueryRowContext(ctx,
		`SELECT task_settings_version FROM families WHERE id = ?`, familyID).Scan(&v)
	return v, err
}

// GetSettings returns the family defaults and part-of-day times, with the version in ETag.
func (h *Handlers) GetSettings(w http.ResponseWriter, r *http.Request) {
	familyID, _, ok := h.memberFamily(w, r)
	if !ok {
		return
	}

	ctx := r.Context()
	d, pod, err := loadSettings(ctx, h.db, familyID)
	if err != nil {
		h.internalError(w, "load settings", err)
		return
	}
	bonus, err := LoadBonusDefaults(ctx, h.db, familyID)
	if err != nil {
		h.internalError(w, "load bonus defaults", err)
		return
	}
	version, err := familyVersion(ctx, h.db, familyID)
	if err != nil {
		h.internalError(w, "load settings version", err)
		return
	}

	setETag(w, version)
	web.RespondJSON(w, http.StatusOK, settingsResponse{Defaults: d, BonusDefaults: bonus, PartOfDay: pod, Version: version})
}

// UpdateSettings replaces the family defaults and part-of-day times. If-Match must match the
// current version when sent. Each successful write bumps the version.
func (h *Handlers) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	familyID, userID, ok := h.memberFamily(w, r)
	if !ok {
		return
	}

	var req settingsRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if err := req.Defaults.validate(); err != nil {
		respondValidation(w, err)
		return
	}
	if err := req.BonusDefaults.validate(); err != nil {
		respondValidation(w, err)
		return
	}
	if err := req.PartOfDay.validate(); err != nil {
		respondValidation(w, err)
		return
	}

	ctx := r.Context()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "begin update settings", err)
		return
	}
	defer tx.Rollback()

	current, err := familyVersion(ctx, tx, familyID)
	if err != nil {
		h.internalError(w, "load settings version", err)
		return
	}
	if !checkIfMatch(w, r, current) {
		return
	}

	if err := saveSettings(ctx, tx, familyID, req.Defaults, req.BonusDefaults, req.PartOfDay); err != nil {
		h.internalError(w, "save settings", err)
		return
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE families SET task_settings_version = task_settings_version + 1 WHERE id = ?`, familyID,
	); err != nil {
		h.internalError(w, "bump settings version", err)
		return
	}

	if err := audit.Record(ctx, tx, familyID, userID, "task_settings.updated", "family", &familyID,
		map[string]any{"scope": "task_settings"}); err != nil {
		h.internalError(w, "audit update settings", err)
		return
	}

	d, pod, err := loadSettings(ctx, tx, familyID)
	if err != nil {
		h.internalError(w, "reload settings", err)
		return
	}
	bonus, err := LoadBonusDefaults(ctx, tx, familyID)
	if err != nil {
		h.internalError(w, "reload bonus defaults", err)
		return
	}
	version, err := familyVersion(ctx, tx, familyID)
	if err != nil {
		h.internalError(w, "reload settings version", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit update settings", err)
		return
	}

	setETag(w, version)
	web.RespondJSON(w, http.StatusOK, settingsResponse{Defaults: d, BonusDefaults: bonus, PartOfDay: pod, Version: version})
}

func parseIntSetting(value string) (*int, error) {
	n, err := strconv.Atoi(value)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

func parseBoolSetting(value string) (*bool, error) {
	switch value {
	case "1":
		b := true
		return &b, nil
	case "0":
		b := false
		return &b, nil
	}
	return nil, errors.New("invalid boolean setting: " + value)
}

func boolSetting(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
