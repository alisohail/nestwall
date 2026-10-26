package family

import (
	"context"
	"database/sql"
	"fmt"
)

// purgeMemberRecords deletes a member's own records and clears the "who did
// this" references other records hold to them. It runs inside the removal
// transaction before the family_members row is deleted. Order matters: child
// rows go before the rows they reference. The ledger_purge_scope row lifts the
// points ledger's append-only triggers for this family until the purge ends.
func purgeMemberRecords(ctx context.Context, tx *sql.Tx, familyID, memberID string) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO ledger_purge_scope (family_id) VALUES (?)`, familyID); err != nil {
		return fmt.Errorf("open ledger purge scope: %w", err)
	}
	steps := []struct {
		query string
		args  []any
	}{
		{`UPDATE task_checklist_completions SET completed_by_member_id = NULL WHERE family_id = ? AND completed_by_member_id = ?`, []any{familyID, memberID}},
		{`UPDATE task_occurrence_assignees SET decided_by_member_id = NULL WHERE family_id = ? AND decided_by_member_id = ?`, []any{familyID, memberID}},
		{`DELETE FROM task_occurrence_assignees WHERE family_id = ? AND family_member_id = ?`, []any{familyID, memberID}},
		{`DELETE FROM task_definition_assignees WHERE family_id = ? AND family_member_id = ?`, []any{familyID, memberID}},
		{`DELETE FROM task_template_assignees WHERE family_id = ? AND family_member_id = ?`, []any{familyID, memberID}},
		{`DELETE FROM task_penalty_intents WHERE family_id = ? AND family_member_id = ?`, []any{familyID, memberID}},
		{`UPDATE bonus_claims SET decided_by_member_id = NULL WHERE family_id = ? AND decided_by_member_id = ?`, []any{familyID, memberID}},
		{`DELETE FROM bonus_claims WHERE family_id = ? AND family_member_id = ?`, []any{familyID, memberID}},
		{`DELETE FROM bonus_task_members WHERE family_id = ? AND family_member_id = ?`, []any{familyID, memberID}},
		{`DELETE FROM calendar_event_members WHERE family_id = ? AND family_member_id = ?`, []any{familyID, memberID}},
		{`UPDATE redemptions SET decided_by_member_id = NULL WHERE family_id = ? AND decided_by_member_id = ?`, []any{familyID, memberID}},
		{`DELETE FROM redemptions WHERE family_id = ? AND family_member_id = ?`, []any{familyID, memberID}},
		{`UPDATE point_shares SET decided_by_member_id = NULL WHERE family_id = ? AND decided_by_member_id = ?`, []any{familyID, memberID}},
		{`DELETE FROM point_shares WHERE family_id = ? AND (from_member_id = ? OR to_member_id = ?)`, []any{familyID, memberID, memberID}},
		{`DELETE FROM member_badges WHERE family_id = ? AND (family_member_id = ? OR awarded_by_member_id = ?)`, []any{familyID, memberID, memberID}},
		{`UPDATE points_ledger SET actor_member_id = NULL WHERE family_id = ? AND actor_member_id = ?`, []any{familyID, memberID}},
		{`DELETE FROM points_ledger WHERE family_id = ? AND family_member_id = ?`, []any{familyID, memberID}},
	}
	for _, s := range steps {
		if _, err := tx.ExecContext(ctx, s.query, s.args...); err != nil {
			return fmt.Errorf("purge member records: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM ledger_purge_scope WHERE family_id = ?`, familyID); err != nil {
		return fmt.Errorf("close ledger purge scope: %w", err)
	}
	return nil
}
