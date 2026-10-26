package email

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/testhelper"
)

var outboxStart = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

type outboxFixture struct {
	o   *Outbox
	db  *sql.DB
	fam testhelper.Family
}

func newOutboxFixture(t *testing.T) outboxFixture {
	t.Helper()
	db := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, db)
	o := NewOutbox(db, clock.FakeClock{Time: outboxStart}, slog.New(slog.NewTextHandler(io.Discard, nil)), testDBKey)
	return outboxFixture{o: o, db: db, fam: fam}
}

// setSettings saves the family's SMTP row. The password is sealed as the API would seal it. When
// configured is false the sender is blank, so email is not ready.
func (f outboxFixture) setSettings(t *testing.T, configured bool, port, limit int) {
	t.Helper()
	sealed, err := sealSecret(testDBKey, f.fam.FamilyID, "app-pass")
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	on, from := 0, ""
	if configured {
		on, from = 1, "nestwall@example.com"
	}
	if _, err := f.db.Exec(
		`INSERT INTO smtp_settings (family_id, enabled, host, port, security, username, password_sealed,
		                            from_address, from_name, send_limit_per_hour, updated_at)
		 VALUES (?, ?, '127.0.0.1', ?, 'tls', 'mailer', ?, ?, 'NestWall', ?, '2026-10-21T00:00:00.000Z')
		 ON CONFLICT(family_id) DO UPDATE SET enabled = excluded.enabled, from_address = excluded.from_address,
		        port = excluded.port, send_limit_per_hour = excluded.send_limit_per_hour`,
		f.fam.FamilyID, on, port, sealed, from, limit); err != nil {
		t.Fatalf("save settings: %v", err)
	}
}

// at moves the outbox clock.
func (f outboxFixture) at(t time.Time) {
	f.o.clock = clock.FakeClock{Time: t}
}

// status returns a row's status, attempts and next attempt time, for the one queued message.
func (f outboxFixture) status(t *testing.T) (string, int, string, string) {
	t.Helper()
	var status, next, body string
	var attempts int
	if err := f.db.QueryRow(`SELECT status, attempts, next_attempt_at, body FROM email_outbox LIMIT 1`).
		Scan(&status, &attempts, &next, &body); err != nil {
		t.Fatalf("read outbox row: %v", err)
	}
	return status, attempts, next, body
}

// closedPort returns a loopback port with nothing listening on it.
func closedPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func (f outboxFixture) enqueue(t *testing.T) {
	t.Helper()
	if err := f.o.Enqueue(context.Background(), f.fam.FamilyID, "parent@example.com", "Subject", "Reset link: https://x/abc"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
}

func TestEnqueueRefusedWhileEmailIsNotSetUp(t *testing.T) {
	f := newOutboxFixture(t)
	f.setSettings(t, false, 587, 30)
	err := f.o.Enqueue(context.Background(), f.fam.FamilyID, "parent@example.com", "S", "B")
	if err != ErrNotReady {
		t.Fatalf("Enqueue err = %v, want ErrNotReady", err)
	}
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM email_outbox`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("queued %d rows while email is not set up, want 0", n)
	}
}

func TestSendDueDeliversAndClearsBody(t *testing.T) {
	f := newOutboxFixture(t)
	srv, clientCfg := newFakeSMTP(t, true, false, "mailer", "app-pass")
	f.o.Mailer = Mailer{TLSConfig: clientCfg}
	f.setSettings(t, true, srv.port(), 30)
	f.enqueue(t)

	n, err := f.o.SendDue(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("SendDue = %d, %v; want 1, nil", n, err)
	}
	status, attempts, _, body := f.status(t)
	if status != "sent" || attempts != 1 || body != "" {
		t.Fatalf("after send: status=%q attempts=%d body=%q", status, attempts, body)
	}
	_, ok, _, rcpt, data := srv.received()
	if !ok || rcpt == "" || data == "" {
		t.Fatalf("server did not receive the message: ok=%v rcpt=%q", ok, rcpt)
	}
}

func TestFailedSendBacksOffAndGivesUp(t *testing.T) {
	f := newOutboxFixture(t)
	f.setSettings(t, true, closedPort(t), 30)
	f.enqueue(t)
	ctx := context.Background()

	// Each failure waits longer: 1, 5, 30, then 120 minutes. The fifth failure gives up.
	waits := []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour}
	now := outboxStart
	for i, wait := range waits {
		f.at(now)
		if n, _ := f.o.SendDue(ctx); n != 1 {
			t.Fatalf("attempt %d: SendDue attempted %d, want 1", i+1, n)
		}
		status, attempts, next, _ := f.status(t)
		if status != "pending" || attempts != i+1 {
			t.Fatalf("attempt %d: status=%q attempts=%d", i+1, status, attempts)
		}
		want := formatTime(now.Add(wait))
		if next != want {
			t.Fatalf("attempt %d: next=%s want %s", i+1, next, want)
		}
		// Not due yet: nothing is attempted just before the wait ends.
		f.at(now.Add(wait - time.Second))
		if n, _ := f.o.SendDue(ctx); n != 0 {
			t.Fatalf("attempt %d: retried before back-off ended", i+1)
		}
		now = now.Add(wait)
	}

	f.at(now)
	if n, _ := f.o.SendDue(ctx); n != 1 {
		t.Fatalf("final attempt: SendDue attempted %d, want 1", n)
	}
	status, attempts, _, body := f.status(t)
	if status != "failed" || attempts != maxAttempts || body != "" {
		t.Fatalf("after final failure: status=%q attempts=%d body=%q", status, attempts, body)
	}
}

func TestSendLimitDefersUntilTheHourPasses(t *testing.T) {
	f := newOutboxFixture(t)
	srv, clientCfg := newFakeSMTP(t, true, false, "mailer", "app-pass")
	f.o.Mailer = Mailer{TLSConfig: clientCfg}
	f.setSettings(t, true, srv.port(), 1)
	ctx := context.Background()

	f.enqueue(t)
	if n, _ := f.o.SendDue(ctx); n != 1 {
		t.Fatalf("first send attempted %d, want 1", n)
	}
	f.enqueue(t)

	// One message was sent in the last hour and the limit is 1, so the second waits.
	f.at(outboxStart.Add(30 * time.Minute))
	if n, _ := f.o.SendDue(ctx); n != 0 {
		t.Fatalf("second send attempted %d within the limit, want 0", n)
	}
	var pending int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM email_outbox WHERE status = 'pending'`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 1 {
		t.Fatalf("pending = %d, want 1", pending)
	}

	// Once the first message is more than an hour old, the second goes out.
	f.at(outboxStart.Add(61 * time.Minute))
	if n, _ := f.o.SendDue(ctx); n != 1 {
		t.Fatalf("second send attempted %d after the hour, want 1", n)
	}
}

func TestRunDeliversDueMessagesUntilCancelled(t *testing.T) {
	f := newOutboxFixture(t)
	srv, clientCfg := newFakeSMTP(t, true, false, "mailer", "app-pass")
	f.o.Mailer = Mailer{TLSConfig: clientCfg}
	f.setSettings(t, true, srv.port(), 30)
	f.enqueue(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.o.Run(ctx, 10*time.Millisecond)
	}()

	deadline := time.Now().Add(10 * time.Second)
	for {
		status, _, _, _ := f.status(t)
		if status == "sent" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("message not delivered by the loop, status=%q", status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestQueueWaitsWhileEmailIsNotSetUpThenExpires(t *testing.T) {
	f := newOutboxFixture(t)
	srv, clientCfg := newFakeSMTP(t, true, false, "mailer", "app-pass")
	f.o.Mailer = Mailer{TLSConfig: clientCfg}
	f.setSettings(t, true, srv.port(), 30)
	f.enqueue(t)
	ctx := context.Background()

	// Turning email off holds the message without an attempt.
	f.setSettings(t, false, srv.port(), 30)
	if n, _ := f.o.SendDue(ctx); n != 0 {
		t.Fatalf("attempted %d while email is not set up, want 0", n)
	}
	status, attempts, _, _ := f.status(t)
	if status != "pending" || attempts != 0 {
		t.Fatalf("while off: status=%q attempts=%d", status, attempts)
	}

	// A message held for more than a day is given up on and its body cleared.
	f.at(outboxStart.Add(maxQueueAge + time.Minute))
	f.o.SendDue(ctx)
	status, _, _, body := f.status(t)
	if status != "failed" || body != "" {
		t.Fatalf("after expiry: status=%q body=%q", status, body)
	}
}
