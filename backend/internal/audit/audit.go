package audit

import (
	"context"
	"database/sql"
	"encoding/json"

	"nestwall/backend/internal/platform/ids"
)

// Record adds an entry to the audit log.
func Record(ctx context.Context, tx *sql.Tx, familyID, actorID, action, targetType string, targetID *string, metadata map[string]any) error {
	var metaJSON *string
	if metadata != nil {
		b, err := json.Marshal(metadata)
		if err != nil {
			return err
		}
		s := string(b)
		metaJSON = &s
	}

	_, err := tx.ExecContext(ctx,
		`INSERT INTO audit_logs (id, family_id, actor_id, action, target_type, target_id, metadata) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ids.New(), familyID, actorID, action, targetType, targetID, metaJSON,
	)
	return err
}
