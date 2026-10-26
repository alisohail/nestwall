// Package catalogue holds the 24 built-in badges (PBI M4-09). Each badge has a stable key, a group, a
// target and, for the skill badges, the category badge key it counts. The UI translates the name and
// emoji from the key. SeedDefaults gives a new family one definition row per badge.
package catalogue

import (
	"context"
	"database/sql"
	"fmt"

	"nestwall/backend/internal/platform/ids"
)

// Group is a section of the badge list. A group can be switched off as a whole.
type Group string

const (
	GroupStreaks  Group = "streaks"
	GroupPoints   Group = "points"
	GroupTasks    Group = "tasks"
	GroupSkills   Group = "skills"
	GroupTiming   Group = "timing"
	GroupKindness Group = "kindness"
)

// Badge is one built-in badge.
type Badge struct {
	Key   string
	Group Group
	// Target is the value the child must reach. For streaks it is consecutive complete days; for
	// points it is points earned; for counts it is the number of copies or awards.
	Target int
	// Category is the task category badge key a skill badge counts (for example "tidy_pro"). Empty
	// for every other badge.
	Category string
}

// BuiltIn lists the badges in display order.
var BuiltIn = []Badge{
	{Key: "s3", Group: GroupStreaks, Target: 3},
	{Key: "s7", Group: GroupStreaks, Target: 7},
	{Key: "s14", Group: GroupStreaks, Target: 14},
	{Key: "s30", Group: GroupStreaks, Target: 30},
	{Key: "s100", Group: GroupStreaks, Target: 100},
	{Key: "p100", Group: GroupPoints, Target: 100},
	{Key: "p250", Group: GroupPoints, Target: 250},
	{Key: "p500", Group: GroupPoints, Target: 500},
	{Key: "p1000", Group: GroupPoints, Target: 1000},
	{Key: "p_reward", Group: GroupPoints, Target: 1},
	{Key: "t1", Group: GroupTasks, Target: 1},
	{Key: "t10", Group: GroupTasks, Target: 10},
	{Key: "t50", Group: GroupTasks, Target: 50},
	{Key: "t100", Group: GroupTasks, Target: 100},
	{Key: "k_tidy", Group: GroupSkills, Target: 20, Category: "tidy_pro"},
	{Key: "k_hw", Group: GroupSkills, Target: 20, Category: "homework_hero"},
	{Key: "k_pet", Group: GroupSkills, Target: 20, Category: "pet_pal"},
	{Key: "k_green", Group: GroupSkills, Target: 20, Category: "green_thumb"},
	{Key: "e_early", Group: GroupTiming, Target: 10},
	{Key: "e_week", Group: GroupTiming, Target: 7},
	{Key: "e_month", Group: GroupTiming, Target: 30},
	{Key: "e_back", Group: GroupTiming, Target: 5},
	{Key: "k_share", Group: GroupKindness, Target: 1},
	{Key: "k_help", Group: GroupKindness, Target: 3},
}

// ByKey returns the built-in badge with the key.
func ByKey(key string) (Badge, bool) {
	for _, b := range BuiltIn {
		if b.Key == key {
			return b, true
		}
	}
	return Badge{}, false
}

// SeedDefaults inserts one enabled definition for each built-in badge into a family, and the family's
// badge settings row (badges on by default). It runs in the setup or demo transaction, next to the
// default categories, after the family row exists.
func SeedDefaults(ctx context.Context, tx *sql.Tx, familyID string) error {
	for _, b := range BuiltIn {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO badge_definitions (id, family_id, badge_key, group_key) VALUES (?, ?, ?, ?)`,
			ids.New(), familyID, b.Key, string(b.Group),
		); err != nil {
			return fmt.Errorf("seed badge %s: %w", b.Key, err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO badge_settings (family_id) VALUES (?)`, familyID,
	); err != nil {
		return fmt.Errorf("seed badge settings: %w", err)
	}
	return nil
}
