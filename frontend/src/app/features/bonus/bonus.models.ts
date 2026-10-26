/** How kids take a bonus task. first_come: one child owns it. everyone_once: each eligible child can do it once. */
export type BonusClaimMode = 'first_come' | 'everyone_once';

/** Claim status from the API. A claim is live while claimed, submitted or approved. */
export type BonusClaimStatus = 'claimed' | 'submitted' | 'approved' | 'rejected' | 'released';

/** Task state from the API. */
export type BonusTaskState = 'scheduled' | 'open' | 'expired' | 'cancelled';

/** Tab of the parent Bonus tasks page. today is the default. */
export type BonusScope = 'today' | 'scheduled' | 'past';

export interface BonusClaim {
  id: string;
  bonus_task_id: string;
  family_member_id: string;
  status: BonusClaimStatus;
  claimed_at: string;
  released_at: string | null;
  completed_at: string | null;
  decided_at: string | null;
  decided_by_member_id: string | null;
  decision_note: string | null;
  version: number;
}

export interface BonusTask {
  id: string;
  template_id: string | null;
  title: string;
  description: string | null;
  points: number;
  claim_mode: BonusClaimMode;
  approval_required: boolean;
  /** Family-local day as YYYY-MM-DD. */
  day: string;
  show_from: string | null;
  ends_at: string;
  state: BonusTaskState;
  cancelled_at: string | null;
  /** Optimistic concurrency version of the task. */
  version: number;
  /** True while the task has no claims, is not cancelled and has not ended. */
  editable: boolean;
  eligible_member_ids: string[];
  /** Parent view only: every claim on the task. */
  claims?: BonusClaim[];
  /** Child view only. */
  my_claim?: BonusClaim | null;
  taken_by_other?: boolean;
}

export interface ListBonusTasksResponse {
  tasks: BonusTask[];
}

/** Body of PATCH /api/v1/bonus-tasks/{id}. Only the fields sent change. */
export interface BonusTaskEditRequest {
  title?: string;
  description?: string | null;
  points?: number;
  /** Family-local day as YYYY-MM-DD. Today or later. */
  day?: string;
}

/** Body of POST /api/v1/bonus-tasks. */
export interface BonusTaskRequest {
  title: string;
  description: string | null;
  points: number;
  claim_mode: BonusClaimMode;
  approval_required: boolean;
  eligible_member_ids: string[];
  /** Family-local day as YYYY-MM-DD. Omit for today. */
  day?: string;
  /** RFC3339 instant the task appears. null or omitted shows it at once. */
  show_from?: string | null;
  /** RFC3339 instant the task ends. null or omitted ends it at the end of the day. */
  ends_at?: string | null;
  /** Bonus template the task starts from. The fields in this body win over the template. */
  template_id?: string;
}
