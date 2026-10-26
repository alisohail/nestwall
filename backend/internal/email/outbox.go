package email

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"nestwall/backend/internal/diagnostics"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/ids"
)

const (
	outboxBatch = 50
	maxAttempts = 5
	claimLease  = 5 * time.Minute
	capDeferral = 5 * time.Minute
	maxQueueAge = 24 * time.Hour
	// DefaultPollInterval is how often the outbox looks for due messages. Reset links need to arrive
	// within seconds, so this is much shorter than the scheduler's tick.
	DefaultPollInterval = 5 * time.Second
	maxErrorChars       = 200
	timeLayout          = "2006-01-02T15:04:05.000Z"
)

// backoff is the wait before each retry. After the last one, the message is given up on.
var backoff = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour}

// ErrNotReady is returned by Enqueue while email is not set up. Nothing is queued.
var ErrNotReady = errors.New("email is not set up")

// Outbox queues email and delivers it in the background. Each family's hourly send limit applies.
type Outbox struct {
	db     *sql.DB
	dbKey  string
	clock  clock.Clock
	log    *slog.Logger
	Mailer Mailer
}

// NewOutbox returns an outbox. The zero Mailer value is ready to use.
func NewOutbox(db *sql.DB, clk clock.Clock, log *slog.Logger, dbKey string) *Outbox {
	return &Outbox{db: db, dbKey: dbKey, clock: clk, log: log}
}

// queued is one pending row.
type queued struct {
	ID, FamilyID, To, Subject, Body string
	HTML                            bool
	Attempts                        int
}

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// Enqueue stores one plain-text message for delivery. It returns ErrNotReady, and stores nothing, while
// email is not set up, so callers can fall back to another route.
func (o *Outbox) Enqueue(ctx context.Context, familyID, to, subject, body string) error {
	return o.enqueue(ctx, familyID, to, subject, body, false)
}

// EnqueueHTML stores one HTML message for delivery. It follows the same rules as Enqueue.
func (o *Outbox) EnqueueHTML(ctx context.Context, familyID, to, subject, body string) error {
	return o.enqueue(ctx, familyID, to, subject, body, true)
}

func (o *Outbox) enqueue(ctx context.Context, familyID, to, subject, body string, isHTML bool) error {
	cur, err := loadStored(ctx, o.db, familyID)
	if err != nil {
		return err
	}
	if !cur.Ready {
		return ErrNotReady
	}
	now := formatTime(o.clock.Now())
	_, err = o.db.ExecContext(ctx,
		`INSERT INTO email_outbox (id, family_id, to_address, subject, body, is_html, next_attempt_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		ids.New(), familyID, to, subject, body, isHTML, now, now)
	return err
}

// Run sends due messages every interval until ctx is cancelled. A failed run is logged and retried
// on the next tick.
func (o *Outbox) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		o.sendDueLogged(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// sendDueLogged runs SendDue and logs what changed. Errors caused by shutdown are not logged.
func (o *Outbox) sendDueLogged(ctx context.Context) {
	n, err := o.SendDue(ctx)
	if err != nil {
		if ctx.Err() == nil {
			o.log.Error("email outbox run failed", slog.String("error", err.Error()))
		}
		return
	}
	if n > 0 {
		o.log.Info("email outbox run", slog.Int("attempted", n))
	}
}

// SendDue delivers the pending messages that are due. It returns how many delivery attempts it made.
// A message waits while email is not set up, and is given up on after maxQueueAge. Failures are retried
// with back-off; the failure is logged without the address or body.
func (o *Outbox) SendDue(ctx context.Context) (int, error) {
	now := o.clock.Now()
	if _, err := o.db.ExecContext(ctx,
		`UPDATE email_outbox SET status = 'failed', last_error = 'expired before it could be sent', body = ''
		  WHERE status = 'pending' AND created_at < ?`,
		formatTime(now.Add(-maxQueueAge))); err != nil {
		return 0, err
	}

	rows, err := o.db.QueryContext(ctx,
		`SELECT id, family_id, to_address, subject, body, is_html, attempts FROM email_outbox
		  WHERE status = 'pending' AND next_attempt_at <= ?
		  ORDER BY next_attempt_at LIMIT ?`,
		formatTime(now), outboxBatch)
	if err != nil {
		return 0, err
	}
	var due []queued
	for rows.Next() {
		var m queued
		if err := rows.Scan(&m.ID, &m.FamilyID, &m.To, &m.Subject, &m.Body, &m.HTML, &m.Attempts); err != nil {
			rows.Close()
			return 0, err
		}
		due = append(due, m)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	settings := map[string]stored{}
	attempted := 0
	for _, m := range due {
		if ctx.Err() != nil {
			break
		}
		cur, ok := settings[m.FamilyID]
		if !ok {
			cur, err = loadStored(ctx, o.db, m.FamilyID)
			if err != nil {
				return attempted, err
			}
			settings[m.FamilyID] = cur
		}
		if !cur.Ready {
			continue
		}
		sent, err := o.sentSince(ctx, m.FamilyID, now.Add(-time.Hour))
		if err != nil {
			return attempted, err
		}
		if sent >= cur.SendLimitPerHour {
			if err := o.setNextAttempt(ctx, m.ID, now.Add(capDeferral)); err != nil {
				return attempted, err
			}
			continue
		}
		claimed, err := o.claim(ctx, m.ID, now)
		if err != nil {
			return attempted, err
		}
		if !claimed {
			continue
		}
		attempted++
		if err := o.deliver(ctx, m, cur, now); err != nil {
			return attempted, err
		}
	}
	return attempted, nil
}

// claim takes a short lease on a row so another worker does not send it too. It reports false if
// the row was already taken.
func (o *Outbox) claim(ctx context.Context, id string, now time.Time) (bool, error) {
	res, err := o.db.ExecContext(ctx,
		`UPDATE email_outbox SET next_attempt_at = ?
		  WHERE id = ? AND status = 'pending' AND next_attempt_at <= ?`,
		formatTime(now.Add(claimLease)), id, formatTime(now))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// deliver sends one claimed message and records the result.
func (o *Outbox) deliver(ctx context.Context, m queued, cur stored, now time.Time) error {
	account := Account{
		Host:        cur.Host,
		Port:        cur.Port,
		Security:    cur.Security,
		Username:    cur.Username,
		FromAddress: cur.FromAddress,
		FromName:    cur.FromName,
	}
	var sendErr error
	if cur.Sealed != "" {
		account.Password, sendErr = openSecret(o.dbKey, m.FamilyID, cur.Sealed)
	}
	if sendErr == nil {
		sendErr = o.Mailer.Send(ctx, account, Message{To: m.To, Subject: m.Subject, Body: m.Body, HTML: m.HTML})
	}

	if sendErr == nil {
		_, err := o.db.ExecContext(ctx,
			`UPDATE email_outbox SET status = 'sent', attempts = ?, sent_at = ?, body = '', last_error = ''
			  WHERE id = ?`,
			m.Attempts+1, formatTime(o.clock.Now()), m.ID)
		return err
	}
	// A cancelled run is not a failed delivery, so it does not use up an attempt.
	if ctx.Err() != nil {
		return o.setNextAttempt(context.WithoutCancel(ctx), m.ID, now)
	}

	attempts := m.Attempts + 1
	msg := truncate(sendErr.Error(), maxErrorChars)
	o.log.Warn("email delivery failed", slog.Int("attempt", attempts), slog.String("error", msg))
	if attempts >= maxAttempts {
		if _, err := o.db.ExecContext(ctx,
			`UPDATE email_outbox SET status = 'failed', attempts = ?, last_error = ?, body = '' WHERE id = ?`,
			attempts, msg, m.ID); err != nil {
			return err
		}
		return diagnostics.Record(ctx, o.db, &m.FamilyID, diagnostics.KindEmail, "send_failed", nil, "", now)
	}
	_, err := o.db.ExecContext(ctx,
		`UPDATE email_outbox SET attempts = ?, last_error = ?, next_attempt_at = ? WHERE id = ?`,
		attempts, msg, formatTime(now.Add(backoff[attempts-1])), m.ID)
	return err
}

// setNextAttempt moves a pending row to a later time without counting an attempt.
func (o *Outbox) setNextAttempt(ctx context.Context, id string, at time.Time) error {
	_, err := o.db.ExecContext(ctx,
		`UPDATE email_outbox SET next_attempt_at = ? WHERE id = ? AND status = 'pending'`,
		formatTime(at), id)
	return err
}

// sentSince counts the family's messages sent at or after since.
func (o *Outbox) sentSince(ctx context.Context, familyID string, since time.Time) (int, error) {
	var n int
	err := o.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM email_outbox WHERE family_id = ? AND status = 'sent' AND sent_at >= ?`,
		familyID, formatTime(since)).Scan(&n)
	return n, err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
