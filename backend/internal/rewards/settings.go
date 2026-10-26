package rewards

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"nestwall/backend/internal/platform/web"
)

// settingRewardApproval is the app_settings key for the family default that new rewards start from.
const settingRewardApproval = "rewards.default_approval_required"

// rewardSettings is the family's default for new rewards.
type rewardSettings struct {
	ApprovalRequiredDefault bool `json:"approval_required_default"`
}

// rewardSettingsRequest is the body of PUT /api/v1/rewards/settings. The field is required.
type rewardSettingsRequest struct {
	ApprovalRequiredDefault *bool `json:"approval_required_default"`
}

// loadRewardSettings returns the family's reward defaults. A missing setting means no approval.
func loadRewardSettings(ctx context.Context, q Querier, familyID string) (rewardSettings, error) {
	var value string
	err := q.QueryRowContext(ctx,
		`SELECT setting_value FROM app_settings WHERE family_id = ? AND setting_key = ?`,
		familyID, settingRewardApproval,
	).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return rewardSettings{}, nil
	}
	if err != nil {
		return rewardSettings{}, err
	}
	return rewardSettings{ApprovalRequiredDefault: value == "true"}, nil
}

// saveRewardSettings stores the family's reward defaults.
func saveRewardSettings(ctx context.Context, db *sql.DB, familyID string, s rewardSettings) error {
	value := "false"
	if s.ApprovalRequiredDefault {
		value = "true"
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO app_settings (family_id, setting_key, setting_value) VALUES (?, ?, ?)
		 ON CONFLICT (family_id, setting_key) DO UPDATE SET
		     setting_value = excluded.setting_value,
		     updated_at = strftime('%Y-%m-%d %H:%M:%f+00:00', 'now')`,
		familyID, settingRewardApproval, value)
	return err
}

// GetRewardSettings handles GET /api/v1/rewards/settings.
func (h *Handlers) GetRewardSettings(w http.ResponseWriter, r *http.Request) {
	familyID, _, _, ok := h.actor(w, r)
	if !ok {
		return
	}
	s, err := loadRewardSettings(r.Context(), h.db, familyID)
	if err != nil {
		h.internalError(w, "load reward settings", err)
		return
	}
	web.RespondJSON(w, http.StatusOK, s)
}

// PutRewardSettings handles PUT /api/v1/rewards/settings. It replaces the family's reward defaults.
func (h *Handlers) PutRewardSettings(w http.ResponseWriter, r *http.Request) {
	familyID, _, _, ok := h.actor(w, r)
	if !ok {
		return
	}
	var req rewardSettingsRequest
	if !decodeReward(w, r, &req) {
		return
	}
	if req.ApprovalRequiredDefault == nil {
		invalid(w, "approval_required_default", "required")
		return
	}
	s := rewardSettings{ApprovalRequiredDefault: *req.ApprovalRequiredDefault}
	if err := saveRewardSettings(r.Context(), h.db, familyID, s); err != nil {
		h.internalError(w, "save reward settings", err)
		return
	}
	web.RespondJSON(w, http.StatusOK, s)
}
