export type RepeatFreq = 'none' | 'daily' | 'weekly' | 'monthly' | 'yearly';
export type MonthlyMode = 'day' | 'nth' | 'last';
export type EndsMode = 'never' | 'date' | 'count';

/** Repeat builder state. Weekdays use 0 for Sunday to 6 for Saturday. */
export interface RepeatState {
  freq: RepeatFreq;
  interval: number;
  weekdays: number[];
  monthlyMode: MonthlyMode;
  monthDay: number;
  nth: number;
  nthWeekday: number;
  ends: EndsMode;
  endDate: string;
  endCount: number;
}

export type RepeatProblem = 'interval' | 'weekdays' | 'month_day' | 'end_date' | 'end_count';

const RRULE_DAYS = ['SU', 'MO', 'TU', 'WE', 'TH', 'FR', 'SA'];
const MONDAY_FIRST = [1, 2, 3, 4, 5, 6, 0];
const DAY_MS = 86400000;
const PREVIEW_DAYS = 1500;

export function defaultRepeat(startDate: string): RepeatState {
  const start = parseDate(startDate);
  return {
    freq: 'none',
    interval: 1,
    weekdays: [1, 2, 3, 4, 5],
    monthlyMode: 'day',
    monthDay: start.getUTCDate(),
    nth: 1,
    nthWeekday: start.getUTCDay(),
    ends: 'never',
    endDate: startDate,
    endCount: 10,
  };
}

/** The first problem with the repeat settings, or null when they can be saved. */
export function repeatProblem(state: RepeatState, startDate: string): RepeatProblem | null {
  if (state.freq === 'none') return null;
  if (!Number.isInteger(state.interval) || state.interval < 1) return 'interval';
  if (state.freq === 'weekly' && state.weekdays.length === 0) return 'weekdays';
  if (state.freq === 'monthly' && state.monthlyMode === 'day' && !(state.monthDay >= 1 && state.monthDay <= 31)) {
    return 'month_day';
  }
  if (state.ends === 'date' && (!state.endDate || state.endDate < startDate)) return 'end_date';
  if (state.ends === 'count' && !(Number.isInteger(state.endCount) && state.endCount >= 1)) return 'end_count';
  return null;
}

/** Builds the RRULE body (without the RRULE prefix) for the server, or null when the task does not repeat. */
export function buildRule(state: RepeatState, startDate: string): string | null {
  if (state.freq === 'none') return null;
  const start = parseDate(startDate);
  const parts = [`FREQ=${state.freq.toUpperCase()}`, `INTERVAL=${state.interval}`];
  switch (state.freq) {
    case 'weekly':
      parts.push(`BYDAY=${sortedWeekdays(state.weekdays).map((d) => RRULE_DAYS[d]).join(',')}`);
      break;
    case 'monthly':
      if (state.monthlyMode === 'day') {
        parts.push(`BYMONTHDAY=${state.monthDay}`);
      } else if (state.monthlyMode === 'last') {
        parts.push('BYMONTHDAY=-1');
      } else {
        parts.push(`BYDAY=${RRULE_DAYS[state.nthWeekday]}`, `BYSETPOS=${state.nth === 5 ? -1 : state.nth}`);
      }
      break;
    case 'yearly':
      parts.push(`BYMONTH=${start.getUTCMonth() + 1}`, `BYMONTHDAY=${start.getUTCDate()}`);
      break;
  }
  if (state.ends === 'count') parts.push(`COUNT=${state.endCount}`);
  if (state.ends === 'date') parts.push(`UNTIL=${state.endDate.replace(/-/g, '')}T000000Z`);
  return parts.join(';');
}

/** The next dates a repeat would create, as YYYY-MM-DD, for the live preview. Mirrors the server rules. */
export function previewDates(state: RepeatState, startDate: string, limit = 6): string[] {
  if (state.freq === 'none' || repeatProblem(state, startDate)) return [];
  const start = parseDate(startDate);
  const endDate = state.ends === 'date' ? state.endDate : null;
  const maxCount = state.ends === 'count' ? state.endCount : Infinity;
  const out: string[] = [];
  let found = 0;
  for (let i = 0; i < PREVIEW_DAYS && out.length < limit && found < maxCount; i++) {
    const day = new Date(start.getTime() + i * DAY_MS);
    const iso = toIso(day);
    if (endDate && iso > endDate) break;
    if (matches(day, start, state)) {
      found++;
      out.push(iso);
    }
  }
  return out;
}

function matches(day: Date, start: Date, state: RepeatState): boolean {
  const diffDays = Math.round((day.getTime() - start.getTime()) / DAY_MS);
  switch (state.freq) {
    case 'daily':
      return diffDays % state.interval === 0;
    case 'weekly': {
      const weeks = Math.round((mondayOf(day) - mondayOf(start)) / (7 * DAY_MS));
      return weeks % state.interval === 0 && state.weekdays.includes(day.getUTCDay());
    }
    case 'monthly': {
      const months = (day.getUTCFullYear() - start.getUTCFullYear()) * 12 + day.getUTCMonth() - start.getUTCMonth();
      if (months % state.interval !== 0) return false;
      if (state.monthlyMode === 'day') return day.getUTCDate() === state.monthDay;
      if (state.monthlyMode === 'last') return day.getUTCDate() === daysInMonth(day);
      if (day.getUTCDay() !== state.nthWeekday) return false;
      return state.nth === 5 ? day.getUTCDate() + 7 > daysInMonth(day) : Math.ceil(day.getUTCDate() / 7) === state.nth;
    }
    case 'yearly':
      return (
        (day.getUTCFullYear() - start.getUTCFullYear()) % state.interval === 0 &&
        day.getUTCMonth() === start.getUTCMonth() &&
        day.getUTCDate() === start.getUTCDate()
      );
    default:
      return false;
  }
}

/** Weekdays sorted Monday first, the order the server expects. */
function sortedWeekdays(days: number[]): number[] {
  return MONDAY_FIRST.filter((d) => days.includes(d));
}

function mondayOf(day: Date): number {
  const offset = (day.getUTCDay() + 6) % 7;
  return day.getTime() - offset * DAY_MS;
}

function daysInMonth(day: Date): number {
  return new Date(Date.UTC(day.getUTCFullYear(), day.getUTCMonth() + 1, 0)).getUTCDate();
}

function parseDate(iso: string): Date {
  return new Date(`${iso}T00:00:00Z`);
}

function toIso(day: Date): string {
  return day.toISOString().slice(0, 10);
}
