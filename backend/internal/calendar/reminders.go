package calendar

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	ical "github.com/emersion/go-ical"

	"nestwall/backend/internal/notifypolicy"
	"nestwall/backend/internal/platform/clock"
	"nestwall/backend/internal/platform/ids"
)

const (
	// ReminderPollInterval is how often due reminders are checked.
	ReminderPollInterval = 30 * time.Second
	reminderKind         = "event_reminder"
)

// triggerPattern matches an ICS duration (RFC 5545), such as -PT15M, -P1D or -PT1H30M.
var triggerPattern = regexp.MustCompile(`^([+-])?P(?:(\d+)W)?(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?$`)

// normaliseReminders checks the reminders in a request. Each is a whole number of minutes before the
// start. Repeats are dropped and the result is sorted.
func normaliseReminders(minutes []int) ([]int, error) {
	seen := map[int]bool{}
	out := make([]int, 0, len(minutes))
	for _, m := range minutes {
		if m < 0 {
			return nil, fieldError("reminders", "invalid")
		}
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	sort.Ints(out)
	return out, nil
}

// parseTriggerMinutes reads a VALARM TRIGGER that is a duration before the start. It reports false for
// a duration after the start, an absolute time, or anything it cannot read.
func parseTriggerMinutes(value string) (int, bool) {
	m := triggerPattern.FindStringSubmatch(strings.TrimSpace(value))
	if m == nil || (m[2] == "" && m[3] == "" && m[4] == "" && m[5] == "" && m[6] == "") {
		return 0, false
	}
	total := 0
	for i, mult := range []int{10080, 1440, 60, 1} {
		if m[2+i] == "" {
			continue
		}
		n, err := strconv.Atoi(m[2+i])
		if err != nil {
			return 0, false
		}
		total += n * mult
	}
	if m[6] != "" {
		n, err := strconv.Atoi(m[6])
		if err != nil {
			return 0, false
		}
		total += n / 60
	}
	if m[1] != "-" && total > 0 {
		return 0, false
	}
	return total, true
}

// alarmMinutes returns the reminders that a VEVENT's VALARM blocks ask for, as minutes before the
// start. Alarms relative to the end, and alarms with an absolute time, are skipped.
func alarmMinutes(event *ical.Component) []int {
	var out []int
	for _, child := range event.Children {
		if child.Name != "VALARM" {
			continue
		}
		trigger := child.Props.Get("TRIGGER")
		if trigger == nil || strings.EqualFold(trigger.Params.Get("RELATED"), "END") {
			continue
		}
		if m, ok := parseTriggerMinutes(trigger.Value); ok {
			out = append(out, m)
		}
	}
	normalised, _ := normaliseReminders(out)
	return normalised
}

// replaceReminders stores the reminders of an event. Each reminder is armed at now. When rearm is false
// (the start did not change), a reminder that is already stored keeps its arm time, so an edit does not
// make past occurrences remindable. For a single event, a reminder whose time has already passed is not
// stored. A repeating event keeps every reminder, since a later occurrence can still use it.
func replaceReminders(ctx context.Context, tx *sql.Tx, familyID, eventID string, minutes []int,
	startsAt, now time.Time, repeats, rearm bool) error {
	armed := map[int]string{}
	if !rearm {
		rows, err := tx.QueryContext(ctx,
			`SELECT minutes, created_at FROM calendar_event_reminders WHERE family_id = ? AND event_id = ?`,
			familyID, eventID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var m int
			var at string
			if err := rows.Scan(&m, &at); err != nil {
				rows.Close()
				return err
			}
			armed[m] = at
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM calendar_event_reminders WHERE family_id = ? AND event_id = ?`, familyID, eventID); err != nil {
		return err
	}
	stamp := dbTime(now)
	for _, m := range minutes {
		if !repeats && !startsAt.Add(-time.Duration(m)*time.Minute).After(now) {
			continue
		}
		created := stamp
		if at, ok := armed[m]; ok {
			created = at
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO calendar_event_reminders (id, family_id, event_id, minutes, created_at)
			 VALUES (?, ?, ?, ?, ?)`,
			ids.New(), familyID, eventID, m, created); err != nil {
			return err
		}
	}
	return nil
}

// loadReminders returns the reminder minutes of an event, smallest first.
func loadReminders(ctx context.Context, q querier, familyID, eventID string) ([]int, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT minutes FROM calendar_event_reminders WHERE family_id = ? AND event_id = ? ORDER BY minutes`,
		familyID, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []int{}
	for rows.Next() {
		var m int
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Reminders delivers event reminders when they fall due. Each reminder is sent once, as an in-app
// notice to the people on the event and to its creator. Email is not used for reminders.
type Reminders struct {
	db  *sql.DB
	clk clock.Clock
	log *slog.Logger
}

// NewReminders returns a dispatcher for event reminders.
func NewReminders(db *sql.DB, clk clock.Clock, log *slog.Logger) *Reminders {
	return &Reminders{db: db, clk: clk, log: log}
}

// Run sends due reminders every interval until ctx is cancelled.
func (r *Reminders) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := r.Send(ctx); err != nil && ctx.Err() == nil {
			r.log.Error("event reminders failed", slog.String("error", err.Error()))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type dueReminder struct {
	familyID, eventID, title, creatorID string
	minutes                             int
	starts                              time.Time
	members                             []string
}

// reminderHorizon is how far ahead the occurrences of a repeating event are checked for reminders.
const reminderHorizon = 365 * 24 * time.Hour

// Send delivers the reminders that are due and returns how many it sent. A reminder is due for an occurrence
// once its time has passed, the occurrence has not started yet, the reminder was armed before its time, and it
// has not been sent for that occurrence before.
func (r *Reminders) Send(ctx context.Context) (int, error) {
	now := r.clk.Now()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	type eventRef struct{ familyID, eventID string }
	rows, err := tx.QueryContext(ctx,
		`SELECT DISTINCT family_id, event_id FROM calendar_event_reminders ORDER BY family_id, event_id`)
	if err != nil {
		return 0, err
	}
	var refs []eventRef
	for rows.Next() {
		var ref eventRef
		if err := rows.Scan(&ref.familyID, &ref.eventID); err != nil {
			rows.Close()
			return 0, err
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	var due []dueReminder
	for _, ref := range refs {
		found, err := dueForEvent(ctx, tx, ref.familyID, ref.eventID, now)
		if err != nil {
			return 0, err
		}
		due = append(due, found...)
	}
	for _, d := range due {
		if err := deliverReminder(ctx, tx, d, now); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(due), nil
}

// dueForEvent returns the reminders of one event that are due for its occurrences now.
func dueForEvent(ctx context.Context, tx *sql.Tx, familyID, eventID string, now time.Time) ([]dueReminder, error) {
	e, err := loadEvent(ctx, tx, familyID, eventID)
	if err != nil {
		return nil, err
	}
	armed, err := loadArmed(ctx, tx, familyID, eventID)
	if err != nil {
		return nil, err
	}
	series, err := e.series()
	if err != nil {
		return nil, err
	}
	occurrences, err := Expand(series, e.exceptionList(), now, now.Add(reminderHorizon))
	if err != nil {
		return nil, err
	}

	var out []dueReminder
	for _, o := range occurrences {
		if !o.StartsAt.After(now) {
			continue
		}
		key := dbTime(o.StartsAt)
		for _, m := range e.Reminders {
			fire := o.StartsAt.Add(-time.Duration(m) * time.Minute)
			if fire.After(now) {
				continue
			}
			if at, ok := armed[m]; !ok || fire.Before(at) {
				continue
			}
			var sent int
			if err := tx.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM calendar_reminder_deliveries WHERE event_id = ? AND minutes = ? AND occurrence_at = ?`,
				eventID, m, key).Scan(&sent); err != nil {
				return nil, err
			}
			if sent > 0 {
				continue
			}
			out = append(out, dueReminder{
				familyID: familyID, eventID: eventID, title: o.Title, creatorID: e.CreatedBy,
				minutes: m, starts: o.StartsAt, members: e.MemberIDs,
			})
		}
	}
	return out, nil
}

// loadArmed returns the arm time of each reminder of an event, by minutes.
func loadArmed(ctx context.Context, q querier, familyID, eventID string) (map[int]time.Time, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT minutes, created_at FROM calendar_event_reminders WHERE family_id = ? AND event_id = ?`,
		familyID, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[int]time.Time{}
	for rows.Next() {
		var m int
		var at storedTime
		if err := rows.Scan(&m, &at); err != nil {
			return nil, err
		}
		out[m] = at.T
	}
	return out, rows.Err()
}


// deliverReminder writes the in-app notices for one reminder and marks it sent. When the family's
// in-app switch is off, nothing is written, but the reminder is still marked sent.
func deliverReminder(ctx context.Context, tx *sql.Tx, d dueReminder, now time.Time) error {
	settings, err := notifypolicy.Load(ctx, tx, d.familyID)
	if err != nil {
		return err
	}
	if settings.InApp {
		recipients, err := reminderRecipients(ctx, tx, d)
		if err != nil {
			return err
		}
		body := reminderBody(d.title, d.minutes)
		for _, recipient := range recipients {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO notifications (id, family_id, recipient_id, kind, subject_member_id, body, email_pending, push_pending, created_at)
				 VALUES (?, ?, ?, ?, NULL, ?, 0, ?, ?)`,
				ids.New(), d.familyID, recipient, reminderKind, body, settings.Browser, dbTime(now)); err != nil {
				return fmt.Errorf("insert reminder notice: %w", err)
			}
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO calendar_reminder_deliveries (family_id, event_id, minutes, occurrence_at, sent_at)
		 VALUES (?, ?, ?, ?, ?)`,
		d.familyID, d.eventID, d.minutes, dbTime(d.starts), dbTime(now)); err != nil {
		return fmt.Errorf("mark reminder sent: %w", err)
	}
	return nil
}

// reminderRecipients returns the family members who get the reminder. An event with people assigned
// goes to them, and to its creator when the creator is not one of them. An event with nobody assigned
// is family-wide, so it goes to every active member.
func reminderRecipients(ctx context.Context, tx *sql.Tx, d dueReminder) ([]string, error) {
	if len(d.members) == 0 {
		return activeMemberIDs(ctx, tx, d.familyID)
	}
	out := append([]string(nil), d.members...)
	var creator string
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM family_members WHERE family_id = ? AND user_id = ?`, d.familyID, d.creatorID,
	).Scan(&creator)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if creator != "" && !slices.Contains(out, creator) {
		out = append(out, creator)
	}
	return out, nil
}

// activeMemberIDs returns the IDs of the family's active members.
func activeMemberIDs(ctx context.Context, tx *sql.Tx, familyID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT id FROM family_members WHERE family_id = ? AND status = 'active' ORDER BY id`, familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}


// reminderBody is the notice text, such as "Dentist starts in 1 hour".
func reminderBody(title string, minutes int) string {
	if minutes == 0 {
		return title + " starts now"
	}
	return title + " starts in " + durationText(minutes)
}

// durationText writes a reminder offset in the largest whole unit: days, hours or minutes.
func durationText(minutes int) string {
	switch {
	case minutes%1440 == 0:
		return countOf(minutes/1440, "day")
	case minutes%60 == 0:
		return countOf(minutes/60, "hour")
	default:
		return countOf(minutes, "minute")
	}
}

func countOf(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
}
