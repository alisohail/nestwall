import { addDaysIso } from '../../tasks/tasks.format';
import { buildRule, defaultRepeat, RepeatFreq, RepeatState } from '../../tasks/task-form/recurrence';

const FREQS: Record<string, RepeatFreq> = { DAILY: 'daily', WEEKLY: 'weekly', MONTHLY: 'monthly', YEARLY: 'yearly' };
/** RRULE day codes, indexed by JavaScript weekday (0 = Sunday). */
const RRULE_DAYS = ['SU', 'MO', 'TU', 'WE', 'TH', 'FR', 'SA'];
const DAY_MS = 86400000;

/** Compares rules ignoring the RRULE prefix, case and part order. */
export function normaliseRule(rule: string): string {
  return rule
    .replace(/^RRULE:/i, '')
    .toUpperCase()
    .split(';')
    .map((part) => part.trim())
    .filter(Boolean)
    .sort()
    .join(';');
}

/**
 * The repeat builder state for a stored rule, or null when the builder cannot rebuild exactly this rule.
 * Such a rule is kept as it is.
 */
export function stateFromRule(rule: string, startDate: string): RepeatState | null {
  const parts = new Map<string, string>();
  for (const item of rule.replace(/^RRULE:/i, '').toUpperCase().split(';')) {
    const [key, value] = item.split('=');
    if (key && value) parts.set(key.trim(), value.trim());
  }
  const freq = FREQS[parts.get('FREQ') ?? ''];
  if (!freq) return null;

  const state: RepeatState = { ...defaultRepeat(startDate), freq, interval: Number(parts.get('INTERVAL') ?? 1) };
  const byDay = parts.get('BYDAY');
  const monthDay = parts.get('BYMONTHDAY');
  const setPos = parts.get('BYSETPOS');
  if (freq === 'weekly' && byDay) {
    state.weekdays = byDay.split(',').map((code) => RRULE_DAYS.indexOf(code));
  }
  if (freq === 'monthly') {
    if (monthDay === '-1') {
      state.monthlyMode = 'last';
    } else if (monthDay) {
      state.monthlyMode = 'day';
      state.monthDay = Number(monthDay);
    } else if (byDay && setPos) {
      state.monthlyMode = 'nth';
      state.nthWeekday = RRULE_DAYS.indexOf(byDay);
      state.nth = setPos === '-1' ? 5 : Number(setPos);
    }
  }
  const count = parts.get('COUNT');
  const until = parts.get('UNTIL');
  if (count) {
    state.ends = 'count';
    state.endCount = Number(count);
  } else if (until && /^\d{8}/.test(until)) {
    state.ends = 'date';
    state.endDate = `${until.slice(0, 4)}-${until.slice(4, 6)}-${until.slice(6, 8)}`;
  }

  const rebuilt = buildRule(state, startDate);
  return rebuilt !== null && normaliseRule(rebuilt) === normaliseRule(rule) ? state : null;
}

/** Whole days from one YYYY-MM-DD date to another. */
export function daysBetween(from: string, to: string): number {
  const a = Date.parse(`${from}T00:00:00Z`);
  const b = Date.parse(`${to}T00:00:00Z`);
  return Math.round((b - a) / DAY_MS);
}

/**
 * End date that keeps the same span as the original event, starting on startDate.
 * A timed event that ends before it starts runs past midnight, so it ends the next day.
 */
export function endDateFor(
  startDate: string,
  spanDays: number,
  startTime: string | null = null,
  endTime: string | null = null,
): string {
  const span = Math.max(spanDays, 0);
  const crossesMidnight = span === 0 && startTime !== null && endTime !== null && endTime < startTime;
  return addDaysIso(startDate, crossesMidnight ? 1 : span);
}

/** The viewer's zone, used when the family zone is not known yet. */
export function browserZone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
}

/** The instant (epoch ms) a wall-clock date and time means in a zone. A null time means midnight. */
export function startInstantMs(date: string, time: string | null, zone: string): number {
  const [year, month, day] = date.split('-').map(Number);
  const [hour, minute] = (time ?? '00:00').split(':').map(Number);
  const wall = Date.UTC(year, month - 1, day, hour, minute);
  return wall - zoneOffsetMs(wall, zone);
}

/** How far a zone's wall clock is ahead of UTC at an instant, in ms. */
function zoneOffsetMs(instant: number, zone: string): number {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone: zone,
    hourCycle: 'h23',
    year: 'numeric',
    month: 'numeric',
    day: 'numeric',
    hour: 'numeric',
    minute: 'numeric',
    second: 'numeric',
  }).formatToParts(new Date(instant));
  const part = (type: string): number => Number(parts.find((p) => p.type === type)?.value);
  const wallAsUtc = Date.UTC(part('year'), part('month') - 1, part('day'), part('hour'), part('minute'), part('second'));
  return wallAsUtc - Math.floor(instant / 1000) * 1000;
}
