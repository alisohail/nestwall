/** The six built-in badge groups, in display order. */
export type BadgeGroupKey = 'streaks' | 'points' | 'tasks' | 'skills' | 'timing' | 'kindness';

/** Family-wide badge switches. Each one is sent back in full on save. */
export interface BadgeSettings {
  enabled: boolean;
  show_on_kid_dashboard: boolean;
  show_on_wall: boolean;
  notify_child: boolean;
}

export interface BuiltInBadge {
  id: string;
  key: string;
  enabled: boolean;
}

export interface BadgeGroup {
  group: BadgeGroupKey;
  /** True only when every badge in the group is on. */
  enabled: boolean;
  badges: BuiltInBadge[];
}

/** A family-made badge that a parent awards by hand. Version is sent as If-Match on edits. */
export interface CustomBadge {
  id: string;
  name: string;
  emoji: string;
  description: string | null;
  enabled: boolean;
  version: number;
  awarded_count: number;
}

/** The whole Settings > Badges view. Version is the settings version used for If-Match on switch writes. */
export interface BadgeConfig {
  version: number;
  settings: BadgeSettings;
  groups: BadgeGroup[];
  custom: CustomBadge[];
}

/** Body for creating or editing a custom badge. Enabled is only sent on edit. */
export interface CustomBadgeRequest {
  name: string;
  emoji: string;
  description: string | null;
  enabled?: boolean;
}

export interface BadgeAwardRequest {
  member_id: string;
  badge_id: string;
  note: string;
}

export interface BadgeAward {
  id: string;
  member_id: string;
  badge_id: string;
  note: string;
  awarded_at: string;
  replayed: boolean;
}

/** One built-in badge as the signed-in child sees it. Progress equals the target once earned. */
export interface MyBadge {
  key: string;
  earned: boolean;
  earned_at: string | null;
  progress: number;
  target: number;
}

/** One built-in group as the child sees it, with only the badges that are shown. */
export interface MyBadgeGroup {
  group: BadgeGroupKey;
  earned: number;
  total: number;
  badges: MyBadge[];
}

/** A custom badge a parent gave the child by hand. */
export interface MyAward {
  id: string;
  name: string;
  emoji: string;
  note: string;
  awarded_by: string;
  awarded_at: string;
}

/** The signed-in child's badges. Only the child's own records are included. */
export interface MyBadges {
  enabled: boolean;
  show_on_kid_dashboard: boolean;
  earned_count: number;
  total_count: number;
  groups: MyBadgeGroup[];
  from_parents: MyAward[];
}

/** Pictures offered for custom badges, from the Settings > Badges mock. */
export const BADGE_PICTURES: readonly string[] = ['⭐', '🎵', '🎨', '🏅', '🤷', '💡', '🖤'];

/** Number of built-in badges NestWall ships. Custom badges are not counted. */
export const BUILT_IN_BADGE_COUNT = 24;

export const BADGE_NAME_MAX = 60;
export const BADGE_DESCRIPTION_MAX = 200;
export const BADGE_NOTE_MAX = 200;

/** Picture for each built-in badge key, from the badge catalogue in the mock. */
export const BUILT_IN_EMOJI: Readonly<Record<string, string>> = {
  s3: '🔥',
  s7: '🔥',
  s14: '🔥',
  s30: '🔥',
  s100: '🔥',
  p100: '🌟',
  p250: '💫',
  p500: '🏆',
  p1000: '👑',
  p_reward: '🎁',
  t1: '🎯',
  t10: '✅',
  t50: '💪',
  t100: '🚀',
  k_tidy: '🧹',
  k_hw: '📚',
  k_pet: '🐕',
  k_green: '🪴',
  e_early: '🐦',
  e_week: '🌈',
  e_month: '🎖️',
  e_back: '🔁',
  k_share: '💛',
  k_help: '🤝',
};
