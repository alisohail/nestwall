// Package seed holds the built-in task categories and the insert used when a family is
// created. It imports only the ID helper, so the auth package can call it during setup
// without an import cycle.
package seed

import (
	"context"
	"database/sql"
	"fmt"

	"nestwall/backend/internal/platform/ids"
)

// Category is one built-in category. Nil defaults mean "do not prefill".
type Category struct {
	Key                 string
	Name                string
	Emoji               string
	Colour              string
	SortOrder           int
	BadgeKey            *string
	Points              *int
	ApprovalRequired    *bool
	ExpiresAfterMinutes *int
}

func ptr[T any](v T) *T { return &v }

// Categories is the default list, in display order. The values match the mock
// (settings categories mock) and migration 000005. Self-care and Pets use
// "end of day" in the mock, which is not a minutes value, so their expiry is nil.
var Categories = []Category{
	{Key: "chores", Name: "Chores", Emoji: "\U0001F9F9", Colour: "#0F766E", SortOrder: 1,
		BadgeKey: ptr("tidy_pro"), Points: ptr(5), ApprovalRequired: ptr(true), ExpiresAfterMinutes: ptr(2880)},
	{Key: "school_work", Name: "School work", Emoji: "\U0001F4DA", Colour: "#2563EB", SortOrder: 2,
		BadgeKey: ptr("homework_hero"), Points: ptr(15), ApprovalRequired: ptr(true), ExpiresAfterMinutes: ptr(1440)},
	{Key: "pets", Name: "Pets", Emoji: "\U0001F415", Colour: "#F59E0B", SortOrder: 3,
		BadgeKey: ptr("pet_pal"), Points: ptr(5)},
	{Key: "outdoor", Name: "Outdoor", Emoji: "\U0001F333", Colour: "#16A34A", SortOrder: 4,
		BadgeKey: ptr("green_thumb"), Points: ptr(10), ApprovalRequired: ptr(true), ExpiresAfterMinutes: ptr(2880)},
	{Key: "self_care", Name: "Self-care", Emoji: "\U0001FAA5", Colour: "#EC4899", SortOrder: 5,
		Points: ptr(3)},
	{Key: "activities", Name: "Activities", Emoji: "\U0001F3B9", Colour: "#6366F1", SortOrder: 6},
	{Key: "family", Name: "Family", Emoji: "\U0001F468\u200D\U0001F469\u200D\U0001F467", Colour: "#0EA5E9", SortOrder: 7},
}

// SeedDefaultCategories inserts the built-in categories for a new family inside the
// caller's transaction, so the family and its categories are created together or not at all.
func SeedDefaultCategories(ctx context.Context, tx *sql.Tx, familyID string) error {
	for _, c := range Categories {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO task_categories (
				id, family_id, seed_key, name, emoji, colour, sort_order, badge_key,
				default_points, default_approval_required, default_expires_after_minutes
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			ids.New(), familyID, c.Key, c.Name, c.Emoji, c.Colour, c.SortOrder, c.BadgeKey,
			c.Points, c.ApprovalRequired, c.ExpiresAfterMinutes,
		)
		if err != nil {
			return fmt.Errorf("seed category %s: %w", c.Key, err)
		}
	}
	return nil
}
