package tasks

import (
	"context"
	"strconv"
)

// Keys in app_settings for the bonus task defaults. As with the regular defaults, a missing row means no default.
const (
	keyBonusPoints   = "bonus_defaults.points"
	keyBonusMode     = "bonus_defaults.claim_mode"
	keyBonusShowFrom = "bonus_defaults.show_from"
	keyBonusEnds     = "bonus_defaults.ends"
	keyBonusApproval = "bonus_defaults.approval_required"
)

// MaxBonusPoints is the largest number of points a bonus task can offer.
const MaxBonusPoints = 100000

// EndOfDay is the Ends value that closes a bonus task at the end of its day.
const EndOfDay = "end_of_day"

// BonusDefaults prefill a new bonus task. A nil field means no default, so the built-in value is used.
// ShowFrom and Ends are local HH:MM times; Ends may also be EndOfDay.
type BonusDefaults struct {
	Points           *int    `json:"points"`
	ClaimMode        *string `json:"claim_mode"`
	ShowFrom         *string `json:"show_from"`
	Ends             *string `json:"ends"`
	ApprovalRequired *bool   `json:"approval_required"`
}

// validate checks the ranges and formats of the bonus defaults.
func (b BonusDefaults) validate() error {
	if b.Points != nil && (*b.Points < 1 || *b.Points > MaxBonusPoints) {
		return &FieldError{Field: "bonus_points", Reason: "out_of_range"}
	}
	if b.ClaimMode != nil && *b.ClaimMode != "first_come" && *b.ClaimMode != "everyone_once" {
		return &FieldError{Field: "bonus_claim_mode", Reason: "invalid"}
	}
	if b.ShowFrom != nil && !clockPattern.MatchString(*b.ShowFrom) {
		return &FieldError{Field: "bonus_show_from", Reason: "invalid_time"}
	}
	if b.Ends != nil && *b.Ends != EndOfDay && !clockPattern.MatchString(*b.Ends) {
		return &FieldError{Field: "bonus_ends", Reason: "invalid_time"}
	}
	return nil
}

// settingRows returns the app_settings rows that hold the bonus defaults.
func (b BonusDefaults) settingRows() []setting {
	var rows []setting
	if b.Points != nil {
		rows = append(rows, setting{keyBonusPoints, strconv.Itoa(*b.Points)})
	}
	if b.ClaimMode != nil {
		rows = append(rows, setting{keyBonusMode, *b.ClaimMode})
	}
	if b.ShowFrom != nil {
		rows = append(rows, setting{keyBonusShowFrom, *b.ShowFrom})
	}
	if b.Ends != nil {
		rows = append(rows, setting{keyBonusEnds, *b.Ends})
	}
	if b.ApprovalRequired != nil {
		rows = append(rows, setting{keyBonusApproval, boolSetting(*b.ApprovalRequired)})
	}
	return rows
}

// LoadBonusDefaults reads the bonus task defaults of a family.
func LoadBonusDefaults(ctx context.Context, q querier, familyID string) (BonusDefaults, error) {
	var b BonusDefaults

	rows, err := q.QueryContext(ctx,
		`SELECT setting_key, setting_value FROM app_settings WHERE family_id = ?`, familyID)
	if err != nil {
		return b, err
	}
	defer rows.Close()

	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return b, err
		}
		switch key {
		case keyBonusPoints:
			b.Points, err = parseIntSetting(value)
		case keyBonusMode:
			mode := value
			b.ClaimMode = &mode
		case keyBonusShowFrom:
			from := value
			b.ShowFrom = &from
		case keyBonusEnds:
			ends := value
			b.Ends = &ends
		case keyBonusApproval:
			b.ApprovalRequired, err = parseBoolSetting(value)
		}
		if err != nil {
			return b, err
		}
	}
	return b, rows.Err()
}
