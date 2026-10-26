package email

import (
	"context"
	"database/sql"
	"errors"
	"net/mail"
	"strings"
)

const (
	defaultPort         = 587
	defaultSendLimit    = 30
	defaultFromName     = "NestWall"
	maxHostLength       = 253
	maxAddressLength    = 254
	maxUsernameLength   = 254
	maxPasswordLength   = 512
	maxSendLimitPerHour = 1000
)

// Settings is what the API returns. The password is never included, only whether one is saved.
// Ready is true when email can be sent: the host and sender are set. There is no separate on/off switch.
type Settings struct {
	Host             string `json:"host"`
	Port             int    `json:"port"`
	Security         string `json:"security"`
	Username         string `json:"username"`
	FromAddress      string `json:"from_address"`
	FromName         string `json:"from_name"`
	SendLimitPerHour int    `json:"send_limit_per_hour"`
	PasswordSet      bool   `json:"password_set"`
	Ready            bool   `json:"ready"`
}

// UpdateRequest is the body of PUT /api/v1/settings/email. A missing password keeps the saved one.
// An empty string clears it. Any other value replaces it.
type UpdateRequest struct {
	Host             string  `json:"host"`
	Port             int     `json:"port"`
	Security         string  `json:"security"`
	Username         string  `json:"username"`
	Password         *string `json:"password"`
	FromAddress      string  `json:"from_address"`
	FromName         string  `json:"from_name"`
	SendLimitPerHour int     `json:"send_limit_per_hour"`
}

// stored is one row of smtp_settings. Sealed holds the encrypted password, if any.
type stored struct {
	Settings
	Sealed string
}

// ValidationError is a client error with a code and a message the UI can show.
type ValidationError struct {
	Message string
}

func (e ValidationError) Error() string { return e.Message }

// normalize trims the text fields and fills defaults. It returns a ValidationError when a value
// is out of range. The result is ready to store.
func normalize(req UpdateRequest) (UpdateRequest, error) {
	req.Host = strings.TrimSpace(req.Host)
	req.Username = strings.TrimSpace(req.Username)
	req.FromAddress = strings.ToLower(strings.TrimSpace(req.FromAddress))
	req.FromName = strings.TrimSpace(req.FromName)
	if req.FromName == "" {
		req.FromName = defaultFromName
	}
	if req.Port == 0 {
		req.Port = defaultPort
	}
	if req.SendLimitPerHour == 0 {
		req.SendLimitPerHour = defaultSendLimit
	}
	if req.Port < 1 || req.Port > 65535 {
		return req, ValidationError{"Port must be between 1 and 65535"}
	}
	if req.Security != securityTLS && req.Security != securitySTARTTLS {
		return req, ValidationError{"Security must be STARTTLS or SSL/TLS"}
	}
	if req.SendLimitPerHour < 1 || req.SendLimitPerHour > maxSendLimitPerHour {
		return req, ValidationError{"Send limit must be between 1 and 1000 per hour"}
	}
	if len(req.Host) > maxHostLength || strings.ContainsAny(req.Host, " \t/@") {
		return req, ValidationError{"Enter the server host name, without a scheme or port"}
	}
	if len(req.Username) > maxUsernameLength {
		return req, ValidationError{"Username is too long"}
	}
	if req.Password != nil && len(*req.Password) > maxPasswordLength {
		return req, ValidationError{"Password is too long"}
	}
	if req.FromAddress != "" {
		if len(req.FromAddress) > maxAddressLength {
			return req, ValidationError{"Enter a valid sender address"}
		}
		addr, err := mail.ParseAddress(req.FromAddress)
		if err != nil || addr.Address != req.FromAddress {
			return req, ValidationError{"Enter a valid sender address"}
		}
	}
	return req, nil
}

// ready reports whether email can be sent. It does not check the password, which some relays do not need.
func ready(s Settings) bool {
	return s.Host != "" && s.FromAddress != ""
}

// loadStored reads the family row. Without a row it returns the defaults.
func loadStored(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, familyID string) (stored, error) {
	s := stored{Settings: Settings{Port: defaultPort, Security: securitySTARTTLS, FromName: defaultFromName, SendLimitPerHour: defaultSendLimit}}
	err := q.QueryRowContext(ctx,
		`SELECT host, port, security, username, password_sealed, from_address, from_name, send_limit_per_hour
		   FROM smtp_settings WHERE family_id = ?`,
		familyID,
	).Scan(&s.Host, &s.Port, &s.Security, &s.Username, &s.Sealed, &s.FromAddress, &s.FromName, &s.SendLimitPerHour)
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	s.PasswordSet = s.Sealed != ""
	s.Ready = ready(s.Settings)
	return s, nil
}

// fromRequest builds the stored shape from a normalized request. The password is set by the caller.
func fromRequest(req UpdateRequest) Settings {
	s := Settings{
		Host:             req.Host,
		Port:             req.Port,
		Security:         req.Security,
		Username:         req.Username,
		FromAddress:      req.FromAddress,
		FromName:         req.FromName,
		SendLimitPerHour: req.SendLimitPerHour,
	}
	s.Ready = ready(s)
	return s
}
