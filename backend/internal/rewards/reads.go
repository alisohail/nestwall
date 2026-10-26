package rewards

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"nestwall/backend/internal/platform/web"
)

// Read endpoints for the points and redemption screens (M4-06). A child sees only their own
// rows. A parent sees every member of the family and may filter by member.

const (
	defaultPageSize = 50
	maxPageSize     = 200
)

// earnedTypes are the ledger types that count as points earned in the week chart.
var earnedTypes = []EntryType{TaskAward, BonusTaskAward, Bonus, TransferIn}

// MyPointsResponse is the body of GET /api/v1/me/points. Balance is the spendable total, after
// points held by pending requests. Reserved is the sum of those held points. Week counts points
// earned per local day, Monday first, and Today is the index of the current day in Week. TasksDone
// counts approved tasks by completion day, TasksMissed counts missed tasks by due day, and Bonus
// sums bonus points earned per day. All three use the same day indexing as Week.
type MyPointsResponse struct {
	MemberID    string `json:"member_id"`
	Balance     int    `json:"balance"`
	Reserved    int    `json:"reserved"`
	Timezone    string `json:"timezone"`
	WeekStart   string `json:"week_start"`
	Today       int    `json:"today"`
	Week        [7]int `json:"week"`
	TasksDone   [7]int `json:"tasks_done"`
	TasksMissed [7]int `json:"tasks_missed"`
	Bonus       [7]int `json:"bonus"`
}

// PointsEntry is one ledger row, as shown in the recent points and points history lists.
type PointsEntry struct {
	ID         string    `json:"id"`
	MemberID   string    `json:"member_id"`
	Type       string    `json:"type"`
	Amount     int       `json:"amount"`
	Reason     *string   `json:"reason"`
	RewardName *string   `json:"reward_name"`
	CreatedAt  time.Time `json:"created_at"`
}

// PointsEntryPage is the body of GET /api/v1/points/entries.
type PointsEntryPage struct {
	Entries    []PointsEntry `json:"entries"`
	NextCursor *string       `json:"next_cursor"`
}

// RedemptionListItem is a redemption with the reward and the requester's display name.
type RedemptionListItem struct {
	RewardRedemption
	RewardName string `json:"reward_name"`
	RewardIcon string `json:"reward_icon"`
	MemberName string `json:"member_name"`
}

// RedemptionPage is the body of GET /api/v1/redemptions.
type RedemptionPage struct {
	Redemptions []RedemptionListItem `json:"redemptions"`
	NextCursor  *string              `json:"next_cursor"`
}

// MyPoints handles GET /api/v1/me/points. It always describes the caller, for child and parent
// alike.
func (h *Handlers) MyPoints(w http.ResponseWriter, r *http.Request) {
	familyID, _, memberID, ok := h.actor(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	loc, err := familyLocation(ctx, h.db, familyID)
	if err != nil {
		h.internalError(w, "points: family zone", err)
		return
	}
	local := h.clock.Now().In(loc)
	weekStart := periodStart("week", local)

	balance, err := Balance(ctx, h.db, familyID, memberID)
	if err != nil {
		h.internalError(w, "points: balance", err)
		return
	}
	var reserved int
	if err := h.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(cost), 0) FROM redemptions WHERE family_id = ? AND family_member_id = ? AND status = 'pending'`,
		familyID, memberID,
	).Scan(&reserved); err != nil {
		h.internalError(w, "points: reserved", err)
		return
	}
	week, err := h.weekEarned(ctx, familyID, memberID, loc, weekStart)
	if err != nil {
		h.internalError(w, "points: week", err)
		return
	}
	bonus, err := h.weekLedger(ctx, familyID, memberID, loc, weekStart, Bonus, BonusTaskAward)
	if err != nil {
		h.internalError(w, "points: bonus", err)
		return
	}
	done, err := h.weekTasksDone(ctx, familyID, memberID, loc, weekStart)
	if err != nil {
		h.internalError(w, "points: tasks done", err)
		return
	}
	missed, err := h.weekTasksMissed(ctx, familyID, memberID, loc, weekStart)
	if err != nil {
		h.internalError(w, "points: tasks missed", err)
		return
	}

	web.RespondJSON(w, http.StatusOK, MyPointsResponse{
		MemberID:    memberID,
		Balance:     balance,
		Reserved:    reserved,
		Timezone:    loc.String(),
		WeekStart:   weekStart.Format("2006-01-02"),
		Today:       dayIndex(weekStart, local),
		Week:        week,
		TasksDone:   done,
		TasksMissed: missed,
		Bonus:       bonus,
	})
}

// MemberBalance is one child's row in GET /api/v1/points/balances. Balance and Reserved follow the
// same rules as MyPointsResponse.
type MemberBalance struct {
	MemberID string `json:"member_id"`
	Balance  int    `json:"balance"`
	Reserved int    `json:"reserved"`
}

// MemberBalancesResponse is the body of GET /api/v1/points/balances.
type MemberBalancesResponse struct {
	Members []MemberBalance `json:"members"`
}

// MemberBalances handles GET /api/v1/points/balances. It is parent-only and lists each active
// child in the family, by name. The balance subquery matches Balance.
func (h *Handlers) MemberBalances(w http.ResponseWriter, r *http.Request) {
	familyID, _, memberID, ok := h.actor(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	child, err := h.isChild(ctx, memberID)
	if err != nil {
		h.internalError(w, "balances: caller", err)
		return
	}
	if child {
		web.RespondError(w, http.StatusForbidden, "forbidden", "Only parents can view member balances", nil)
		return
	}

	rows, err := h.db.QueryContext(ctx, `
		SELECT m.id,
		       (SELECT COALESCE(SUM(l.amount), 0) FROM points_ledger l WHERE l.family_id = m.family_id AND l.family_member_id = m.id),
		       (SELECT COALESCE(SUM(x.cost), 0) FROM redemptions x WHERE x.family_id = m.family_id AND x.family_member_id = m.id AND x.status = 'pending')
		FROM family_members m
		JOIN users u ON u.id = m.user_id
		WHERE m.family_id = ? AND m.status = 'active' AND u.is_child = 1
		ORDER BY u.display_name COLLATE NOCASE, m.id`,
		familyID)
	if err != nil {
		h.internalError(w, "balances: query", err)
		return
	}
	defer rows.Close()

	out := MemberBalancesResponse{Members: []MemberBalance{}}
	for rows.Next() {
		var b MemberBalance
		if err := rows.Scan(&b.MemberID, &b.Balance, &b.Reserved); err != nil {
			h.internalError(w, "balances: scan", err)
			return
		}
		out.Members = append(out.Members, b)
	}
	if err := rows.Err(); err != nil {
		h.internalError(w, "balances: rows", err)
		return
	}
	web.RespondJSON(w, http.StatusOK, out)
}

// weekEarned sums the positive earning entries of the member for each day of the week that
// starts at weekStart in the family zone.
func (h *Handlers) weekEarned(ctx context.Context, familyID, memberID string, loc *time.Location, weekStart time.Time) ([7]int, error) {
	return h.weekLedger(ctx, familyID, memberID, loc, weekStart, earnedTypes...)
}

// weekLedger sums the positive ledger entries of the given types for each day of the week.
func (h *Handlers) weekLedger(ctx context.Context, familyID, memberID string, loc *time.Location, weekStart time.Time, types ...EntryType) ([7]int, error) {
	var week [7]int
	placeholders := make([]string, len(types))
	args := []any{familyID, memberID, dbTime(weekStart.UTC())}
	for i, t := range types {
		placeholders[i] = "?"
		args = append(args, string(t))
	}
	rows, err := h.db.QueryContext(ctx,
		`SELECT CAST(created_at AS TEXT), amount FROM points_ledger
		 WHERE family_id = ? AND family_member_id = ? AND created_at >= ? AND amount > 0
		   AND entry_type IN (`+strings.Join(placeholders, ",")+`)`,
		args...)
	if err != nil {
		return week, err
	}
	defer rows.Close()
	return week, addWeekRows(rows, loc, weekStart, &week)
}

// weekTasksDone counts the member's approved tasks for each day, by the time they were completed.
func (h *Handlers) weekTasksDone(ctx context.Context, familyID, memberID string, loc *time.Location, weekStart time.Time) ([7]int, error) {
	var week [7]int
	rows, err := h.db.QueryContext(ctx,
		`SELECT CAST(completed_at AS TEXT), 1 FROM task_occurrence_assignees
		 WHERE family_id = ? AND family_member_id = ? AND status = 'approved' AND completed_at >= ?`,
		familyID, memberID, dbTime(weekStart.UTC()))
	if err != nil {
		return week, err
	}
	defer rows.Close()
	return week, addWeekRows(rows, loc, weekStart, &week)
}

// weekTasksMissed counts the member's missed tasks for each day, by their due date.
func (h *Handlers) weekTasksMissed(ctx context.Context, familyID, memberID string, loc *time.Location, weekStart time.Time) ([7]int, error) {
	var week [7]int
	rows, err := h.db.QueryContext(ctx,
		`SELECT o.due_date FROM task_occurrences o
		 JOIN task_occurrence_assignees a ON a.family_id = o.family_id AND a.task_occurrence_id = o.id
		 WHERE a.family_id = ? AND a.family_member_id = ? AND o.status = 'missed'
		   AND a.status IN ('to_do', 'rejected') AND o.due_date >= ? AND o.due_date <= ?`,
		familyID, memberID, weekStart.Format("2006-01-02"), weekStart.AddDate(0, 0, 6).Format("2006-01-02"))
	if err != nil {
		return week, err
	}
	defer rows.Close()
	for rows.Next() {
		var due string
		if err := rows.Scan(&due); err != nil {
			return week, err
		}
		day, err := time.ParseInLocation("2006-01-02", due, loc)
		if err != nil {
			return week, err
		}
		if d := dayIndex(weekStart, day); d >= 0 && d < 7 {
			week[d]++
		}
	}
	return week, rows.Err()
}

// addWeekRows adds each (timestamp, amount) row to the day of the week it falls in.
func addWeekRows(rows *sql.Rows, loc *time.Location, weekStart time.Time, week *[7]int) error {
	for rows.Next() {
		var created string
		var amount int
		if err := rows.Scan(&created, &amount); err != nil {
			return err
		}
		at, err := parseDBTime(created)
		if err != nil {
			return err
		}
		if d := dayIndex(weekStart, at.In(loc)); d >= 0 && d < 7 {
			week[d] += amount
		}
	}
	return rows.Err()
}


// WeekStart returns the Monday, at midnight, of the week that local falls in.
func WeekStart(local time.Time) time.Time {
	return periodStart("week", local)
}

// EarnedThisWeek returns the points each family member earned since the start of the current
// week in the family zone, keyed by member ID. Members who earned nothing are not in the map.
func EarnedThisWeek(ctx context.Context, db *sql.DB, familyID string, loc *time.Location, now time.Time) (map[string]int, error) {
	weekStart := periodStart("week", now.In(loc))
	placeholders := make([]string, len(earnedTypes))
	args := []any{familyID, dbTime(weekStart.UTC())}
	for i, t := range earnedTypes {
		placeholders[i] = "?"
		args = append(args, string(t))
	}
	rows, err := db.QueryContext(ctx,
		`SELECT family_member_id, SUM(amount) FROM points_ledger
		 WHERE family_id = ? AND created_at >= ? AND amount > 0
		   AND entry_type IN (`+strings.Join(placeholders, ",")+`)
		 GROUP BY family_member_id`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var memberID string
		var total int
		if err := rows.Scan(&memberID, &total); err != nil {
			return nil, err
		}
		out[memberID] = total
	}
	return out, rows.Err()
}

// dayIndex returns the number of whole local days from weekStart to local. Both are compared by
// calendar date, so daylight saving changes do not shift a day.
func dayIndex(weekStart, local time.Time) int {
	start := time.Date(weekStart.Year(), weekStart.Month(), weekStart.Day(), 0, 0, 0, 0, time.UTC)
	y, m, d := local.Date()
	cur := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	return int(cur.Sub(start) / (24 * time.Hour))
}

// ListEntries handles GET /api/v1/points/entries. Query values: member_id, type, from and to
// (YYYY-MM-DD in the family zone, both inclusive), limit (1 to 200, default 50) and cursor (the
// last ID of the previous page). Results are newest first.
func (h *Handlers) ListEntries(w http.ResponseWriter, r *http.Request) {
	familyID, _, memberID, ok := h.actor(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	q := r.URL.Query()

	target, ok := h.scopeMember(ctx, w, memberID, q.Get("member_id"))
	if !ok {
		return
	}
	entryType := q.Get("type")
	if entryType != "" && !knownEntryType(entryType) {
		invalid(w, "type", "unknown entry type")
		return
	}
	loc, err := familyLocation(ctx, h.db, familyID)
	if err != nil {
		h.internalError(w, "entries: family zone", err)
		return
	}
	from, hasFrom, err := parseLocalDay(q.Get("from"), loc)
	if err != nil {
		invalid(w, "from", "use YYYY-MM-DD")
		return
	}
	to, hasTo, err := parseLocalDay(q.Get("to"), loc)
	if err != nil {
		invalid(w, "to", "use YYYY-MM-DD")
		return
	}
	limit, ok := pageSize(q.Get("limit"))
	if !ok {
		invalid(w, "limit", "1 to 200")
		return
	}
	cursorAt, cursorID, ok := h.resolveCursor(ctx, w, "points_ledger", familyID, "created_at", q.Get("cursor"))
	if !ok {
		return
	}

	where := []string{"l.family_id = ?"}
	args := []any{familyID}
	if target != "" {
		where = append(where, "l.family_member_id = ?")
		args = append(args, target)
	}
	if entryType != "" {
		where = append(where, "l.entry_type = ?")
		args = append(args, entryType)
	}
	if hasFrom {
		where = append(where, "l.created_at >= ?")
		args = append(args, dbTime(from.UTC()))
	}
	if hasTo {
		// The end date is inclusive, so the bound is the start of the following day.
		where = append(where, "l.created_at < ?")
		args = append(args, dbTime(to.AddDate(0, 0, 1).UTC()))
	}
	if cursorID != "" {
		where = append(where, "(l.created_at, l.id) < (?, ?)")
		args = append(args, cursorAt, cursorID)
	}
	args = append(args, limit+1)

	rows, err := h.db.QueryContext(ctx,
		`SELECT l.id, l.family_member_id, l.entry_type, l.amount, l.reason, rw.name,
		        CAST(l.created_at AS TEXT)
		 FROM points_ledger l
		 LEFT JOIN redemptions rd ON rd.family_id = l.family_id AND rd.id = l.reference_id AND l.reference_type = 'redemption'
		 LEFT JOIN rewards rw ON rw.family_id = rd.family_id AND rw.id = rd.reward_id
		 WHERE `+strings.Join(where, " AND ")+`
		 ORDER BY l.created_at DESC, l.id DESC
		 LIMIT ?`, args...)
	if err != nil {
		h.internalError(w, "entries: query", err)
		return
	}
	defer rows.Close()

	out := make([]PointsEntry, 0, limit)
	for rows.Next() {
		var e PointsEntry
		var reason, rewardName sql.NullString
		var created string
		if err := rows.Scan(&e.ID, &e.MemberID, &e.Type, &e.Amount, &reason, &rewardName, &created); err != nil {
			h.internalError(w, "entries: scan", err)
			return
		}
		if e.CreatedAt, err = parseDBTime(created); err != nil {
			h.internalError(w, "entries: time", err)
			return
		}
		if reason.Valid {
			e.Reason = &reason.String
		}
		if rewardName.Valid {
			e.RewardName = &rewardName.String
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		h.internalError(w, "entries: rows", err)
		return
	}

	page := PointsEntryPage{Entries: out}
	if len(out) > limit {
		page.Entries = out[:limit]
		next := out[limit-1].ID
		page.NextCursor = &next
	}
	web.RespondJSON(w, http.StatusOK, page)
}

// ListRedemptions handles GET /api/v1/redemptions. Query values: member_id, status (one or more
// of pending, approved, rejected and cancelled, comma separated), limit (1 to 200, default 50)
// and cursor (the last ID of the previous page). Results are newest request first.
func (h *Handlers) ListRedemptions(w http.ResponseWriter, r *http.Request) {
	familyID, _, memberID, ok := h.actor(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	q := r.URL.Query()

	target, ok := h.scopeMember(ctx, w, memberID, q.Get("member_id"))
	if !ok {
		return
	}
	statuses, ok := parseStatuses(q.Get("status"))
	if !ok {
		invalid(w, "status", "use pending, approved, rejected or cancelled")
		return
	}
	limit, ok := pageSize(q.Get("limit"))
	if !ok {
		invalid(w, "limit", "1 to 200")
		return
	}
	cursorAt, cursorID, ok := h.resolveCursor(ctx, w, "redemptions", familyID, "requested_at", q.Get("cursor"))
	if !ok {
		return
	}

	where := []string{"r.family_id = ?"}
	args := []any{familyID}
	if target != "" {
		where = append(where, "r.family_member_id = ?")
		args = append(args, target)
	}
	if len(statuses) > 0 {
		marks := make([]string, len(statuses))
		for i, s := range statuses {
			marks[i] = "?"
			args = append(args, s)
		}
		where = append(where, "r.status IN ("+strings.Join(marks, ",")+")")
	}
	if cursorID != "" {
		where = append(where, "(r.requested_at, r.id) < (?, ?)")
		args = append(args, cursorAt, cursorID)
	}
	args = append(args, limit+1)

	rows, err := h.db.QueryContext(ctx,
		`SELECT r.id, r.reward_id, r.family_member_id, r.status, r.cost,
		        CAST(r.requested_at AS TEXT), CAST(r.decided_at AS TEXT),
		        r.decided_by_member_id, r.decision_reason, r.version,
		        rw.name, rw.icon, u.display_name
		 FROM redemptions r
		 JOIN rewards rw ON rw.family_id = r.family_id AND rw.id = r.reward_id
		 JOIN family_members m ON m.family_id = r.family_id AND m.id = r.family_member_id
		 JOIN users u ON u.id = m.user_id
		 WHERE `+strings.Join(where, " AND ")+`
		 ORDER BY r.requested_at DESC, r.id DESC
		 LIMIT ?`, args...)
	if err != nil {
		h.internalError(w, "redemptions: query", err)
		return
	}
	defer rows.Close()

	out := make([]RedemptionListItem, 0, limit)
	for rows.Next() {
		item, err := scanListItem(rows)
		if err != nil {
			h.internalError(w, "redemptions: scan", err)
			return
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		h.internalError(w, "redemptions: rows", err)
		return
	}

	page := RedemptionPage{Redemptions: out}
	if len(out) > limit {
		page.Redemptions = out[:limit]
		next := out[limit-1].ID
		page.NextCursor = &next
	}
	web.RespondJSON(w, http.StatusOK, page)
}

// scanListItem reads one row of the redemption list query.
func scanListItem(rows *sql.Rows) (RedemptionListItem, error) {
	var (
		item                       RedemptionListItem
		requested                  string
		decided, decidedBy, reason sql.NullString
	)
	if err := rows.Scan(&item.ID, &item.RewardID, &item.MemberID, &item.Status, &item.Cost,
		&requested, &decided, &decidedBy, &reason, &item.Version,
		&item.RewardName, &item.RewardIcon, &item.MemberName); err != nil {
		return RedemptionListItem{}, err
	}
	t, err := parseDBTime(requested)
	if err != nil {
		return RedemptionListItem{}, err
	}
	item.RequestedAt = t
	if decided.Valid {
		t, err := parseDBTime(decided.String)
		if err != nil {
			return RedemptionListItem{}, err
		}
		item.DecidedAt = &t
	}
	if decidedBy.Valid {
		item.DecidedByMemberID = &decidedBy.String
	}
	if reason.Valid {
		item.DecisionReason = &reason.String
	}
	return item, nil
}

// scopeMember decides which member a list covers. A child always gets their own rows, and asking
// for another member is a 403. A parent may pass member_id or leave it empty for the whole family.
// The returned string is empty when no member filter applies.
func (h *Handlers) scopeMember(ctx context.Context, w http.ResponseWriter, callerID, requested string) (string, bool) {
	child, err := h.isChild(ctx, callerID)
	if err != nil {
		h.internalError(w, "scope: member", err)
		return "", false
	}
	if child {
		if requested != "" && requested != callerID {
			web.RespondError(w, http.StatusForbidden, "forbidden", "You can only view your own points", nil)
			return "", false
		}
		return callerID, true
	}
	return requested, true
}

// resolveCursor looks up the row named by the cursor so the keyset comparison can use its
// timestamp. An empty cursor is the first page. An unknown cursor is a 400.
func (h *Handlers) resolveCursor(ctx context.Context, w http.ResponseWriter, table, familyID, column, cursor string) (string, string, bool) {
	if cursor == "" {
		return "", "", true
	}
	var at string
	err := h.db.QueryRowContext(ctx,
		`SELECT CAST(`+column+` AS TEXT) FROM `+table+` WHERE family_id = ? AND id = ?`,
		familyID, cursor,
	).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		invalid(w, "cursor", "unknown cursor")
		return "", "", false
	}
	if err != nil {
		h.internalError(w, "cursor: "+table, err)
		return "", "", false
	}
	return at, cursor, true
}

// pageSize reads the limit query value. An absent value gets the default page size.
func pageSize(raw string) (int, bool) {
	if raw == "" {
		return defaultPageSize, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxPageSize {
		return 0, false
	}
	return n, true
}

// parseLocalDay reads a YYYY-MM-DD value as midnight in the family zone. An empty value means no
// bound.
func parseLocalDay(raw string, loc *time.Location) (time.Time, bool, error) {
	if raw == "" {
		return time.Time{}, false, nil
	}
	t, err := time.ParseInLocation("2006-01-02", raw, loc)
	if err != nil {
		return time.Time{}, false, err
	}
	return t, true, nil
}

// parseStatuses reads a comma-separated list of redemption statuses. An empty value means all.
func parseStatuses(raw string) ([]string, bool) {
	if raw == "" {
		return nil, true
	}
	var out []string
	for _, s := range strings.Split(raw, ",") {
		switch s {
		case statusPending, statusApproved, statusRejected, statusCancelled:
			out = append(out, s)
		default:
			return nil, false
		}
	}
	return out, true
}

// knownEntryType reports whether s is one of the ledger entry types.
func knownEntryType(s string) bool {
	for _, t := range []EntryType{TaskAward, BonusTaskAward, Bonus, Penalty, Redemption,
		RedemptionReversal, Reversal, Adjustment, TransferOut, TransferIn} {
		if string(t) == s {
			return true
		}
	}
	return false
}
