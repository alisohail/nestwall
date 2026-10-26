import { RepeatState, buildRule, defaultRepeat } from '../../tasks/task-form/recurrence';
import { daysBetween, endDateFor, normaliseRule, startInstantMs, stateFromRule } from './event-form';

// 2026-10-09 is a Friday.
const START = '2026-10-09';

describe('event-form', () => {
  describe('normaliseRule', () => {
    it('ignores the prefix, case and part order', () => {
      expect(normaliseRule('RRULE:freq=weekly;byday=FR;interval=1')).toBe('BYDAY=FR;FREQ=WEEKLY;INTERVAL=1');
    });
  });

  describe('stateFromRule', () => {
    it('rebuilds every option the repeat builder can produce to the same rule', () => {
      const base = defaultRepeat(START);
      const states: RepeatState[] = [
        { ...base, freq: 'daily' },
        { ...base, freq: 'daily', interval: 3 },
        { ...base, freq: 'weekly', weekdays: [5] },
        { ...base, freq: 'weekly', interval: 2, weekdays: [2, 4], ends: 'count', endCount: 6 },
        { ...base, freq: 'monthly', monthlyMode: 'day', monthDay: 9 },
        { ...base, freq: 'monthly', monthlyMode: 'nth', nth: 2, nthWeekday: 5 },
        { ...base, freq: 'monthly', monthlyMode: 'nth', nth: 5, nthWeekday: 5 },
        { ...base, freq: 'monthly', monthlyMode: 'last' },
        { ...base, freq: 'yearly' },
        { ...base, freq: 'daily', ends: 'date', endDate: '2026-12-18' },
      ];

      for (const state of states) {
        const rule = buildRule(state, START) as string;
        const parsed = stateFromRule(rule, START);

        expect(parsed).not.toBeNull();
        expect(buildRule(parsed as RepeatState, START)).toBe(rule);
      }
    });

    it('reads the interval, weekdays and end settings from the rule', () => {
      const parsed = stateFromRule('FREQ=WEEKLY;INTERVAL=2;BYDAY=TU,TH;COUNT=5', START);

      expect(parsed).toEqual(
        jasmine.objectContaining({ freq: 'weekly', interval: 2, weekdays: [2, 4], ends: 'count', endCount: 5 }),
      );
    });

    it('reads a last-day monthly rule as last', () => {
      expect(stateFromRule('FREQ=MONTHLY;INTERVAL=1;BYMONTHDAY=-1', START)?.monthlyMode).toBe('last');
    });

    it('returns null for a rule the builder cannot rebuild exactly, so it is kept as custom', () => {
      expect(stateFromRule('FREQ=DAILY', START)).toBeNull();
      expect(stateFromRule('FREQ=YEARLY;INTERVAL=1;BYMONTH=3;BYMONTHDAY=9', START)).toBeNull();
      expect(stateFromRule('FREQ=HOURLY;INTERVAL=1', START)).toBeNull();
      expect(stateFromRule('not a rule', START)).toBeNull();
    });
  });

  describe('daysBetween and endDateFor', () => {
    it('counts whole days, across a month end', () => {
      expect(daysBetween('2026-10-09', '2026-10-09')).toBe(0);
      expect(daysBetween('2026-10-30', '2026-11-02')).toBe(3);
    });

    it('keeps the original span when the start moves', () => {
      expect(endDateFor('2026-10-09', 0)).toBe('2026-10-09');
      expect(endDateFor('2026-10-09', 2)).toBe('2026-10-11');
      expect(endDateFor('2026-10-09', -1)).toBe('2026-10-09');
    });

      it('ends the next day when a timed event ends before it starts', () => {
        expect(endDateFor('2026-10-09', 0, '22:00', '01:00')).toBe('2026-10-10');
        expect(endDateFor('2026-12-31', 0, '23:30', '00:30')).toBe('2027-01-01');
      });

      it('keeps the same day for a timed event that ends after it starts, and for all-day', () => {
        expect(endDateFor('2026-10-09', 0, '09:00', '10:00')).toBe('2026-10-09');
        expect(endDateFor('2026-10-09', 0, null, null)).toBe('2026-10-09');
      });
  });

  describe('startInstantMs', () => {
    it('reads the wall clock in the event zone', () => {
      expect(startInstantMs('2026-10-09', '16:00', 'UTC')).toBe(Date.UTC(2026, 9, 9, 16, 0));
      // Berlin is UTC+2 in October (summer time).
      expect(startInstantMs('2026-10-09', '16:00', 'Europe/Berlin')).toBe(Date.UTC(2026, 9, 9, 14, 0));
    });

    it('uses midnight for an all-day start', () => {
      expect(startInstantMs('2026-01-15', null, 'America/New_York')).toBe(Date.UTC(2026, 0, 15, 5, 0));
    });
  });
});
