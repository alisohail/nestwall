package auth

import (
	"net/http"
	"time"
)

// IsSecureRequest reports whether the client reached the API over HTTPS, either
// directly or through the TLS-terminating reverse proxy (which sets X-Forwarded-Proto).
// Secure cookies are only set for HTTPS so that a plain-HTTP LAN deployment still works.
func IsSecureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return r.Header.Get("X-Forwarded-Proto") == "https"
}

// setSessionCookie stores the raw session token in the session cookie.
// Only the SHA-256 hash of the token is kept in the database.
func setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   IsSecureRequest(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(SessionMaxAge / time.Second),
	})
}

// clearSessionCookie expires the session cookie.
func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   IsSecureRequest(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}
