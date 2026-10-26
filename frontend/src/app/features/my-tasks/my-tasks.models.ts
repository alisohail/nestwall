import { ChecklistItem, PartOfDay, TaskStatus } from '../tasks/tasks.models';

/** One of the signed-in child's own copies. task_id is the occurrence ID. */
export interface MyTaskItem {
  task_id: string;
  title: string;
  /** Definition version. Send it as If-Match when ticking a step. */
  version: number;
  /** The child's own steps on this copy. Absent when the task has no steps. */
  checklist?: ChecklistItem[];
  category_emoji: string | null;
  category_colour: string | null;
  part_of_day: PartOfDay | null;
  due_date: string;
  due_at: string;
  expires_at: string | null;
  points: number;
  approval_required: boolean;
  status: TaskStatus;
  past_due: boolean;
  completed_at: string | null;
  completed_late: boolean;
  /** When a parent decided on a submitted copy. Null until then, and for automatic approvals it is set with no name. */
  decided_at: string | null;
  decided_by_name: string | null;
  late_points_percent: number | null;
  penalty_value: number | null;
  penalty_unit: 'percent' | 'points' | null;
  penalty_points: number | null;
}

export interface MySummary {
  today_total: number;
  today_done: number;
  next_up: MyTaskItem | null;
}

/** Response of GET /api/v1/me/tasks. Coming up and Done are paged separately. */
export interface MyTasksResponse {
  date: string;
  timezone: string;
  now: string;
  streak_days: number;
  summary: MySummary;
  today: MyTaskItem[];
  catch_up: MyTaskItem[];
  waiting: MyTaskItem[];
  expired: MyTaskItem[];
}

/** One page of GET /api/v1/me/tasks/coming or /done. next_cursor is null on the last page. */
export interface MyTasksPage {
  tasks: MyTaskItem[];
  next_cursor: string | null;
}
