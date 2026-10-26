package calendar

import (
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// visibilityWindow covers every event the visibility world creates.
const visibilityWindow = "from=2026-10-10&to=2026-10-20"

// visibilityWant lists the titles each person may see in the window. A person sees the events
// assigned to them and the events from imports for everyone.
var visibilityWant = map[string][]string{
	"parent": {"Everyone event", "Kid event", "Kid only", "Other kid event", "Other kid only", "Parent only", "Parents event", "Shared"},
	"kid":    {"Everyone event", "Kid event", "Kid only", "Shared"},
	"kid2":   {"Everyone event", "Other kid event", "Other kid only"},
}

// visibilityWorld is a family with a parent, a child and a second child. It has one local event and
// one imported feed for each audience, so every rule has a visible and a hidden case for each person.
type visibilityWorld struct {
	f       fixture
	kid2    actor
	events  map[string]string // event title -> event ID, as the parent sees it
	imports map[string]string // import name -> import ID
}

func newVisibilityWorld(t *testing.T) visibilityWorld {
	t.Helper()
	f := newFixture(t)
	kid2 := addChild(t, f.db, f.clk, f.a.FamilyID)
	w := visibilityWorld{f: f, kid2: kid2, events: map[string]string{}, imports: map[string]string{}}

	locals := []struct {
		title   string
		members []string
	}{
		{"Parent only", []string{f.parent.memberID}},
		{"Kid only", []string{f.kid.memberID}},
		{"Shared", []string{f.parent.memberID, f.kid.memberID}},
		{"Other kid only", []string{kid2.memberID}},
	}
	for _, l := range locals {
		f.createEvent(t, f.parent, timedBody(l.title, "2026-10-12", "09:00", "10:00", l.members...))
	}

	feeds := []struct {
		name, audience, uid, summary string
		members                      []string
	}{
		{"Everyone", "everyone", "everyone-1", "Everyone event", nil},
		{"Parents", "parents", "parents-1", "Parents event", nil},
		{"Kid", "specific", "kid-1", "Kid event", []string{f.kid.memberID}},
		{"Other kid", "specific", "other-1", "Other kid event", []string{kid2.memberID}},
	}
	for _, feed := range feeds {
		fields := url.Values{"name": {feed.name}, "audience": {feed.audience}}
		for _, m := range feed.members {
			fields.Add("member_ids", m)
		}
		rec := uploadImport(t, f, f.parent, fields, "feed.ics", visibilityICS(feed.uid, feed.summary))
		expectStatus(t, rec, http.StatusCreated)
		w.imports[feed.name] = decodeImport(t, rec).ID
	}

	resp, _ := f.rangeGet(t, f.parent, visibilityWindow)
	for _, it := range resp.Items {
		if it.EventID == nil {
			t.Fatalf("item %q has no event_id", it.Title)
		}
		w.events[it.Title] = *it.EventID
	}
	if len(w.events) != 8 {
		t.Fatalf("parent sees %d events, want 8: %v", len(w.events), w.events)
	}
	return w
}

// visibilityICS is a feed with one event on 12 Oct 2026.
func visibilityICS(uid, summary string) []byte {
	return wrapCalendar(vevent(
		"UID:"+uid+"@test",
		"DTSTAMP:20260901T000000Z",
		"DTSTART:20261012T090000Z",
		"DTEND:20261012T100000Z",
		"SUMMARY:"+summary,
	))
}

// who returns the actor for a name used in visibilityWant.
func (w visibilityWorld) who(name string) actor {
	switch name {
	case "parent":
		return w.f.parent
	case "kid":
		return w.f.kid
	default:
		return w.kid2
	}
}

func visibleTitles(items []rangeItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Title)
	}
	sort.Strings(out)
	return out
}

// checkTitles compares the titles, ignoring order.
func checkTitles(t *testing.T, label string, got, want []string) {
	t.Helper()
	want = append([]string(nil), want...)
	sort.Strings(want)
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
}

func TestVisibilityRangeShowsOnlyVisibleEvents(t *testing.T) {
	w := newVisibilityWorld(t)
	for _, name := range []string{"parent", "kid", "kid2"} {
		resp, _ := w.f.rangeGet(t, w.who(name), visibilityWindow)
		checkTitles(t, name+" range", visibleTitles(resp.Items), visibilityWant[name])
	}
}

func TestVisibilityEventReadMatchesRange(t *testing.T) {
	w := newVisibilityWorld(t)
	const missing = "00000000-0000-4000-8000-000000000000"
	for _, name := range []string{"parent", "kid", "kid2"} {
		visible := map[string]bool{}
		for _, title := range visibilityWant[name] {
			visible[title] = true
		}
		for title, id := range w.events {
			t.Run(name+" reads "+title, func(t *testing.T) {
				want := http.StatusNotFound
				if visible[title] {
					want = http.StatusOK
				}
				expectStatus(t, w.f.get(t, w.who(name), id), want)
			})
		}
		// A hidden event must look the same as one that does not exist.
		t.Run(name+" reads a missing event", func(t *testing.T) {
			expectStatus(t, w.f.get(t, w.who(name), missing), http.StatusNotFound)
		})
	}
}

func TestVisibilityFiltersNeverWiden(t *testing.T) {
	w := newVisibilityWorld(t)
	cases := []struct {
		name, who, query string
		want             []string
	}{
		{"child filtered to the parent", "kid", "member_id=" + w.f.parent.memberID, []string{"Shared"}},
		{"child filtered to a sibling", "kid", "member_id=" + w.kid2.memberID, nil},
		{"child on the parents import", "kid", "calendar_id=" + w.imports["Parents"], nil},
		{"child on a sibling's import", "kid", "calendar_id=" + w.imports["Other kid"], nil},
		{"child on the everyone import", "kid", "calendar_id=" + w.imports["Everyone"], []string{"Everyone event"}},
		{"sibling on the child's import", "kid2", "calendar_id=" + w.imports["Kid"], nil},
		{"parent filtered to the child", "parent", "member_id=" + w.f.kid.memberID, []string{"Kid event", "Kid only", "Shared"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, _ := w.f.rangeGet(t, w.who(tc.who), visibilityWindow+"&"+tc.query)
			checkTitles(t, tc.name, visibleTitles(resp.Items), tc.want)
		})
	}
}

func TestVisibilityExportIsFullViewOnly(t *testing.T) {
	w := newVisibilityWorld(t)
	for _, name := range []string{"kid", "kid2"} {
		rec := w.f.do(t, w.who(name), http.MethodGet, "/api/v1/calendar/export.ics", nil, nil)
		expectError(t, rec, http.StatusForbidden, "forbidden")
		for title := range w.events {
			if strings.Contains(rec.Body.String(), title) {
				t.Fatalf("%s export refusal mentions %q", name, title)
			}
		}
	}

	rec := w.f.do(t, w.f.parent, http.MethodGet, "/api/v1/calendar/export.ics", nil, nil)
	expectStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	if n := strings.Count(body, "BEGIN:VEVENT"); n != len(w.events) {
		t.Fatalf("parent export has %d events, want %d", n, len(w.events))
	}
	for title := range w.events {
		if !strings.Contains(body, "SUMMARY:"+title) {
			t.Fatalf("parent export is missing %q", title)
		}
	}
}

func TestVisibilityWritesNeedCalendarManage(t *testing.T) {
	w := newVisibilityWorld(t)
	shared := "/api/v1/calendar/events/" + w.events["Shared"]
	writes := []struct {
		name, method, path string
		body               any
	}{
		{"edit a shared event", http.MethodPut, shared,
			timedBody("Changed", "2026-10-12", "09:00", "10:00", w.f.kid.memberID)},
		{"delete a shared event", http.MethodDelete, shared, nil},
		{"cancel an occurrence", http.MethodDelete, shared + "/occurrences/2026-10-12", nil},
		{"edit an import", http.MethodPatch, "/api/v1/calendar/imports/" + w.imports["Everyone"],
			map[string]any{"name": "Everyone", "colour": "#14b8a6", "audience": "specific",
				"member_ids": []string{w.f.kid.memberID}}},
		{"delete an import", http.MethodDelete, "/api/v1/calendar/imports/" + w.imports["Everyone"], nil},
	}
	for _, tc := range writes {
		t.Run(tc.name, func(t *testing.T) {
			rec := w.f.do(t, w.f.kid, tc.method, tc.path, tc.body, map[string]string{"If-Match": `"1"`})
			expectError(t, rec, http.StatusForbidden, "forbidden")
		})
	}

	resp, _ := w.f.rangeGet(t, w.f.parent, visibilityWindow)
	checkTitles(t, "parent range after refused writes", visibleTitles(resp.Items), visibilityWant["parent"])
}
