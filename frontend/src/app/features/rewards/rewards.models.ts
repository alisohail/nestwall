export type RewardStatus = 'active' | 'inactive';
export type LimitPeriod = 'day' | 'week' | 'total';

/** A reward from the catalogue, as the signed-in user may see it. Matches the Reward schema. */
export interface Reward {
  id: string;
  name: string;
  description: string | null;
  icon: string;
  cost: number;
  status: RewardStatus;
  limit: { period: LimitPeriod; count: number } | null;
  approval_required: boolean;
  available_from: string | null;
  available_until: string | null;
  available_days: string[] | null;
  eligible_member_ids: string[];
  in_window: boolean;
  archived: boolean;
  version: number;
}

export interface RewardList {
  rewards: Reward[];
}

/** Family defaults for new rewards. Matches GET and PUT /api/v1/rewards/settings. */
export interface RewardSettings {
  approval_required_default: boolean;
}

/**
 * Body of POST /api/v1/rewards and PATCH /api/v1/rewards/{id}. Send every field on update: null clears
 * a limit or a date, and an empty eligible list means every child.
 */
export interface RewardInput {
  name: string;
  description: string | null;
  icon: string;
  cost: number;
  status: RewardStatus;
  limit_period: LimitPeriod | null;
  limit_count: number | null;
  approval_required: boolean;
  available_from: string | null;
  available_until: string | null;
  available_days: string[] | null;
  eligible_member_ids: string[];
}

export type RedemptionStatus = 'pending' | 'approved' | 'rejected' | 'cancelled';

/** One redemption request. Matches the RewardRedemption schema. */
export interface RewardRedemption {
  id: string;
  reward_id: string;
  member_id: string;
  status: RedemptionStatus;
  cost: number;
  requested_at: string;
  decided_at: string | null;
  decided_by_member_id: string | null;
  decision_reason: string | null;
  version: number;
}

/** A redemption with the reward and requester names, for lists. */
export interface RedemptionListItem extends RewardRedemption {
  reward_name: string;
  reward_icon: string;
  member_name: string;
}

export interface RedemptionPage {
  redemptions: RedemptionListItem[];
  next_cursor: string | null;
}

/** Filters for GET /api/v1/redemptions. status takes a comma-separated list. */
export interface RedemptionQuery {
  status?: string;
  member_id?: string;
  cursor?: string;
  limit?: number;
}

/** Response of POST /api/v1/rewards/{id}/redeem and of cancel. */
export interface RedeemResult extends RewardRedemption {
  balance: number;
  replayed: boolean;
}

/** Response of GET /api/v1/me/points. */
export interface MyPointsResponse {
  member_id: string;
  balance: number;
  reserved: number;
  timezone: string;
  week_start: string;
  today: number;
  week: number[];
  tasks_done: number[];
  tasks_missed: number[];
  bonus: number[];
}

/** One child's row in GET /api/v1/points/balances. Balance is spendable, after points held by pending requests. */
export interface MemberBalance {
  member_id: string;
  balance: number;
  reserved: number;
}

export interface MemberBalancesResponse {
  members: MemberBalance[];
}

export type PointsEntryType =
  | 'task_award'
  | 'bonus_task_award'
  | 'bonus'
  | 'penalty'
  | 'redemption'
  | 'redemption_reversal'
  | 'reversal'
  | 'adjustment'
  | 'transfer_out'
  | 'transfer_in';

export interface PointsEntry {
  id: string;
  member_id: string;
  type: PointsEntryType;
  amount: number;
  reason: string | null;
  reward_name: string | null;
  created_at: string;
}

export interface PointsEntryPage {
  entries: PointsEntry[];
  next_cursor: string | null;
}

/** Filters for GET /api/v1/points/entries. Unset fields are left out of the query. */
export interface PointsEntryQuery {
  member_id?: string;
  type?: PointsEntryType;
  cursor?: string;
  limit?: number;
}

export type ManualPointsType = 'bonus' | 'penalty' | 'adjustment';

/** Body of POST /api/v1/family/members/{id}/points. */
export interface ManualPointsRequest {
  type: ManualPointsType;
  points: number;
  reason: string;
}

/** Response of a manual change and of a reversal. */
export interface PointsChangeResult {
  entry_id: string;
  type: PointsEntryType;
  amount: number;
  balance: number;
  replayed: boolean;
}
