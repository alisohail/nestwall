package calendar

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"nestwall/backend/internal/platform/web"
)

var errInvalidIfMatch = errors.New("invalid If-Match header")

// parseIfMatch returns the version in the If-Match header, or -1 when the header is absent.
// Versions are quoted, as in an ETag ("3"). A weak prefix (W/) is accepted.
func parseIfMatch(r *http.Request) (int, error) {
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw == "" {
		return -1, nil
	}
	n, err := strconv.Atoi(strings.Trim(strings.TrimPrefix(raw, "W/"), `"`))
	if err != nil || n < 1 {
		return 0, errInvalidIfMatch
	}
	return n, nil
}

// checkIfMatch compares the If-Match header with the current version. It writes a 400 or
// 412 response and returns false when the write must not go ahead. A missing header skips
// the check.
func checkIfMatch(w http.ResponseWriter, r *http.Request, current int) bool {
	expected, err := parseIfMatch(r)
	if err != nil {
		web.RespondError(w, http.StatusBadRequest, "bad_request", "Invalid If-Match header", nil)
		return false
	}
	if expected >= 0 && expected != current {
		versionConflict(w, current)
		return false
	}
	return true
}

// setETag sets the ETag header to the quoted version.
func setETag(w http.ResponseWriter, version int) {
	w.Header().Set("ETag", `"`+strconv.Itoa(version)+`"`)
}
