package tasks

import "fmt"

// Penalty units. A percent is taken from the task's points; points is a fixed amount.
const (
	unitPercent = "percent"
	unitPoints  = "points"
)

// Defaults are the optional prefill values for a new task. A nil field means "do not
// prefill" and is never zero, so an explicit 0 is kept as 0.
type Defaults struct {
	Points              *int    `json:"points"`
	ApprovalRequired    *bool   `json:"approval_required"`
	ExpiresAfterMinutes *int    `json:"expires_after_minutes"`
	PenaltyValue        *int    `json:"penalty_value"`
	PenaltyUnit         *string `json:"penalty_unit"`
}

// FieldError is a validation failure on one request field.
type FieldError struct {
	Field  string
	Reason string
}

func (e *FieldError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Reason)
}

// Validate checks the ranges and the penalty pair. A penalty value and unit are both set or both nil.
func (d Defaults) Validate() error {
	if d.Points != nil && *d.Points < 0 {
		return &FieldError{Field: "points", Reason: "negative"}
	}
	if d.ExpiresAfterMinutes != nil && *d.ExpiresAfterMinutes < 0 {
		return &FieldError{Field: "expires_after_minutes", Reason: "negative"}
	}
	if (d.PenaltyValue == nil) != (d.PenaltyUnit == nil) {
		return &FieldError{Field: "penalty", Reason: "incomplete"}
	}
	if d.PenaltyValue == nil {
		return nil
	}
	if *d.PenaltyValue < 0 {
		return &FieldError{Field: "penalty_value", Reason: "negative"}
	}
	switch *d.PenaltyUnit {
	case unitPercent:
		if *d.PenaltyValue > 100 {
			return &FieldError{Field: "penalty_value", Reason: "over_100"}
		}
	case unitPoints:
	default:
		return &FieldError{Field: "penalty_unit", Reason: "invalid"}
	}
	return nil
}

// PenaltyPoints converts a penalty to points. A percent is applied to the task's points and
// rounded half up, so 33% of 15 is 5 and 50% of 10 is 5. A points penalty is returned as is.
func PenaltyPoints(value int, unit string, points int) int {
	if unit == unitPoints {
		return value
	}
	return (points*value + 50) / 100
}

// ResolvePrefill returns the values a new task form starts with. For each field the order is
// template, then category, then family default, and the first non-nil value wins. The penalty
// value and unit come together from the first level that sets a penalty, so a unit is never
// paired with a value from another level. A nil result stays nil ("do not prefill").
func ResolvePrefill(template, category, family Defaults) Defaults {
	out := Defaults{
		Points:              first(template.Points, category.Points, family.Points),
		ApprovalRequired:    first(template.ApprovalRequired, category.ApprovalRequired, family.ApprovalRequired),
		ExpiresAfterMinutes: first(template.ExpiresAfterMinutes, category.ExpiresAfterMinutes, family.ExpiresAfterMinutes),
	}
	for _, level := range []Defaults{template, category, family} {
		if level.PenaltyValue != nil {
			out.PenaltyValue = level.PenaltyValue
			out.PenaltyUnit = level.PenaltyUnit
			break
		}
	}
	return out
}

// first returns the first non-nil pointer, or nil when all are nil.
func first[T any](values ...*T) *T {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}
