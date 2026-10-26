package wall

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"time"

	"nestwall/backend/internal/bonus"
	"nestwall/backend/internal/calendar"
	"nestwall/backend/internal/platform/web"
	"nestwall/backend/internal/rewards"
	"nestwall/backend/internal/weather"
)

const (
	dateLayout = "2006-01-02"
	dbTimeUTC  = "%Y-%m-%dT%H:%M:%SZ"
	// summaryWindowDays is how many local days the events cover, starting today.
	summaryWindowDays = 7
)

// summaryOptions are the device's display options. The wall reads them to decide what to draw.
type summaryOptions struct {
	ShowPoints      bool `json:"show_points"`
	ShowWeather     bool `json:"show_weather"`
	ShowTomorrow    bool `json:"show_tomorrow"`
	ShowPictures    bool `json:"show_pictures"`
	ShowGreeting    bool `json:"show_greeting"`
	NightMode       bool `json:"night_mode"`
	ShowBonusBanner bool `json:"show_bonus_banner"`
}

type summaryTask struct {
	OccurrenceID string     `json:"occurrence_id"`
	Title        string     `json:"title"`
	DueAt        time.Time  `json:"due_at"`
	State        string     `json:"state"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
}

type summaryChild struct {
	MemberID string        `json:"member_id"`
	Name     string        `json:"name"`
	Colour   string        `json:"colour"`
	AvatarID *string       `json:"avatar_id"`
	Points   *int          `json:"points"`
	Tasks    []summaryTask `json:"tasks"`
}

type summaryEvent struct {
	Title     string            `json:"title"`
	Date      string            `json:"date"`
	StartsAt  time.Time         `json:"starts_at"`
	EndsAt    time.Time         `json:"ends_at"`
	AllDay    bool              `json:"all_day"`
	Location  *string           `json:"location"`
	Attendees []summaryAttendee `json:"attendees"`
}

// summaryAttendee is one family member an event is shown to. The wall draws the member's picture when
// the family and the device show pictures, and initials in the member's colour otherwise.
type summaryAttendee struct {
	MemberID string  `json:"member_id"`
	Name     string  `json:"name"`
	Colour   string  `json:"colour"`
	AvatarID *string `json:"avatar_id"`
}

type summaryBonus struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Points    int       `json:"points"`
	ClaimMode string    `json:"claim_mode"`
	EndsAt    time.Time `json:"ends_at"`
}

type summaryResponse struct {
	FamilyName string            `json:"family_name"`
	Date       string            `json:"date"`
	Timezone   string            `json:"timezone"`
	Now        time.Time         `json:"now"`
	Options    summaryOptions    `json:"options"`
	Children   []summaryChild    `json:"children"`
	Events     []summaryEvent    `json:"events"`
	BonusTasks []summaryBonus    `json:"bonus_tasks"`
	Weather    *weather.Response `json:"weather"`
}

// Summary returns what a paired wall device shows: each child's tasks and points, the events for the
// next week, the open bonus tasks and, when the device shows it, the family's weather. The device's
// options decide which parts are filled in. The server makes the weather call, never the device. The
// device may pass its own location, which the forecast uses when the family has no location. A
// malformed location is ignored so the summary still loads.
func (h *Handlers) Summary(w http.ResponseWriter, r *http.Request) {
	dev, ok := DeviceFromContext(r.Context())
	if !ok {
		web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "Device authentication required", nil)
		return
	}
	device, _, _ := weather.ParseDeviceLocation(r)
	resp, err := h.summary(r.Context(), dev, device)
	if err != nil {
		h.internalError(w, "wall summary", err)
		return
	}
	web.RespondJSON(w, http.StatusOK, resp)
}

func (h *Handlers) summary(ctx context.Context, dev Device, device *weather.Coordinates) (summaryResponse, error) {
	opts, err := h.deviceOptions(ctx, dev)
	if err != nil {
		return summaryResponse{}, err
	}
	zoneName, zone, err := h.familyZone(ctx, dev.FamilyID)
	if err != nil {
		return summaryResponse{}, err
	}
	now := h.clk.Now().UTC()
	local := now.In(zone)
	today := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	tomorrow := today.AddDate(0, 0, 1).Format(dateLayout)

	pictures, err := h.summaryAvatars(ctx, dev.FamilyID, opts.ShowPictures)
	if err != nil {
		return summaryResponse{}, err
	}
	children, err := h.summaryChildren(ctx, dev.FamilyID, opts, today.Format(dateLayout), pictures)
	if err != nil {
		return summaryResponse{}, err
	}
	events, err := calendar.WallEvents(ctx, h.db, dev.FamilyID, zone, today, today.AddDate(0, 0, summaryWindowDays-1))
	if err != nil {
		return summaryResponse{}, err
	}
	bonusTasks, err := h.summaryBonus(ctx, dev.FamilyID, now, opts.ShowBonusBanner)
	if err != nil {
		return summaryResponse{}, err
	}
	var wx *weather.Response
	if opts.ShowWeather {
		wx = h.summaryWeather(ctx, dev.FamilyID, device)
	}

	var familyName string
	if err := h.db.QueryRowContext(ctx, `SELECT name FROM families WHERE id = ?`, dev.FamilyID).Scan(&familyName); err != nil {
		return summaryResponse{}, err
	}

	out := summaryResponse{
		FamilyName: familyName,
		Date:       today.Format(dateLayout),
		Timezone:   zoneName,
		Now:        now,
		Options:    opts,
		Children:   children,
		Events:     []summaryEvent{},
		BonusTasks: bonusTasks,
		Weather:    wx,
	}
	members, err := h.summaryMembers(ctx, dev.FamilyID, pictures)
	if err != nil {
		return summaryResponse{}, err
	}
	for _, e := range events {
		if !opts.ShowTomorrow && e.OccurrenceDate == tomorrow {
			continue
		}
		attendees := []summaryAttendee{}
		for _, id := range e.MemberIDs {
			if m, ok := members[id]; ok {
				attendees = append(attendees, m)
			}
		}
		out.Events = append(out.Events, summaryEvent{
			Title:     e.Title,
			Date:      e.OccurrenceDate,
			StartsAt:  e.StartsAt.UTC(),
			EndsAt:    e.EndsAt.UTC(),
			AllDay:    e.AllDay,
			Location:  e.Location,
			Attendees: attendees,
		})
	}
	return out, nil
}

// summaryAvatars returns the current picture ID of each active member, keyed by member ID. It is empty
// unless the wall shows pictures.
func (h *Handlers) summaryAvatars(ctx context.Context, familyID string, show bool) (map[string]string, error) {
	out := map[string]string{}
	if !show {
		return out, nil
	}
	rows, err := h.db.QueryContext(ctx,
		`SELECT a.family_member_id, a.id
		   FROM member_avatars a
		   JOIN family_members fm ON fm.id = a.family_member_id AND fm.status = 'active'
		  WHERE a.family_id = ?`, familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var memberID, avatarID string
		if err := rows.Scan(&memberID, &avatarID); err != nil {
			return nil, err
		}
		out[memberID] = avatarID
	}
	return out, rows.Err()
}

// summaryMembers returns the active members of the family by ID, with the name, colour and picture
// the wall shows for them.
func (h *Handlers) summaryMembers(ctx context.Context, familyID string, pictures map[string]string) (map[string]summaryAttendee, error) {
	rows, err := h.db.QueryContext(ctx,
		`SELECT fm.id, u.display_name, fm.colour
		   FROM family_members fm
		   JOIN users u ON u.id = fm.user_id
		  WHERE fm.family_id = ? AND fm.status = 'active'`, familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]summaryAttendee{}
	for rows.Next() {
		var a summaryAttendee
		if err := rows.Scan(&a.MemberID, &a.Name, &a.Colour); err != nil {
			return nil, err
		}
		if id, ok := pictures[a.MemberID]; ok {
			a.AvatarID = &id
		}
		out[a.MemberID] = a
	}
	return out, rows.Err()
}

// summaryWeather returns the family's weather for the wall. The device location is used only when the
// family has no location. A failed forecast leaves the location and unit in place and drops only the
// forecast, so the rest of the summary still loads. The weather service logs the failure.
func (h *Handlers) summaryWeather(ctx context.Context, familyID string, device *weather.Coordinates) *weather.Response {
	if h.weather == nil {
		return nil
	}
	resp, err := h.weather.Current(ctx, familyID, device)
	if err != nil {
		resp.Forecast = nil
	}
	return &resp
}

// deviceOptions reads the display options of the device. Pictures show only when the device and the
// family both allow them.
func (h *Handlers) deviceOptions(ctx context.Context, dev Device) (summaryOptions, error) {
	var points, showWeather, tomorrow, pictures, greeting, night, bonusBanner, avatarsOnWall int
	err := h.db.QueryRowContext(ctx,
		`SELECT d.show_points, d.show_weather, d.show_tomorrow, d.show_pictures, d.show_greeting, d.night_mode,
		        d.show_bonus_banner, f.show_avatars_on_wall
		   FROM wall_devices d
		   JOIN families f ON f.id = d.family_id
		  WHERE d.family_id = ? AND d.id = ?`,
		dev.FamilyID, dev.ID).Scan(&points, &showWeather, &tomorrow, &pictures, &greeting, &night, &bonusBanner, &avatarsOnWall)
	if err != nil {
		return summaryOptions{}, err
	}
	return summaryOptions{
		ShowPoints:      points != 0,
		ShowWeather:     showWeather != 0,
		ShowTomorrow:    tomorrow != 0,
		ShowPictures:    pictures != 0 && avatarsOnWall != 0,
		ShowGreeting:    greeting != 0,
		NightMode:       night != 0,
		ShowBonusBanner: bonusBanner != 0,
	}, nil
}

// summaryChildren returns the active children with their tasks and, when the device shows points,
// their balance. Parents never appear here. pictures maps member IDs to their current picture.
func (h *Handlers) summaryChildren(ctx context.Context, familyID string, opts summaryOptions, today string, pictures map[string]string) ([]summaryChild, error) {
	rows, err := h.db.QueryContext(ctx,
		`SELECT fm.id, u.display_name, fm.colour
		   FROM family_members fm
		   JOIN users u ON u.id = fm.user_id
		   JOIN roles r ON r.id = fm.role_id AND r.name = 'Child'
		  WHERE fm.family_id = ? AND fm.status = 'active'
		  ORDER BY u.display_name, fm.id`, familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []summaryChild{}
	for rows.Next() {
		var c summaryChild
		if err := rows.Scan(&c.MemberID, &c.Name, &c.Colour); err != nil {
			return nil, err
		}
		if id, ok := pictures[c.MemberID]; ok {
			c.AvatarID = &id
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	for i := range out {
		tasks, err := h.childTasks(ctx, familyID, out[i].MemberID, today)
		if err != nil {
			return nil, err
		}
		out[i].Tasks = tasks
		if opts.ShowPoints {
			balance, err := rewards.Balance(ctx, h.db, familyID, out[i].MemberID)
			if err != nil {
				return nil, err
			}
			out[i].Points = &balance
		}
	}
	return out, nil
}

// childTasks returns a child's copies for today and the overdue copies still open, ordered: overdue
// first (oldest first), then today's open copies by due time, then done copies, most recent first.
func (h *Handlers) childTasks(ctx context.Context, familyID, memberID, today string) ([]summaryTask, error) {
	rows, err := h.db.QueryContext(ctx,
		`SELECT o.id, d.title, strftime(?, o.due_at), oa.status, o.due_date < ? AND oa.status IN ('to_do', 'rejected'),
		        strftime(?, oa.completed_at)
		   FROM task_occurrence_assignees oa
		   JOIN task_occurrences o ON o.family_id = oa.family_id AND o.id = oa.task_occurrence_id
		   JOIN task_definitions d ON d.family_id = o.family_id AND d.id = o.task_definition_id AND d.status = 'active'
		  WHERE oa.family_id = ? AND oa.family_member_id = ? AND oa.status != 'skipped'
		    AND (o.due_date = ? OR (o.due_date < ? AND o.status != 'missed' AND oa.status IN ('to_do', 'rejected')))`,
		dbTimeUTC, today, dbTimeUTC, familyID, memberID, today, today)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type row struct {
		task      summaryTask
		status    string
		overdue   bool
		completed *time.Time
	}
	var found []row
	for rows.Next() {
		var r row
		var dueAt string
		var completed sql.NullString
		if err := rows.Scan(&r.task.OccurrenceID, &r.task.Title, &dueAt, &r.status, &r.overdue, &completed); err != nil {
			return nil, err
		}
		if r.task.DueAt, err = time.Parse(time.RFC3339, dueAt); err != nil {
			return nil, err
		}
		if completed.Valid {
			at, err := time.Parse(time.RFC3339, completed.String)
			if err != nil {
				return nil, err
			}
			r.completed = &at
		}
		found = append(found, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rank := func(r row) int {
		switch {
		case r.overdue:
			return 0
		case r.status == "submitted" || r.status == "approved":
			return 2
		default:
			return 1
		}
	}
	sort.SliceStable(found, func(i, j int) bool {
		a, b := found[i], found[j]
		if rank(a) != rank(b) {
			return rank(a) < rank(b)
		}
		if rank(a) == 2 {
			return completedAfter(a.completed, b.completed)
		}
		return a.task.DueAt.Before(b.task.DueAt)
	})

	out := make([]summaryTask, 0, len(found))
	for _, r := range found {
		r.task.State = taskState(rank(r))
		r.task.CompletedAt = r.completed
		out = append(out, r.task)
	}
	return out, nil
}

// completedAfter orders done copies most recent first; a copy without a time goes last.
func completedAfter(a, b *time.Time) bool {
	if a == nil || b == nil {
		return b == nil && a != nil
	}
	return a.After(*b)
}

func taskState(rank int) string {
	switch rank {
	case 0:
		return "overdue"
	case 2:
		return "done"
	default:
		return "pending"
	}
}

// summaryBonus returns the open bonus tasks: not cancelled, shown from now on, and not yet ended.
// The wall never shows who claimed them.
func (h *Handlers) summaryBonus(ctx context.Context, familyID string, now time.Time, show bool) ([]summaryBonus, error) {
	out := []summaryBonus{}
	if !show {
		return out, nil
	}
	actor := bonus.Actor{FamilyID: familyID}
	for _, scope := range []bonus.Scope{bonus.ScopeToday, bonus.ScopeScheduled} {
		list, err := bonus.List(ctx, h.db, h.clk, actor, true, scope)
		if err != nil {
			return nil, err
		}
		for _, t := range list {
			if t.CancelledAt != nil || !t.EndsAt.After(now) || (t.ShowFrom != nil && t.ShowFrom.After(now)) {
				continue
			}
			out = append(out, summaryBonus{ID: t.ID, Title: t.Title, Points: t.Points, ClaimMode: t.ClaimMode, EndsAt: t.EndsAt.UTC()})
		}
	}
	return out, nil
}

// familyZone returns the family's time zone and its name.
func (h *Handlers) familyZone(ctx context.Context, familyID string) (string, *time.Location, error) {
	var name string
	if err := h.db.QueryRowContext(ctx, `SELECT timezone FROM families WHERE id = ?`, familyID).Scan(&name); err != nil {
		return "", nil, err
	}
	zone, err := time.LoadLocation(name)
	if err != nil {
		return "", nil, errors.Join(errors.New("wall: family time zone"), err)
	}
	return name, zone, nil
}
