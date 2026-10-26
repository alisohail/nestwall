package email

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"io"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP is a minimal SMTP server for tests. It records what a client sent.
type fakeSMTP struct {
	ln         net.Listener
	serverCfg  *tls.Config
	implicit   bool // true: TLS from the first byte. false: STARTTLS on demand.
	noSTARTTLS bool // true: do not advertise or accept STARTTLS.
	user, pass string

	mu       sync.Mutex
	authUser string
	authOK   bool
	from     string
	rcpt     string
	data     string
}

func newFakeSMTP(t *testing.T, implicit, noSTARTTLS bool, user, pass string) (*fakeSMTP, *tls.Config) {
	t.Helper()
	serverCfg, clientCfg := newTestTLS(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeSMTP{ln: ln, serverCfg: serverCfg, implicit: implicit, noSTARTTLS: noSTARTTLS, user: user, pass: pass}
	t.Cleanup(func() { ln.Close() })
	go f.serve()
	return f, clientCfg
}

func (f *fakeSMTP) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func (f *fakeSMTP) serve() {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(c)
	}
}

func (f *fakeSMTP) handle(raw net.Conn) {
	defer raw.Close()
	_ = raw.SetDeadline(time.Now().Add(10 * time.Second))
	var conn io.ReadWriter = raw
	if f.implicit {
		conn = tls.Server(raw, f.serverCfg)
	}
	r := bufio.NewReader(conn)
	write := func(s string) { _, _ = io.WriteString(conn, s+"\r\n") }
	upgraded := f.implicit
	write("220 fake.test ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"):
			write("250-fake.test")
			if !upgraded && !f.noSTARTTLS {
				write("250-STARTTLS")
			}
			write("250 AUTH PLAIN")
		case strings.HasPrefix(upper, "STARTTLS"):
			if upgraded || f.noSTARTTLS {
				write("502 not available")
				continue
			}
			write("220 go ahead")
			tc := tls.Server(raw, f.serverCfg)
			if err := tc.Handshake(); err != nil {
				return
			}
			conn = tc
			r = bufio.NewReader(conn)
			write = func(s string) { _, _ = io.WriteString(conn, s+"\r\n") }
			upgraded = true
		case strings.HasPrefix(upper, "AUTH PLAIN "):
			decoded, err := base64.StdEncoding.DecodeString(line[len("AUTH PLAIN "):])
			parts := strings.Split(string(decoded), "\x00")
			if err != nil || len(parts) != 3 {
				write("501 bad auth")
				continue
			}
			f.mu.Lock()
			f.authUser = parts[1]
			f.authOK = parts[1] == f.user && parts[2] == f.pass
			ok := f.authOK
			f.mu.Unlock()
			if ok {
				write("235 authenticated")
			} else {
				write("535 invalid credentials")
			}
		case strings.HasPrefix(upper, "MAIL FROM:"):
			f.mu.Lock()
			f.from = line[len("MAIL FROM:"):]
			f.mu.Unlock()
			write("250 ok")
		case strings.HasPrefix(upper, "RCPT TO:"):
			f.mu.Lock()
			f.rcpt = line[len("RCPT TO:"):]
			f.mu.Unlock()
			write("250 ok")
		case upper == "DATA":
			write("354 send data")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			f.mu.Lock()
			f.data = b.String()
			f.mu.Unlock()
			write("250 queued")
		case upper == "QUIT":
			write("221 bye")
			return
		default:
			write("500 unknown")
		}
	}
}

// received returns what the server recorded.
func (f *fakeSMTP) received() (authUser string, authOK bool, from, rcpt, data string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.authUser, f.authOK, f.from, f.rcpt, f.data
}

// newTestTLS makes a self-signed certificate for 127.0.0.1 and returns the server and client configs.
func newTestTLS(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("cert: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	server := &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	client := &tls.Config{RootCAs: pool, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12}
	return server, client
}

func testAccount(port int, security string) Account {
	return Account{
		Host:        "127.0.0.1",
		Port:        port,
		Security:    security,
		Username:    "mailer",
		Password:    "app-pass",
		FromAddress: "nestwall@example.com",
		FromName:    "NestWall",
	}
}

func TestSendImplicitTLSAuthenticatesAndDelivers(t *testing.T) {
	srv, clientCfg := newFakeSMTP(t, true, false, "mailer", "app-pass")
	m := Mailer{TLSConfig: clientCfg}
	err := m.Send(context.Background(), testAccount(srv.port(), securityTLS), Message{To: "parent@example.com", Subject: "Hello", Body: "Body text"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	user, ok, from, rcpt, data := srv.received()
	if user != "mailer" || !ok {
		t.Fatalf("auth: user=%q ok=%v", user, ok)
	}
	if !strings.Contains(from, "nestwall@example.com") || !strings.Contains(rcpt, "parent@example.com") {
		t.Fatalf("envelope: from=%q rcpt=%q", from, rcpt)
	}
	if !strings.Contains(data, "Body text") {
		t.Fatalf("data missing body: %q", data)
	}
}

func TestSendSTARTTLSAuthenticatesAndDelivers(t *testing.T) {
	srv, clientCfg := newFakeSMTP(t, false, false, "mailer", "app-pass")
	m := Mailer{TLSConfig: clientCfg}
	err := m.Send(context.Background(), testAccount(srv.port(), securitySTARTTLS), Message{To: "parent@example.com", Subject: "Hello", Body: "Body text"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	user, ok, _, rcpt, _ := srv.received()
	if user != "mailer" || !ok {
		t.Fatalf("auth: user=%q ok=%v", user, ok)
	}
	if !strings.Contains(rcpt, "parent@example.com") {
		t.Fatalf("rcpt=%q", rcpt)
	}
}

func TestSendRefusesSTARTTLSWhenNotOffered(t *testing.T) {
	srv, clientCfg := newFakeSMTP(t, false, true, "mailer", "app-pass")
	m := Mailer{TLSConfig: clientCfg}
	err := m.Send(context.Background(), testAccount(srv.port(), securitySTARTTLS), Message{To: "parent@example.com", Subject: "x", Body: "x"})
	if !errors.Is(err, ErrSTARTTLSMissing) {
		t.Fatalf("want ErrSTARTTLSMissing, got %v", err)
	}
	if user, _, _, _, _ := srv.received(); user != "" {
		t.Fatalf("credentials were sent without TLS: user=%q", user)
	}
}

func TestSendReportsRejectedCredentialsWithoutPassword(t *testing.T) {
	srv, clientCfg := newFakeSMTP(t, true, false, "mailer", "other-pass")
	m := Mailer{TLSConfig: clientCfg}
	err := m.Send(context.Background(), testAccount(srv.port(), securityTLS), Message{To: "parent@example.com", Subject: "x", Body: "x"})
	if err == nil {
		t.Fatal("want error for wrong password")
	}
	if strings.Contains(err.Error(), "app-pass") {
		t.Fatalf("error leaks the password: %v", err)
	}
	if _, _, _, rcpt, _ := srv.received(); rcpt != "" {
		t.Fatalf("message sent after failed sign-in: rcpt=%q", rcpt)
	}
}

func TestBuildMessageStripsHeaderInjection(t *testing.T) {
	out := string(buildMessage(testAccount(587, securitySTARTTLS), Message{
		To:      "parent@example.com",
		Subject: "Hi\r\nBcc: evil@example.com",
		Body:    "Body",
	}))
	if strings.Contains(out, "\r\nBcc:") || strings.Contains(out, "\nBcc: evil") {
		t.Fatalf("injected header present:\n%s", out)
	}
	if !strings.Contains(out, "\r\n\r\nBody") {
		t.Fatalf("missing header/body separator:\n%s", out)
	}
}
