package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sqlite3 "github.com/mutecomm/go-sqlcipher/v4"
	"nestwall/backend/internal/platform/clock"
)

// stepClock is a mutable test clock so rate-limit windows can be advanced.
type stepClock struct{ now time.Time }

func (c *stepClock) Now() time.Time { return c.now }

var _ clock.Clock = (*stepClock)(nil)

func TestPasswordHashing(t *testing.T) {
	password := "correct-horse-battery-staple"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}
	if hash == password {
		t.Fatal("hash must not equal plaintext password")
	}
	if !strings.Contains(hash, ":") {
		t.Fatalf("hash should contain salt separator, got %s", hash)
	}
	if !VerifyPassword(password, hash) {
		t.Fatal("VerifyPassword failed for correct password")
	}
	if VerifyPassword("wrong-password", hash) {
		t.Fatal("VerifyPassword succeeded for incorrect password")
	}
	if strings.Contains(hash, password) {
		t.Fatal("password hash must not contain plaintext password")
	}
}

func TestSessionTokenGeneration(t *testing.T) {
	token1, err := GenerateSessionToken()
	if err != nil {
		t.Fatalf("GenerateSessionToken failed: %v", err)
	}
	token2, err := GenerateSessionToken()
	if err != nil {
		t.Fatalf("GenerateSessionToken failed: %v", err)
	}
	if token1 == token2 {
		t.Fatal("tokens should be distinct")
	}
}

func TestHashTokenIsSHA256(t *testing.T) {
	token := "example-token"
	sum := sha256.Sum256([]byte(token))
	want := hex.EncodeToString(sum[:])

	if got := HashToken(token); got != want {
		t.Fatalf("HashToken = %s, want SHA-256 hex %s", got, want)
	}
	if HashToken(token) == token {
		t.Fatal("token hash must not equal the plaintext token")
	}
}

func TestRateLimiterUserLimit(t *testing.T) {
	clk := &stepClock{now: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)}
	rl := NewRateLimiter(clk)
	ip := "192.168.1.100"
	userID := "user123"

	if err := rl.CheckRateLimit(ip, userID); err != nil {
		t.Fatalf("unexpected rate limit before any failure: %v", err)
	}

	// Four failures: still under the per-user limit of five.
	for i := 0; i < 4; i++ {
		rl.RecordFailure(ip, userID)
	}
	if err := rl.CheckRateLimit(ip, userID); err != nil {
		t.Fatalf("four failures should not block (each failure counted once), got %v", err)
	}

	// Fifth failure reaches the limit.
	rl.RecordFailure(ip, userID)
	if err := rl.CheckRateLimit(ip, userID); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited after five failures, got %v", err)
	}

	// A successful login clears the user counter.
	rl.ClearUser(userID)
	if err := rl.CheckRateLimit(ip, userID); err != nil {
		t.Fatalf("expected user to be cleared, got %v", err)
	}
}

func TestRateLimiterIPLimitAcrossUsers(t *testing.T) {
	clk := &stepClock{now: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)}
	rl := NewRateLimiter(clk)
	ip := "10.0.0.1"

	// Ten failures from one IP against different users reach the IP limit.
	for i := 0; i < 10; i++ {
		rl.RecordFailure(ip, "user-"+string(rune('a'+i)))
	}
	if err := rl.CheckRateLimit(ip, "someone-else"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected IP limit to block, got %v", err)
	}
}

func TestRateLimiterWindowExpires(t *testing.T) {
	clk := &stepClock{now: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)}
	rl := NewRateLimiter(clk)

	for i := 0; i < 5; i++ {
		rl.RecordFailure("1.1.1.1", "alice")
	}
	if err := rl.CheckRateLimit("1.1.1.1", "alice"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected block inside the window, got %v", err)
	}

	clk.now = clk.now.Add(loginAttemptWindow + time.Second)
	if err := rl.CheckRateLimit("1.1.1.1", "alice"); err != nil {
		t.Fatalf("expected block to expire after the window, got %v", err)
	}
}

// TestCSRFRealChain exercises the same middleware order as the server:
// the token middleware wraps the validation middleware.
func TestCSRFRealChain(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := CSRFTokenMiddleware()(CSRFMiddleware(ok))

	// GET without a cookie issues the cookie and echoes the token in the header.
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, "/api/v1/test", nil))
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET: expected 200, got %d", getRec.Code)
	}
	var csrf *http.Cookie
	for _, c := range getRec.Result().Cookies() {
		if c.Name == CSRFCookieName {
			csrf = c
		}
	}
	if csrf == nil || csrf.Value == "" {
		t.Fatal("GET: expected CSRF cookie to be issued")
	}
	if got := getRec.Header().Get(CSRFHeaderName); got != csrf.Value {
		t.Fatalf("GET: header %q does not match cookie %q", got, csrf.Value)
	}

	post := func(cookie *http.Cookie, header string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/test", nil)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		if header != "" {
			req.Header.Set(CSRFHeaderName, header)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := post(nil, ""); code != http.StatusForbidden {
		t.Fatalf("POST without cookie or header: expected 403, got %d", code)
	}
	if code := post(csrf, ""); code != http.StatusForbidden {
		t.Fatalf("POST with cookie but no header: expected 403, got %d", code)
	}
	if code := post(csrf, "wrong-value"); code != http.StatusForbidden {
		t.Fatalf("POST with mismatched header: expected 403, got %d", code)
	}
	if code := post(csrf, csrf.Value); code != http.StatusOK {
		t.Fatalf("POST with matching cookie and header: expected 200, got %d", code)
	}
}

func TestCSRFReplacesMalformedCookie(t *testing.T) {
	handler := CSRFTokenMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	req.AddCookie(&http.Cookie{Name: CSRFCookieName, Value: "not-hex"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if len(rec.Result().Cookies()) == 0 {
		t.Fatal("expected a fresh CSRF cookie to replace the malformed one")
	}
	if rec.Header().Get(CSRFHeaderName) == "not-hex" {
		t.Fatal("malformed cookie value must not be echoed")
	}
}

func TestSessionCookieFlags(t *testing.T) {
	// Plain HTTP on the LAN: the cookie must not be Secure, or browsers drop it.
	plain := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	rec := httptest.NewRecorder()
	setSessionCookie(rec, plain, "raw-token-value")
	c := rec.Result().Cookies()[0]
	if c.Secure {
		t.Error("plain HTTP request: cookie must not be Secure")
	}
	if !c.HttpOnly {
		t.Error("session cookie must be HttpOnly")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Error("session cookie must be SameSite=Strict")
	}
	if c.Value != "raw-token-value" {
		t.Errorf("cookie must carry the raw token, got %q", c.Value)
	}

	// Behind the TLS proxy: X-Forwarded-Proto marks the request as HTTPS.
	tls := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	tls.Header.Set("X-Forwarded-Proto", "https")
	rec = httptest.NewRecorder()
	setSessionCookie(rec, tls, "raw-token-value")
	if !rec.Result().Cookies()[0].Secure {
		t.Error("HTTPS request via proxy: cookie must be Secure")
	}
}

func TestAuthErrors(t *testing.T) {
	if ErrInvalidCredentials.Error() == "" {
		t.Fatal("ErrInvalidCredentials should have a message")
	}
	if ErrSessionExpired.Error() == "" {
		t.Fatal("ErrSessionExpired should have a message")
	}
	if ErrSessionRevoked.Error() == "" {
		t.Fatal("ErrSessionRevoked should have a message")
	}
	if ErrInvalidCredentials == ErrSessionExpired {
		t.Fatal("errors should be distinct")
	}
}

func TestValidateTimezone(t *testing.T) {
	tests := []struct {
		name  string
		tz    string
		valid bool
	}{
		{"UTC", "UTC", true},
		{"America/New_York", "America/New_York", true},
		{"Europe/London", "Europe/London", true},
		{"invalid zone", "Invalid/Zone", false},
		{"random text", "NotAZone", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTimezone(tt.tz)
			if (err == nil) != tt.valid {
				t.Errorf("ValidateTimezone(%q) error = %v, want valid=%v", tt.tz, err, tt.valid)
			}
		})
	}
}

func TestValidateNewUser(t *testing.T) {
	tests := []struct {
		name        string
		userID      string
		password    string
		displayName string
		wantField   string
	}{
		{"valid", "alice.r", "longenough", "Alice", ""},
		{"user id too short", "al", "longenough", "Alice", "user_id"},
		{"user id bad chars", "al ice", "longenough", "Alice", "user_id"},
		{"password too short", "alice", "short", "Alice", "password"},
		{"blank display name", "alice", "longenough", "   ", "display_name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateNewUser(tt.userID, tt.password, tt.displayName)
			if tt.wantField == "" {
				if err != nil {
					t.Fatalf("expected valid, got %v", err)
				}
				return
			}
			var ve *ValidationError
			if !errors.As(err, &ve) || ve.Field != tt.wantField {
				t.Fatalf("expected ValidationError on %q, got %v", tt.wantField, err)
			}
			params := ve.Params()
			if params["field"] != tt.wantField {
				t.Errorf("Params field = %v, want %s", params["field"], tt.wantField)
			}
		})
	}
}

func TestValidatePINAndColour(t *testing.T) {
	if err := ValidatePIN("1234"); err != nil {
		t.Errorf("ValidatePIN(1234) unexpected error: %v", err)
	}
	if err := ValidatePIN("12"); err == nil {
		t.Error("ValidatePIN(12) should fail: too short")
	}
	if err := ValidatePIN("12ab"); err == nil {
		t.Error("ValidatePIN(12ab) should fail: non-digit")
	}
	if err := ValidateColour("#6366F1"); err != nil {
		t.Errorf("ValidateColour(#6366F1) unexpected error: %v", err)
	}
	if err := ValidateColour("red"); err == nil {
		t.Error("ValidateColour(red) should fail")
	}
}

func TestValidateName(t *testing.T) {
	if err := ValidateName("name", "Rivera Family"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := ValidateName("name", strings.Repeat("a", MaxNameLength+1)); err == nil {
		t.Error("expected error for name over the maximum length")
	}
}

func TestIsUniqueViolation(t *testing.T) {
	if !IsUniqueViolation(sqlite3.Error{Code: sqlite3.ErrConstraint, ExtendedCode: sqlite3.ErrConstraintUnique}) {
		t.Error("unique constraint should be a unique violation")
	}
	if IsUniqueViolation(sqlite3.Error{Code: sqlite3.ErrConstraint, ExtendedCode: sqlite3.ErrConstraintForeignKey}) {
		t.Error("foreign key constraint should not be a unique violation")
	}
	if IsUniqueViolation(errors.New("boom")) {
		t.Error("plain error should not be a unique violation")
	}
}

func TestSetupStatusResponse(t *testing.T) {
	resp := SetupStatusResponse{SetupRequired: true}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal SetupStatusResponse: %v", err)
	}
	if !strings.Contains(string(data), `"setup_required":true`) {
		t.Errorf("expected setup_required:true in JSON, got %s", string(data))
	}
}
