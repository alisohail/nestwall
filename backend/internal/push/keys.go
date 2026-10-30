// Package push sends Web Push messages to the phones and browsers that members subscribe.
// Notifications flagged push_pending are delivered by a background sender, so the transaction that
// writes a notice never waits on the network.
package push

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/SherClockHolmes/webpush-go"
)

// vapidKeys is the server's VAPID key pair. The public key is handed to browsers, and the private key
// signs every push request.
type vapidKeys struct {
	public, private string
}

// loadOrCreateKeys returns the stored VAPID key pair, creating it on first use. The key pair is kept
// in the encrypted database, so a restored backup keeps the same subscriptions working.
func loadOrCreateKeys(ctx context.Context, db *sql.DB, now time.Time) (vapidKeys, error) {
	var k vapidKeys
	err := db.QueryRowContext(ctx, `SELECT public_key, private_key FROM push_config WHERE id = 1`).
		Scan(&k.public, &k.private)
	if err == nil {
		return k, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return vapidKeys{}, fmt.Errorf("load vapid keys: %w", err)
	}
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return vapidKeys{}, fmt.Errorf("generate vapid keys: %w", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO push_config (id, public_key, private_key, created_at) VALUES (1, ?, ?, ?)`,
		pub, priv, now); err != nil {
		return vapidKeys{}, fmt.Errorf("store vapid keys: %w", err)
	}
	// Re-read, so a concurrent first call keeps whichever pair was stored first.
	if err := db.QueryRowContext(ctx, `SELECT public_key, private_key FROM push_config WHERE id = 1`).
		Scan(&k.public, &k.private); err != nil {
		return vapidKeys{}, fmt.Errorf("reload vapid keys: %w", err)
	}
	return k, nil
}
