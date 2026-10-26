package badges

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"nestwall/backend/internal/audit"
	"nestwall/backend/internal/badges/catalogue"
	"nestwall/backend/internal/platform/ids"
)

// Limits for user-entered text (characters, not bytes).
const (
	maxName        = 60
	maxEmoji       = 16
	maxDescription = 200
	maxNote        = 200
)

// singleEmojiPattern is one emoji, the same rule as the tasks category picture. It mirrors isSingleEmoji in the frontend.
var singleEmojiPattern = regexp.MustCompile(`^(?:[\x{1F1E6}-\x{1F1FF}]{2}|\p{So}(?:\x{FE0F}|[\x{1F3FB}-\x{1F3FF}])?(?:\x{200D}\p{So}(?:\x{FE0F}|[\x{1F3FB}-\x{1F3FF}])?)*)$`)

var (
	// ErrMemberNotFound means the member is not an active child in the family.
	ErrMemberNotFound = errors.New("badges: member not found")
	// ErrBadgeNotFound means the badge is not in the family.
	ErrBadgeNotFound = errors.New("badges: badge not found")
	// ErrNotCustom means a built-in badge was sent to the manual award endpoint.
	ErrNotCustom = errors.New("badges: built-in badges are not awarded by hand")
	// ErrBadgeDisabled means the custom badge is switched off.
	ErrBadgeDisabled = errors.New("badges: badge is off")
	// ErrBadgesOff means the family's master switch is off.
	ErrBadgesOff = errors.New("badges: badges are off for the family")
	// ErrUnknownGroup means the group key is not one of the six built-in groups.
	ErrUnknownGroup = errors.New("badges: unknown group")
	// ErrIdempotencyConflict means the key was used for a different award.
	ErrIdempotencyConflict = errors.New("badges: idempotency key reused for a different award")
)

// FieldError names the input field that failed validation and why.
type FieldError struct {
	Field  string
	Reason string
}

func (e *FieldError) Error() string { return "badges: invalid " + e.Field + ": " + e.Reason }

// staleVersionError reports a write made against a version that is no longer current. Current is the
// version now stored.
type staleVersionError struct{ Current int }

func (e staleVersionError) Error() string {
	return fmt.Sprintf("badges: stale version (current %d)", e.Current)
}

// Settings are the family's badge switches.
type Settings struct {
	Enabled            bool `json:"enabled"`
	ShowOnKidDashboard bool `json:"show_on_kid_dashboard"`
	ShowOnWall         bool `json:"show_on_wall"`
	NotifyChild        bool `json:"notify_child"`
}

// BuiltInBadge is one of the 24 built-in badges, with its on or off state.
type BuiltInBadge struct {
	ID      string `json:"id"`
	Key     string `json:"key"`
	Enabled bool   `json:"enabled"`
}

// GroupView is a built-in group. Enabled is true only when every badge in the group is on.
type GroupView struct {
	Group   string         `json:"group"`
	Enabled bool           `json:"enabled"`
	Badges  []BuiltInBadge `json:"badges"`
}

// Custom is a family-made badge that a parent awards by hand.
type Custom struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Emoji        string  `json:"emoji"`
	Description  *string `json:"description"`
	Enabled      bool    `json:"enabled"`
	Version      int     `json:"version"`
	AwardedCount int     `json:"awarded_count"`
}

// Config is the whole Settings > Badges view. Version is the settings version, sent back as If-Match
// on writes to the switches and the groups.
type Config struct {
	Version  int         `json:"version"`
	Settings Settings    `json:"settings"`
	Groups   []GroupView `json:"groups"`
	Custom   []Custom    `json:"custom"`
}

// CustomInput is the editable part of a custom badge.
type CustomInput struct {
	Name        string
	Emoji       string
	Description *string
}

// CustomUpdate is a custom badge edit. Enabled is sent on every update.
type CustomUpdate struct {
	CustomInput
	Enabled bool
}

// AwardInput is a manual award. IdempotencyKey comes from the Idempotency-Key header.
type AwardInput struct {
	MemberID       string
	BadgeID        string
	Note           string
	IdempotencyKey string
}

// AwardResult is the stored award. Replayed is true when the key was already used for this award.
type AwardResult struct {
	ID        string `json:"id"`
	MemberID  string `json:"member_id"`
	BadgeID   string `json:"badge_id"`
	Note      string `json:"note"`
	AwardedAt string `json:"awarded_at"`
	Replayed  bool   `json:"replayed"`
}

// customSelect lists the family's custom badges with their award counts. Callers add the ordering or
// the id filter.
const customSelect = `SELECT d.id, d.name, d.emoji, d.description, d.enabled, d.version,
	(SELECT COUNT(*) FROM member_badges mb
	  WHERE mb.family_id = d.family_id AND mb.badge_definition_id = d.id AND mb.source = 'manual')
	FROM badge_definitions d
	WHERE d.family_id = ? AND d.badge_key IS NULL`

// LoadConfig reads the family's badge settings, built-in groups and custom badges.
func LoadConfig(ctx context.Context, q querier, familyID string) (Config, error) {
	cfg := Config{Groups: []GroupView{}, Custom: []Custom{}}
	err := q.QueryRowContext(ctx,
		`SELECT enabled, show_on_kid_dashboard, show_on_wall, notify_child, version
		 FROM badge_settings WHERE family_id = ?`, familyID,
	).Scan(&cfg.Settings.Enabled, &cfg.Settings.ShowOnKidDashboard, &cfg.Settings.ShowOnWall,
		&cfg.Settings.NotifyChild, &cfg.Version)
	if err != nil {
		return Config{}, fmt.Errorf("load badge settings: %w", err)
	}

	rows, err := q.QueryContext(ctx,
		`SELECT id, badge_key, enabled FROM badge_definitions
		 WHERE family_id = ? AND badge_key IS NOT NULL`, familyID)
	if err != nil {
		return Config{}, fmt.Errorf("load built-in badges: %w", err)
	}
	defs := map[string]BuiltInBadge{}
	for rows.Next() {
		var d BuiltInBadge
		if err := rows.Scan(&d.ID, &d.Key, &d.Enabled); err != nil {
			rows.Close()
			return Config{}, fmt.Errorf("scan built-in badge: %w", err)
		}
		defs[d.Key] = d
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Config{}, fmt.Errorf("read built-in badges: %w", err)
	}

	// Groups follow the catalogue's display order.
	index := map[string]int{}
	for _, b := range catalogue.BuiltIn {
		d, ok := defs[b.Key]
		if !ok {
			continue
		}
		g := string(b.Group)
		i, seen := index[g]
		if !seen {
			i = len(cfg.Groups)
			index[g] = i
			cfg.Groups = append(cfg.Groups, GroupView{Group: g, Enabled: true, Badges: []BuiltInBadge{}})
		}
		cfg.Groups[i].Badges = append(cfg.Groups[i].Badges, d)
		cfg.Groups[i].Enabled = cfg.Groups[i].Enabled && d.Enabled
	}

	rows, err = q.QueryContext(ctx, customSelect+` ORDER BY d.created_at, d.id`, familyID)
	if err != nil {
		return Config{}, fmt.Errorf("load custom badges: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		c, err := scanCustom(rows)
		if err != nil {
			return Config{}, fmt.Errorf("scan custom badge: %w", err)
		}
		cfg.Custom = append(cfg.Custom, c)
	}
	if err := rows.Err(); err != nil {
		return Config{}, fmt.Errorf("read custom badges: %w", err)
	}
	return cfg, nil
}

// SaveSettings replaces the family's master switch and display settings. expected is the version from
// If-Match, or -1 to skip the check.
func SaveSettings(ctx context.Context, db *sql.DB, now time.Time, familyID, userID string, s Settings, expected int) (Config, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Config{}, fmt.Errorf("begin settings save: %w", err)
	}
	defer tx.Rollback()

	if err := bumpSettings(ctx, tx, familyID, expected, now); err != nil {
		return Config{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE badge_settings
		 SET enabled = ?, show_on_kid_dashboard = ?, show_on_wall = ?, notify_child = ?
		 WHERE family_id = ?`,
		boolInt(s.Enabled), boolInt(s.ShowOnKidDashboard), boolInt(s.ShowOnWall), boolInt(s.NotifyChild), familyID,
	); err != nil {
		return Config{}, fmt.Errorf("update badge settings: %w", err)
	}
	if err := audit.Record(ctx, tx, familyID, userID, "badge.settings_updated", "badge_settings", nil, map[string]any{
		"enabled":               s.Enabled,
		"show_on_kid_dashboard": s.ShowOnKidDashboard,
		"show_on_wall":          s.ShowOnWall,
		"notify_child":          s.NotifyChild,
	}); err != nil {
		return Config{}, fmt.Errorf("audit badge settings: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Config{}, fmt.Errorf("commit settings save: %w", err)
	}
	return LoadConfig(ctx, db, familyID)
}

// SetGroup switches a built-in group on or off. It turns every badge in the group on or off. expected
// is the settings version from If-Match, or -1.
func SetGroup(ctx context.Context, db *sql.DB, now time.Time, familyID, userID, group string, enabled bool, expected int) (Config, error) {
	if !knownGroup(group) {
		return Config{}, ErrUnknownGroup
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Config{}, fmt.Errorf("begin group save: %w", err)
	}
	defer tx.Rollback()

	if err := bumpSettings(ctx, tx, familyID, expected, now); err != nil {
		return Config{}, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE badge_definitions SET enabled = ?, updated_at = ?
		 WHERE family_id = ? AND group_key = ? AND badge_key IS NOT NULL`,
		boolInt(enabled), formatTime(now), familyID, group,
	); err != nil {
		return Config{}, fmt.Errorf("update badge group: %w", err)
	}
	if err := audit.Record(ctx, tx, familyID, userID, "badge.group_updated", "badge_group", nil, map[string]any{
		"group":   group,
		"enabled": enabled,
	}); err != nil {
		return Config{}, fmt.Errorf("audit badge group: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Config{}, fmt.Errorf("commit group save: %w", err)
	}
	return LoadConfig(ctx, db, familyID)
}

// CreateCustom adds a custom badge. It is on when created.
func CreateCustom(ctx context.Context, db *sql.DB, now time.Time, familyID, userID string, in CustomInput) (Custom, error) {
	clean, err := cleanCustom(in)
	if err != nil {
		return Custom{}, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Custom{}, fmt.Errorf("begin custom create: %w", err)
	}
	defer tx.Rollback()

	id := ids.New()
	stamp := formatTime(now)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO badge_definitions (id, family_id, name, emoji, description, enabled, version, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, 1, 1, ?, ?)`,
		id, familyID, clean.Name, clean.Emoji, clean.Description, stamp, stamp,
	); err != nil {
		return Custom{}, fmt.Errorf("insert custom badge: %w", err)
	}
	if err := audit.Record(ctx, tx, familyID, userID, "badge.custom_created", "badge_definition", &id, map[string]any{
		"name": clean.Name,
	}); err != nil {
		return Custom{}, fmt.Errorf("audit custom badge: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Custom{}, fmt.Errorf("commit custom create: %w", err)
	}
	return customByID(ctx, db, familyID, id)
}

// UpdateCustom edits a custom badge. expected is the badge's version from If-Match, or -1. It returns
// ErrBadgeNotFound for a built-in or unknown id, and a staleVersionError when the version is old.
func UpdateCustom(ctx context.Context, db *sql.DB, now time.Time, familyID, userID, id string, in CustomUpdate, expected int) (Custom, error) {
	clean, err := cleanCustom(in.CustomInput)
	if err != nil {
		return Custom{}, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Custom{}, fmt.Errorf("begin custom update: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`UPDATE badge_definitions
		 SET name = ?, emoji = ?, description = ?, enabled = ?, version = version + 1, updated_at = ?
		 WHERE family_id = ? AND id = ? AND badge_key IS NULL AND (? < 0 OR version = ?)`,
		clean.Name, clean.Emoji, clean.Description, boolInt(in.Enabled), formatTime(now),
		familyID, id, expected, expected,
	)
	if err != nil {
		return Custom{}, fmt.Errorf("update custom badge: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var current int
		err := tx.QueryRowContext(ctx,
			`SELECT version FROM badge_definitions WHERE family_id = ? AND id = ? AND badge_key IS NULL`,
			familyID, id,
		).Scan(&current)
		if errors.Is(err, sql.ErrNoRows) {
			return Custom{}, ErrBadgeNotFound
		}
		if err != nil {
			return Custom{}, fmt.Errorf("load custom badge version: %w", err)
		}
		return Custom{}, staleVersionError{Current: current}
	}
	if err := audit.Record(ctx, tx, familyID, userID, "badge.custom_updated", "badge_definition", &id, map[string]any{
		"name":    clean.Name,
		"enabled": in.Enabled,
	}); err != nil {
		return Custom{}, fmt.Errorf("audit custom badge: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Custom{}, fmt.Errorf("commit custom update: %w", err)
	}
	return customByID(ctx, db, familyID, id)
}

// Award gives a custom badge to an active child by hand. The same idempotency key with the same
// member, badge and note returns the stored award with Replayed set. The same key with different
// content returns ErrIdempotencyConflict.
func Award(ctx context.Context, db *sql.DB, now time.Time, familyID, userID, actorMemberID string, in AwardInput) (AwardResult, error) {
	memberID := strings.TrimSpace(in.MemberID)
	badgeID := strings.TrimSpace(in.BadgeID)
	if memberID == "" {
		return AwardResult{}, &FieldError{Field: "member_id", Reason: "required"}
	}
	if badgeID == "" {
		return AwardResult{}, &FieldError{Field: "badge_id", Reason: "required"}
	}
	note := strings.TrimSpace(in.Note)
	if note == "" || utf8.RuneCountInString(note) > maxNote {
		return AwardResult{}, &FieldError{Field: "note", Reason: "required, up to 200 characters"}
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return AwardResult{}, fmt.Errorf("begin award: %w", err)
	}
	defer tx.Rollback()

	var prev AwardResult
	err = tx.QueryRowContext(ctx,
		`SELECT id, family_member_id, badge_definition_id, COALESCE(note, ''), awarded_at
		 FROM member_badges WHERE family_id = ? AND idempotency_key = ?`,
		familyID, in.IdempotencyKey,
	).Scan(&prev.ID, &prev.MemberID, &prev.BadgeID, &prev.Note, &prev.AwardedAt)
	switch {
	case err == nil:
		if prev.MemberID != memberID || prev.BadgeID != badgeID || prev.Note != note {
			return AwardResult{}, ErrIdempotencyConflict
		}
		prev.Replayed = true
		return prev, nil
	case !errors.Is(err, sql.ErrNoRows):
		return AwardResult{}, fmt.Errorf("find award by key: %w", err)
	}

	var on bool
	if err := tx.QueryRowContext(ctx,
		`SELECT enabled FROM badge_settings WHERE family_id = ?`, familyID,
	).Scan(&on); err != nil {
		return AwardResult{}, fmt.Errorf("load badge settings: %w", err)
	}
	if !on {
		return AwardResult{}, ErrBadgesOff
	}

	var child int
	err = tx.QueryRowContext(ctx,
		`SELECT 1 FROM family_members m JOIN users u ON u.id = m.user_id
		 WHERE m.family_id = ? AND m.id = ? AND m.status = 'active' AND u.is_child = 1`,
		familyID, memberID,
	).Scan(&child)
	if errors.Is(err, sql.ErrNoRows) {
		return AwardResult{}, ErrMemberNotFound
	}
	if err != nil {
		return AwardResult{}, fmt.Errorf("load member: %w", err)
	}

	var enabled, custom bool
	var badgeName string
	err = tx.QueryRowContext(ctx,
		`SELECT enabled, badge_key IS NULL, COALESCE(name, '') FROM badge_definitions WHERE family_id = ? AND id = ?`,
		familyID, badgeID,
	).Scan(&enabled, &custom, &badgeName)
	if errors.Is(err, sql.ErrNoRows) {
		return AwardResult{}, ErrBadgeNotFound
	}
	if err != nil {
		return AwardResult{}, fmt.Errorf("load badge: %w", err)
	}
	if !custom {
		return AwardResult{}, ErrNotCustom
	}
	if !enabled {
		return AwardResult{}, ErrBadgeDisabled
	}

	id := ids.New()
	stamp := formatTime(now)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO member_badges
		   (id, family_id, family_member_id, badge_definition_id, source, note, awarded_by_member_id, idempotency_key, awarded_at)
		 VALUES (?, ?, ?, ?, 'manual', ?, ?, ?, ?)`,
		id, familyID, memberID, badgeID, note, actorMemberID, in.IdempotencyKey, stamp,
	); err != nil {
		return AwardResult{}, fmt.Errorf("insert award: %w", err)
	}
	awarder, err := memberDisplayName(ctx, tx, actorMemberID)
	if err != nil {
		return AwardResult{}, fmt.Errorf("load awarder: %w", err)
	}
	if err := notifyChild(ctx, tx, familyID, memberID, kindBadgeAwarded,
		fmt.Sprintf("%s gave you the %s badge.", awarder, badgeName), now); err != nil {
		return AwardResult{}, err
	}
	if err := audit.Record(ctx, tx, familyID, userID, "badge.awarded", "member_badge", &id, map[string]any{
		"member_id": memberID,
		"badge_id":  badgeID,
	}); err != nil {
		return AwardResult{}, fmt.Errorf("audit award: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return AwardResult{}, fmt.Errorf("commit award: %w", err)
	}
	return AwardResult{ID: id, MemberID: memberID, BadgeID: badgeID, Note: note, AwardedAt: stamp}, nil
}

// bumpSettings checks expected against the stored settings version and bumps it. It runs first in a
// write transaction, so the check and the bump are one atomic statement. It returns a
// staleVersionError with the stored version when expected does not match.
func bumpSettings(ctx context.Context, tx *sql.Tx, familyID string, expected int, now time.Time) error {
	res, err := tx.ExecContext(ctx,
		`UPDATE badge_settings SET version = version + 1, updated_at = ?
		 WHERE family_id = ? AND (? < 0 OR version = ?)`,
		formatTime(now), familyID, expected, expected,
	)
	if err != nil {
		return fmt.Errorf("bump badge settings: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 1 {
		return nil
	}
	var current int
	if err := tx.QueryRowContext(ctx,
		`SELECT version FROM badge_settings WHERE family_id = ?`, familyID,
	).Scan(&current); err != nil {
		return fmt.Errorf("load badge settings version: %w", err)
	}
	return staleVersionError{Current: current}
}

// customByID returns one custom badge with its award count.
func customByID(ctx context.Context, q querier, familyID, id string) (Custom, error) {
	c, err := scanCustom(q.QueryRowContext(ctx, customSelect+` AND d.id = ?`, familyID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Custom{}, ErrBadgeNotFound
	}
	if err != nil {
		return Custom{}, fmt.Errorf("load custom badge: %w", err)
	}
	return c, nil
}

// rowScanner is satisfied by *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanCustom(s rowScanner) (Custom, error) {
	var c Custom
	var desc sql.NullString
	if err := s.Scan(&c.ID, &c.Name, &c.Emoji, &desc, &c.Enabled, &c.Version, &c.AwardedCount); err != nil {
		return Custom{}, err
	}
	if desc.Valid {
		d := desc.String
		c.Description = &d
	}
	return c, nil
}

// cleanCustom trims the badge text and checks the limits. An empty description is stored as NULL.
func cleanCustom(in CustomInput) (CustomInput, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || utf8.RuneCountInString(name) > maxName {
		return CustomInput{}, &FieldError{Field: "name", Reason: "required, up to 60 characters"}
	}
	emoji := strings.TrimSpace(in.Emoji)
	if emoji == "" || utf8.RuneCountInString(emoji) > maxEmoji {
		return CustomInput{}, &FieldError{Field: "emoji", Reason: "choose a picture"}
	}
	if !singleEmojiPattern.MatchString(emoji) {
		return CustomInput{}, &FieldError{Field: "emoji", Reason: "choose one emoji"}
	}
	var desc *string
	if in.Description != nil {
		d := strings.TrimSpace(*in.Description)
		if utf8.RuneCountInString(d) > maxDescription {
			return CustomInput{}, &FieldError{Field: "description", Reason: "up to 200 characters"}
		}
		if d != "" {
			desc = &d
		}
	}
	return CustomInput{Name: name, Emoji: emoji, Description: desc}, nil
}

// knownGroup reports whether the key is one of the built-in groups.
func knownGroup(group string) bool {
	for _, b := range catalogue.BuiltIn {
		if string(b.Group) == group {
			return true
		}
	}
	return false
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
