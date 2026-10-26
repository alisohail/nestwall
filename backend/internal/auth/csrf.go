package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"nestwall/backend/internal/platform/web"
)

// CSRF protection constants.
const (
	CSRFTokenLen   = 32
	CSRFCookieName = "nestwall-csrf"
	CSRFHeaderName = "X-CSRF-Token"
)

// GenerateCSRFToken generates a new random CSRF token (hex encoded).
func GenerateCSRFToken() (string, error) {
	token := make([]byte, CSRFTokenLen)
	if _, err := rand.Read(token); err != nil {
		return "", fmt.Errorf("generate CSRF token: %w", err)
	}
	return hex.EncodeToString(token), nil
}

// CSRFTokenMiddleware implements the double-submit cookie pattern, part one.
// It issues the CSRF cookie when the client does not have one yet and echoes
// its value in the X-CSRF-Token response header, so the SPA can read it and
// send it back on mutating requests. Wrap CSRFMiddleware with it so that the
// header is also present on rejected requests.
func CSRFTokenMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := existingCSRFToken(r)
			if token == "" {
				generated, err := GenerateCSRFToken()
				if err != nil {
					web.RespondError(w, http.StatusInternalServerError, "internal_error", "Internal server error", nil)
					return
				}
				token = generated
				http.SetCookie(w, &http.Cookie{
					Name:     CSRFCookieName,
					Value:    token,
					Path:     "/",
					HttpOnly: true,
					Secure:   IsSecureRequest(r),
					SameSite: http.SameSiteStrictMode,
					MaxAge:   int(SessionMaxAge / time.Second),
				})
			}
			w.Header().Set(CSRFHeaderName, token)
			next.ServeHTTP(w, r)
		})
	}
}

// CSRFMiddleware implements the double-submit cookie pattern, part two.
// Mutating requests (anything other than GET, HEAD, OPTIONS, TRACE) must send
// the X-CSRF-Token header with the same value as the CSRF cookie. A cross-site
// attacker can make the browser send the cookie but cannot read it to copy it into the header.
func CSRFMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSafeMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}

		cookie, err := r.Cookie(CSRFCookieName)
		header := r.Header.Get(CSRFHeaderName)
		if err != nil || header == "" || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(header)) != 1 {
			web.RespondError(w, http.StatusForbidden, "csrf_invalid", "Missing or invalid CSRF token", nil)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// existingCSRFToken returns the CSRF cookie value if it is well formed, otherwise "".
func existingCSRFToken(r *http.Request) string {
	cookie, err := r.Cookie(CSRFCookieName)
	if err != nil || len(cookie.Value) != CSRFTokenLen*2 {
		return ""
	}
	if _, err := hex.DecodeString(cookie.Value); err != nil {
		return ""
	}
	return cookie.Value
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	}
	return false
}
