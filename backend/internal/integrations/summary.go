// Package integrations serves the REST endpoints used by external tools such as Home Assistant
// (milestone M7-08). Callers authenticate with an API token, so the route is wrapped by the token
// middleware and the scopes decide what is returned.
package integrations

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"nestwall/backend/internal/calendar"
	"nestwall/backend/internal/dashboard"
	"nestwall/backend/internal/permissions"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/web"
)

const (
	dateLayout     = "2006-01-02"
	nextEventDays  = 7
	nextEventLimit = 5
)

// Handlers serves the integration endpoints.
type Handlers struct {
	db     *sql.DB
	clock  clock.Clock
	logger *slog.Logger
	dash   *dashboard.Handlers
}

// NewHandlers returns the integration handlers.
func NewHandlers(db *sql.DB, clk clock.Clock, logger *slog.Logger) *Handlers {
	return &Handlers{db: db, clock: clk, logger: logger, dash: dashboard.NewHandlers(db, clk, logger)}
}

// SummaryResponse is the data of GET /api/v1/integrations/summary. Task counts come from the
// parent dashboard. NextEvents lists up to nextEventLimit events from today over the next week.
type SummaryResponse struct {
	Date                 string      `json:"date"`
	Timezone             string      `json:"timezone"`
	TasksDueToday        int         `json:"tasks_due_today"`
	TasksDoneToday       int         `json:"tasks_done_today"`
	TasksOverdue         int         `json:"tasks_overdue"`
	TasksWaitingApproval int         `json:"tasks_waiting_approval"`
	EventsToday          int         `json:"events_today"`
	NextEvents           []NextEvent `json:"next_events"`
}

// NextEvent is one upcoming calendar occurrence.
type NextEvent struct {
	Title    string    `json:"title"`
	Date     string    `json:"date"`
	StartsAt time.Time `json:"starts_at"`
	AllDay   bool      `json:"all_day"`
}

// summaryEnvelope wraps the response in "data", which the Home Assistant REST sensor template reads.
type summaryEnvelope struct {
	Data SummaryResponse `json:"data"`
}

// Summary handles GET /api/v1/integrations/summary. The route requires the tasks:read and
// calendar:read scopes on the token.
func (h *Handlers) Summary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	callerID, ok := permissions.GetFamilyMemberID(ctx)
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "API token required", nil)
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
	first := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)

	counts, err := h.dash.TodayCounts(ctx, familyID, today)
	if err != nil {
		h.internalError(w, "task counts", err)
		return
	}
	events, err := calendar.WallEvents(ctx, h.db, familyID, zone, first, first.AddDate(0, 0, nextEventDays-1))
	if err != nil {
		h.internalError(w, "events", err)
		return
	}
	slices.SortStableFunc(events, func(a, b calendar.WallEvent) int { return a.StartsAt.Compare(b.StartsAt) })

	resp := SummaryResponse{
		Date:                 today,
		Timezone:             zoneName,
		TasksDueToday:        counts.TodayTotal,
		TasksDoneToday:       counts.TodayDone,
		TasksOverdue:         counts.Overdue,
		TasksWaitingApproval: counts.WaitingApproval,
		NextEvents:           []NextEvent{},
	}
	for _, e := range events {
		if e.OccurrenceDate == today {
			resp.EventsToday++
		}
		if len(resp.NextEvents) < nextEventLimit {
			resp.NextEvents = append(resp.NextEvents, NextEvent{
				Title:    e.Title,
				Date:     e.OccurrenceDate,
				StartsAt: e.StartsAt.UTC(),
				AllDay:   e.AllDay,
			})
		}
	}
	web.RespondJSON(w, http.StatusOK, summaryEnvelope{Data: resp})
}

// family returns the token owner's family ID and its IANA time zone.
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

func (h *Handlers) internalError(w http.ResponseWriter, what string, err error) {
	h.logger.Error("integration summary", "step", what, "err", err)
	web.RespondError(w, http.StatusInternalServerError, "internal_error", "Something went wrong", nil)
}
