package tasks

import (
	"context"
	"database/sql"
	"fmt"

	"nestwall/backend/internal/notifypolicy"
	"nestwall/backend/internal/platform/clock"
)

// Notifier writes the task notices for the people an action involves. It runs inside the
// action's transaction, so a notice is written only when the change is committed.
type Notifier struct {
	clock clock.Clock
}

// NewNotifier returns a Notifier that stamps notices with clk.
func NewNotifier(clk clock.Clock) *Notifier {
	return &Notifier{clock: clk}
}

// Apply writes the notices for one task event. Unrelated events are ignored.
func (n *Notifier) Apply(ctx context.Context, tx *sql.Tx, e Event) error {
	switch e.Type {
	case EventApproved:
		title, err := occurrenceTitle(ctx, tx, e.FamilyID, e.TaskID)
		if err != nil {
			return err
		}
		return n.notify(ctx, tx, notifypolicy.Notice{
			FamilyID:    e.FamilyID,
			RecipientID: e.MemberID,
			Kind:        "task_approved",
			SubjectID:   e.MemberID,
			Body:        fmt.Sprintf("Your task \"%s\" was approved.", title),
		})
	case EventRejected:
		title, err := occurrenceTitle(ctx, tx, e.FamilyID, e.TaskID)
		if err != nil {
			return err
		}
		return n.notify(ctx, tx, notifypolicy.Notice{
			FamilyID:    e.FamilyID,
			RecipientID: e.MemberID,
			Kind:        "task_rejected",
			SubjectID:   e.MemberID,
			Body:        fmt.Sprintf("Your task \"%s\" was sent back. Check the note and try again.", title),
		})
	case EventSubmitted:
		title, err := occurrenceTitle(ctx, tx, e.FamilyID, e.TaskID)
		if err != nil {
			return err
		}
		var name string
		if err := tx.QueryRowContext(ctx,
			`SELECT u.display_name FROM family_members m JOIN users u ON u.id = m.user_id
			  WHERE m.id = ? AND m.family_id = ?`, e.MemberID, e.FamilyID,
		).Scan(&name); err != nil {
			return fmt.Errorf("load member name: %w", err)
		}
		return notifypolicy.NotifyParents(ctx, tx, notifypolicy.Notice{
			FamilyID:  e.FamilyID,
			Kind:      "task_submitted",
			SubjectID: e.MemberID,
			Body:      fmt.Sprintf("%s finished \"%s\" and it needs your approval.", name, title),
		}, n.clock.Now())
	}
	return nil
}

func (n *Notifier) notify(ctx context.Context, tx *sql.Tx, notice notifypolicy.Notice) error {
	if _, err := notifypolicy.Notify(ctx, tx, notice, n.clock.Now()); err != nil {
		return fmt.Errorf("notify task %s: %w", notice.Kind, err)
	}
	return nil
}

// occurrenceTitle returns the title of the task behind an occurrence.
func occurrenceTitle(ctx context.Context, tx *sql.Tx, familyID, occurrenceID string) (string, error) {
	var title string
	if err := tx.QueryRowContext(ctx,
		`SELECT d.title FROM task_occurrences o
		   JOIN task_definitions d ON d.family_id = o.family_id AND d.id = o.task_definition_id
		  WHERE o.id = ? AND o.family_id = ?`, occurrenceID, familyID,
	).Scan(&title); err != nil {
		return "", fmt.Errorf("load task title: %w", err)
	}
	return title, nil
}
