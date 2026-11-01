package rewards

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/platform/clock"
)

const (
	maxRewardName = 80
	maxRewardDesc = 500
	maxRewardIcon = 16
	maxLimitCount = 1000
	defaultIcon   = "🎁"
	dateLayout    = "2006-01-02"
	rewardSelect  = `SELECT id, name, description, icon, cost, status, limit_period, limit_count, approval_required, available_from, available_until, available_days, version, archived_at IS NOT NULL FROM rewards WHERE id = ? AND family_id = ?`
	familyZoneSQL = `SELECT timezone FROM families WHERE id = ?`
)

// ErrRewardNotFound is returned when the reward is not in the caller's family.
var ErrRewardNotFound = errors.New("reward not found")

// errStaleVersion is returned when a versioned write matched no row.
var errStaleVersion = errors.New("reward version is stale")

// catalogueDB is satisfied by *sql.DB and *sql.Tx.
type catalogueDB interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Limit caps how often a reward can be redeemed. The cap is enforced when a request is made (M4-05).
type Limit struct {
	Period string `json:"period"`
	Count  int    `json:"count"`
}

// Reward is one catalogue item as the API returns it.
type Reward struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Description       *string  `json:"description"`
	Icon              string   `json:"icon"`
	Cost              int      `json:"cost"`
	Status            string   `json:"status"`
	Limit             *Limit   `json:"limit"`
	ApprovalRequired  bool     `json:"approval_required"`
	AvailableFrom     *string  `json:"available_from"`
	AvailableUntil    *string  `json:"available_until"`
	AvailableDays     []string `json:"available_days"`
	EligibleMemberIDs []string `json:"eligible_member_ids"`
	// InWindow is true when the reward is active and today (family time zone) is inside its
	// date range and weekday set. It does not check points, limits or eligibility.
	InWindow bool `json:"in_window"`
	Archived bool `json:"archived"`
	Version  int  `json:"version"`

	days int // weekday mask from the row; 0 means every day
}

// rewardFields is the writable part of a reward. Pointer fields let a PATCH tell an absent
// field (keep) from null (clear). The fields are pre-filled from the stored row before the
// body is decoded.
type rewardFields struct {
	Name              *string   `json:"name"`
	Description       *string   `json:"description"`
	Icon              *string   `json:"icon"`
	Cost              *int      `json:"cost"`
	Status            *string   `json:"status"`
	LimitPeriod       *string   `json:"limit_period"`
	LimitCount        *int      `json:"limit_count"`
	ApprovalRequired  *bool     `json:"approval_required"`
	AvailableFrom     *string   `json:"available_from"`
	AvailableUntil    *string   `json:"available_until"`
	AvailableDays     *[]string `json:"available_days"`
	EligibleMemberIDs *[]string `json:"eligible_member_ids"`
}

// fieldError names the field that failed validation.
type fieldError struct {
	field  string
	reason string
}

func fail(field, reason string) *fieldError {
	return &fieldError{field: field, reason: reason}
}

// newRewardFields returns the defaults for a new reward: an active reward that needs no approval.
func newRewardFields() rewardFields {
	icon := defaultIcon
	status := "active"
	approval := false
	return rewardFields{Icon: &icon, Status: &status, ApprovalRequired: &approval}
}

// fieldsOf copies a stored reward into the writable fields, for a PATCH to change.
func fieldsOf(r Reward) rewardFields {
	name, icon, status := r.Name, r.Icon, r.Status
	cost, approval := r.Cost, r.ApprovalRequired
	eligible := append([]string(nil), r.EligibleMemberIDs...)
	f := rewardFields{
		Name:              &name,
		Icon:              &icon,
		Cost:              &cost,
		Status:            &status,
		ApprovalRequired:  &approval,
		AvailableFrom:     copyStr(r.AvailableFrom),
		AvailableUntil:    copyStr(r.AvailableUntil),
		Description:       copyStr(r.Description),
		EligibleMemberIDs: &eligible,
	}
	if r.Limit != nil {
		period, count := r.Limit.Period, r.Limit.Count
		f.LimitPeriod = &period
		f.LimitCount = &count
	}
	if r.AvailableDays != nil {
		days := append([]string(nil), r.AvailableDays...)
		f.AvailableDays = &days
	}
	return f
}

func copyStr(s *string) *string {
	if s == nil {
		return nil
	}
	v := *s
	return &v
}

// normalise trims and checks every field, and puts the optional lists in canonical form.
func (f *rewardFields) normalise() *fieldError {
	if f.Name == nil {
		return fail("name", "required")
	}
	name := strings.TrimSpace(*f.Name)
	if n := utf8.RuneCountInString(name); n < 1 || n > maxRewardName {
		return fail("name", "required, up to 80 characters")
	}
	f.Name = &name

	if f.Description != nil {
		desc := strings.TrimSpace(*f.Description)
		if utf8.RuneCountInString(desc) > maxRewardDesc {
			return fail("description", "up to 500 characters")
		}
		if desc == "" {
			f.Description = nil
		} else {
			f.Description = &desc
		}
	}

	if f.Icon == nil {
		return fail("icon", "required")
	}
	icon := strings.TrimSpace(*f.Icon)
	if n := utf8.RuneCountInString(icon); n < 1 || n > maxRewardIcon {
		return fail("icon", "required, up to 16 characters")
	}
	f.Icon = &icon

	if f.Cost == nil || *f.Cost < 1 || *f.Cost > maxPoints {
		return fail("cost", "must be a whole number from 1 to 100000")
	}
	if f.Status == nil || (*f.Status != "active" && *f.Status != "inactive") {
		return fail("status", "must be active or inactive")
	}
	if fe := f.normaliseLimit(); fe != nil {
		return fe
	}
	if f.ApprovalRequired == nil {
		return fail("approval_required", "required")
	}
	if fe := f.normaliseWindow(); fe != nil {
		return fe
	}
	return f.normaliseEligibility()
}

func (f *rewardFields) normaliseLimit() *fieldError {
	if f.LimitPeriod == nil && f.LimitCount == nil {
		return nil
	}
	if f.LimitPeriod == nil {
		return fail("limit_period", "set it with limit_count, or clear both")
	}
	if f.LimitCount == nil {
		return fail("limit_count", "set it with limit_period, or clear both")
	}
	switch *f.LimitPeriod {
	case "day", "week", "month", "total":
	default:
		return fail("limit_period", "must be day, week, month or total")
	}
	if *f.LimitCount < 1 || *f.LimitCount > maxLimitCount {
		return fail("limit_count", "must be a whole number from 1 to 1000")
	}
	return nil
}

func (f *rewardFields) normaliseWindow() *fieldError {
	from, fe := normaliseDate("available_from", f.AvailableFrom)
	if fe != nil {
		return fe
	}
	until, fe := normaliseDate("available_until", f.AvailableUntil)
	if fe != nil {
		return fe
	}
	if from != nil && until != nil && *from > *until {
		return fail("available_until", "must not be before available_from")
	}
	f.AvailableFrom, f.AvailableUntil = from, until

	if f.AvailableDays == nil {
		return nil
	}
	mask := 0
	for _, d := range *f.AvailableDays {
		bit, ok := dayBit(strings.ToLower(strings.TrimSpace(d)))
		if !ok {
			return fail("available_days", "must be mon, tue, wed, thu, fri, sat or sun")
		}
		mask |= bit
	}
	if mask == allDaysMask {
		mask = 0
	}
	names := namesFromMask(mask)
	if names == nil {
		f.AvailableDays = nil
	} else {
		f.AvailableDays = &names
	}
	return nil
}

// normaliseDate checks a YYYY-MM-DD date and returns it in canonical form.
func normaliseDate(field string, s *string) (*string, *fieldError) {
	if s == nil {
		return nil, nil
	}
	t, err := time.Parse(dateLayout, strings.TrimSpace(*s))
	if err != nil {
		return nil, fail(field, "must be a date as YYYY-MM-DD")
	}
	canonical := t.Format(dateLayout)
	return &canonical, nil
}

func (f *rewardFields) normaliseEligibility() *fieldError {
	seen := map[string]bool{}
	list := []string{}
	if f.EligibleMemberIDs != nil {
		for _, id := range *f.EligibleMemberIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				return fail("eligible_member_ids", "must contain member IDs")
			}
			if !seen[id] {
				seen[id] = true
				list = append(list, id)
			}
		}
	}
	f.EligibleMemberIDs = &list
	return nil
}

// daysArg is the value stored in available_days: NULL for every day.
func (f rewardFields) daysArg() any {
	if f.AvailableDays == nil {
		return nil
	}
	return maskOf(*f.AvailableDays)
}

// checkMembers confirms every eligible member is an active child of the family. It returns a
// fieldError when one is not.
func checkMembers(ctx context.Context, tx *sql.Tx, familyID string, memberIDs []string) (*fieldError, error) {
	for _, id := range memberIDs {
		var n int
		err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM family_members m JOIN users u ON u.id = m.user_id
			 WHERE m.family_id = ? AND m.id = ? AND m.status = 'active' AND u.is_child = 1`,
			familyID, id,
		).Scan(&n)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return fail("eligible_member_ids", "must be active children in this family"), nil
		}
	}
	return nil, nil
}

// loadReward reads one reward of the family.
func loadReward(ctx context.Context, db catalogueDB, familyID, id string) (Reward, error) {
	var (
		r                         Reward
		desc, period, from, until sql.NullString
		limitCount, days          sql.NullInt64
		approval                  int
	)
	err := db.QueryRowContext(ctx, rewardSelect, id, familyID).Scan(
		&r.ID, &r.Name, &desc, &r.Icon, &r.Cost, &r.Status, &period, &limitCount, &approval,
		&from, &until, &days, &r.Version, &r.Archived,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Reward{}, ErrRewardNotFound
	}
	if err != nil {
		return Reward{}, err
	}
	if desc.Valid {
		r.Description = &desc.String
	}
	if period.Valid {
		r.Limit = &Limit{Period: period.String, Count: int(limitCount.Int64)}
	}
	r.ApprovalRequired = approval == 1
	if from.Valid {
		r.AvailableFrom = &from.String
	}
	if until.Valid {
		r.AvailableUntil = &until.String
	}
	if days.Valid {
		r.days = int(days.Int64)
		r.AvailableDays = namesFromMask(r.days)
	}

	rows, err := db.QueryContext(ctx,
		`SELECT family_member_id FROM reward_eligibility WHERE family_id = ? AND reward_id = ? ORDER BY family_member_id`,
		familyID, id)
	if err != nil {
		return Reward{}, err
	}
	defer rows.Close()
	r.EligibleMemberIDs = []string{}
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return Reward{}, err
		}
		r.EligibleMemberIDs = append(r.EligibleMemberIDs, m)
	}
	return r, rows.Err()
}

// viewReward loads a reward and fills in InWindow for the family's local time.
func viewReward(ctx context.Context, db catalogueDB, clk clock.Clock, familyID, id string) (Reward, error) {
	r, err := loadReward(ctx, db, familyID, id)
	if err != nil {
		return Reward{}, err
	}
	var zone string
	if err := db.QueryRowContext(ctx, familyZoneSQL, familyID).Scan(&zone); err != nil {
		return Reward{}, err
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		loc = time.UTC
	}
	r.InWindow = r.Status == "active" && !r.Archived && r.inWindow(clk.Now().In(loc))
	return r, nil
}

// insertReward writes a new reward row.
func insertReward(ctx context.Context, tx *sql.Tx, familyID, id string, f rewardFields) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO rewards (id, family_id, name, description, icon, cost, status, limit_period, limit_count,
		     approval_required, available_from, available_until, available_days)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, familyID, *f.Name, f.Description, *f.Icon, *f.Cost, *f.Status, f.LimitPeriod, f.LimitCount,
		boolInt(*f.ApprovalRequired), f.AvailableFrom, f.AvailableUntil, f.daysArg(),
	)
	return err
}

// updateReward writes the fields of a reward when its version matches. It returns errStaleVersion
// when no row matched.
func updateReward(ctx context.Context, tx *sql.Tx, familyID, id string, version int, f rewardFields) error {
	res, err := tx.ExecContext(ctx,
		`UPDATE rewards SET name = ?, description = ?, icon = ?, cost = ?, status = ?, limit_period = ?,
		     limit_count = ?, approval_required = ?, available_from = ?, available_until = ?, available_days = ?,
		     version = version + 1, updated_at = strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')
		 WHERE id = ? AND family_id = ? AND version = ? AND archived_at IS NULL`,
		*f.Name, f.Description, *f.Icon, *f.Cost, *f.Status, f.LimitPeriod, f.LimitCount,
		boolInt(*f.ApprovalRequired), f.AvailableFrom, f.AvailableUntil, f.daysArg(),
		id, familyID, version,
	)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return errStaleVersion
	}
	return nil
}

// archiveReward marks a reward archived when its version matches.
func archiveReward(ctx context.Context, tx *sql.Tx, familyID, id string, version int) error {
	res, err := tx.ExecContext(ctx,
		`UPDATE rewards SET archived_at = strftime('%Y-%m-%d %H:%M:%f+00:00', 'now'), version = version + 1,
		     updated_at = strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')
		 WHERE id = ? AND family_id = ? AND version = ? AND archived_at IS NULL`,
		id, familyID, version,
	)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return errStaleVersion
	}
	return nil
}

// replaceEligibility sets the members who may request a reward.
func replaceEligibility(ctx context.Context, tx *sql.Tx, familyID, rewardID string, memberIDs []string) error {
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM reward_eligibility WHERE family_id = ? AND reward_id = ?`, familyID, rewardID,
	); err != nil {
		return err
	}
	for _, m := range memberIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO reward_eligibility (family_id, reward_id, family_member_id) VALUES (?, ?, ?)`,
			familyID, rewardID, m,
		); err != nil {
			return err
		}
	}
	return nil
}

// recordAudit writes an audit entry for a catalogue change.
func recordAudit(ctx context.Context, tx *sql.Tx, familyID, userID, action, rewardID string, name string) error {
	return audit.Record(ctx, tx, familyID, userID, action, "reward", &rewardID, map[string]any{"name": name})
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
