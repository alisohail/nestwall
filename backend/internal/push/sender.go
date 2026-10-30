package push

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/SherClockHolmes/webpush-go"

	"nestwall/backend/internal/platform/clock"
)

// DefaultPollInterval is how often the sender checks for notices to push.
const DefaultPollInterval = 15 * time.Second

// batchSize caps how many notices one pass takes, so a backlog drains over several passes.
const batchSize = 100

// pushTTL is how long the push service keeps a message for a device that is offline, in seconds.
const pushTTL = 60 * 60

// Sender delivers pending notices as Web Push messages.
type Sender struct {
	db     *sql.DB
	clock  clock.Clock
	logger *slog.Logger
	// subject identifies the server to push services. It is the app's public https address.
	subject string
	client  *http.Client
}

// NewSender creates a sender. appURL is the public address of the app, sent to push services as the
// contact for the VAPID key.
func NewSender(db *sql.DB, clk clock.Clock, logger *slog.Logger, appURL string) *Sender {
	return &Sender{
		db:      db,
		clock:   clk,
		logger:  logger,
		subject: appURL,
		client:  &http.Client{Timeout: 15 * time.Second},
	}
}

// Run sends pending pushes every interval until ctx is cancelled.
func (s *Sender) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := s.SendDue(ctx); err != nil && ctx.Err() == nil {
				s.logger.Error("web push failed", slog.String("error", err.Error()))
			}
		}
	}
}

// pending is one notice waiting for a push, with the member it is for.
type pending struct {
	id, recipientID, body string
}

// SendDue pushes the pending notices to each subscribed device of the recipient, then clears their
// flag. A notice is cleared even when a device is unreachable, so one bad device cannot hold the
// queue. A device that the push service reports as gone is removed. It returns the number of notices
// processed.
func (s *Sender) SendDue(ctx context.Context) (int, error) {
	keys, err := loadOrCreateKeys(ctx, s.db, s.clock.Now())
	if err != nil {
		return 0, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, recipient_id, body FROM notifications
		  WHERE push_pending = 1 ORDER BY created_at, id LIMIT ?`, batchSize)
	if err != nil {
		return 0, fmt.Errorf("load pending pushes: %w", err)
	}
	var due []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.recipientID, &p.body); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan pending push: %w", err)
		}
		due = append(due, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("pending pushes: %w", err)
	}
	rows.Close()

	for _, p := range due {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		if err := s.deliver(ctx, keys, p); err != nil {
			s.logger.Error("web push delivery failed", slog.String("error", err.Error()))
		}
		if _, err := s.db.ExecContext(ctx,
			`UPDATE notifications SET push_pending = 0 WHERE id = ?`, p.id); err != nil {
			return 0, fmt.Errorf("clear push flag: %w", err)
		}
	}
	return len(due), nil
}

// deliver sends one notice to every subscribed device of its recipient.
func (s *Sender) deliver(ctx context.Context, keys vapidKeys, p pending) error {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, endpoint, p256dh, auth FROM push_subscriptions WHERE member_id = ?`, p.recipientID)
	if err != nil {
		return fmt.Errorf("load subscriptions: %w", err)
	}
	type device struct{ id, endpoint, p256dh, auth string }
	var devices []device
	for rows.Next() {
		var d device
		if err := rows.Scan(&d.id, &d.endpoint, &d.p256dh, &d.auth); err != nil {
			rows.Close()
			return fmt.Errorf("scan subscription: %w", err)
		}
		devices = append(devices, d)
	}
	rows.Close()

	payload, err := json.Marshal(map[string]any{
		"notification": map[string]any{
			"title": "NestWall",
			"body":  p.body,
			"tag":   p.id,
			// Read by the Angular service worker: tapping the notification focuses or opens the app.
			"data": map[string]any{
				"onActionClick": map[string]any{
					"default": map[string]string{"operation": "focusLastFocusedOrOpen", "url": "/"},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("encode push payload: %w", err)
	}
	for _, d := range devices {
		gone, err := s.send(keys, payload, d.endpoint, d.p256dh, d.auth)
		if err != nil {
			s.logger.Warn("web push send failed", slog.String("error", err.Error()))
			continue
		}
		if gone {
			if _, err := s.db.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE id = ?`, d.id); err != nil {
				return fmt.Errorf("remove expired subscription: %w", err)
			}
		}
	}
	return nil
}

// send posts one message. gone is true when the push service says the subscription no longer exists.
func (s *Sender) send(keys vapidKeys, payload []byte, endpoint, p256dh, auth string) (gone bool, err error) {
	resp, err := webpush.SendNotification(payload,
		&webpush.Subscription{Endpoint: endpoint, Keys: webpush.Keys{P256dh: p256dh, Auth: auth}},
		&webpush.Options{
			HTTPClient:      s.client,
			Subscriber:      s.subject,
			VAPIDPublicKey:  keys.public,
			VAPIDPrivateKey: keys.private,
			TTL:             pushTTL,
		})
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return true, nil
	case resp.StatusCode >= 300:
		return false, fmt.Errorf("push service answered %d", resp.StatusCode)
	}
	return false, nil
}
