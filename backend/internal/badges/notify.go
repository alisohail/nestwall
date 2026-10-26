package badges

import (
	"context"
	"fmt"
	"time"

	"nestwall/backend/internal/notifypolicy"
)

// Kinds of the in-app notifications a child's badge events write (PBI M4-09, batch 09c).
const (
	kindBadgeEarned  = "badge_earned"
	kindBadgeAwarded = "badge_awarded"
)

// notifyChild writes an in-app notification to the child when the family's child notices are on. It
// runs in the caller's transaction, so the notice is written with the award it reports and a replay
// that awards nothing writes nothing. The body is English, as the other in-app notifications are.
func notifyChild(ctx context.Context, q querier, familyID, memberID, kind, body string, now time.Time) error {
	var on bool
	if err := q.QueryRowContext(ctx,
		`SELECT notify_child FROM badge_settings WHERE family_id = ?`, familyID,
	).Scan(&on); err != nil {
		return fmt.Errorf("load child notice setting: %w", err)
	}
	if !on {
		return nil
	}
	if _, err := notifypolicy.Notify(ctx, q, notifypolicy.Notice{
		FamilyID: familyID, RecipientID: memberID, Kind: kind, SubjectID: memberID, Body: body,
	}, now); err != nil {
		return fmt.Errorf("insert child notice: %w", err)
	}
	return nil
}

// memberDisplayName returns the display name of a family member.
func memberDisplayName(ctx context.Context, q querier, memberID string) (string, error) {
	var name string
	err := q.QueryRowContext(ctx,
		`SELECT u.display_name FROM family_members m JOIN users u ON u.id = m.user_id WHERE m.id = ?`, memberID,
	).Scan(&name)
	if err != nil {
		return "", fmt.Errorf("load display name: %w", err)
	}
	return name, nil
}
