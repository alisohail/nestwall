import { ApiError } from "../../core/api/api-client.service";
import { clockOptions } from "../../core/time-format/clock-style";
import { TaskListItem, TaskStatus } from "./tasks.models";

export type DayGroup = "overdue" | "earlier" | "today" | "tomorrow" | "later";
export type DayWord = "yesterday" | "today" | "tomorrow";
export type TaskTab =
  | "overdue"
  | "today"
  | "upcoming"
  | "submitted"
  | "approved"
  | "expired"
  | "archived";

export const TASK_TABS: TaskTab[] = [
  "overdue",
  "today",
  "upcoming",
  "submitted",
  "approved",
  "expired",
  "archived",
];

const DAY_MS = 86400000;

const CONFLICT_KEYS: Record<string, string> = {
  task_archived: "tasks.error.archived",
  task_missed: "tasks.error.missed",
  task_not_due: "tasks.error.not_due",
  task_not_archived: "tasks.error.not_archived",
  task_not_restorable: "tasks.error.not_restorable",
  checklist_locked: "tasks.error.checklist_locked",
  repeat_edit_unsupported: "tasks.error.repeat_edit",
};

/** Calendar date as YYYY-MM-DD. With a family time zone it is the date in that zone; otherwise the browser's local date. */
export function todayIso(now: Date = new Date(), zone?: string): string {
  if (zone) {
    const parts = new Intl.DateTimeFormat("en-CA", {
      timeZone: zone,
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
    }).formatToParts(now);
    const part = (type: string) => parts.find((p) => p.type === type)?.value ?? "";
    return `${part("year")}-${part("month")}-${part("day")}`;
  }
  const m = String(now.getMonth() + 1).padStart(2, "0");
  const d = String(now.getDate()).padStart(2, "0");
  return `${now.getFullYear()}-${m}-${d}`;
}

export function addDaysIso(iso: string, days: number): string {
  return new Date(Date.parse(`${iso}T00:00:00Z`) + days * DAY_MS)
    .toISOString()
    .slice(0, 10);
}

/** API status filter for a tab. Overdue, Today and Upcoming list open tasks; the Overdue list keeps only those past their due time. Expired is set by its own flag, so it sends no status. */
export function statusForTab(tab: TaskTab): TaskStatus | "" {
  switch (tab) {
    case "overdue":
    case "today":
    case "upcoming":
      return "to_do";
    case "submitted":
      return "submitted";
    case "approved":
      return "approved";
    case "expired":
    case "archived":
      return "";
  }
}

/** Due-date bounds sent to the API for a tab. Dates are inclusive. */
export function dueRangeForTab(
  tab: TaskTab,
  today: string,
): { due_from?: string; due_to?: string } {
  switch (tab) {
    case "overdue":
      return { due_to: today };
    case "today":
      return { due_from: today, due_to: today };
    case "upcoming":
      return { due_from: addDaysIso(today, 1) };
    default:
      return {};
  }
}

/** Overdue is an open copy whose due time has passed. Other copies group by their due date. */
export function dayGroup(
  item: TaskListItem,
  today: string,
  now: number = Date.now(),
): DayGroup {
  if (item.status === "to_do" && Date.parse(item.due_at) < now)
    return "overdue";
  if (item.due_date < today) return "earlier";
  if (item.due_date === today) return "today";
  if (item.due_date === addDaysIso(today, 1)) return "tomorrow";
  return "later";
}

export interface TaskGroup {
  key: DayGroup | null;
  items: TaskListItem[];
}

const GROUP_ORDER: DayGroup[] = ["overdue", "earlier", "today", "tomorrow", "later"];

/**
 * Splits the visible rows into due-date groups, in GROUP_ORDER. Archived rows are not grouped:
 * they stay one list in server order, with no key, so no label is shown.
 */
export function groupTasks(
  items: TaskListItem[],
  tab: string,
  today: string,
  now: number = Date.now(),
): TaskGroup[] {
  if (tab === "archived") return [{ key: null, items }];
  const buckets: Record<DayGroup, TaskListItem[]> = {
    overdue: [],
    earlier: [],
    today: [],
    tomorrow: [],
    later: [],
  };
  for (const item of items) buckets[dayGroup(item, today, now)].push(item);
  return GROUP_ORDER.map((key) => ({ key, items: buckets[key] })).filter(
    (g) => g.items.length > 0,
  );
}

/** Yesterday, today or tomorrow when the date is one of those; otherwise null. */
export function dayWord(iso: string, today: string): DayWord | null {
  if (iso === today) return "today";
  if (iso === addDaysIso(today, 1)) return "tomorrow";
  if (iso === addDaysIso(today, -1)) return "yesterday";
  return null;
}

/** Short date such as "Fri 9 Oct", formatted in the viewer's locale. */
export function formatDay(iso: string): string {
  return new Intl.DateTimeFormat(undefined, {
    weekday: "short",
    day: "numeric",
    month: "short",
    timeZone: "UTC",
  }).format(new Date(`${iso}T00:00:00Z`));
}

/** Time such as "5:00 pm" from HH:MM, formatted in the viewer's locale. */
export function formatTime(hhmm: string): string {
  const [h, m] = hhmm.split(":").map(Number);
  return new Intl.DateTimeFormat(undefined, clockOptions({
    hour: "numeric",
    minute: "2-digit",
  })).format(new Date(2000, 0, 1, h, m));
}

/** Date such as "9 Oct 2025" for when a task was archived, in the family time zone when given. */
export function formatArchivedDate(iso: string, zone?: string): string {
  return new Intl.DateTimeFormat(undefined, {
    day: "numeric",
    month: "short",
    year: "numeric",
    timeZone: zone,
  }).format(new Date(iso));
}

/** Time left until the given instant, as a translation key and params. Null when it has passed. */
export function remaining(
  untilIso: string,
  now: number = Date.now(),
): { key: string; params: Record<string, number> } | null {
  const ms = Date.parse(untilIso) - now;
  if (ms <= 0) return null;
  const totalMinutes = Math.ceil(ms / 60000);
  const days = Math.floor(totalMinutes / 1440);
  const hours = Math.floor((totalMinutes % 1440) / 60);
  if (days > 0)
    return { key: "tasks.duration.days_hours", params: { days, hours } };
  if (hours > 0) return { key: "tasks.duration.hours", params: { hours } };
  return {
    key: "tasks.duration.minutes",
    params: { minutes: Math.max(totalMinutes, 1) },
  };
}

/** Points lost when a copy is missed. The unit decides whether value is a percent or a fixed amount. */
export function penaltyPoints(
  points: number,
  value: number,
  unit: "percent" | "points",
): number {
  return unit === "percent" ? Math.round((points * value) / 100) : value;
}

/** Translation key for a failed task request. Uses the status and, for 409, the error code. */
export function taskErrorKey(err: unknown): string {
  const e = err as Partial<ApiError> | null;
  switch (e?.status) {
    case 0:
      return "tasks.error.network";
    case 400:
      return "tasks.error.validation";
    case 403:
      return "tasks.error.forbidden";
    case 404:
      return "tasks.error.not_found";
    case 409:
      return CONFLICT_KEYS[e.code ?? ""] ?? "tasks.error.conflict";
    case 412:
      return "tasks.error.stale";
    default:
      return "tasks.error.generic";
  }
}
