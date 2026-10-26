import { Priority, TaskStatus } from '../tasks/tasks.models';

/** The person a band item, approval or activity row belongs to. */
export interface DashboardMemberRef {
  member_id: string;
  member_name: string;
  member_colour: string;
  avatar_id: string | null;
}

export interface DashboardSummary {
  today_total: number;
  today_done: number;
  overdue: number;
  waiting_approval: number;
  waiting_reward_requests: number;
  points_week: number;
}

/** One person's copy of a task due today. task_id is the occurrence ID. */
export interface DashboardItem extends DashboardMemberRef {
  task_id: string;
  title: string;
  due_at: string;
  priority: Priority;
  status: TaskStatus;
}

export interface DashboardPerson extends DashboardMemberRef {
  total: number;
  done: number;
  week_total: number;
  week_done: number;
  points_week: number;
  overdue: number;
}

/** A submitted copy that waits for a parent. */
export interface DashboardApproval extends DashboardMemberRef {
  task_id: string;
  title: string;
  completed_at: string;
  completed_late: boolean;
}

/** A pending reward redemption that waits for a parent. */
export interface DashboardRewardRequest extends DashboardMemberRef {
  redemption_id: string;
  reward_name: string;
  reward_icon: string;
  cost: number;
  requested_at: string;
}

export interface DashboardActivity extends DashboardMemberRef {
  action: 'task.completed' | 'task.approved' | 'task.rejected' | 'task.skipped';
  task_id: string;
  task_title: string;
  created_at: string;
}

/** Response of GET /api/v1/dashboard/parent. */
export interface ParentDashboard {
  date: string;
  timezone: string;
  summary: DashboardSummary;
  today: DashboardItem[];
  people: DashboardPerson[];
  approvals: DashboardApproval[];
  reward_requests: DashboardRewardRequest[];
  activity: DashboardActivity[];
}
