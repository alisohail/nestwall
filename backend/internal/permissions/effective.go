package permissions

import (
	"context"
	"database/sql"
	"net/http"

	"nestwall/backend/internal/platform/web"
)

// EffectiveCodes returns every permission code the family member may use, in code order.
// It uses Policy.Check for each code, so member overrides, role grants and access levels
// are evaluated exactly as they are for the routes.
func EffectiveCodes(ctx context.Context, db *sql.DB, familyMemberID string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT code FROM permissions ORDER BY code`)
	if err != nil {
		return nil, err
	}
	var codes []string
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			rows.Close()
			return nil, err
		}
		codes = append(codes, code)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	policy := NewPolicy(db)
	granted := []string{}
	for _, code := range codes {
		res, err := policy.Check(ctx, familyMemberID, code)
		if err != nil {
			return nil, err
		}
		if res.Allowed {
			granted = append(granted, code)
		}
	}
	return granted, nil
}

// Effective serves GET /api/v1/auth/permissions. It must be wrapped with Authenticated.
// It returns only the caller's own grants. The frontend uses them to disable actions;
// every route still enforces its permission on the server.
func Effective(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		familyMemberID, ok := GetFamilyMemberID(r.Context())
		if !ok {
			web.RespondError(w, http.StatusUnauthorized, "unauthenticated", "Authentication required", nil)
			return
		}
		codes, err := EffectiveCodes(r.Context(), db, familyMemberID)
		if err != nil {
			web.RespondError(w, http.StatusInternalServerError, "internal_error", "Permission lookup failed", nil)
			return
		}
		web.RespondJSON(w, http.StatusOK, map[string][]string{"permissions": codes})
	}
}
