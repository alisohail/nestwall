package rewards

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"nestwall/backend/internal/platform/web"
)

// approvalHistorySQL is the parent's decision history: task completions, bonus claims and reward
// requests that were approved or declined, newest first. Each part takes the family, then the
// member filter twice (an empty filter means everyone). The outer query pages on decided_at and
// entry_key, so the three sources interleave in one order.
const approvalHistorySQL = `
SELECT entry_key, kind, title, icon, member_id, member_name, status, cost,
       decided_at, decided_by, reason
FROM (
	SELECT 'task:' || oa.id AS entry_key, 'task' AS kind, d.title AS title,
	       COALESCE(c.emoji, '') AS icon, oa.family_member_id AS member_id, su.display_name AS member_name,
	       oa.status AS status, 0 AS cost, CAST(oa.decided_at AS TEXT) AS decided_at,
	       oa.decided_by_member_id AS decided_by, oa.decision_note AS reason
	FROM task_occurrence_assignees oa
	JOIN task_occurrences o ON o.family_id = oa.family_id AND o.id = oa.task_occurrence_id
	JOIN task_definitions d ON d.family_id = o.family_id AND d.id = o.task_definition_id
	LEFT JOIN task_categories c ON c.family_id = d.family_id AND c.id = d.category_id
	JOIN family_members sm ON sm.family_id = oa.family_id AND sm.id = oa.family_member_id
	JOIN users su ON su.id = sm.user_id
	WHERE oa.family_id = ? AND oa.status IN ('approved', 'rejected') AND oa.decided_at IS NOT NULL
	  AND (? = '' OR oa.family_member_id = ?)
	UNION ALL
	SELECT 'bonus:' || bc.id, 'bonus', bt.title, '', bc.family_member_id, bu.display_name,
	       bc.status, bt.points, CAST(bc.decided_at AS TEXT), bc.decided_by_member_id, bc.decision_note
	FROM bonus_claims bc
	JOIN bonus_tasks bt ON bt.family_id = bc.family_id AND bt.id = bc.bonus_task_id
	JOIN family_members bm ON bm.family_id = bc.family_id AND bm.id = bc.family_member_id
	JOIN users bu ON bu.id = bm.user_id
	WHERE bc.family_id = ? AND bc.status IN ('approved', 'rejected') AND bc.decided_at IS NOT NULL
	  AND (? = '' OR bc.family_member_id = ?)
	UNION ALL
	SELECT 'reward:' || r.id, 'reward', rw.name, rw.icon, r.family_member_id, u.display_name,
	       r.status, r.cost, CAST(r.decided_at AS TEXT), r.decided_by_member_id, r.decision_reason
	FROM redemptions r
	JOIN rewards rw ON rw.family_id = r.family_id AND rw.id = r.reward_id
	JOIN family_members m ON m.family_id = r.family_id AND m.id = r.family_member_id
	JOIN users u ON u.id = m.user_id
	WHERE r.family_id = ? AND r.status IN ('approved', 'rejected') AND r.decided_at IS NOT NULL
	  AND (? = '' OR r.family_member_id = ?)
) h
WHERE (? = '' OR decided_at < ? OR (decided_at = ? AND entry_key < ?))
ORDER BY decided_at DESC, entry_key DESC
LIMIT ?`

// ApprovalHistoryItem is one decided task completion, bonus claim or reward request.
type ApprovalHistoryItem struct {
	ID                string    `json:"id"`
	Kind              string    `json:"kind"`
	Title             string    `json:"title"`
	Icon              string    `json:"icon"`
	MemberID          string    `json:"member_id"`
	MemberName        string    `json:"member_name"`
	Status            string    `json:"status"`
	Cost              int       `json:"cost"`
	DecidedAt         time.Time `json:"decided_at"`
	DecidedByMemberID *string   `json:"decided_by_member_id"`
	DecisionReason    *string   `json:"decision_reason"`
}

// ApprovalHistoryPage is the body of GET /api/v1/approvals/history.
type ApprovalHistoryPage struct {
	Items      []ApprovalHistoryItem `json:"items"`
	NextCursor *string               `json:"next_cursor"`
}

// ApprovalHistory handles GET /api/v1/approvals/history. Query values: member_id, limit (1 to 200,
// default 50) and cursor (the value of next_cursor from the previous page). It requires rewards.approve.
func (h *Handlers) ApprovalHistory(w http.ResponseWriter, r *http.Request) {
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
	limit, ok := pageSize(q.Get("limit"))
	if !ok {
		invalid(w, "limit", "1 to 200")
		return
	}
	var cursorAt, cursorKey string
	if raw := q.Get("cursor"); raw != "" {
		var found bool
		cursorAt, cursorKey, found = strings.Cut(raw, "|")
		if !found {
			invalid(w, "cursor", "unknown cursor")
			return
		}
	}

	rows, err := h.db.QueryContext(ctx, approvalHistorySQL,
		familyID, target, target,
		familyID, target, target,
		familyID, target, target,
		cursorAt, cursorAt, cursorAt, cursorKey,
		limit+1,
	)
	if err != nil {
		h.internalError(w, "approval history: query", err)
		return
	}
	defer rows.Close()

	out := make([]ApprovalHistoryItem, 0, limit)
	// raw keeps each row's decided_at as stored, because the next cursor compares against that text.
	raw := make([]string, 0, limit)
	for rows.Next() {
		var (
			item              ApprovalHistoryItem
			decidedAt         string
			decidedBy, reason sql.NullString
		)
		if err := rows.Scan(&item.ID, &item.Kind, &item.Title, &item.Icon, &item.MemberID, &item.MemberName,
			&item.Status, &item.Cost, &decidedAt, &decidedBy, &reason); err != nil {
			h.internalError(w, "approval history: scan", err)
			return
		}
		t, err := parseDBTime(decidedAt)
		if err != nil {
			h.internalError(w, "approval history: decided_at", err)
			return
		}
		item.DecidedAt = t
		if decidedBy.Valid {
			item.DecidedByMemberID = &decidedBy.String
		}
		if reason.Valid {
			item.DecisionReason = &reason.String
		}
		out = append(out, item)
		raw = append(raw, decidedAt)
	}
	if err := rows.Err(); err != nil {
		h.internalError(w, "approval history: rows", err)
		return
	}

	page := ApprovalHistoryPage{Items: out}
	if len(out) > limit {
		page.Items = out[:limit]
		last := out[limit-1]
		next := raw[limit-1] + "|" + last.ID
		page.NextCursor = &next
	}
	web.RespondJSON(w, http.StatusOK, page)
}
