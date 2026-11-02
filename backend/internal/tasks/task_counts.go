package tasks

import (
	"net/http"
	"net/url"

	"nestwall/backend/internal/platform/web"
)

// CountTasks returns how many copies each task tab lists, in the family's time zone. Each count is
// the list for that tab with the same filters, so a count matches what the tab shows. The tabs are
// overdue, today, upcoming, submitted, approved, expired and archived. The member filter and the
// caller's scope apply as they do to the list.
func (h *Handlers) CountTasks(w http.ResponseWriter, r *http.Request) {
	familyID, scope, ok := h.readScope(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	loc, _, err := familyZone(ctx, h.db, familyID)
	if err != nil {
		h.internalError(w, "load family time zone for counts", err)
		return
	}
	now := h.clock.Now()
	today := now.In(loc).Format("2006-01-02")
	tomorrow := now.In(loc).AddDate(0, 0, 1).Format("2006-01-02")
	stamp := dbTime(now)

	member := r.URL.Query().Get("member")
	type tabFilter struct {
		query url.Values
		// extra is a condition only the overdue tab adds: an open copy whose due time has passed.
		extra bool
	}
	tabs := map[string]tabFilter{
		"overdue":   {query: url.Values{"status": {"to_do"}, "due_to": {today}}, extra: true},
		"today":     {query: url.Values{"status": {"to_do"}, "due_from": {today}, "due_to": {today}}},
		"upcoming":  {query: url.Values{"status": {"to_do"}, "due_from": {tomorrow}}},
		"submitted": {query: url.Values{"status": {"submitted"}}},
		"approved":  {query: url.Values{"status": {"approved"}}},
		"expired":   {query: url.Values{"expired": {"true"}}},
		"archived":  {query: url.Values{"archived": {"true"}}},
	}

	counts := make(map[string]int, len(tabs))
	for name, tab := range tabs {
		if member != "" {
			tab.query.Set("member", member)
		}
		where, args, err := listConditions(tab.query)
		if err != nil {
			respondValidation(w, err)
			return
		}
		args = append([]any{familyID}, args...)
		if tab.extra {
			where += ` AND CAST(o.due_at AS TEXT) < ?`
			args = append(args, stamp)
		}
		if scope != "" {
			where += ` AND oa.family_member_id = ?`
			args = append(args, scope)
		}
		var n int
		if err := h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+taskRowSelect+where+`)`, args...).Scan(&n); err != nil {
			h.internalError(w, "count tasks for "+name, err)
			return
		}
		counts[name] = n
	}
	web.RespondJSON(w, http.StatusOK, counts)
}
