export interface GridDay {
  iso: string;
  day: number;
  inMonth: boolean;
}

const DAY_MS = 86_400_000;

/** Month cells from the Sunday on or before the 1st to the Saturday on or after the last day. Months are 0-based. */
export function monthGrid(year: number, month: number): GridDay[] {
  const first = Date.UTC(year, month, 1);
  const lastDay = new Date(Date.UTC(year, month + 1, 0)).getUTCDate();
  const leading = new Date(first).getUTCDay();
  const rows = Math.ceil((leading + lastDay) / 7);
  const start = first - leading * DAY_MS;
  return Array.from({ length: rows * 7 }, (_, i) => {
    const date = new Date(start + i * DAY_MS);
    return {
      iso: date.toISOString().slice(0, 10),
      day: date.getUTCDate(),
      inMonth: date.getUTCMonth() === month,
    };
  });
}

/** Moves an ISO date by a number of days. */
export function addDays(iso: string, delta: number): string {
  return new Date(Date.parse(`${iso}T00:00:00Z`) + delta * DAY_MS).toISOString().slice(0, 10);
}

/** The seven days, Sunday to Saturday, of the week that holds the ISO date. */
export function weekDays(iso: string): GridDay[] {
  const start = addDays(iso, -new Date(`${iso}T00:00:00Z`).getUTCDay());
  return Array.from({ length: 7 }, (_, i) => {
    const day = addDays(start, i);
    return { iso: day, day: Number(day.slice(8)), inMonth: true };
  });
}

/** Full day heading such as "Sunday, October 18, 2026", in the viewer's locale. */
export function dayTitle(iso: string): string {
  return new Intl.DateTimeFormat(undefined, { weekday: 'long', month: 'long', day: 'numeric', year: 'numeric', timeZone: 'UTC' }).format(
    new Date(`${iso}T00:00:00Z`),
  );
}

/** Week heading such as "Oct 18 – Oct 24, 2026", in the viewer's locale. */
export function weekTitle(iso: string): string {
  const days = weekDays(iso);
  const first = days[0].iso;
  const last = days[6].iso;
  const short = new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric', timeZone: 'UTC' });
  const withYear = new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric', year: 'numeric', timeZone: 'UTC' });
  const from = first.slice(0, 4) === last.slice(0, 4) ? short : withYear;
  return `${from.format(new Date(`${first}T00:00:00Z`))} – ${withYear.format(new Date(`${last}T00:00:00Z`))}`;
}

/** Moves a year and 0-based month by a number of months. */
export function shiftMonth(year: number, month: number, delta: number): { year: number; month: number } {
  const date = new Date(Date.UTC(year, month + delta, 1));
  return { year: date.getUTCFullYear(), month: date.getUTCMonth() };
}

/** Month heading such as "October 2026", in the viewer's locale. */
export function monthTitle(year: number, month: number): string {
  return new Intl.DateTimeFormat(undefined, { month: 'long', year: 'numeric', timeZone: 'UTC' }).format(
    new Date(Date.UTC(year, month, 1)),
  );
}

/** Short month names from January, in the viewer's locale. */
export function shortMonthLabels(): string[] {
  const formatter = new Intl.DateTimeFormat(undefined, { month: 'short', timeZone: 'UTC' });
  return Array.from({ length: 12 }, (_, m) => formatter.format(new Date(Date.UTC(2026, m, 1))));
}

/** Short weekday names from Sunday, in the viewer's locale. */
export function weekdayLabels(): string[] {
  const formatter = new Intl.DateTimeFormat(undefined, { weekday: 'short', timeZone: 'UTC' });
  // 4 January 2026 was a Sunday.
  return Array.from({ length: 7 }, (_, i) => formatter.format(new Date(Date.UTC(2026, 0, 4 + i))));
}

/** Short day such as "Sat 10", in the viewer's locale. */
export function shortDay(iso: string): string {
  return new Intl.DateTimeFormat(undefined, { weekday: 'short', day: 'numeric', timeZone: 'UTC' }).format(
    new Date(`${iso}T00:00:00Z`),
  );
}

/** Clock time such as "4:00 pm" in the given IANA zone, or the viewer's zone when none is given. */
export function clockIn(iso: string, zone?: string): string {
  return new Intl.DateTimeFormat(undefined, { hour: 'numeric', minute: '2-digit', timeZone: zone || undefined }).format(
    new Date(iso),
  );
}

/** Time left until the instant, as a translation key and params. Null once it has passed. */
export function countdown(untilIso: string, now: number): { key: string; params: Record<string, number> } | null {
  const ms = Date.parse(untilIso) - now;
  if (ms <= 0) return null;
  const totalMinutes = Math.ceil(ms / 60000);
  const hours = Math.floor(totalMinutes / 60);
  if (hours > 0) {
    return { key: 'calendar.band.in_hours', params: { hours, minutes: totalMinutes % 60 } };
  }
  return { key: 'calendar.band.in_minutes', params: { minutes: Math.max(totalMinutes, 1) } };
}
