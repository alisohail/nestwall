package auth

import (
	"errors"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata" // the alpine runtime image has no system zoneinfo
	"unicode/utf8"

	sqlite3 "github.com/mutecomm/go-sqlcipher/v4"
)

// Input rules shared by setup, member creation and the family name.
const (
	MinPasswordLength = 8
	MaxPasswordLength = 256
	MaxNameLength     = 80
)

var (
	userIDPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]{3,32}$`)
	pinPattern    = regexp.MustCompile(`^[0-9]{4,8}$`)
	colourPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	localePattern = regexp.MustCompile(`^[a-zA-Z]{2,3}(-[a-zA-Z]{2,4})?$`)
	emailPattern  = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
)

// ValidationError describes an invalid input field. Handlers return it as a
// 400 response with code "validation_failed" and the field and reason as params.
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Reason
}

// Params returns the error details for the response envelope.
func (e *ValidationError) Params() map[string]any {
	return map[string]any{"field": e.Field, "reason": e.Reason}
}

// ValidatePassword checks a new password against the length rules.
func ValidatePassword(password string) error {
	if len(password) < MinPasswordLength {
		return &ValidationError{Field: "password", Reason: "too_short"}
	}
	if len(password) > MaxPasswordLength {
		return &ValidationError{Field: "password", Reason: "too_long"}
	}
	return nil
}

// ValidateUserID checks the format of a sign-in username.
func ValidateUserID(userID string) error {
	if !userIDPattern.MatchString(userID) {
		return &ValidationError{Field: "user_id", Reason: "invalid_format"}
	}
	return nil
}

// ValidateNewUser checks the credentials and display name for a new account.
func ValidateNewUser(userID, password, displayName string) error {
	if err := ValidateUserID(userID); err != nil {
		return err
	}
	if err := ValidatePassword(password); err != nil {
		return err
	}
	name := strings.TrimSpace(displayName)
	if name == "" || utf8.RuneCountInString(name) > MaxNameLength {
		return &ValidationError{Field: "display_name", Reason: "invalid_length"}
	}
	return nil
}

// ValidateName checks a family or other free-text name.
func ValidateName(field, value string) error {
	name := strings.TrimSpace(value)
	if name == "" || utf8.RuneCountInString(name) > MaxNameLength {
		return &ValidationError{Field: field, Reason: "invalid_length"}
	}
	return nil
}

// ValidatePIN checks an optional child PIN (4 to 8 digits).
func ValidatePIN(pin string) error {
	if !pinPattern.MatchString(pin) {
		return &ValidationError{Field: "pin", Reason: "invalid_format"}
	}
	return nil
}

// MaxEmailLength is the longest accepted email address (RFC 5321 path limit).
const MaxEmailLength = 254

// ValidateEmail checks an email address. The caller stores it lower-cased.
func ValidateEmail(email string) error {
	if len(email) > MaxEmailLength || !emailPattern.MatchString(email) {
		return &ValidationError{Field: "email", Reason: "invalid_format"}
	}
	return nil
}

// ValidateBirthDate checks a date of birth in YYYY-MM-DD form, not in the future and not before 1900.
func ValidateBirthDate(value string, today time.Time) error {
	day, err := time.Parse(time.DateOnly, value)
	if err != nil || day.Year() < 1900 || day.After(today) {
		return &ValidationError{Field: "date_of_birth", Reason: "invalid_date"}
	}
	return nil
}

// ValidateGender checks the optional gender choice.
func ValidateGender(value string) error {
	switch value {
	case "female", "male", "prefer_not_to_say":
		return nil
	}
	return &ValidationError{Field: "gender", Reason: "invalid_value"}
}

// ValidateColour checks a hex colour such as #6366F1.
func ValidateColour(colour string) error {
	if !colourPattern.MatchString(colour) {
		return &ValidationError{Field: "colour", Reason: "invalid_format"}
	}
	return nil
}

// ValidateTimezone checks that a value is a valid IANA timezone.
func ValidateTimezone(tz string) error {
	if _, err := time.LoadLocation(tz); err != nil {
		return &ValidationError{Field: "timezone", Reason: "invalid_timezone"}
	}
	return nil
}

// ValidateLocale checks a BCP 47 style locale such as en or en-US.
func ValidateLocale(locale string) error {
	if !localePattern.MatchString(locale) {
		return &ValidationError{Field: "locale", Reason: "invalid_format"}
	}
	return nil
}

// IsUniqueViolation reports whether err is a SQLite unique constraint violation.
func IsUniqueViolation(err error) bool {
	var sqliteErr sqlite3.Error
	return errors.As(err, &sqliteErr) && sqliteErr.ExtendedCode == sqlite3.ErrConstraintUnique
}
