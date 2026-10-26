package calendar

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// linkedFeed is a calendar server the tests control. It answers 304 when the request carries the
// current ETag, the way a real calendar host does.
type linkedFeed struct {
	mu     sync.Mutex
	status int
	body   []byte
	etag   string
	srv    *httptest.Server
	hits   int
}

func newLinkedFeed(t *testing.T, body []byte) *linkedFeed {
	t.Helper()
	lf := &linkedFeed{status: http.StatusOK, body: body, etag: `"v1"`}
	lf.srv = httptest.NewServer(http.HandlerFunc(lf.serve))
	t.Cleanup(lf.srv.Close)
	return lf
}

func (lf *linkedFeed) serve(w http.ResponseWriter, r *http.Request) {
	lf.mu.Lock()
	defer lf.mu.Unlock()
	lf.hits++
	if lf.status != http.StatusOK {
		w.WriteHeader(lf.status)
		return
	}
	if lf.etag != "" {
		w.Header().Set("ETag", lf.etag)
		if r.Header.Get("If-None-Match") == lf.etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	w.Header().Set("Content-Type", "text/calendar")
	_, _ = w.Write(lf.body)
}

// fetches reports how many requests the feed has answered.
func (lf *linkedFeed) fetches() int {
	lf.mu.Lock()
	defer lf.mu.Unlock()
	return lf.hits
}

func (lf *linkedFeed) set(status int, body []byte, etag string) {
	lf.mu.Lock()
	defer lf.mu.Unlock()
	lf.status, lf.body, lf.etag = status, body, etag
}

// link returns the feed address with a secret token, the way calendar providers issue them.
func (lf *linkedFeed) link() string {
	return lf.srv.URL + "/feeds/family.ics?token=" + secretToken
}

const secretToken = "secret-token-7f3a9c"

// allowLoopback lets the fixture fetch from the local test server. Production never sets this.
func allowLoopback(f fixture) {
	f.h.feeds = newFeedFetcher(true, 5*time.Second)
}

func createLinked(t *testing.T, f fixture, lf *linkedFeed) importResponse {
	t.Helper()
	rec := uploadImport(t, f, f.parent, url.Values{"url": {lf.link()}}, "", nil)
	expectStatus(t, rec, http.StatusCreated)
	return decodeImport(t, rec)
}

// importErrorCode reads the error code from a failed response.
func importErrorCode(rec *httptest.ResponseRecorder) string {
	var out struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out.Error.Code
}

func TestImportFromLinkStoresSealedLinkAndHidesIt(t *testing.T) {
	f := newFixture(t)
	allowLoopback(f)
	lf := newLinkedFeed(t, termICS)

	created := createLinked(t, f, lf)
	if created.Kind != "link" || created.Name != defaultLinked || created.FileName != nil {
		t.Fatalf("linked import = %+v", created)
	}
	if created.LastSyncedAt == nil || created.LastSyncError != nil {
		t.Fatalf("fresh link should show a sync time and no error: %+v", created)
	}
	if created.RefreshMinutes != defaultRefresh || created.Paused {
		t.Fatalf("defaults refresh=%d paused=%v", created.RefreshMinutes, created.Paused)
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_events WHERE import_id = ?`, created.ID); n == 0 {
		t.Fatal("linked import stored no events")
	}

	var sealed string
	if err := f.db.QueryRow(`SELECT source_url_sealed FROM calendar_imports WHERE id = ?`, created.ID).Scan(&sealed); err != nil {
		t.Fatalf("read sealed link: %v", err)
	}
	if strings.Contains(sealed, secretToken) || strings.Contains(sealed, lf.srv.URL) {
		t.Fatalf("link stored in plaintext: %q", sealed)
	}
	if got, err := openLink(testDBKey, created.ID, sealed); err != nil || got != lf.link() {
		t.Fatalf("stored link does not open to the original: %v", err)
	}

	// The link must not leave the server through the create response, the list or the audit log.
	rec := uploadImport(t, f, f.parent, url.Values{"url": {lf.link()}, "name": {"Club"}}, "", nil)
	expectStatus(t, rec, http.StatusCreated)
	if strings.Contains(rec.Body.String(), secretToken) {
		t.Fatalf("create response leaks the link: %s", rec.Body.String())
	}
	list := f.do(t, f.parent, http.MethodGet, "/api/v1/calendar/imports", nil, nil)
	if strings.Contains(list.Body.String(), secretToken) {
		t.Fatalf("list response leaks the link: %s", list.Body.String())
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM audit_logs WHERE metadata LIKE ?`, "%"+secretToken+"%"); n != 0 {
		t.Fatalf("audit log contains the link in %d rows", n)
	}
}

func TestImportLinkRejectsDisallowedAddresses(t *testing.T) {
	f := newFixture(t)
	cases := map[string]string{
		"not a url":                     "invalid_url",
		"ftp://calendar.example.com/a":  "link_not_allowed",
		"http://10.0.0.5/family.ics":    "link_not_allowed",
		"http://169.254.169.254/latest": "link_not_allowed",
		"https://user:pw@example.com/a": "link_not_allowed",
		"http://127.0.0.1:9/family.ics": "link_not_allowed",
	}
	for link, want := range cases {
		rec := uploadImport(t, f, f.parent, url.Values{"url": {link}}, "", nil)
		if rec.Code < 400 {
			t.Errorf("link %q accepted with status %d", link, rec.Code)
			continue
		}
		if got := importErrorCode(rec); got != want {
			t.Errorf("link %q error code = %q, want %q", link, got, want)
		}
		if strings.Contains(rec.Body.String(), "127.0.0.1") || strings.Contains(rec.Body.String(), "pw@") {
			t.Errorf("link %q echoed back in the error: %s", link, rec.Body.String())
		}
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_imports WHERE family_id = ?`, f.a.FamilyID); n != 0 {
		t.Fatalf("rejected links created %d imports", n)
	}
}

func TestImportFromLinkAndFileTogetherIsRejected(t *testing.T) {
	f := newFixture(t)
	rec := uploadImport(t, f, f.parent, url.Values{"url": {"https://example.com/a.ics"}}, "a.ics", termICS)
	expectStatus(t, rec, http.StatusBadRequest)
	if got := importErrorCode(rec); got != "one_source" {
		t.Fatalf("code = %q, want one_source", got)
	}
}

func TestImportFromLinkUnavailableIsBadGateway(t *testing.T) {
	f := newFixture(t)
	allowLoopback(f)
	lf := newLinkedFeed(t, termICS)
	lf.set(http.StatusInternalServerError, nil, "")

	rec := uploadImport(t, f, f.parent, url.Values{"url": {lf.link()}}, "", nil)
	expectStatus(t, rec, http.StatusBadGateway)
	if got := importErrorCode(rec); got != "calendar_unavailable" {
		t.Fatalf("code = %q, want calendar_unavailable", got)
	}
	if strings.Contains(rec.Body.String(), secretToken) {
		t.Fatalf("error leaks the link: %s", rec.Body.String())
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_imports WHERE family_id = ?`, f.a.FamilyID); n != 0 {
		t.Fatalf("failed link created %d imports", n)
	}
}

func TestImportFromLinkNeedsCalendarManage(t *testing.T) {
	f := newFixture(t)
	allowLoopback(f)
	lf := newLinkedFeed(t, termICS)
	rec := uploadImport(t, f, f.kid, url.Values{"url": {lf.link()}}, "", nil)
	expectStatus(t, rec, http.StatusForbidden)
}

func TestRefreshKeepsEventsWhenUnchanged(t *testing.T) {
	f := newFixture(t)
	allowLoopback(f)
	lf := newLinkedFeed(t, termICS)
	created := createLinked(t, f, lf)
	before := countRows(t, f, `SELECT COUNT(*) FROM calendar_events WHERE import_id = ?`, created.ID)

	if err := f.h.syncImportFeed(context.Background(), f.a.FamilyID, created.ID); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_events WHERE import_id = ?`, created.ID); n != before {
		t.Fatalf("events after 304 = %d, want %d", n, before)
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_imports WHERE id = ? AND last_sync_error IS NULL`, created.ID); n != 1 {
		t.Fatal("a 304 should leave no sync error")
	}
}

func TestRefreshReparsesWhenParserChanged(t *testing.T) {
	f := newFixture(t)
	allowLoopback(f)
	lf := newLinkedFeed(t, termICS)
	created := createLinked(t, f, lf)

	// Events stored by an older parser. The source is unchanged, so only a full fetch can fix them.
	mark := func(title string) {
		t.Helper()
		if _, err := f.db.Exec(`UPDATE calendar_events SET title = ? WHERE import_id = ?`, title, created.ID); err != nil {
			t.Fatalf("mark events: %v", err)
		}
	}
	mark("stale")
	if _, err := f.db.Exec(`UPDATE calendar_imports SET parser_version = 0 WHERE id = ?`, created.ID); err != nil {
		t.Fatalf("age parser version: %v", err)
	}
	if err := f.h.syncImportFeed(context.Background(), f.a.FamilyID, created.ID); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_events WHERE import_id = ? AND title = 'stale'`, created.ID); n != 0 {
		t.Fatal("an older parser version should be re-parsed even when the source is unchanged")
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_imports WHERE id = ? AND parser_version = ?`, created.ID, icsParserVersion); n != 1 {
		t.Fatal("parser version was not brought up to date")
	}

	// With the current parser, an unchanged source still returns 304 and the events are kept as they are.
	mark("kept")
	if err := f.h.syncImportFeed(context.Background(), f.a.FamilyID, created.ID); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_events WHERE import_id = ? AND title = 'kept'`, created.ID); n == 0 {
		t.Fatal("an unchanged source with the current parser should not be re-parsed")
	}
}

func TestRefreshReplacesEventsWhenCalendarChanges(t *testing.T) {
	f := newFixture(t)
	allowLoopback(f)
	lf := newLinkedFeed(t, termICS)
	created := createLinked(t, f, lf)

	dentist := []byte(strings.Join([]string{
		"BEGIN:VCALENDAR",
		"VERSION:2.0",
		"PRODID:-//NestWall test//EN",
		"BEGIN:VEVENT",
		"UID:dentist@test",
		"DTSTART;VALUE=DATE:20261020",
		"DTEND;VALUE=DATE:20261021",
		"SUMMARY:Dentist",
		"END:VEVENT",
		"END:VCALENDAR",
	}, "\r\n") + "\r\n")
	lf.set(http.StatusOK, dentist, `"v2"`)

	if err := f.h.syncImportFeed(context.Background(), f.a.FamilyID, created.ID); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_events WHERE import_id = ?`, created.ID); n != 1 {
		t.Fatalf("events after refresh = %d, want 1", n)
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_events WHERE import_id = ? AND title = 'Dentist'`, created.ID); n != 1 {
		t.Fatal("refreshed event is not the new calendar's event")
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_imports WHERE id = ? AND source_etag = ?`, created.ID, `"v2"`); n != 1 {
		t.Fatal("new ETag was not stored")
	}
}

func TestRefreshFailureKeepsLastGoodEvents(t *testing.T) {
	f := newFixture(t)
	allowLoopback(f)
	lf := newLinkedFeed(t, termICS)
	created := createLinked(t, f, lf)
	before := countRows(t, f, `SELECT COUNT(*) FROM calendar_events WHERE import_id = ?`, created.ID)

	cases := []struct {
		name   string
		status int
		body   []byte
		code   string
	}{
		{"server error", http.StatusInternalServerError, nil, "unavailable"},
		{"not an ICS file", http.StatusOK, []byte("<html>login</html>"), "invalid_ics"},
		{"no events", http.StatusOK, []byte("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nEND:VCALENDAR\r\n"), "no_events"},
	}
	for _, tc := range cases {
		lf.set(tc.status, tc.body, `"`+tc.name+`"`)
		if err := f.h.syncImportFeed(context.Background(), f.a.FamilyID, created.ID); err != nil {
			t.Fatalf("%s: sync returned a database error: %v", tc.name, err)
		}
		if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_events WHERE import_id = ?`, created.ID); n != before {
			t.Fatalf("%s: events changed to %d, want %d kept", tc.name, n, before)
		}
		if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_imports WHERE id = ? AND last_sync_error = ?`, created.ID, tc.code); n != 1 {
			t.Fatalf("%s: last_sync_error is not %q", tc.name, tc.code)
		}
	}

	// A good fetch after the failures clears the error.
	lf.set(http.StatusOK, termICS, `"recovered"`)
	if err := f.h.syncImportFeed(context.Background(), f.a.FamilyID, created.ID); err != nil {
		t.Fatalf("recover: %v", err)
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_imports WHERE id = ? AND last_sync_error IS NULL`, created.ID); n != 1 {
		t.Fatal("successful refresh did not clear last_sync_error")
	}
}

func TestSyncOfFileImportIsNotLinked(t *testing.T) {
	f := newFixture(t)
	rec := uploadICS(t, f, f.parent, "", "school.ics", termICS)
	expectStatus(t, rec, http.StatusCreated)
	created := decodeImport(t, rec)
	if err := f.h.syncImportFeed(context.Background(), f.a.FamilyID, created.ID); err != errNotLinked {
		t.Fatalf("sync of file import error = %v, want errNotLinked", err)
	}
}

func TestUpdateLinkedImportRefreshAndPause(t *testing.T) {
	f := newFixture(t)
	allowLoopback(f)
	created := createLinked(t, f, newLinkedFeed(t, termICS))
	path := "/api/v1/calendar/imports/" + created.ID

	bad := f.do(t, f.parent, http.MethodPatch, path,
		map[string]any{"name": "Club", "colour": "#0D9488", "refresh_minutes": 45}, nil)
	expectStatus(t, bad, http.StatusBadRequest)

	ok := f.do(t, f.parent, http.MethodPatch, path,
		map[string]any{"name": "Club", "colour": "#0D9488", "refresh_minutes": 60, "paused": true}, nil)
	expectStatus(t, ok, http.StatusNoContent)
	for _, item := range listImports(t, f, f.parent) {
		if item.ID == created.ID {
			if item.RefreshMinutes != 60 || !item.Paused {
				t.Fatalf("updated refresh=%d paused=%v", item.RefreshMinutes, item.Paused)
			}
			return
		}
	}
	t.Fatal("updated import missing from list")
}

func TestDeleteLinkedImportRemovesEvents(t *testing.T) {
	f := newFixture(t)
	allowLoopback(f)
	created := createLinked(t, f, newLinkedFeed(t, termICS))
	rec := f.do(t, f.parent, http.MethodDelete, "/api/v1/calendar/imports/"+created.ID, nil, nil)
	if rec.Code >= 300 {
		t.Fatalf("delete status = %d: %s", rec.Code, rec.Body.String())
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_events WHERE import_id = ?`, created.ID); n != 0 {
		t.Fatalf("delete left %d events", n)
	}
}

func TestSyncNowReportsStatusAndRecovers(t *testing.T) {
	f := newFixture(t)
	allowLoopback(f)
	lf := newLinkedFeed(t, termICS)
	created := createLinked(t, f, lf)
	before := countRows(t, f, `SELECT COUNT(*) FROM calendar_events WHERE import_id = ?`, created.ID)
	path := "/api/v1/calendar/imports/" + created.ID + "/sync"

	// Creating the link counts as an attempt, so move past the sync-now cooldown first.
	f.clk.now = f.clk.now.Add(2 * time.Minute)
	lf.set(http.StatusServiceUnavailable, nil, "")
	rec := f.do(t, f.parent, http.MethodPost, path, nil, nil)
	expectStatus(t, rec, http.StatusOK)
	failed := decodeImport(t, rec)
	if failed.LastSyncError == nil || *failed.LastSyncError != "unavailable" {
		t.Fatalf("sync error = %v, want unavailable", failed.LastSyncError)
	}
	if failed.EventCount != before || failed.Kind != "link" {
		t.Fatalf("failed sync changed the import: events=%d kind=%s", failed.EventCount, failed.Kind)
	}

	f.clk.now = f.clk.now.Add(2 * time.Minute)
	lf.set(http.StatusOK, termICS, `"v2"`)
	rec = f.do(t, f.parent, http.MethodPost, path, nil, nil)
	expectStatus(t, rec, http.StatusOK)
	if fixed := decodeImport(t, rec); fixed.LastSyncError != nil || fixed.LastSyncedAt == nil {
		t.Fatalf("repaired sync = %+v, want no error and a sync time", fixed)
	}
}

func TestSyncNowIsRateLimitedAndAudited(t *testing.T) {
	f := newFixture(t)
	allowLoopback(f)
	created := createLinked(t, f, newLinkedFeed(t, termICS))
	path := "/api/v1/calendar/imports/" + created.ID + "/sync"

	// The link was just created, so the first manual sync is inside the cooldown.
	rec := f.do(t, f.parent, http.MethodPost, path, nil, nil)
	expectStatus(t, rec, http.StatusTooManyRequests)
	if code := importErrorCode(rec); code != "sync_too_soon" {
		t.Fatalf("error code = %q, want sync_too_soon", code)
	}

	f.clk.now = f.clk.now.Add(syncNowCooldown)
	expectStatus(t, f.do(t, f.parent, http.MethodPost, path, nil, nil), http.StatusOK)
	expectStatus(t, f.do(t, f.parent, http.MethodPost, path, nil, nil), http.StatusTooManyRequests)

	if n := countRows(t, f, `SELECT COUNT(*) FROM audit_logs WHERE action = ? AND target_id = ?`, "calendar_import.synced", created.ID); n != 1 {
		t.Fatalf("synced audit rows = %d, want 1 (refused attempts are not audited)", n)
	}
}

func TestSyncNowAccessControl(t *testing.T) {
	f := newFixture(t)
	allowLoopback(f)
	created := createLinked(t, f, newLinkedFeed(t, termICS))
	path := "/api/v1/calendar/imports/" + created.ID + "/sync"

	expectStatus(t, f.do(t, f.kid, http.MethodPost, path, nil, nil), http.StatusForbidden)
	expectStatus(t, f.do(t, f.other, http.MethodPost, path, nil, nil), http.StatusNotFound)
	expectStatus(t, f.do(t, f.parent, http.MethodPost, "/api/v1/calendar/imports/not-a-uuid/sync", nil, nil), http.StatusNotFound)
}

func TestSyncNowOfFileImportIsBadRequest(t *testing.T) {
	f := newFixture(t)
	created := decodeImport(t, uploadICS(t, f, f.parent, "", "school.ics", termICS))
	rec := f.do(t, f.parent, http.MethodPost, "/api/v1/calendar/imports/"+created.ID+"/sync", nil, nil)
	expectStatus(t, rec, http.StatusBadRequest)
	if code := importErrorCode(rec); code != "not_linked" {
		t.Fatalf("error code = %q, want not_linked", code)
	}
}

func TestSyncDueLinksWaitsForInterval(t *testing.T) {
	f := newFixture(t)
	allowLoopback(f)
	ctx := context.Background()
	lf := newLinkedFeed(t, termICS)
	created := createLinked(t, f, lf)
	before := countRows(t, f, `SELECT COUNT(*) FROM calendar_events WHERE import_id = ?`, created.ID)

	// Creating the import counts as its first attempt, so nothing is due straight away.
	if n, err := f.h.SyncDueLinks(ctx); err != nil || n != 0 {
		t.Fatalf("right after create: n=%d err=%v, want 0", n, err)
	}
	f.clk.now = fixtureNow.Add(29 * time.Minute)
	if n, _ := f.h.SyncDueLinks(ctx); n != 0 {
		t.Fatalf("at 29 min: n=%d, want 0", n)
	}
	fetchesBefore := lf.fetches()
	f.clk.now = fixtureNow.Add(30 * time.Minute)
	if n, err := f.h.SyncDueLinks(ctx); err != nil || n != 1 {
		t.Fatalf("at 30 min: n=%d err=%v, want 1", n, err)
	}
	if lf.fetches() != fetchesBefore+1 {
		t.Fatal("due link was not fetched")
	}

	// A source that fails is retried once per interval, keeps its events, and recovers when repaired.
	lf.set(http.StatusInternalServerError, nil, "")
	f.clk.now = fixtureNow.Add(60 * time.Minute)
	if n, _ := f.h.SyncDueLinks(ctx); n != 1 {
		t.Fatalf("failing source at 60 min: n=%d, want 1", n)
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_imports WHERE id = ? AND last_sync_error = 'unavailable'`, created.ID); n != 1 {
		t.Fatal("failed scheduled sync did not record its error")
	}
	f.clk.now = fixtureNow.Add(75 * time.Minute)
	if n, _ := f.h.SyncDueLinks(ctx); n != 0 {
		t.Fatalf("failing source retried before its interval: n=%d", n)
	}
	lf.set(http.StatusOK, termICS, `"v2"`)
	f.clk.now = fixtureNow.Add(90 * time.Minute)
	if n, _ := f.h.SyncDueLinks(ctx); n != 1 {
		t.Fatalf("repaired source at 90 min: n=%d, want 1", n)
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_imports WHERE id = ? AND last_sync_error IS NULL`, created.ID); n != 1 {
		t.Fatal("repaired source did not clear its error")
	}
	if n := countRows(t, f, `SELECT COUNT(*) FROM calendar_events WHERE import_id = ?`, created.ID); n != before {
		t.Fatalf("events after failures = %d, want %d kept", n, before)
	}
}

func TestSyncDueLinksSkipsPausedAndFileImports(t *testing.T) {
	f := newFixture(t)
	allowLoopback(f)
	ctx := context.Background()
	lf := newLinkedFeed(t, termICS)
	created := createLinked(t, f, lf)
	uploadICS(t, f, f.parent, "", "school.ics", termICS)

	pause := f.do(t, f.parent, http.MethodPatch, "/api/v1/calendar/imports/"+created.ID,
		map[string]any{"name": created.Name, "colour": created.Colour, "paused": true}, nil)
	expectStatus(t, pause, http.StatusNoContent)

	fetchesBefore := lf.fetches()
	f.clk.now = fixtureNow.Add(2 * time.Hour)
	if n, err := f.h.SyncDueLinks(ctx); err != nil || n != 0 {
		t.Fatalf("paused link and file import: n=%d err=%v, want 0", n, err)
	}
	if lf.fetches() != fetchesBefore {
		t.Fatal("paused link was fetched")
	}
}

func TestSyncDueLinksCapsEachPass(t *testing.T) {
	f := newFixture(t)
	allowLoopback(f)
	ctx := context.Background()
	lf := newLinkedFeed(t, termICS)
	for i := 0; i < maxSyncPerPass+1; i++ {
		createLinked(t, f, lf)
	}

	f.clk.now = fixtureNow.Add(30 * time.Minute)
	if n, err := f.h.SyncDueLinks(ctx); err != nil || n != maxSyncPerPass {
		t.Fatalf("first pass: n=%d err=%v, want %d", n, err, maxSyncPerPass)
	}
	if n, err := f.h.SyncDueLinks(ctx); err != nil || n != 1 {
		t.Fatalf("second pass: n=%d err=%v, want the one left over", n, err)
	}
}
