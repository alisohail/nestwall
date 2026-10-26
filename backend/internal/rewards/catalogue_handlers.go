package rewards

import (
	"context"
	"errors"
	"net/http"

	"nestwall/backend/internal/platform/ids"
	"nestwall/backend/internal/platform/web"
)

// ListRewards handles GET /api/v1/rewards. Parents and caregivers see every reward that is not
// archived. A child sees only active rewards that are open to them.
func (h *Handlers) ListRewards(w http.ResponseWriter, r *http.Request) {
	familyID, _, memberID, ok := h.actor(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	isChild, err := h.isChild(ctx, memberID)
	if err != nil {
		h.internalError(w, "load member", err)
		return
	}
	rewardIDs, err := h.activeIDs(ctx, familyID)
	if err != nil {
		h.internalError(w, "list rewards", err)
		return
	}

	out := []Reward{}
	for _, id := range rewardIDs {
		rw, err := viewReward(ctx, h.db, h.clock, familyID, id)
		if err != nil {
			h.internalError(w, "load reward", err)
			return
		}
		if visibleTo(rw, memberID, isChild) {
			out = append(out, rw)
		}
	}
	web.RespondJSON(w, http.StatusOK, map[string]any{"rewards": out})
}

// GetReward handles GET /api/v1/rewards/{id}. A reward a child may not see returns 404.
func (h *Handlers) GetReward(w http.ResponseWriter, r *http.Request) {
	familyID, _, memberID, ok := h.actor(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	isChild, err := h.isChild(ctx, memberID)
	if err != nil {
		h.internalError(w, "load member", err)
		return
	}
	rw, err := viewReward(ctx, h.db, h.clock, familyID, r.PathValue("id"))
	if errors.Is(err, ErrRewardNotFound) || (err == nil && !visibleTo(rw, memberID, isChild)) {
		web.RespondError(w, http.StatusNotFound, "reward_not_found", "Reward not found", nil)
		return
	}
	if err != nil {
		h.internalError(w, "get reward", err)
		return
	}
	setETag(w, rw.Version)
	web.RespondJSON(w, http.StatusOK, rw)
}

// CreateReward handles POST /api/v1/rewards. It requires rewards.manage.
func (h *Handlers) CreateReward(w http.ResponseWriter, r *http.Request) {
	familyID, userID, _, ok := h.actor(w, r)
	if !ok {
		return
	}
	f := newRewardFields()
	if !decodeReward(w, r, &f) {
		return
	}
	if fe := f.normalise(); fe != nil {
		invalid(w, fe.field, fe.reason)
		return
	}

	ctx := r.Context()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "begin reward", err)
		return
	}
	defer tx.Rollback()

	if fe, err := checkMembers(ctx, tx, familyID, *f.EligibleMemberIDs); err != nil {
		h.internalError(w, "check eligibility", err)
		return
	} else if fe != nil {
		invalid(w, fe.field, fe.reason)
		return
	}

	id := ids.New()
	if err := insertReward(ctx, tx, familyID, id, f); err != nil {
		h.internalError(w, "insert reward", err)
		return
	}
	if err := replaceEligibility(ctx, tx, familyID, id, *f.EligibleMemberIDs); err != nil {
		h.internalError(w, "write eligibility", err)
		return
	}
	if err := recordAudit(ctx, tx, familyID, userID, "reward.created", id, *f.Name); err != nil {
		h.internalError(w, "audit reward", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit reward", err)
		return
	}

	rw, err := viewReward(ctx, h.db, h.clock, familyID, id)
	if err != nil {
		h.internalError(w, "load reward", err)
		return
	}
	setETag(w, rw.Version)
	web.RespondJSON(w, http.StatusCreated, rw)
}

// UpdateReward handles PATCH /api/v1/rewards/{id}. Absent fields keep their value and null
// clears an optional one. An If-Match header guards against a stale write. It requires
// rewards.manage.
func (h *Handlers) UpdateReward(w http.ResponseWriter, r *http.Request) {
	familyID, userID, _, ok := h.actor(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	id := r.PathValue("id")

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "begin reward", err)
		return
	}
	defer tx.Rollback()

	current, err := loadReward(ctx, tx, familyID, id)
	if errors.Is(err, ErrRewardNotFound) {
		web.RespondError(w, http.StatusNotFound, "reward_not_found", "Reward not found", nil)
		return
	}
	if err != nil {
		h.internalError(w, "load reward", err)
		return
	}
	if current.Archived {
		web.RespondError(w, http.StatusConflict, "reward_archived", "An archived reward cannot be changed", nil)
		return
	}
	if !checkIfMatch(w, r, current.Version) {
		return
	}

	f := fieldsOf(current)
	if !decodeReward(w, r, &f) {
		return
	}
	if fe := f.normalise(); fe != nil {
		invalid(w, fe.field, fe.reason)
		return
	}
	if fe, err := checkMembers(ctx, tx, familyID, *f.EligibleMemberIDs); err != nil {
		h.internalError(w, "check eligibility", err)
		return
	} else if fe != nil {
		invalid(w, fe.field, fe.reason)
		return
	}

	err = updateReward(ctx, tx, familyID, id, current.Version, f)
	if errors.Is(err, errStaleVersion) {
		web.RespondError(w, http.StatusPreconditionFailed, "version_conflict",
			"This changed since you loaded it. Reload and try again.", map[string]any{"version": current.Version})
		return
	}
	if err != nil {
		h.internalError(w, "update reward", err)
		return
	}
	if err := replaceEligibility(ctx, tx, familyID, id, *f.EligibleMemberIDs); err != nil {
		h.internalError(w, "write eligibility", err)
		return
	}
	if err := recordAudit(ctx, tx, familyID, userID, "reward.updated", id, *f.Name); err != nil {
		h.internalError(w, "audit reward", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit reward", err)
		return
	}

	rw, err := viewReward(ctx, h.db, h.clock, familyID, id)
	if err != nil {
		h.internalError(w, "load reward", err)
		return
	}
	setETag(w, rw.Version)
	web.RespondJSON(w, http.StatusOK, rw)
}

// ArchiveReward handles DELETE /api/v1/rewards/{id}. The reward is archived rather than deleted,
// because redemptions refer to it. An If-Match header guards against a stale write. It requires
// rewards.manage.
func (h *Handlers) ArchiveReward(w http.ResponseWriter, r *http.Request) {
	familyID, userID, _, ok := h.actor(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	id := r.PathValue("id")

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.internalError(w, "begin reward", err)
		return
	}
	defer tx.Rollback()

	current, err := loadReward(ctx, tx, familyID, id)
	if errors.Is(err, ErrRewardNotFound) {
		web.RespondError(w, http.StatusNotFound, "reward_not_found", "Reward not found", nil)
		return
	}
	if err != nil {
		h.internalError(w, "load reward", err)
		return
	}
	if current.Archived {
		web.RespondError(w, http.StatusConflict, "reward_archived", "This reward is already archived", nil)
		return
	}
	if !checkIfMatch(w, r, current.Version) {
		return
	}

	err = archiveReward(ctx, tx, familyID, id, current.Version)
	if errors.Is(err, errStaleVersion) {
		web.RespondError(w, http.StatusPreconditionFailed, "version_conflict",
			"This changed since you loaded it. Reload and try again.", map[string]any{"version": current.Version})
		return
	}
	if err != nil {
		h.internalError(w, "archive reward", err)
		return
	}
	if err := recordAudit(ctx, tx, familyID, userID, "reward.archived", id, current.Name); err != nil {
		h.internalError(w, "audit reward", err)
		return
	}
	if err := tx.Commit(); err != nil {
		h.internalError(w, "commit reward", err)
		return
	}

	rw, err := viewReward(ctx, h.db, h.clock, familyID, id)
	if err != nil {
		h.internalError(w, "load reward", err)
		return
	}
	setETag(w, rw.Version)
	web.RespondJSON(w, http.StatusOK, rw)
}

// isChild reports whether the member is a child account.
func (h *Handlers) isChild(ctx context.Context, memberID string) (bool, error) {
	return memberIsChild(ctx, h.db, memberID)
}

// activeIDs lists the IDs of the family's rewards that are not archived, by name.
func (h *Handlers) activeIDs(ctx context.Context, familyID string) ([]string, error) {
	rows, err := h.db.QueryContext(ctx,
		`SELECT id FROM rewards WHERE family_id = ? AND archived_at IS NULL ORDER BY name COLLATE NOCASE, id`,
		familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// visibleTo reports whether a member may see a reward. Adults (non-children) see all of them.
// A child sees only active, non-archived rewards that are open to them.
func visibleTo(rw Reward, memberID string, isChild bool) bool {
	if !isChild {
		return true
	}
	if rw.Archived || rw.Status != "active" {
		return false
	}
	if len(rw.EligibleMemberIDs) == 0 {
		return true
	}
	for _, id := range rw.EligibleMemberIDs {
		if id == memberID {
			return true
		}
	}
	return false
}
