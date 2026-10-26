export type TaskStatus =
  "to_do" | "submitted" | "approved" | "rejected" | "skipped";
export type PartOfDay = "anytime" | "morning" | "after_school" | "evening";
export type Priority = "low" | "normal" | "high";
export type PenaltyUnit = "percent" | "points";
/** What removing one person's copy touches: that day only, or the person's upcoming copies and place in the series. */
export type RemoveScope = "occurrence" | "series";

/** Defaults shared by categories, templates and tasks. Null means no default is set. */
export interface TaskDefaults {
  points: number | null;
  approval_required: boolean | null;
  expires_after_minutes: number | null;
  penalty_value: number | null;
  penalty_unit: PenaltyUnit | null;
}

export interface TaskAssignee {
  member_id: string;
  status: TaskStatus;
}

export interface ChecklistItem {
  id: string;
  text: string;
  done: boolean;
}

export interface Task extends TaskDefaults {
  id: string;
  template_id: string | null;
  title: string;
  description: string | null;
  category_id: string | null;
  part_of_day: PartOfDay | null;
  priority: Priority;
  due_date: string;
  due_time: string | null;
  due_at: string;
  expires_at: string | null;
  reminder_minutes_before: number | null;
  late_points_percent: number | null;
  archived: boolean;
  archived_at?: string | null;
  assignee_ids: string[];
  assignees: TaskAssignee[];
  repeats?: boolean;
  checklist?: ChecklistItem[];
  version: number;
}

/** One person's copy of a task, as returned by the list endpoint. */
export interface TaskListItem extends Task {
  member_id: string;
  status: TaskStatus;
}

export interface ListTasksResponse {
  tasks: TaskListItem[];
  next_cursor: string | null;
}

export interface TaskListParams {
  member?: string;
  status?: TaskStatus | "";
  category?: string;
  due_from?: string;
  due_to?: string;
  cursor?: string | null;
  limit?: number;
  archived?: boolean;
  /** Open copies whose occurrence was missed. */
  expired?: boolean;
}

export interface TaskCategory extends TaskDefaults {
  id: string;
  seed_key: string | null;
  name: string;
  emoji: string;
  colour: string;
  sort_order: number;
  badge_key: string | null;
  version: number;
}

/** Regular task templates or bonus task templates. The kind cannot change after create. */
export type TemplateKind = "regular" | "bonus";

export interface TaskTemplate extends TaskDefaults {
  id: string;
  kind: TemplateKind;
  name: string;
  category_id: string | null;
  title: string | null;
  description: string | null;
  part_of_day: PartOfDay | null;
  priority: Priority | null;
  due_time: string | null;
  reminder_minutes_before: number | null;
  late_points_percent: number | null;
  default_assignee_ids: string[];
  version: number;
}

export interface ListTaskTemplatesResponse {
  templates: TaskTemplate[];
  next_cursor: string | null;
}

export interface ListTaskCategoriesResponse {
  categories: TaskCategory[];
}

export type MissedAfterUnit = "days" | "weeks" | "months";

/** Cut-off after the due date. Null in the settings means tasks never expire. */
export interface MissedAfter {
  value: number;
  unit: MissedAfterUnit;
}

/** Family-wide defaults. The penalty fields are not edited on the settings screens, but they are sent back on save. */
export interface FamilyDefaults {
  points: number | null;
  approval_required: boolean | null;
  missed_after: MissedAfter | null;
  penalty_value: number | null;
  penalty_unit: PenaltyUnit | null;
}

/** Start times of the parts of day, as HH:MM. */
export interface PartOfDayTimes {
  morning_start: string;
  after_school_start: string;
  evening_start: string;
}

export type BonusClaimMode = "first_come" | "everyone_once";

/**
 * Bonus task defaults, used when a bonus task is published. Null means no default. `show_from` is HH:MM,
 * `ends` is HH:MM or end_of_day.
 */
export interface BonusDefaults {
  points: number | null;
  claim_mode: BonusClaimMode | null;
  show_from: string | null;
  ends: string | null;
  approval_required: boolean | null;
}

/** Family task settings. The PUT body replaces the whole object, so every field is sent. */
export interface TaskSettingsResponse {
  defaults: FamilyDefaults;
  part_of_day: PartOfDayTimes;
  bonus_defaults: BonusDefaults;
  version: number;
}

export interface TaskSettingsRequest {
  defaults: FamilyDefaults;
  part_of_day: PartOfDayTimes;
  bonus_defaults: BonusDefaults;
}

/** Body of POST and PUT for a category. PUT replaces the whole category, so every field is sent. */
export interface TaskCategoryRequest extends TaskDefaults {
  name: string;
  emoji: string;
  colour: string;
  sort_order?: number;
  badge_key?: string | null;
}

/** Body of POST /api/v1/tasks and the base of the repeat body. Null or absent fields take the family default. */
export interface TaskRequest extends Partial<TaskDefaults> {
  title?: string | null;
  description?: string | null;
  category_id?: string | null;
  part_of_day?: PartOfDay | null;
  priority?: Priority | null;
  due_date?: string | null;
  due_time?: string | null;
  reminder_minutes_before?: number | null;
  late_points_percent?: number | null;
  assignee_ids?: string[] | null;
}

export interface RepeatCreateRequest extends TaskRequest {
  recurrence_rule: string;
}

export interface RepeatCreated {
  task_definition_id: string;
  recurrence_rule: string;
  occurrences: number;
  first_task: Task | null;
}

/** PATCH body. Absent fields keep their value; a null field clears it. */
export interface TaskPatchRequest {
  title?: string;
  description?: string | null;
  category_id?: string | null;
  part_of_day?: PartOfDay | null;
  priority?: Priority | null;
  due_date?: string;
  due_time?: string | null;
  reminder_minutes_before?: number | null;
  late_points_percent?: number | null;
  assignee_ids?: string[];
  points?: number;
  approval_required?: boolean;
  expires_after_minutes?: number | null;
  penalty_value?: number | null;
  penalty_unit?: PenaltyUnit | null;
}

export interface TaskTemplateRequest extends Partial<TaskDefaults> {
  /** Only used on create. Defaults to regular on the server. */
  kind?: TemplateKind;
  name: string;
  category_id?: string | null;
  title?: string | null;
  description?: string | null;
  part_of_day?: PartOfDay | null;
  priority?: Priority | null;
  due_time?: string | null;
  reminder_minutes_before?: number | null;
  late_points_percent?: number | null;
  default_assignee_ids?: string[] | null;
}
