// Package notifypolicy holds the family notification settings and the single function that writes
// a notification. The settings are one switch per channel for the whole family, plus the email
// digest and the email template. The package has no HTTP code, so the auth, badges, rewards and
// backup packages can call it without an import cycle.
package notifypolicy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"nestwall/backend/internal/platform/ids"
)

// Querier is the part of *sql.DB and *sql.Tx that the package needs.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Settings are the family notification switches. Browser delivery is decided by the browser, so the
// server only stores the switch and reports it to the client.
type Settings struct {
	InApp         bool   `json:"in_app"`
	Browser       bool   `json:"browser"`
	Email         bool   `json:"email"`
	EmailDigest   bool   `json:"email_digest"`
	DigestMinutes int    `json:"digest_minutes"`
	EmailTemplate string `json:"email_template"`
}

// ShippedSettings are the values for a family that has not changed them. Email starts off, because
// it also needs the mail server to be set up. The digest is off too, so each notice is sent alone
// until a parent turns it on. When it is on, it goes out every 15 minutes unless changed.
func ShippedSettings() Settings {
	return Settings{InApp: true, Browser: true, Email: false, EmailDigest: false, DigestMinutes: 15, EmailTemplate: ""}
}

// Load returns the family settings, or the shipped values when the family has not saved any.
func Load(ctx context.Context, q Querier, familyID string) (Settings, error) {
	s := ShippedSettings()
	err := q.QueryRowContext(ctx,
		`SELECT in_app, browser, email, email_digest, digest_minutes, email_template
		   FROM notification_settings WHERE family_id = ?`, familyID,
	).Scan(&s.InApp, &s.Browser, &s.Email, &s.EmailDigest, &s.DigestMinutes, &s.EmailTemplate)
	if errors.Is(err, sql.ErrNoRows) {
		return ShippedSettings(), nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("load notification settings: %w", err)
	}
	return s, nil
}

// Save stores the family settings. It leaves the digest send time alone.
func Save(ctx context.Context, q Querier, familyID string, s Settings, now time.Time) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO notification_settings (family_id, in_app, browser, email, email_digest, digest_minutes, email_template, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(family_id) DO UPDATE SET
		        in_app = excluded.in_app, browser = excluded.browser, email = excluded.email,
		        email_digest = excluded.email_digest, digest_minutes = excluded.digest_minutes,
		        email_template = excluded.email_template, updated_at = excluded.updated_at`,
		familyID, s.InApp, s.Browser, s.Email, s.EmailDigest, s.DigestMinutes, s.EmailTemplate, now,
	)
	if err != nil {
		return fmt.Errorf("save notification settings: %w", err)
	}
	return nil
}

// Notice is one notification to write. SubjectID is the member the notice is about, if any.
type Notice struct {
	FamilyID    string
	RecipientID string
	Kind        string
	SubjectID   string
	Body        string
}

// Notify writes a notification for one member when the family's in-app switch is on. It runs in the
// caller's transaction, so a notice is written with the change it reports. When email is on, the
// notice is also marked for email. Only parents receive email; children get in-app and browser
// notices. It returns false, and writes nothing, when the in-app switch is off.
func Notify(ctx context.Context, q Querier, n Notice, now time.Time) (bool, error) {
	s, err := Load(ctx, q, n.FamilyID)
	if err != nil {
		return false, err
	}
	if !s.InApp {
		return false, nil
	}
	var isChild bool
	if err := q.QueryRowContext(ctx,
		`SELECT u.is_child FROM family_members m JOIN users u ON u.id = m.user_id
		  WHERE m.id = ? AND m.family_id = ?`, n.RecipientID, n.FamilyID,
	).Scan(&isChild); err != nil {
		return false, fmt.Errorf("load recipient: %w", err)
	}
	pending := s.Email && !isChild
	// A push is sent later by the push worker, so the transaction never waits on the network.
	pushPending := s.Browser
	var subject any
	if n.SubjectID != "" {
		subject = n.SubjectID
	}
	if _, err := q.ExecContext(ctx,
		`INSERT INTO notifications (id, family_id, recipient_id, kind, subject_member_id, body, email_pending, push_pending, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ids.New(), n.FamilyID, n.RecipientID, n.Kind, subject, n.Body, pending, pushPending, now,
	); err != nil {
		return false, fmt.Errorf("insert notification: %w", err)
	}
	return true, nil
}
