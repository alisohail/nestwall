package badges

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"nestwall/backend/internal/badges/catalogue"
	"nestwall/backend/internal/platform/web"
)

// MyBadges is the signed-in child's own view of the badges (PBI M4-09, batch 09c). It holds only the
// child's records and the family's display switches. A built-in badge that is off and not yet earned is
// left out, so a group switched off with nothing earned is left out too. Earned badges stay visible.
type MyBadges struct {
	Enabled            bool      `json:"enabled"`
	ShowOnKidDashboard bool      `json:"show_on_kid_dashboard"`
	EarnedCount        int       `json:"earned_count"`
	TotalCount         int       `json:"total_count"`
	Groups             []MyGroup `json:"groups"`
	FromParents        []MyAward `json:"from_parents"`
}

// MyGroup is one built-in group as the child sees it. Earned and Total count the badges shown.
type MyGroup struct {
	Group  string    `json:"group"`
	Earned int       `json:"earned"`
	Total  int       `json:"total"`
	Badges []MyBadge `json:"badges"`
}

// MyBadge is one built-in badge. Progress is capped at Target and is Target once earned.
type MyBadge struct {
	Key      string     `json:"key"`
	Earned   bool       `json:"earned"`
	EarnedAt *time.Time `json:"earned_at"`
	Progress int        `json:"progress"`
	Target   int        `json:"target"`
}

// MyAward is a custom badge a parent gave the child by hand.
type MyAward struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Emoji     string    `json:"emoji"`
	Note      string    `json:"note"`
	AwardedBy string    `json:"awarded_by"`
	AwardedAt time.Time `json:"awarded_at"`
}

// myDefinition is a built-in badge definition with the child's award time, if any.
type myDefinition struct {
	enabled  bool
	earnedAt sql.NullTime
}

// MyBadges handles GET /api/v1/me/badges. It returns the signed-in child's badges only.
func (h *Handlers) MyBadges(w http.ResponseWriter, r *http.Request) {
	familyID, _, memberID, ok := h.actor(w, r)
	if !ok {
		return
	}
	out, err := loadMyBadges(r.Context(), h.db, familyID, memberID)
	if err != nil {
		h.internalError(w, "load my badges", err)
		return
	}
	web.RespondJSON(w, http.StatusOK, out)
}

// loadMyBadges reads one child's badges. Every query is scoped to the family and the child's member ID.
func loadMyBadges(ctx context.Context, q querier, familyID, memberID string) (MyBadges, error) {
	out := MyBadges{Groups: []MyGroup{}, FromParents: []MyAward{}}
	if err := q.QueryRowContext(ctx,
		`SELECT enabled, show_on_kid_dashboard FROM badge_settings WHERE family_id = ?`, familyID,
	).Scan(&out.Enabled, &out.ShowOnKidDashboard); err != nil {
		return MyBadges{}, fmt.Errorf("load badge settings: %w", err)
	}

	rows, err := q.QueryContext(ctx,
		`SELECT d.badge_key, d.enabled, mb.awarded_at
		 FROM badge_definitions d
		 LEFT JOIN member_badges mb
		   ON mb.family_id = d.family_id AND mb.badge_definition_id = d.id
		  AND mb.family_member_id = ? AND mb.source = 'auto'
		 WHERE d.family_id = ? AND d.badge_key IS NOT NULL`,
		memberID, familyID)
	if err != nil {
		return MyBadges{}, fmt.Errorf("load built-in badges: %w", err)
	}
	defs := map[string]myDefinition{}
	for rows.Next() {
		var key string
		var d myDefinition
		if err := rows.Scan(&key, &d.enabled, &d.earnedAt); err != nil {
			rows.Close()
			return MyBadges{}, fmt.Errorf("scan built-in badge: %w", err)
		}
		defs[key] = d
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return MyBadges{}, fmt.Errorf("read built-in badges: %w", err)
	}

	m, err := loadMetrics(ctx, q, familyID, memberID)
	if err != nil {
		return MyBadges{}, fmt.Errorf("load metrics: %w", err)
	}

	// Groups follow the catalogue's display order.
	index := map[string]int{}
	for _, b := range catalogue.BuiltIn {
		d, ok := defs[b.Key]
		if !ok {
			continue
		}
		earned := d.earnedAt.Valid
		if !d.enabled && !earned {
			continue
		}
		g := string(b.Group)
		i, seen := index[g]
		if !seen {
			i = len(out.Groups)
			index[g] = i
			out.Groups = append(out.Groups, MyGroup{Group: g, Badges: []MyBadge{}})
		}

		item := MyBadge{Key: b.Key, Earned: earned, Target: b.Target}
		if earned {
			at := d.earnedAt.Time
			item.EarnedAt = &at
			item.Progress = b.Target
		} else {
			item.Progress = min(m.valueOf(b), b.Target)
		}

		grp := &out.Groups[i]
		grp.Total++
		out.TotalCount++
		if earned {
			grp.Earned++
			out.EarnedCount++
		}
		grp.Badges = append(grp.Badges, item)
	}

	rows, err = q.QueryContext(ctx,
		`SELECT mb.id, d.name, d.emoji, COALESCE(mb.note, ''), COALESCE(u.display_name, ''), mb.awarded_at
		 FROM member_badges mb
		 JOIN badge_definitions d ON d.family_id = mb.family_id AND d.id = mb.badge_definition_id
		 LEFT JOIN family_members am ON am.family_id = mb.family_id AND am.id = mb.awarded_by_member_id
		 LEFT JOIN users u ON u.id = am.user_id
		 WHERE mb.family_id = ? AND mb.family_member_id = ? AND mb.source = 'manual'
		 ORDER BY mb.awarded_at DESC, mb.id`,
		familyID, memberID)
	if err != nil {
		return MyBadges{}, fmt.Errorf("load awards from parents: %w", err)
	}
	for rows.Next() {
		var a MyAward
		if err := rows.Scan(&a.ID, &a.Name, &a.Emoji, &a.Note, &a.AwardedBy, &a.AwardedAt); err != nil {
			rows.Close()
			return MyBadges{}, fmt.Errorf("scan award from parents: %w", err)
		}
		out.FromParents = append(out.FromParents, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return MyBadges{}, fmt.Errorf("read awards from parents: %w", err)
	}
	out.EarnedCount += len(out.FromParents)
	return out, nil
}
