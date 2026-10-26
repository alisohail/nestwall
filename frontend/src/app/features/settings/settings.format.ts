import { ApiError } from '../../core/api/api-client.service';
import {
  BonusDefaults,
  FamilyDefaults,
  MissedAfter,
  MissedAfterUnit,
  PartOfDayTimes,
  PenaltyUnit,
  TaskCategory,
  TaskCategoryRequest,
  TaskSettingsRequest,
  TaskSettingsResponse,
  TaskTemplate,
  TaskTemplateRequest,
} from '../tasks/tasks.models';

export const MINUTES_PER_HOUR = 60;
export const MINUTES_PER_DAY = 1440;

/** The fields a template can prefill. The name is required and is not counted. */
export const TEMPLATE_PREFILL_FIELD_COUNT = 10;

export const CATEGORY_EMOJIS: readonly string[] = ['🧹', '🎒', '🐶', '🌳', '💅', '🎨', '👪', '⭐', '🍳', '📚', '🛒', '🏠'];

/** Longest category picture, in code points. Matches the API limit (`maxCategoryEmoji`). */
export const CATEGORY_EMOJI_MAX = 16;

// One emoji: a flag pair, or a pictograph with an optional skin tone or variation selector,
// joined by zero-width joiners (for example 🧑🏽‍🍳 or 👨‍👩‍👧).
const SINGLE_EMOJI =
  /^(?:\p{Regional_Indicator}{2}|\p{Extended_Pictographic}(?:\uFE0F|\p{Emoji_Modifier})?(?:\u200D\p{Extended_Pictographic}(?:\uFE0F|\p{Emoji_Modifier})?)*)$/u;

/** True when the text is exactly one emoji. */
export function isSingleEmoji(text: string): boolean {
  return SINGLE_EMOJI.test(text);
}

export const DEFAULT_PART_OF_DAY: PartOfDayTimes = {
  morning_start: '05:00',
  after_school_start: '12:00',
  evening_start: '17:00',
};

export const REMINDER_OPTIONS: readonly number[] = [15, 30, 60, 120];

export type ExpiryUnit = 'days' | 'hours';

export interface ExpiryParts {
  value: number | null;
  unit: ExpiryUnit;
}

/** Splits a stored expiry into the value and unit shown in the form. Null means no default. */
export function expiryParts(minutes: number | null): ExpiryParts {
  if (minutes === null) return { value: null, unit: 'days' };
  if (minutes % MINUTES_PER_DAY === 0) return { value: minutes / MINUTES_PER_DAY, unit: 'days' };
  return { value: minutes / MINUTES_PER_HOUR, unit: 'hours' };
}

/** Converts a form value back to minutes. An empty value means no default. */
export function expiryMinutes(value: number | null, unit: ExpiryUnit): number | null {
  if (value === null || Number.isNaN(value)) return null;
  return Math.round(value * (unit === 'days' ? MINUTES_PER_DAY : MINUTES_PER_HOUR));
}

/** A days value must be a whole number. Hours may be fractional. Both must be above zero. */
export function isExpiryValid(value: number | null, unit: ExpiryUnit): boolean {
  if (value === null) return true;
  if (Number.isNaN(value) || value <= 0) return false;
  return unit === 'hours' || Number.isInteger(value);
}

/** Whole number inside an inclusive range. An empty value counts as valid, because it means no default. */
export function isOptionalWhole(value: number | null, min: number, max: number): boolean {
  if (value === null) return true;
  return Number.isInteger(value) && value >= min && value <= max;
}

/** True when the HH:MM start times are strictly increasing. Empty values fail. */
export function timesInOrder(times: PartOfDayTimes): boolean {
  const { morning_start: morning, after_school_start: afterSchool, evening_start: evening } = times;
  if (!morning || !afterSchool || !evening) return false;
  return morning < afterSchool && afterSchool < evening;
}

export function blankToNull(text: string | null | undefined): string | null {
  const trimmed = (text ?? '').trim();
  return trimmed === '' ? null : trimmed;
}

/** The template editor values, as typed in the form. */
export interface TemplateFormValue {
  name: string;
  category_id: string | null;
  title: string;
  description: string;
  points: number | null;
  due_time: string;
  penalty_value: number | null;
  penalty_unit: PenaltyUnit;
  reminder_minutes_before: number | null;
  expiry_value: number | null;
  expiry_unit: ExpiryUnit;
  late_points_percent: number | null;
  default_assignee_ids: string[];
}

export function emptyTemplateForm(): TemplateFormValue {
  return {
    name: '',
    category_id: null,
    title: '',
    description: '',
    points: null,
    due_time: '',
    penalty_value: null,
    penalty_unit: 'percent',
    reminder_minutes_before: null,
    expiry_value: null,
    expiry_unit: 'days',
    late_points_percent: null,
    default_assignee_ids: [],
  };
}

export function templateToForm(template: TaskTemplate): TemplateFormValue {
  const expiry = expiryParts(template.expires_after_minutes);
  return {
    name: template.name,
    category_id: template.category_id,
    title: template.title ?? '',
    description: template.description ?? '',
    points: template.points,
    due_time: template.due_time ?? '',
    penalty_value: template.penalty_value,
    penalty_unit: template.penalty_unit ?? 'percent',
    reminder_minutes_before: template.reminder_minutes_before,
    expiry_value: expiry.value,
    expiry_unit: expiry.unit,
    late_points_percent: template.late_points_percent,
    default_assignee_ids: [...template.default_assignee_ids],
  };
}

/**
 * Body for POST and PATCH. It carries only the fields the editor owns. Fields the editor does not show
 * (part of day, priority, approval, repeat) are left out, so PATCH keeps them.
 */
export function templateBody(value: TemplateFormValue): TaskTemplateRequest {
  return {
    name: value.name.trim(),
    category_id: value.category_id,
    title: blankToNull(value.title),
    description: blankToNull(value.description),
    points: value.points,
    due_time: value.due_time || null,
    penalty_value: value.penalty_value,
    penalty_unit: value.penalty_value === null ? null : value.penalty_unit,
    reminder_minutes_before: value.reminder_minutes_before,
    expires_after_minutes: expiryMinutes(value.expiry_value, value.expiry_unit),
    late_points_percent: value.late_points_percent,
    default_assignee_ids: [...value.default_assignee_ids],
  };
}

/** How many of the prefillable fields a template body sets. Only set fields prefill the new-task form. */
export function prefillCount(body: TaskTemplateRequest): number {
  let count = 0;
  if (body.category_id) count++;
  if (body.title) count++;
  if (body.description) count++;
  if (body.points !== null && body.points !== undefined) count++;
  if (body.due_time) count++;
  if (body.penalty_value !== null && body.penalty_value !== undefined) count++;
  if (body.reminder_minutes_before !== null && body.reminder_minutes_before !== undefined) count++;
  if (body.expires_after_minutes !== null && body.expires_after_minutes !== undefined) count++;
  if (body.late_points_percent !== null && body.late_points_percent !== undefined) count++;
  if (body.default_assignee_ids && body.default_assignee_ids.length > 0) count++;
  return count;
}

export interface CategoryFormValue {
  name: string;
  emoji: string;
  colour: string;
  points: number | null;
  approval_required: boolean | null;
  expiry_value: number | null;
  expiry_unit: ExpiryUnit;
  badge_key: string | null;
}

export function categoryToForm(category: TaskCategory): CategoryFormValue {
  const expiry = expiryParts(category.expires_after_minutes);
  return {
    name: category.name,
    emoji: category.emoji,
    colour: category.colour,
    points: category.points,
    approval_required: category.approval_required,
    expiry_value: expiry.value,
    expiry_unit: expiry.unit,
    badge_key: category.badge_key,
  };
}

/**
 * Body for POST and PUT. PUT replaces the whole category, so the fields the dialog does not show
 * (sort order, penalty) are copied from the stored category.
 */
export function categoryBody(value: CategoryFormValue, existing: TaskCategory | null): TaskCategoryRequest {
  return {
    name: value.name.trim(),
    emoji: value.emoji.trim(),
    colour: value.colour,
    sort_order: existing?.sort_order,
    badge_key: value.badge_key,
    points: value.points,
    approval_required: value.approval_required,
    expires_after_minutes: expiryMinutes(value.expiry_value, value.expiry_unit),
    penalty_value: existing?.penalty_value ?? null,
    penalty_unit: existing?.penalty_unit ?? null,
  };
}

/**
 * Settings body. The PUT replaces the whole object, so the current defaults (including the penalty
 * fields that are not on screen) are sent back with the changed fields.
 */
export function settingsBody(
  current: TaskSettingsResponse,
  changes: { defaults?: Partial<FamilyDefaults>; partOfDay?: PartOfDayTimes; bonusDefaults?: BonusDefaults },
): TaskSettingsRequest {
  return {
    defaults: { ...current.defaults, ...(changes.defaults ?? {}) },
    part_of_day: changes.partOfDay ?? current.part_of_day,
    bonus_defaults: changes.bonusDefaults ?? current.bonus_defaults,
  };
}

export function missedAfterOf(value: number | null, unit: MissedAfterUnit): MissedAfter | null {
  if (value === null) return null;
  return { value, unit };
}

export type ApprovalChoice = 'unset' | 'required' | 'not_required';

export function approvalChoice(value: boolean | null): ApprovalChoice {
  if (value === null) return 'unset';
  return value ? 'required' : 'not_required';
}

export function approvalValue(choice: ApprovalChoice): boolean | null {
  if (choice === 'unset') return null;
  return choice === 'required';
}

/** Maps a settings or template or category error to a settings.error.* key. */
export function settingsErrorKey(err: ApiError): string {
  switch (err.code) {
    case 'name_taken':
      return 'settings.error.name_taken';
    case 'category_in_use':
      return 'settings.error.category_in_use';
    case 'template_in_use':
      return 'settings.error.template_in_use';
  }
  if (err.status === 0) return 'settings.error.network';
  if (err.status === 400 && err.params?.['reason'] === 'out_of_order') return 'settings.error.out_of_order';
  if (err.status === 400) return 'settings.error.validation';
  if (err.status === 403) return 'settings.error.forbidden';
  if (err.status === 404) return 'settings.error.not_found';
  if (err.status === 412) return 'settings.error.stale';
  return 'settings.error.generic';
}
