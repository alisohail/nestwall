package push

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/testhelper"
)

type fixture struct {
	db  *sql.DB
	h   *Handlers
	fam testhelper.Family
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	db := testhelper.OpenDB(t)
	fam := testhelper.OneFamily(t, db)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return fixture{db: db, h: NewHandlers(db, clock.RealClock{}, logger), fam: fam}
}

// call runs a handler as the signed-in parent with the given body.
func (f fixture) call(t *testing.T, fn http.HandlerFunc, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	ctx := context.WithValue(req.Context(), permissions.ContextKeyFamilyMemberID, f.fam.MemberID)
	rec := httptest.NewRecorder()
	fn(rec, req.WithContext(ctx))
	return rec
}

func expectStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, want, rec.Body.String())
	}
}

func subscribeBody(endpoint string) string {
	return `{"endpoint":"` + endpoint + `","expirationTime":null,"keys":{"p256dh":"BPk-key","auth":"auth-secret"}}`
}

func (f fixture) subscriptionCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM push_subscriptions WHERE member_id = ?`, f.fam.MemberID).Scan(&n); err != nil {
		t.Fatalf("count subscriptions: %v", err)
	}
	return n
}

func TestSubscribeStoresDeviceForMember(t *testing.T) {
	f := newFixture(t)
	expectStatus(t, f.call(t, f.h.Subscribe, subscribeBody("https://push.example/one")), http.StatusNoContent)
	if n := f.subscriptionCount(t); n != 1 {
		t.Fatalf("subscriptions = %d, want 1", n)
	}
}

func TestSubscribeSameEndpointUpdatesKeys(t *testing.T) {
	f := newFixture(t)
	f.call(t, f.h.Subscribe, subscribeBody("https://push.example/one"))
	f.call(t, f.h.Subscribe, subscribeBody("https://push.example/one"))
	if n := f.subscriptionCount(t); n != 1 {
		t.Fatalf("subscriptions = %d, want 1 after resubscribing", n)
	}
}

func TestSubscribeRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"plain http":   `{"endpoint":"http://push.example/one","keys":{"p256dh":"k","auth":"a"}}`,
		"no keys":      `{"endpoint":"https://push.example/one","keys":{}}`,
		"bad json":     `{"endpoint":`,
		"empty target": `{}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			rec := f.call(t, f.h.Subscribe, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if n := f.subscriptionCount(t); n != 0 {
				t.Fatalf("subscriptions = %d, want 0", n)
			}
		})
	}
}

func TestUnsubscribeRemovesOwnDevice(t *testing.T) {
	f := newFixture(t)
	f.call(t, f.h.Subscribe, subscribeBody("https://push.example/one"))
	expectStatus(t, f.call(t, f.h.Unsubscribe, `{"endpoint":"https://push.example/one"}`), http.StatusNoContent)
	if n := f.subscriptionCount(t); n != 0 {
		t.Fatalf("subscriptions = %d, want 0", n)
	}
	// Removing an endpoint that is already gone is not an error.
	expectStatus(t, f.call(t, f.h.Unsubscribe, `{"endpoint":"https://push.example/one"}`), http.StatusNoContent)
}

func TestPublicKeyIsCreatedOnce(t *testing.T) {
	f := newFixture(t)
	get := func() string {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		ctx := context.WithValue(req.Context(), permissions.ContextKeyFamilyMemberID, f.fam.MemberID)
		rec := httptest.NewRecorder()
		f.h.PublicKey(rec, req.WithContext(ctx))
		expectStatus(t, rec, http.StatusOK)
		return rec.Body.String()
	}
	first, second := get(), get()
	if first == "" || !strings.Contains(first, `"public_key"`) || first != second {
		t.Fatalf("public key changed between calls: %s vs %s", first, second)
	}
	var rows int
	if err := f.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM push_config`).Scan(&rows); err != nil {
		t.Fatalf("count keys: %v", err)
	}
	if rows != 1 {
		t.Fatalf("push_config rows = %d, want 1", rows)
	}
}

// pushServer records the requests it receives and answers each with status.
type pushServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests int
}

func newPushServer(t *testing.T, status int) *pushServer {
	t.Helper()
	ps := &pushServer{}
	ps.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ps.mu.Lock()
		ps.requests++
		ps.mu.Unlock()
		if !strings.HasPrefix(r.Header.Get("Authorization"), "vapid ") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(ps.Close)
	return ps
}

func (ps *pushServer) count() int {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.requests
}

// browserKeys returns a P-256 public key and auth secret in the form a browser's PushSubscription uses.
func browserKeys(t *testing.T) (p256dh, auth string) {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		t.Fatalf("random auth: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes()),
		base64.RawURLEncoding.EncodeToString(secret)
}

// pendingNotice inserts a notice flagged for push and a device subscribed to the push server.
func (f fixture) pendingNotice(t *testing.T, endpoint string) string {
	t.Helper()
	ctx := context.Background()
	id := ids.New()
	if _, err := f.db.ExecContext(ctx,
		`INSERT INTO notifications (id, family_id, recipient_id, kind, subject_member_id, body, push_pending)
		 VALUES (?, ?, ?, 'task_approved', NULL, 'Good job', 1)`,
		id, f.fam.FamilyID, f.fam.MemberID); err != nil {
		t.Fatalf("insert notification: %v", err)
	}
	p256dh, auth := browserKeys(t)
	if _, err := f.db.ExecContext(ctx,
		`INSERT INTO push_subscriptions (id, family_id, member_id, endpoint, p256dh, auth)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		ids.New(), f.fam.FamilyID, f.fam.MemberID, endpoint, p256dh, auth); err != nil {
		t.Fatalf("insert subscription: %v", err)
	}
	return id
}

func newSender(f fixture, ps *pushServer) *Sender {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewSender(f.db, clock.RealClock{}, logger, "https://nestwall.example")
	s.client = ps.Client()
	return s
}

func TestSenderPushesAndClearsFlag(t *testing.T) {
	f := newFixture(t)
	ps := newPushServer(t, http.StatusCreated)
	id := f.pendingNotice(t, ps.URL+"/device")

	n, err := newSender(f, ps).SendDue(context.Background())
	if err != nil {
		t.Fatalf("SendDue: %v", err)
	}
	if n != 1 || ps.count() != 1 {
		t.Fatalf("processed = %d, requests = %d; want 1 and 1", n, ps.count())
	}
	var flag int
	if err := f.db.QueryRowContext(context.Background(),
		`SELECT push_pending FROM notifications WHERE id = ?`, id).Scan(&flag); err != nil {
		t.Fatalf("read flag: %v", err)
	}
	if flag != 0 {
		t.Fatalf("push_pending = %d after send, want 0", flag)
	}
	if got := f.subscriptionCount(t); got != 1 {
		t.Fatalf("subscriptions = %d, want the device kept", got)
	}
}

func TestSenderRemovesGoneDevice(t *testing.T) {
	f := newFixture(t)
	ps := newPushServer(t, http.StatusGone)
	f.pendingNotice(t, ps.URL+"/device")

	if _, err := newSender(f, ps).SendDue(context.Background()); err != nil {
		t.Fatalf("SendDue: %v", err)
	}
	if got := f.subscriptionCount(t); got != 0 {
		t.Fatalf("subscriptions = %d, want the gone device removed", got)
	}
}

func TestSenderIdleWithNothingPending(t *testing.T) {
	f := newFixture(t)
	ps := newPushServer(t, http.StatusCreated)
	n, err := newSender(f, ps).SendDue(context.Background())
	if err != nil || n != 0 || ps.count() != 0 {
		t.Fatalf("n=%d requests=%d err=%v; want 0, 0, nil", n, ps.count(), err)
	}
}
