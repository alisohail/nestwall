import {
  TASK_TABS,
  addDaysIso,
  dayGroup,
  dayWord,
  dueRangeForTab,
  formatArchivedDate,
  groupTasks,
  penaltyPoints,
  remaining,
  statusForTab,
  taskErrorKey,
  todayIso,
} from "./tasks.format";
import { TaskListItem } from "./tasks.models";

describe("tasks format", () => {
  function item(patch: Partial<TaskListItem>): TaskListItem {
    return {
      id: "t1",
      member_id: "m1",
      status: "to_do",
      due_date: "2026-10-09",
      due_at: "2026-10-09T17:00:00Z",
      ...patch,
    } as TaskListItem;
  }

  it("reads the local calendar date", () => {
    expect(todayIso(new Date(2026, 9, 9, 12))).toBe("2026-10-09");
  });

  it("reads the calendar date in the family time zone", () => {
    // 2026-10-09 23:30 in New York is already 2026-10-10 in UTC.
    const instant = new Date("2026-10-10T03:30:00Z");
    expect(todayIso(instant, "America/New_York")).toBe("2026-10-09");
    expect(todayIso(instant, "UTC")).toBe("2026-10-10");
  });

  it("maps task tabs to API status filters", () => {
    expect(statusForTab("overdue")).toBe("to_do");
    expect(statusForTab("today")).toBe("to_do");
    expect(statusForTab("upcoming")).toBe("to_do");
    expect(statusForTab("submitted")).toBe("submitted");
    expect(statusForTab("approved")).toBe("approved");
    expect(statusForTab("archived")).toBe("");
  });

  it("lists the task tabs without an All tab, with Archived last", () => {
    expect(TASK_TABS).toEqual(["overdue", "today", "upcoming", "submitted", "approved", "expired", "archived"]);
  });

  it("maps task tabs to due-date bounds", () => {
    expect(dueRangeForTab("submitted", "2026-10-09")).toEqual({});
    expect(dueRangeForTab("overdue", "2026-10-09")).toEqual({
      due_to: "2026-10-09",
    });
    expect(dueRangeForTab("today", "2026-10-09")).toEqual({
      due_from: "2026-10-09",
      due_to: "2026-10-09",
    });
    expect(dueRangeForTab("upcoming", "2026-10-31")).toEqual({
      due_from: "2026-11-01",
    });
  });

  it("adds days across month ends", () => {
    expect(addDaysIso("2026-10-31", 1)).toBe("2026-11-01");
    expect(addDaysIso("2026-10-01", -1)).toBe("2026-09-30");
  });

  it("groups open copies past their due time as overdue", () => {
    const now = Date.parse("2026-10-09T18:00:00Z");
    expect(
      dayGroup(item({ due_at: "2026-10-09T17:00:00Z" }), "2026-10-09", now),
    ).toBe("overdue");
    expect(
      dayGroup(
        item({ status: "approved", due_at: "2026-10-09T17:00:00Z" }),
        "2026-10-09",
        now,
      ),
    ).toBe("today");
  });

  it("groups by due date when not overdue", () => {
    const now = Date.parse("2026-10-09T08:00:00Z");
    expect(
      dayGroup(
        item({
          due_date: "2026-10-08",
          due_at: "2026-10-08T17:00:00Z",
          status: "approved",
        }),
        "2026-10-09",
        now,
      ),
    ).toBe("earlier");
    expect(
      dayGroup(
        item({ due_date: "2026-10-10", due_at: "2026-10-10T17:00:00Z" }),
        "2026-10-09",
        now,
      ),
    ).toBe("tomorrow");
    expect(
      dayGroup(
        item({ due_date: "2026-10-12", due_at: "2026-10-12T17:00:00Z" }),
        "2026-10-09",
        now,
      ),
    ).toBe("later");
  });

  it("groups open tabs by due date and drops empty groups", () => {
    const now = Date.parse("2026-10-09T08:00:00Z");
    const rows = [
      item({ id: "a", due_date: "2026-10-12", due_at: "2026-10-12T17:00:00Z" }),
      item({ id: "b", due_date: "2026-10-09", due_at: "2026-10-09T17:00:00Z" }),
    ];
    const groups = groupTasks(rows, "upcoming", "2026-10-09", now);
    expect(groups.map((g) => g.key)).toEqual(["today", "later"]);
    expect(groups[0].items.map((r) => r.id)).toEqual(["b"]);
  });

  it("leaves archived rows as one unlabelled list in server order", () => {
    const now = Date.parse("2026-10-09T08:00:00Z");
    const rows = [
      item({ id: "late", archived: true, due_date: "2026-10-12", due_at: "2026-10-12T17:00:00Z" }),
      item({ id: "early", archived: true, due_date: "2026-10-01", due_at: "2026-10-01T17:00:00Z" }),
    ];
    const groups = groupTasks(rows, "archived", "2026-10-09", now);
    expect(groups).toEqual([{ key: null, items: rows }]);
  });

  it("names the day when it is yesterday, today or tomorrow", () => {
    expect(dayWord("2026-10-08", "2026-10-09")).toBe("yesterday");
    expect(dayWord("2026-10-09", "2026-10-09")).toBe("today");
    expect(dayWord("2026-10-10", "2026-10-09")).toBe("tomorrow");
    expect(dayWord("2026-10-12", "2026-10-09")).toBeNull();
  });

  it("describes time left in days, hours or minutes", () => {
    const now = Date.parse("2026-10-09T00:00:00Z");
    expect(remaining("2026-10-10T08:00:00Z", now)).toEqual({
      key: "tasks.duration.days_hours",
      params: { days: 1, hours: 8 },
    });
    expect(remaining("2026-10-09T06:00:00Z", now)).toEqual({
      key: "tasks.duration.hours",
      params: { hours: 6 },
    });
    expect(remaining("2026-10-09T00:45:00Z", now)).toEqual({
      key: "tasks.duration.minutes",
      params: { minutes: 45 },
    });
    expect(remaining("2026-10-08T23:00:00Z", now)).toBeNull();
  });

  it("works out the penalty in points", () => {
    expect(penaltyPoints(10, 50, "percent")).toBe(5);
    expect(penaltyPoints(10, 3, "points")).toBe(3);
  });

  it("maps errors to translation keys", () => {
    expect(taskErrorKey({ status: 0 })).toBe("tasks.error.network");
    expect(taskErrorKey({ status: 412 })).toBe("tasks.error.stale");
    expect(taskErrorKey({ status: 409, code: "task_missed" })).toBe(
      "tasks.error.missed",
    );
    expect(taskErrorKey({ status: 409, code: "unknown_code" })).toBe(
      "tasks.error.conflict",
    );
    expect(taskErrorKey({ status: 409, code: "task_not_archived" })).toBe(
      "tasks.error.not_archived",
    );
    expect(taskErrorKey({ status: 409, code: "task_not_restorable" })).toBe(
      "tasks.error.not_restorable",
    );
    expect(taskErrorKey({ status: 500 })).toBe("tasks.error.generic");
  });

  it("formats an archived date in the family time zone", () => {
    const auckland = formatArchivedDate("2025-10-09T23:30:00Z", "Pacific/Auckland");
    expect(auckland).toContain("10");
    expect(auckland).toContain("2025");
    const utc = formatArchivedDate("2025-10-09T12:00:00Z", "UTC");
    expect(utc).toContain("9");
    expect(utc).not.toContain("10");
  });
});
