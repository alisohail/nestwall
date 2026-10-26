// Package dashboard serves the parent dashboard (milestone M3-10). Every number comes from
// real task records. The family's time zone decides what "today" means.
package dashboard

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/web"
	"nestwall/backend/internal/rewards"
)

const (
	dateLayout         = "2006-01-02"
	bandLimit          = 100
	approvalLimit      = 10
	rewardRequestLimit = 10
	activityLimit      = 10
)

// Handlers serves the parent dashboard.
type Handlers struct {
	db     *sql.DB
	clock  clock.Clock
	logger *slog.Logger
}

// NewHandlers returns the dashboard handlers.
func NewHandlers(db *sql.DB, clk clock.Clock, logger *slog.Logger) *Handlers {
	return &Handlers{db: db, clock: clk, logger: logger}
}

// Response is the body of GET /api/v1/dashboard/parent.
type Response struct {
	Date           string          `json:"date"`
	Timezone       string          `json:"timezone"`
	Summary        Summary         `json:"summary"`
	Today          []Item          `json:"today"`
	People         []Person        `json:"people"`
	Approvals      []Approval      `json:"approvals"`
	RewardRequests []RewardRequest `json:"reward_requests"`
	Activity       []ActivityItem  `json:"activity"`
}

// Summary holds the counts shown at the top of the dashboard.
// TodayTotal leaves out skipped copies. TodayDone counts approved copies due today.
// Overdue counts to_do copies whose occurrence is past_due. WaitingApproval counts submitted copies.
// WaitingRewards counts pending reward requests. PointsWeek sums the points earned this week by
// the people in People.
type Summary struct {
	TodayTotal      int `json:"today_total"`
	TodayDone       int `json:"today_done"`
	Overdue         int `json:"overdue"`
	WaitingApproval int `json:"waiting_approval"`
	WaitingRewards  int `json:"waiting_reward_requests"`
	PointsWeek      int `json:"points_week"`
}

// memberRef identifies the person a copy or event belongs to.
type memberRef struct {
	MemberID     string  `json:"member_id"`
	MemberName   string  `json:"member_name"`
	MemberColour string  `json:"member_colour"`
	AvatarID     *string `json:"avatar_id"`
}

// Item is one person's copy of a task due today. It feeds the person task chips and counts.
// TaskID is the occurrence ID, which the task action endpoints also use.
type Item struct {
	TaskID   string `json:"task_id"`
	Title    string `json:"title"`
	DueAt    string `json:"due_at"`
	Priority string `json:"priority"`
	Status   string `json:"status"`
	memberRef
}

// Person is one active family member with their progress for today, their progress for the week
// and the points they earned this week.
type Person struct {
	memberRef
	Total      int `json:"total"`
	Done       int `json:"done"`
	WeekTotal  int `json:"week_total"`
	WeekDone   int `json:"week_done"`
	PointsWeek int `json:"points_week"`
	Overdue    int `json:"overdue"`
}

// Approval is a submitted task copy that waits for a parent.
type Approval struct {
	TaskID        string `json:"task_id"`
	Title         string `json:"title"`
	CompletedAt   string `json:"completed_at"`
	CompletedLate bool   `json:"completed_late"`
	memberRef
}

// RewardRequest is a pending reward redemption that waits for a parent.
type RewardRequest struct {
	RedemptionID string `json:"redemption_id"`
	RewardName   string `json:"reward_name"`
	RewardIcon   string `json:"reward_icon"`
	Cost         int    `json:"cost"`
	RequestedAt  string `json:"requested_at"`
	memberRef
}

// ActivityItem is one task event from the audit log.
type ActivityItem struct {
	Action    string `json:"action"`
	TaskID    string `json:"task_id"`
	TaskTitle string `json:"task_title"`
	CreatedAt string `json:"created_at"`
	memberRef
}

// avatarSQL returns the member's current picture ID. Pictures live in member_avatars, one row per
// member, so the legacy family_members.avatar_id column is not read.
const avatarSQL = `(SELECT ma.id FROM member_avatars ma WHERE ma.family_member_id = fm.id LIMIT 1)`

// copyBase joins one person's copy to its task and to the person's account. Archived tasks
// and inactive people are left out. The Wall device is not a person, so it is left out too.
const copyBase = `FROM task_occurrence_assignees oa
JOIN task_occurrences o ON o.family_id = oa.family_id AND o.id = oa.task_occurrence_id
JOIN task_definitions d ON d.family_id = o.family_id AND d.id = o.task_definition_id
JOIN family_members fm ON fm.family_id = oa.family_id AND fm.id = oa.family_member_id
JOIN users u ON u.id = fm.user_id
JOIN roles r ON r.id = fm.role_id
WHERE oa.family_id = ? AND d.status = 'active' AND fm.status = 'active'`

// Parent handles GET /api/v1/dashboard/parent. The route requires tasks.view, so children get 403.
func (h *Handlers) Parent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	callerID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "Authentication required", nil)
		return
	}

	familyID, zoneName, err := h.family(ctx, callerID)
	if errors.Is(err, sql.ErrNoRows) {
		web.RespondError(w, http.StatusForbidden, "no_membership", "No active family membership", nil)
		return
	}
	if err != nil {
		h.internalError(w, "load family", err)
		return
	}
	zone, err := time.LoadLocation(zoneName)
	if err != nil {
		h.internalError(w, "load time zone", err)
		return
	}

	now := h.clock.Now().In(zone)
	today := now.Format(dateLayout)

	resp := Response{
		Date:           today,
		Timezone:       zoneName,
		Today:          []Item{},
		People:         []Person{},
		Approvals:      []Approval{},
		RewardRequests: []RewardRequest{},
		Activity:       []ActivityItem{},
	}

	if resp.Summary, err = h.summary(ctx, familyID, today); err != nil {
		h.internalError(w, "summary", err)
		return
	}
	if resp.Summary.WaitingRewards, err = h.waitingRewards(ctx, familyID); err != nil {
		h.internalError(w, "waiting rewards", err)
		return
	}
	if resp.Today, err = h.items(ctx, familyID, "AND o.due_date = ?", today); err != nil {
		h.internalError(w, "today band", err)
		return
	}
	earned, err := rewards.EarnedThisWeek(ctx, h.db, familyID, zone, now)
	if err != nil {
		h.internalError(w, "points week", err)
		return
	}
	weekStart := rewards.WeekStart(now)
	weekEnd := weekStart.AddDate(0, 0, 7).Format(dateLayout)
	if resp.People, err = h.people(ctx, familyID, today, weekStart.Format(dateLayout), weekEnd, earned); err != nil {
		h.internalError(w, "people", err)
		return
	}
	for _, p := range resp.People {
		resp.Summary.PointsWeek += p.PointsWeek
	}
	overdue, err := h.overdueByMember(ctx, familyID)
	if err != nil {
		h.internalError(w, "overdue by member", err)
		return
	}
	for i := range resp.People {
		resp.People[i].Overdue = overdue[resp.People[i].MemberID]
	}
	if resp.Approvals, err = h.approvals(ctx, familyID); err != nil {
		h.internalError(w, "approvals", err)
		return
	}
	if resp.RewardRequests, err = h.rewardRequests(ctx, familyID); err != nil {
		h.internalError(w, "reward requests", err)
		return
	}
	if resp.Activity, err = h.activity(ctx, familyID); err != nil {
		h.internalError(w, "activity", err)
		return
	}

	web.RespondJSON(w, http.StatusOK, resp)
}

// family returns the caller's family ID and its IANA time zone.
func (h *Handlers) family(ctx context.Context, memberID string) (string, string, error) {
	var familyID, zone string
	err := h.db.QueryRowContext(ctx,
		`SELECT f.id, f.timezone
		   FROM family_members fm
		   JOIN families f ON f.id = fm.family_id
		  WHERE fm.id = ? AND fm.status = 'active'`,
		memberID,
	).Scan(&familyID, &zone)
	return familyID, zone, err
}

// summary computes the four counts in one query.
func (h *Handlers) summary(ctx context.Context, familyID, today string) (Summary, error) {
	var s Summary
	err := h.db.QueryRowContext(ctx,
		`SELECT
			COALESCE(SUM(CASE WHEN o.due_date = ? AND oa.status != 'skipped' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN o.due_date = ? AND oa.status = 'approved' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN oa.status = 'to_do' AND o.status = 'past_due' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN oa.status = 'submitted' THEN 1 ELSE 0 END), 0)
		`+copyBase,
		today, today, familyID,
	).Scan(&s.TodayTotal, &s.TodayDone, &s.Overdue, &s.WaitingApproval)
	return s, err
}

// TodayCounts returns the dashboard counts for a family on the given date (YYYY-MM-DD). It lets
// other packages, such as the integration summary, reuse the same numbers.
func (h *Handlers) TodayCounts(ctx context.Context, familyID, today string) (Summary, error) {
	return h.summary(ctx, familyID, today)
}

// items returns copies for the band. condition is an extra WHERE clause that starts with AND.
// Skipped copies are left out.
func (h *Handlers) items(ctx context.Context, familyID, condition string, args ...any) ([]Item, error) {
	query := `SELECT o.id, d.title, strftime('%Y-%m-%dT%H:%M:%SZ', o.due_at), d.priority, oa.status,
		                 fm.id, u.display_name, fm.colour, `+avatarSQL+`
	          ` + copyBase + ` AND oa.status != 'skipped' ` + condition + `
	          ORDER BY o.due_at, oa.id LIMIT ?`
	params := append([]any{familyID}, args...)
	params = append(params, bandLimit)

	rows, err := h.db.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Item{}
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.TaskID, &it.Title, &it.DueAt, &it.Priority, &it.Status,
			&it.MemberID, &it.MemberName, &it.MemberColour, &it.AvatarID); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// people returns every active person with their copies due today, their copies due this week
// (weekStart up to weekEnd, exclusive) and the points they earned this week. Skipped copies are
// left out.
func (h *Handlers) people(ctx context.Context, familyID, today, weekStart, weekEnd string, earned map[string]int) ([]Person, error) {
	rows, err := h.db.QueryContext(ctx,
		`SELECT fm.id, u.display_name, fm.colour, `+avatarSQL+`,
		        COALESCE(SUM(CASE WHEN d.id IS NOT NULL AND o.due_date = ? THEN 1 ELSE 0 END), 0),
		        COALESCE(SUM(CASE WHEN d.id IS NOT NULL AND o.due_date = ? AND oa.status = 'approved' THEN 1 ELSE 0 END), 0),
		        COALESCE(COUNT(d.id), 0),
		        COALESCE(SUM(CASE WHEN d.id IS NOT NULL AND oa.status = 'approved' THEN 1 ELSE 0 END), 0)
		   FROM family_members fm
		   JOIN users u ON u.id = fm.user_id
		   JOIN roles r ON r.id = fm.role_id
		   LEFT JOIN task_occurrence_assignees oa
		          ON oa.family_id = fm.family_id AND oa.family_member_id = fm.id AND oa.status != 'skipped'
		   LEFT JOIN task_occurrences o
		          ON o.family_id = oa.family_id AND o.id = oa.task_occurrence_id AND o.due_date >= ? AND o.due_date < ?
		   LEFT JOIN task_definitions d
		          ON d.family_id = o.family_id AND d.id = o.task_definition_id AND d.status = 'active'
		  WHERE fm.family_id = ? AND fm.status = 'active'
		  GROUP BY fm.id, u.display_name, fm.colour, `+avatarSQL+`
		  ORDER BY u.display_name, fm.id`,
		today, today, weekStart, weekEnd, familyID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Person{}
	for rows.Next() {
		var p Person
		if err := rows.Scan(&p.MemberID, &p.MemberName, &p.MemberColour, &p.AvatarID,
			&p.Total, &p.Done, &p.WeekTotal, &p.WeekDone); err != nil {
			return nil, err
		}
		p.PointsWeek = earned[p.MemberID]
		out = append(out, p)
	}
	return out, rows.Err()
}

// overdueByMember counts each person's overdue copies, with the same rules as Summary.Overdue,
// so the per-person counts add up to the total.
func (h *Handlers) overdueByMember(ctx context.Context, familyID string) (map[string]int, error) {
	rows, err := h.db.QueryContext(ctx,
		`SELECT fm.id, COUNT(*)
		   `+copyBase+` AND oa.status = 'to_do' AND o.status = 'past_due'
		  GROUP BY fm.id`,
		familyID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var memberID string
		var count int
		if err := rows.Scan(&memberID, &count); err != nil {
			return nil, err
		}
		out[memberID] = count
	}
	return out, rows.Err()
}

// approvals returns the oldest submitted copies that wait for a parent, up to approvalLimit.
func (h *Handlers) approvals(ctx context.Context, familyID string) ([]Approval, error) {
	rows, err := h.db.QueryContext(ctx,
		`SELECT o.id, d.title, strftime('%Y-%m-%dT%H:%M:%SZ', oa.completed_at), oa.completed_late,
		        fm.id, u.display_name, fm.colour, `+avatarSQL+`
		  `+copyBase+` AND oa.status = 'submitted'
		  ORDER BY oa.completed_at, oa.id LIMIT ?`,
		familyID, approvalLimit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Approval{}
	for rows.Next() {
		var a Approval
		if err := rows.Scan(&a.TaskID, &a.Title, &a.CompletedAt, &a.CompletedLate,
			&a.MemberID, &a.MemberName, &a.MemberColour, &a.AvatarID); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// waitingRewards counts the pending reward requests of active people.
func (h *Handlers) waitingRewards(ctx context.Context, familyID string) (int, error) {
	var n int
	err := h.db.QueryRowContext(ctx,
		`SELECT COUNT(*)
		   FROM redemptions rd
		   JOIN family_members fm ON fm.family_id = rd.family_id AND fm.id = rd.family_member_id
		  WHERE rd.family_id = ? AND rd.status = 'pending' AND fm.status = 'active'`,
		familyID,
	).Scan(&n)
	return n, err
}

// rewardRequests returns the oldest pending reward requests, up to rewardRequestLimit.
func (h *Handlers) rewardRequests(ctx context.Context, familyID string) ([]RewardRequest, error) {
	rows, err := h.db.QueryContext(ctx,
		`SELECT rd.id, rw.name, rw.icon, rd.cost, strftime('%Y-%m-%dT%H:%M:%SZ', rd.requested_at),
		        fm.id, u.display_name, fm.colour, `+avatarSQL+`
		   FROM redemptions rd
		   JOIN rewards rw ON rw.family_id = rd.family_id AND rw.id = rd.reward_id
		   JOIN family_members fm ON fm.family_id = rd.family_id AND fm.id = rd.family_member_id
		   JOIN users u ON u.id = fm.user_id
		  WHERE rd.family_id = ? AND rd.status = 'pending' AND fm.status = 'active'
		  ORDER BY rd.requested_at, rd.id LIMIT ?`,
		familyID, rewardRequestLimit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []RewardRequest{}
	for rows.Next() {
		var rq RewardRequest
		if err := rows.Scan(&rq.RedemptionID, &rq.RewardName, &rq.RewardIcon, &rq.Cost, &rq.RequestedAt,
			&rq.MemberID, &rq.MemberName, &rq.MemberColour, &rq.AvatarID); err != nil {
			return nil, err
		}
		out = append(out, rq)
	}
	return out, rows.Err()
}

// activity returns the latest task events from the audit log. Only task actions are read. The
// actor is matched to the family member by user ID.
func (h *Handlers) activity(ctx context.Context, familyID string) ([]ActivityItem, error) {
	rows, err := h.db.QueryContext(ctx,
		`SELECT al.action, o.id, d.title, strftime('%Y-%m-%dT%H:%M:%SZ', al.created_at),
		        fm.id, u.display_name, fm.colour, `+avatarSQL+`
		   FROM audit_logs al
		   JOIN task_occurrences o ON o.family_id = al.family_id AND o.id = al.target_id
		   JOIN task_definitions d ON d.family_id = o.family_id AND d.id = o.task_definition_id
		   JOIN family_members fm ON fm.family_id = al.family_id AND fm.user_id = al.actor_id
		   JOIN users u ON u.id = fm.user_id
		  WHERE al.family_id = ? AND al.target_type = 'task'
		    AND al.action IN ('task.completed', 'task.approved', 'task.rejected', 'task.skipped')
		  ORDER BY al.created_at DESC, al.id DESC LIMIT ?`,
		familyID, activityLimit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ActivityItem{}
	for rows.Next() {
		var a ActivityItem
		if err := rows.Scan(&a.Action, &a.TaskID, &a.TaskTitle, &a.CreatedAt,
			&a.MemberID, &a.MemberName, &a.MemberColour, &a.AvatarID); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (h *Handlers) internalError(w http.ResponseWriter, what string, err error) {
	h.logger.Error("dashboard: "+what, "error", err)
	web.RespondError(w, http.StatusInternalServerError, "internal_error", "Something went wrong", nil)
}
