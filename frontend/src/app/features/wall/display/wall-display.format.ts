/** Date, time and greeting helpers for the wall display. The family timezone comes from the summary. */

export type GreetingPeriod = 'morning' | 'afternoon' | 'evening';

/** Uses the family timezone when it is valid; otherwise the browser's local zone. */
export function usableZone(timeZone: string): string | undefined {
  try {
    new Intl.DateTimeFormat('en-US', { timeZone });
    return timeZone;
  } catch {
    return undefined;
  }
}

/** YYYY-MM-DD for an instant as seen in the given zone. */
export function dateKey(instant: Date, timeZone?: string): string {
  const parts = new Intl.DateTimeFormat('en-CA', {
    timeZone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).formatToParts(instant);
  const part = (type: string) => parts.find(p => p.type === type)?.value ?? '';
  return `${part('year')}-${part('month')}-${part('day')}`;
}

/** Adds whole days to a YYYY-MM-DD key without touching any timezone. */
export function addDays(key: string, days: number): string {
  const [year, month, day] = key.split('-').map(Number);
  const next = new Date(Date.UTC(year, month - 1, day + days));
  return dateKey(next, 'UTC');
}

export function greetingPeriod(instant: Date, timeZone?: string): GreetingPeriod {
  const hour = Number(
    new Intl.DateTimeFormat('en-US', { timeZone, hour: 'numeric', hourCycle: 'h23' })
      .formatToParts(instant)
      .find(p => p.type === 'hour')?.value ?? 0,
  ) % 24;
  if (hour < 12) return 'morning';
  if (hour < 18) return 'afternoon';
  return 'evening';
}

/** Night runs from 9 pm to 6:30 am in the family timezone. It is automatic, with no control on the wall. */
export function isNightTime(instant: Date, timeZone?: string): boolean {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone,
    hour: 'numeric',
    minute: 'numeric',
    hourCycle: 'h23',
  }).formatToParts(instant);
  const part = (type: string) => Number(parts.find(p => p.type === type)?.value ?? 0);
  const minutes = (part('hour') % 24) * 60 + part('minute');
  return minutes >= 21 * 60 || minutes < 6 * 60 + 30;
}

/** Clock time such as "8:00 AM" in the given zone and locale. */
export function formatClock(instant: Date | string, timeZone: string | undefined, locale: string): string {
  return new Intl.DateTimeFormat(locale, { timeZone, hour: 'numeric', minute: '2-digit' }).format(
    new Date(instant),
  );
}

/** Short weekday and day for a YYYY-MM-DD key, such as "Sat 10 Oct". */
export function formatDayKey(key: string, locale: string): string {
  const [year, month, day] = key.split('-').map(Number);
  return new Intl.DateTimeFormat(locale, {
    timeZone: 'UTC',
    weekday: 'short',
    day: 'numeric',
    month: 'short',
  }).format(new Date(Date.UTC(year, month - 1, day, 12)));
}

/** Time for tasks due today, or weekday and time for other days. */
export function formatDue(
  due: Date | string,
  familyDate: string,
  timeZone: string | undefined,
  locale: string,
): string {
  const instant = new Date(due);
  const time = formatClock(instant, timeZone, locale);
  if (dateKey(instant, timeZone) === familyDate) return time;
  const weekday = new Intl.DateTimeFormat(locale, { timeZone, weekday: 'short' }).format(instant);
  return `${weekday} ${time}`;
}

/** Minutes from `now` until `start`, rounded down. Negative once it has started. */
export function minutesUntil(start: Date | string, now: Date): number {
  return Math.floor((new Date(start).getTime() - now.getTime()) / 60000);
}
