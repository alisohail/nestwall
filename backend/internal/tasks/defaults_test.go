package tasks

import (
	"errors"
	"strings"
	"testing"
)

func intPtr(v int) *int       { return &v }
func boolPtr(v bool) *bool    { return &v }
func strPtr(v string) *string { return &v }

// expectFieldError checks that err is a FieldError with the given field and reason.
func expectFieldError(t *testing.T, err error, field, reason string) {
	t.Helper()
	var fe *FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("error = %v, want FieldError %s/%s", err, field, reason)
	}
	if fe.Field != field || fe.Reason != reason {
		t.Fatalf("field error = %s/%s, want %s/%s", fe.Field, fe.Reason, field, reason)
	}
}

// TestPenaltyPointsRoundsHalfUp checks the percent penalty rounding from the spec.
func TestPenaltyPointsRoundsHalfUp(t *testing.T) {
	cases := []struct {
		name   string
		value  int
		unit   string
		points int
		want   int
	}{
		{"33 percent of 15 is 5", 33, unitPercent, 15, 5},
		{"50 percent of 10 is 5", 50, unitPercent, 10, 5},
		{"50 percent of 5 rounds 2.5 up to 3", 50, unitPercent, 5, 3},
		{"100 percent takes all points", 100, unitPercent, 7, 7},
		{"0 percent takes nothing", 0, unitPercent, 15, 0},
		{"points unit is a fixed amount", 33, unitPoints, 15, 33},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PenaltyPoints(tc.value, tc.unit, tc.points); got != tc.want {
				t.Fatalf("PenaltyPoints(%d, %s, %d) = %d, want %d", tc.value, tc.unit, tc.points, got, tc.want)
			}
		})
	}
}

// TestDefaultsValidate checks the ranges and the penalty pair.
func TestDefaultsValidate(t *testing.T) {
	cases := []struct {
		name   string
		d      Defaults
		field  string // empty means valid
		reason string
	}{
		{name: "all nil is valid", d: Defaults{}},
		{name: "zero points is valid", d: Defaults{Points: intPtr(0)}},
		{name: "points unit may exceed 100", d: Defaults{PenaltyValue: intPtr(150), PenaltyUnit: strPtr(unitPoints)}},
		{name: "percent up to 100 is valid", d: Defaults{PenaltyValue: intPtr(100), PenaltyUnit: strPtr(unitPercent)}},
		{name: "negative points", d: Defaults{Points: intPtr(-1)}, field: "points", reason: "negative"},
		{name: "negative expiry", d: Defaults{ExpiresAfterMinutes: intPtr(-5)}, field: "expires_after_minutes", reason: "negative"},
		{name: "penalty value without unit", d: Defaults{PenaltyValue: intPtr(5)}, field: "penalty", reason: "incomplete"},
		{name: "penalty unit without value", d: Defaults{PenaltyUnit: strPtr(unitPoints)}, field: "penalty", reason: "incomplete"},
		{name: "negative penalty", d: Defaults{PenaltyValue: intPtr(-1), PenaltyUnit: strPtr(unitPoints)}, field: "penalty_value", reason: "negative"},
		{name: "percent over 100", d: Defaults{PenaltyValue: intPtr(120), PenaltyUnit: strPtr(unitPercent)}, field: "penalty_value", reason: "over_100"},
		{name: "unknown unit", d: Defaults{PenaltyValue: intPtr(5), PenaltyUnit: strPtr("pounds")}, field: "penalty_unit", reason: "invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.d.Validate()
			if tc.field == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			expectFieldError(t, err, tc.field, tc.reason)
		})
	}
}

// TestPartOfDayValidate checks the HH:MM format and the window order.
func TestPartOfDayValidate(t *testing.T) {
	valid := PartOfDayTimes{MorningStart: "05:00", AfterSchoolStart: "12:00", EveningStart: "17:00"}
	if err := valid.validate(); err != nil {
		t.Fatalf("built-in times rejected: %v", err)
	}

	cases := map[string]PartOfDayTimes{
		"single digit hour": {MorningStart: "5:00", AfterSchoolStart: "12:00", EveningStart: "17:00"},
		"hour 24":           {MorningStart: "24:00", AfterSchoolStart: "12:00", EveningStart: "17:00"},
		"empty":             {},
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			expectFieldError(t, p.validate(), "part_of_day", "invalid_time")
		})
	}

	out := map[string]PartOfDayTimes{
		"morning after after school": {MorningStart: "13:00", AfterSchoolStart: "12:00", EveningStart: "17:00"},
		"equal windows":              {MorningStart: "12:00", AfterSchoolStart: "12:00", EveningStart: "17:00"},
		"evening before after school": {MorningStart: "05:00", AfterSchoolStart: "18:00", EveningStart: "17:00"},
	}
	for name, p := range out {
		t.Run(name, func(t *testing.T) {
			expectFieldError(t, p.validate(), "part_of_day", "out_of_order")
		})
	}
}

// TestResolvePrefill checks the template, category, family order and the penalty pair rule.
func TestResolvePrefill(t *testing.T) {
	family := Defaults{
		Points:              intPtr(3),
		ApprovalRequired:    boolPtr(true),
		ExpiresAfterMinutes: intPtr(1440),
		PenaltyValue:        intPtr(10),
		PenaltyUnit:         strPtr(unitPoints),
	}
	category := Defaults{Points: intPtr(5), PenaltyValue: intPtr(50), PenaltyUnit: strPtr(unitPercent)}
	template := Defaults{Points: intPtr(15)}

	got := ResolvePrefill(template, category, family)
	if *got.Points != 15 {
		t.Errorf("points = %d, want 15 from the template", *got.Points)
	}
	if got.ApprovalRequired == nil || !*got.ApprovalRequired {
		t.Errorf("approval = %v, want true from the family default", got.ApprovalRequired)
	}
	if *got.ExpiresAfterMinutes != 1440 {
		t.Errorf("expiry = %d, want 1440 from the family default", *got.ExpiresAfterMinutes)
	}
	// The category has a penalty, so its pair is used as a whole, not the family's value.
	if *got.PenaltyValue != 50 || *got.PenaltyUnit != unitPercent {
		t.Errorf("penalty = %d %s, want 50 percent from the category", *got.PenaltyValue, *got.PenaltyUnit)
	}

	// The template's penalty wins over the category's and the family's.
	tplPenalty := ResolvePrefill(Defaults{PenaltyValue: intPtr(20), PenaltyUnit: strPtr(unitPoints)}, category, family)
	if *tplPenalty.PenaltyValue != 20 || *tplPenalty.PenaltyUnit != unitPoints {
		t.Errorf("penalty = %d %s, want 20 points from the template", *tplPenalty.PenaltyValue, *tplPenalty.PenaltyUnit)
	}

	// Nothing set at any level stays nil, so the form does not prefill.
	empty := ResolvePrefill(Defaults{}, Defaults{}, Defaults{})
	if empty.Points != nil || empty.ApprovalRequired != nil || empty.ExpiresAfterMinutes != nil ||
		empty.PenaltyValue != nil || empty.PenaltyUnit != nil {
		t.Errorf("empty prefill = %+v, want all nil", empty)
	}

	// An explicit zero is a value, not "unset".
	zero := ResolvePrefill(Defaults{Points: intPtr(0)}, Defaults{Points: intPtr(5)}, Defaults{})
	if zero.Points == nil || *zero.Points != 0 {
		t.Errorf("points = %v, want an explicit 0 from the template", zero.Points)
	}
}

// TestCategoryRequestNormalise checks trimming and the field rules for categories.
func TestCategoryRequestNormalise(t *testing.T) {
	valid := func() categoryRequest {
		return categoryRequest{Name: "  Garden  ", Emoji: "\U0001F331", Colour: "#16A34A"}
	}

	req := valid()
	if err := req.normalise(); err != nil {
		t.Fatalf("normalise(valid) = %v", err)
	}
	if req.Name != "Garden" {
		t.Errorf("name = %q, want trimmed", req.Name)
	}

	cases := map[string]struct {
		mutate func(*categoryRequest)
		field  string
		reason string
	}{
		"blank name":      {func(r *categoryRequest) { r.Name = "   " }, "name", "required"},
		"long name":       {func(r *categoryRequest) { r.Name = strings.Repeat("a", 81) }, "name", "too_long"},
		"blank emoji":     {func(r *categoryRequest) { r.Emoji = "" }, "emoji", "required"},
		"text emoji":      {func(r *categoryRequest) { r.Emoji = "OK" }, "emoji", "invalid"},
		"two emoji":       {func(r *categoryRequest) { r.Emoji = "\U0001F642\U0001F642" }, "emoji", "invalid"},
		"emoji and text":  {func(r *categoryRequest) { r.Emoji = "\U0001F9F9 tidy" }, "emoji", "invalid"},
		"keycap emoji":    {func(r *categoryRequest) { r.Emoji = "1\uFE0F\u20E3" }, "emoji", "invalid"},
		"bad colour":      {func(r *categoryRequest) { r.Colour = "red" }, "colour", "invalid"},
		"short colour":    {func(r *categoryRequest) { r.Colour = "#FFF" }, "colour", "invalid"},
		"negative order":  {func(r *categoryRequest) { r.SortOrder = intPtr(-1) }, "sort_order", "negative"},
		"bad badge key":   {func(r *categoryRequest) { r.BadgeKey = strPtr("Tidy Pro") }, "badge_key", "invalid"},
		"negative points": {func(r *categoryRequest) { r.Points = intPtr(-2) }, "points", "negative"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := valid()
			tc.mutate(&r)
			expectFieldError(t, r.normalise(), tc.field, tc.reason)
		})
	}

	// An 80-character name is allowed.
	edge := valid()
	edge.Name = strings.Repeat("a", 80)
	if err := edge.normalise(); err != nil {
		t.Errorf("80-character name rejected: %v", err)
	}

	// One emoji is allowed: seeded pictures, skin tones, joined sequences, flags and symbols.
	oneEmoji := []string{
		"\U0001F9F9", "\U0001FAA5", "\U0001F468\u200D\U0001F469\u200D\U0001F467",
		"\U0001F9D1\U0001F3FD\u200D\U0001F373", "\U0001F1EC\U0001F1E7", "\u2764\uFE0F", "\U0001F44D\U0001F3FD",
	}
	for _, emoji := range oneEmoji {
		r := valid()
		r.Emoji = emoji
		if err := r.normalise(); err != nil {
			t.Errorf("emoji %q rejected: %v", emoji, err)
		}
	}
}
