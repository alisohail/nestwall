package notifypolicy

import (
	"context"
	"fmt"
	"time"
)

// ParentMemberIDs returns the family member IDs of the parent accounts in a family.
func ParentMemberIDs(ctx context.Context, q Querier, familyID string) ([]string, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT m.id FROM family_members m JOIN users u ON u.id = m.user_id
		  WHERE m.family_id = ? AND u.is_child = 0 ORDER BY m.id`, familyID)
	if err != nil {
		return nil, fmt.Errorf("load parents: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan parent: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// NotifyParents writes the same notice for every parent in the family, except the member named in
// Notice.SubjectID so nobody is told about their own action. Notice.RecipientID is ignored.
func NotifyParents(ctx context.Context, q Querier, n Notice, now time.Time) error {
	parents, err := ParentMemberIDs(ctx, q, n.FamilyID)
	if err != nil {
		return err
	}
	for _, parent := range parents {
		if parent == n.SubjectID {
			continue
		}
		n.RecipientID = parent
		if _, err := Notify(ctx, q, n, now); err != nil {
			return err
		}
	}
	return nil
}
