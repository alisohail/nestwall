package tasks

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/web"
)

// Day windows for the child's own list, in days from the family's today.
const (
	mineCatchUpDays  = 30  // an open copy that is past due stays in catch-up for this long
	mineComingUpDays = 7   // open copies due within this many days after today
	mineDoneDays     = 7   // approved copies due within this many days before today
	mineStreakDays   = 366 // how far back the streak is counted, today included
)

// MyTasksResponse is the child's own copies for the Today and My tasks screens. Today and CatchUp
// hold only open copies. Waiting holds submitted copies. Expired holds open copies whose occurrence
// was missed. Coming up and Done are paged through their own endpoints.
type MyTasksResponse struct {
	Date       string       `json:"date"`
	Timezone   string       `json:"timezone"`
	Now        string       `json:"now"`
	StreakDays int          `json:"streak_days"`
	Summary    MySummary    `json:"summary"`
	Today      []MyTaskItem `json:"today"`
	CatchUp    []MyTaskItem `json:"catch_up"`
	Waiting    []MyTaskItem `json:"waiting"`
	Expired    []MyTaskItem `json:"expired"`
}

// myLists is every section of the child's list. MyTasks sends Today, CatchUp, Waiting and Expired;
// the paged endpoints send ComingUp and Done.
type myLists struct {
	Summary  MySummary
	Today    []MyTaskItem
	CatchUp  []MyTaskItem
	ComingUp []MyTaskItem
	Waiting  []MyTaskItem
	Expired  []MyTaskItem
	Done     []MyTaskItem
}

// myListPage is one page of the child's coming-up or done copies. next_cursor is null on the last page.
type myListPage struct {
	Tasks      []MyTaskItem `json:"tasks"`
	NextCursor *string      `json:"next_cursor"`
}

// MySummary counts today's copies. A missed copy counts toward the total but never toward done.
type MySummary struct {
	TodayTotal int         `json:"today_total"`
	TodayDone  int         `json:"today_done"`
	NextUp     *MyTaskItem `json:"next_up"`
}

// MyTaskItem is one copy as the child sees it. Points are the full value; penalty points are the
// amount deducted for a late completion when the task has a penalty.
type MyTaskItem struct {
	TaskID           string          `json:"task_id"`
	Title            string          `json:"title"`
	CategoryEmoji    *string         `json:"category_emoji"`
	CategoryColour   *string         `json:"category_colour"`
	PartOfDay        *string         `json:"part_of_day"`
	DueDate          string          `json:"due_date"`
	DueAt            string          `json:"due_at"`
	ExpiresAt        *string         `json:"expires_at"`
	Points           int             `json:"points"`
	ApprovalRequired bool            `json:"approval_required"`
	Status           string          `json:"status"`
	PastDue          bool            `json:"past_due"`
	CompletedAt      *string         `json:"completed_at"`
	CompletedLate    bool            `json:"completed_late"`
	DecidedAt        *string         `json:"decided_at"`
	DecidedByName    *string         `json:"decided_by_name"`
	LatePointsPct    *int            `json:"late_points_percent"`
	PenaltyValue     *int            `json:"penalty_value"`
	PenaltyUnit      *string         `json:"penalty_unit"`
	PenaltyPoints    *int            `json:"penalty_points"`
	Version          int             `json:"version"`
	Checklist        []checklistItem `json:"checklist,omitempty"`
}

// myRow is one assignee copy joined to its occurrence, definition and category.
type myRow struct {
	taskID           string
	title            string
	emoji            sql.NullString
	colour           sql.NullString
	partOfDay        sql.NullString
	dueDate          string
	dueAt            time.Time
	dueAtText        string
	expiresAt        *time.Time
	expiresAtText    *string
	points           int
	approvalRequired bool
	latePct          sql.NullInt64
	penaltyValue     sql.NullInt64
	penaltyUnit      sql.NullString
	status           string
	occurrenceStatus string
	completedAt      *string
	completedLate    bool
	decidedAt        *string
	decidedByName    *string
	version          int
	assigneeID       string
	checklist        []checklistItem
}

// myCopyFrom is the shared select for the child's copies. Callers add the date window.
const myCopyFrom = `SELECT o.id, d.title, c.emoji, c.colour, d.part_of_day, o.due_date,
	strftime('%Y-%m-%dT%H:%M:%SZ', o.due_at), strftime('%Y-%m-%dT%H:%M:%SZ', o.expires_at),
	d.points, d.approval_required, d.late_points_percent, d.penalty_value, d.penalty_unit,
	oa.status, o.status, strftime('%Y-%m-%dT%H:%M:%SZ', oa.completed_at), oa.completed_late,
	strftime('%Y-%m-%dT%H:%M:%SZ', oa.decided_at), du.display_name, d.version, oa.id
FROM task_occurrence_assignees oa
JOIN task_occurrences o ON o.family_id = oa.family_id AND o.id = oa.task_occurrence_id
JOIN task_definitions d ON d.family_id = o.family_id AND d.id = o.task_definition_id
LEFT JOIN task_categories c ON c.family_id = d.family_id AND c.id = d.category_id
LEFT JOIN family_members dm ON dm.family_id = oa.family_id AND dm.id = oa.decided_by_member_id
LEFT JOIN users du ON du.id = dm.user_id
WHERE oa.family_id = ? AND oa.family_member_id = ? AND d.status = 'active' AND oa.status != 'skipped'
  AND o.due_date >= ? AND o.due_date <= ?
ORDER BY o.due_at, o.id`

// myStreakDaysQuery totals required and done copies per due date, newest first.
const myStreakDaysQuery = `SELECT o.due_date, COUNT(*),
	SUM(CASE WHEN oa.status IN ('submitted', 'approved') THEN 1 ELSE 0 END)
FROM task_occurrence_assignees oa
JOIN task_occurrences o ON o.family_id = oa.family_id AND o.id = oa.task_occurrence_id
JOIN task_definitions d ON d.family_id = o.family_id AND d.id = o.task_definition_id
WHERE oa.family_id = ? AND oa.family_member_id = ? AND d.status = 'active' AND oa.status != 'skipped'
  AND o.due_date >= ? AND o.due_date <= ?
GROUP BY o.due_date
ORDER BY o.due_date DESC`

// MyTasks handles GET /api/v1/me/tasks. It returns only the signed-in member's own copies; the route
// requires tasks.view. Classification happens here, not in the client.
func (h *Handlers) MyTasks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	familyID, _, ok := h.memberFamily(w, r)
	if !ok {
		return
	}
	memberID, _ := permissions.GetFamilyMemberID(ctx)

	view, err := h.loadMyView(ctx, familyID, memberID)
	if err != nil {
		h.internalError(w, "load my tasks", err)
		return
	}
	totals, err := h.myStreakTotals(ctx, familyID, memberID, addDaysISO(view.today, -(mineStreakDays-1)), view.today)
	if err != nil {
		h.internalError(w, "load streak", err)
		return
	}

	web.RespondJSON(w, http.StatusOK, MyTasksResponse{
		Date:       view.today,
		Timezone:   view.zoneName,
		Now:        view.now.UTC().Format(time.RFC3339),
		StreakDays: streakDays(totals, view.today),
		Summary:    view.lists.Summary,
		Today:      view.lists.Today,
		CatchUp:    view.lists.CatchUp,
		Waiting:    view.lists.Waiting,
		Expired:    view.lists.Expired,
	})
}

// MyComingUp handles GET /api/v1/me/tasks/coming. It returns one page of the child's open copies due
// in the coming days, earliest first.
func (h *Handlers) MyComingUp(w http.ResponseWriter, r *http.Request) {
	h.myPage(w, r, func(l myLists) []MyTaskItem { return l.ComingUp }, false)
}

// MyDone handles GET /api/v1/me/tasks/done. It returns one page of the child's approved copies from
// the last few days, newest first.
func (h *Handlers) MyDone(w http.ResponseWriter, r *http.Request) {
	h.myPage(w, r, func(l myLists) []MyTaskItem { return l.Done }, true)
}

// myPage writes one cursor page of a list. pick chooses the list and newestFirst gives its order.
func (h *Handlers) myPage(w http.ResponseWriter, r *http.Request, pick func(myLists) []MyTaskItem, newestFirst bool) {
	ctx := r.Context()
	familyID, _, ok := h.memberFamily(w, r)
	if !ok {
		return
	}
	memberID, _ := permissions.GetFamilyMemberID(ctx)

	query := r.URL.Query()
	limit, err := parsePageSize(query.Get("limit"))
	if err != nil {
		respondValidation(w, err)
		return
	}
	var after *myPos
	if raw := query.Get("cursor"); raw != "" {
		dueAt, taskID, err := decodeCursor(raw)
		if err != nil {
			respondValidation(w, err)
			return
		}
		after = &myPos{dueAt: dueAt, taskID: taskID}
	}

	view, err := h.loadMyView(ctx, familyID, memberID)
	if err != nil {
		h.internalError(w, "load my tasks", err)
		return
	}
	page, next := pageMyItems(pick(view.lists), limit, after, newestFirst)
	web.RespondJSON(w, http.StatusOK, myListPage{Tasks: page, NextCursor: next})
}

// myPos is a point in a list: the due time and occurrence ID that the lists sort on.
type myPos struct {
	dueAt  string
	taskID string
}

// pageMyItems returns up to limit items after the position, in list order, and the cursor for the
// next page. The lists run by due time then occurrence ID; newestFirst reverses that order. The
// cursor is nil when no items follow.
func pageMyItems(items []MyTaskItem, limit int, after *myPos, newestFirst bool) ([]MyTaskItem, *string) {
	page := []MyTaskItem{}
	for _, it := range items {
		if after != nil {
			c := strings.Compare(it.DueAt, after.dueAt)
			if c == 0 {
				c = strings.Compare(it.TaskID, after.taskID)
			}
			if newestFirst {
				c = -c
			}
			if c <= 0 {
				continue
			}
		}
		if len(page) == limit {
			last := page[len(page)-1]
			next := encodeCursor(last.DueAt, last.TaskID)
			return page, &next
		}
		page = append(page, it)
	}
	return page, nil
}

// myView is the child's sections as of one request, with the family's date and time zone.
type myView struct {
	lists    myLists
	now      time.Time
	today    string
	zoneName string
}

// loadMyView loads the child's copies and sorts them into sections for the family's today.
func (h *Handlers) loadMyView(ctx context.Context, familyID, memberID string) (myView, error) {
	zone, zoneName, err := familyZone(ctx, h.db, familyID)
	if err != nil {
		return myView{}, fmt.Errorf("load family time zone: %w", err)
	}
	now := h.clock.Now()
	today := now.In(zone).Format(time.DateOnly)

	rows, err := h.myCopies(ctx, familyID, memberID, addDaysISO(today, -mineCatchUpDays), addDaysISO(today, mineComingUpDays))
	if err != nil {
		return myView{}, fmt.Errorf("load copies: %w", err)
	}
	return myView{lists: buildMyTasks(rows, now, today), now: now, today: today, zoneName: zoneName}, nil
}

// myCopies loads the member's copies due between from and to, both inclusive family dates.
func (h *Handlers) myCopies(ctx context.Context, familyID, memberID, from, to string) ([]myRow, error) {
	rows, err := h.db.QueryContext(ctx, myCopyFrom, familyID, memberID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []myRow
	for rows.Next() {
		var r myRow
		var dueText string
		var expiresText sql.NullString
		var completedText, decidedText, decidedBy sql.NullString
		if err := rows.Scan(
			&r.taskID, &r.title, &r.emoji, &r.colour, &r.partOfDay, &r.dueDate,
			&dueText, &expiresText, &r.points, &r.approvalRequired, &r.latePct,
			&r.penaltyValue, &r.penaltyUnit, &r.status, &r.occurrenceStatus,
			&completedText, &r.completedLate, &decidedText, &decidedBy, &r.version, &r.assigneeID,
		); err != nil {
			return nil, err
		}
		if decidedText.Valid {
			r.decidedAt = &decidedText.String
		}
		if decidedBy.Valid {
			r.decidedByName = &decidedBy.String
		}
		due, err := time.Parse(time.RFC3339, dueText)
		if err != nil {
			return nil, err
		}
		r.dueAt = due
		r.dueAtText = dueText
		if expiresText.Valid {
			expires, err := time.Parse(time.RFC3339, expiresText.String)
			if err != nil {
				return nil, err
			}
			r.expiresAt = &expires
			r.expiresAtText = &expiresText.String
		}
		if completedText.Valid {
			r.completedAt = &completedText.String
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(out))
	for _, r := range out {
		ids = append(ids, r.assigneeID)
	}
	checklists, err := loadChecklists(ctx, h.db, familyID, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].checklist = checklists[out[i].assigneeID]
	}
	return out, nil
}

// myStreakTotals loads per-day totals for the streak window, newest first.
func (h *Handlers) myStreakTotals(ctx context.Context, familyID, memberID, from, to string) ([]dayTotals, error) {
	rows, err := h.db.QueryContext(ctx, myStreakDaysQuery, familyID, memberID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []dayTotals
	for rows.Next() {
		var d dayTotals
		if err := rows.Scan(&d.date, &d.total, &d.done); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// buildMyTasks sorts copies into the response sections. rows must be ordered by due time. now is
// the instant an open copy is checked against; today is the family's date.
func buildMyTasks(rows []myRow, now time.Time, today string) myLists {
	resp := myLists{
		Today:    []MyTaskItem{},
		CatchUp:  []MyTaskItem{},
		ComingUp: []MyTaskItem{},
		Waiting:  []MyTaskItem{},
		Expired:  []MyTaskItem{},
		Done:     []MyTaskItem{},
	}
	comingEnd := addDaysISO(today, mineComingUpDays)
	doneStart := addDaysISO(today, -(mineDoneDays - 1))

	for _, r := range rows {
		open := r.status == "to_do" || r.status == "rejected"
		done := r.status == "submitted" || r.status == "approved"
		missed := open && (r.occurrenceStatus == "missed" || (r.expiresAt != nil && !r.expiresAt.After(now)))
		pastDue := open && !r.dueAt.After(now)

		if r.dueDate == today {
			resp.Summary.TodayTotal++
			if done {
				resp.Summary.TodayDone++
			}
		}
		item := r.item()
		item.PastDue = pastDue

		if missed {
			resp.Expired = append(resp.Expired, item)
			continue
		}

		if r.status == "submitted" {
			resp.Waiting = append(resp.Waiting, item)
		}
		if r.status == "approved" && r.dueDate >= doneStart {
			resp.Done = append(resp.Done, item)
		}
		switch {
		case pastDue:
			resp.CatchUp = append(resp.CatchUp, item)
		case open && r.dueDate == today:
			resp.Today = append(resp.Today, item)
			if open && resp.Summary.NextUp == nil {
				next := item
				resp.Summary.NextUp = &next
			}
		case open && r.dueDate > today && r.dueDate <= comingEnd:
			resp.ComingUp = append(resp.ComingUp, item)
		}
	}

	// Done is newest first; rows came in due-time order.
	slices.Reverse(resp.Done)
	// Missed is newest first too, so the latest miss sits at the top.
	slices.Reverse(resp.Expired)
	return resp
}

// item converts a row to the response shape with the penalty worked out from the task's points.
func (r myRow) item() MyTaskItem {
	it := MyTaskItem{
		TaskID:           r.taskID,
		Title:            r.title,
		CategoryEmoji:    nullableText(r.emoji),
		CategoryColour:   nullableText(r.colour),
		PartOfDay:        nullableText(r.partOfDay),
		DueDate:          r.dueDate,
		DueAt:            r.dueAtText,
		ExpiresAt:        r.expiresAtText,
		Points:           r.points,
		ApprovalRequired: r.approvalRequired,
		Status:           r.status,
		CompletedAt:      r.completedAt,
		DecidedAt:        r.decidedAt,
		DecidedByName:    r.decidedByName,
		CompletedLate:    r.completedLate,
		Version:          r.version,
		Checklist:        r.checklist,
	}
	if r.latePct.Valid {
		pct := int(r.latePct.Int64)
		it.LatePointsPct = &pct
	}
	if r.penaltyValue.Valid && r.penaltyUnit.Valid {
		value := int(r.penaltyValue.Int64)
		unit := r.penaltyUnit.String
		pts := PenaltyPoints(value, unit, r.points)
		it.PenaltyValue = &value
		it.PenaltyUnit = &unit
		it.PenaltyPoints = &pts
	}
	return it
}

// dayTotals is one family date in the streak window.
type dayTotals struct {
	date  string
	total int
	done  int
}

// streakDays counts the run of complete days ending at the newest day. A day with no required copies
// is neutral. Today is neutral while incomplete. Any other incomplete day ends the run. days must be
// newest first.
func streakDays(days []dayTotals, today string) int {
	n := 0
	for _, d := range days {
		if d.total == 0 {
			continue
		}
		if d.done == d.total {
			n++
			continue
		}
		if d.date == today {
			continue
		}
		break
	}
	return n
}

// addDaysISO adds whole days to a family date (YYYY-MM-DD).
func addDaysISO(date string, days int) string {
	t, err := time.Parse(time.DateOnly, date)
	if err != nil {
		return date
	}
	return t.AddDate(0, 0, days).Format(time.DateOnly)
}

// nullableText returns the string or nil for a NULL column.
func nullableText(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}
