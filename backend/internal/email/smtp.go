package email

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

const (
	securityTLS      = "tls"
	securitySTARTTLS = "starttls"
	helloName        = "nestwall"
	defaultTimeout   = 15 * time.Second
)

// LogoCID is the Content-ID of the NestWall logo. HTML refers to it as src="cid:nestwall-logo", so the
// logo shows without a reachable server URL. Mail clients often block remote images; inline parts are shown.
const LogoCID = "nestwall-logo"

//go:embed assets/nestwall-logo.png
var logoPNG []byte

// LogoPNG returns the NestWall logo that is attached to HTML mail. The preview also uses it.
func LogoPNG() []byte { return logoPNG }

// Account is what the mailer needs to reach an SMTP server. Password is used for AUTH only.
type Account struct {
	Host        string
	Port        int
	Security    string
	Username    string
	Password    string
	FromAddress string
	FromName    string
}

// Message is one email. HTML sends the body as text/html; otherwise it is plain text.
type Message struct {
	To      string
	Subject string
	Body    string
	HTML    bool
}

// Mailer delivers messages over verified TLS. TLSConfig is nil in production, which verifies the
// server certificate for the configured host. Tests set their own roots.
type Mailer struct {
	TLSConfig *tls.Config
	Timeout   time.Duration
}

// ErrSTARTTLSMissing is returned when security is starttls and the server does not offer it.
// Mail is never sent in clear text.
var ErrSTARTTLSMissing = errors.New("server does not offer STARTTLS")

// Send delivers one message. Errors describe the step that failed and never contain the password.
func (m Mailer) Send(ctx context.Context, a Account, msg Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	timeout := m.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	tlsCfg := m.TLSConfig
	if tlsCfg == nil {
		tlsCfg = &tls.Config{ServerName: a.Host, MinVersion: tls.VersionTLS12}
	}

	addr := net.JoinHostPort(a.Host, strconv.Itoa(a.Port))
	dialer := &net.Dialer{Timeout: timeout}
	var conn net.Conn
	var err error
	if a.Security == securityTLS {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}

	c, err := smtp.NewClient(conn, a.Host)
	if err != nil {
		return fmt.Errorf("greeting: %w", err)
	}
	defer c.Close()
	if err := c.Hello(helloName); err != nil {
		return fmt.Errorf("hello: %w", err)
	}
	if a.Security == securitySTARTTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return ErrSTARTTLSMissing
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}
	if a.Username != "" {
		if ok, _ := c.Extension("AUTH"); !ok {
			return errors.New("server does not offer AUTH")
		}
		if err := c.Auth(plainOverTLS{username: a.Username, password: a.Password}); err != nil {
			return errors.New("sign-in rejected by the server")
		}
	}
	if err := c.Mail(a.FromAddress); err != nil {
		return fmt.Errorf("sender rejected: %w", err)
	}
	if err := c.Rcpt(msg.To); err != nil {
		return fmt.Errorf("recipient rejected: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("data: %w", err)
	}
	if _, err := w.Write(buildMessage(a, msg)); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("end of data: %w", err)
	}
	return c.Quit()
}

// plainOverTLS is the AUTH PLAIN mechanism. net/smtp refuses PLAIN unless it knows the connection
// is TLS, and it only learns that after STARTTLS. Mailer always uses TLS before AUTH, so this is safe.
type plainOverTLS struct {
	username string
	password string
}

func (p plainOverTLS) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + p.username + "\x00" + p.password), nil
}

func (p plainOverTLS) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("unexpected server challenge")
	}
	return nil, nil
}

// buildMessage writes the headers and body with CRLF line endings. Header values have line breaks
// removed, so user input cannot add headers.
func buildMessage(a Account, msg Message) []byte {
	from := oneLine(a.FromAddress)
	if name := oneLine(a.FromName); name != "" {
		from = mime.QEncoding.Encode("utf-8", name) + " <" + from + ">"
	}
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + oneLine(msg.To) + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", oneLine(msg.Subject)) + "\r\n")
	b.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	body := strings.ReplaceAll(strings.ReplaceAll(msg.Body, "\r\n", "\n"), "\n", "\r\n")
	if msg.HTML && strings.Contains(body, "cid:"+LogoCID) {
		boundary := newBoundary()
		b.WriteString("Content-Type: multipart/related; boundary=\"" + boundary + "\"\r\n\r\n")
		b.WriteString("--" + boundary + "\r\n")
		b.WriteString("Content-Type: text/html; charset=utf-8\r\n\r\n")
		b.WriteString(body + "\r\n")
		b.WriteString("--" + boundary + "\r\n")
		b.WriteString("Content-Type: image/png\r\n")
		b.WriteString("Content-Transfer-Encoding: base64\r\n")
		b.WriteString("Content-ID: <" + LogoCID + ">\r\n")
		b.WriteString("Content-Disposition: inline; filename=\"nestwall-logo.png\"\r\n\r\n")
		b.WriteString(wrapBase64(logoPNG))
		b.WriteString("--" + boundary + "--\r\n")
		return []byte(b.String())
	}
	if msg.HTML {
		b.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	} else {
		b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	}
	b.WriteString("\r\n")
	b.WriteString(body)
	b.WriteString("\r\n")
	return []byte(b.String())
}

// newBoundary returns a random MIME boundary that cannot clash with the message text.
func newBoundary() string {
	var buf [12]byte
	_, _ = rand.Read(buf[:])
	return "nw-" + hex.EncodeToString(buf[:])
}

// wrapBase64 encodes data in 76-character lines, as MIME requires.
func wrapBase64(data []byte) string {
	enc := base64.StdEncoding.EncodeToString(data)
	var b strings.Builder
	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc + "\r\n")
	return b.String()
}

func oneLine(s string) string {
	return strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ").Replace(s))
}
