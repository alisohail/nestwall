export type ShareNoteKey = 'thank_you' | 'happy_birthday' | 'good_job' | 'just_because';

export type ApprovalMode = 'never' | 'above' | 'always';

/** Family sharing rules. Each save bumps the version. */
export interface ShareSettings {
  enabled: boolean;
  /** Fewest points one share may send. Parents set it with max_per_share. */
  min_per_share: number;
  max_per_share: number;
  daily_limit: number;
  min_balance: number;
  approval_mode: ApprovalMode;
  approval_above: number;
  notify_recipient: boolean;
  notify_parents: boolean;
  version: number;
}

/** A sibling the caller may share with. Children cannot list family members, so the list comes with the summary. */
export interface ShareRecipient {
  member_id: string;
  name: string;
  colour: string;
  avatar_id: string | null;
}

/** What the signed-in child sees on the share screen: the rules plus their own numbers and siblings. */
export interface ShareSummary extends ShareSettings {
  balance: number;
  shared_today: number;
  daily_left: number;
  notes: ShareNoteKey[];
  recipients: ShareRecipient[];
}

/** Body for a settings save. Version is sent in If-Match, not in the body. */
export type ShareSettingsInput = Omit<ShareSettings, 'version'>;

export interface ShareRequest {
  to_member_id: string;
  amount: number;
  /** One of the preset notes. Send this or note_text, not both. */
  note_key?: ShareNoteKey;
  /** The child's own note, up to 200 characters. Send this or note_key, not both. */
  note_text?: string;
}

export interface ShareResult {
  id: string;
  from_member_id: string;
  to_member_id: string;
  amount: number;
  note_key: ShareNoteKey | null;
  note_text: string | null;
  status: 'pending' | 'completed' | 'declined';
  approval_required: boolean;
  requested_at: string;
  decided_at: string | null;
  decided_by_member_id: string | null;
  version: number;
  balance: number;
  replayed: boolean;
}
