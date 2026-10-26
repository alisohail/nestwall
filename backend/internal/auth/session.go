package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/ids"
)

// Session constants.
const (
	SessionExpiryDays = 7
	SessionLen        = 32
)

// Session represents a user session.
type Session struct {
	ID          string
	Token       string // Raw token; only available right after creation, never stored.
	HashedToken string // SHA-256 hex of Token; this is what the database stores.
	UserID      string
	Expiry      time.Time
	RevokedAt   *time.Time
	UserAgent   string
	CreatedAt   time.Time
}

// SessionWithUser combines a session with the associated user.
type SessionWithUser struct {
	Session Session
	User    User
}

// User represents a user record.
type User struct {
	ID                 string
	UserID             string
	Email              *string
	DisplayName        string
	IsChild            bool
	MustChangePassword bool
	DateOfBirth        *string
	Gender             *string
	Theme              string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// SessionMaxAge is the lifetime of a session.
const SessionMaxAge = SessionExpiryDays * 24 * time.Hour

// Errors for authentication failures.
var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrSessionInvalid     = errors.New("session not found")
	ErrSessionExpired     = errors.New("session expired")
	ErrSessionRevoked     = errors.New("session revoked")
)

// DB is the subset of *sql.Tx and *sql.DB used by the session helpers,
// so the same helper can run inside a transaction or directly on the database.
type DB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// GenerateSessionToken generates a new random session token.
func GenerateSessionToken() (string, error) {
	token := make([]byte, SessionLen)
	if _, err := rand.Read(token); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return hex.EncodeToString(token), nil
}

// HashToken returns the SHA-256 hex digest of a token. Session tokens are
// 256-bit random values, so a fast unsalted hash is enough and lets the
// server find a session by its hash.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CreateSession creates a session for a user and returns it with the raw token.
func CreateSession(ctx context.Context, db DB, clk clock.Clock, userID, userAgent string) (*Session, error) {
	token, err := GenerateSessionToken()
	if err != nil {
		return nil, err
	}

	now := clk.Now()
	session := &Session{
		ID:          ids.New(),
		Token:       token,
		HashedToken: HashToken(token),
		UserID:      userID,
		Expiry:      now.Add(SessionMaxAge),
		UserAgent:   userAgent,
		CreatedAt:   now,
	}

	_, err = db.ExecContext(ctx,
		`INSERT INTO sessions (id, hashed_token, user_id, expiry, user_agent, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		session.ID, session.HashedToken, userID, session.Expiry, userAgent, session.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert session: %w", err)
	}

	return session, nil
}

// ValidateSession looks up a raw session token and returns the session with its user.
// It fails if the session is unknown, revoked or expired.
func ValidateSession(ctx context.Context, db DB, clk clock.Clock, token string) (*SessionWithUser, error) {
	var s SessionWithUser

	err := db.QueryRowContext(ctx,
		`SELECT s.id, s.hashed_token, s.user_id, s.expiry, s.revoked_at, s.user_agent, s.created_at,
		u.id, u.user_id, u.email, u.display_name, u.is_child, u.must_change_password, u.date_of_birth, u.gender, u.theme, u.created_at, u.updated_at
		 FROM sessions s
		 JOIN users u ON s.user_id = u.id
		 WHERE s.hashed_token = ?`,
		HashToken(token),
	).Scan(
		&s.Session.ID, &s.Session.HashedToken, &s.Session.UserID, &s.Session.Expiry,
		&s.Session.RevokedAt, &s.Session.UserAgent, &s.Session.CreatedAt,
		&s.User.ID, &s.User.UserID, &s.User.Email, &s.User.DisplayName, &s.User.IsChild,
		&s.User.MustChangePassword, &s.User.DateOfBirth, &s.User.Gender, &s.User.Theme, &s.User.CreatedAt, &s.User.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrSessionInvalid
		}
		return nil, fmt.Errorf("query session: %w", err)
	}

	if s.Session.RevokedAt != nil {
		return nil, ErrSessionRevoked
	}
	if !clk.Now().Before(s.Session.Expiry) {
		return nil, ErrSessionExpired
	}

	return &s, nil
}

// RevokeSession revokes the session identified by a raw token. Unknown tokens are not an error.
func RevokeSession(ctx context.Context, db DB, clk clock.Clock, token string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = ? WHERE hashed_token = ? AND revoked_at IS NULL`,
		clk.Now(), HashToken(token),
	)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

// RevokeAllUserSessions revokes all active sessions for a user except the current one.
func RevokeAllUserSessions(ctx context.Context, db DB, clk clock.Clock, userID, currentSessionID string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND id != ? AND revoked_at IS NULL`,
		clk.Now(), userID, currentSessionID,
	)
	if err != nil {
		return fmt.Errorf("revoke user sessions: %w", err)
	}
	return nil
}
