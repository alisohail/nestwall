import { buildRule, defaultRepeat, previewDates, repeatProblem, RepeatState } from './recurrence';

describe('recurrence', () => {
  const start = '2026-10-09'; // Friday

  function state(patch: Partial<RepeatState>): RepeatState {
    return { ...defaultRepeat(start), ...patch };
  }

  it('builds no rule when the task does not repeat', () => {
    expect(buildRule(defaultRepeat(start), start)).toBeNull();
  });

  it('builds weekly rules with days in Monday-first order', () => {
    const rule = buildRule(state({ freq: 'weekly', weekdays: [5, 1, 3] }), start);
    expect(rule).toBe('FREQ=WEEKLY;INTERVAL=1;BYDAY=MO,WE,FR');
  });

  it('builds monthly rules for a day, the nth weekday and the last day', () => {
    expect(buildRule(state({ freq: 'monthly', monthlyMode: 'day', monthDay: 9 }), start)).toBe(
      'FREQ=MONTHLY;INTERVAL=1;BYMONTHDAY=9',
    );
    expect(
      buildRule(state({ freq: 'monthly', monthlyMode: 'nth', nth: 2, nthWeekday: 5 }), start),
    ).toBe('FREQ=MONTHLY;INTERVAL=1;BYDAY=FR;BYSETPOS=2');
    expect(buildRule(state({ freq: 'monthly', monthlyMode: 'nth', nth: 5, nthWeekday: 5 }), start)).toBe(
      'FREQ=MONTHLY;INTERVAL=1;BYDAY=FR;BYSETPOS=-1',
    );
    expect(buildRule(state({ freq: 'monthly', monthlyMode: 'last' }), start)).toBe(
      'FREQ=MONTHLY;INTERVAL=1;BYMONTHDAY=-1',
    );
  });

  it('builds yearly rules from the start date and adds the end', () => {
    expect(buildRule(state({ freq: 'yearly' }), start)).toBe('FREQ=YEARLY;INTERVAL=1;BYMONTH=10;BYMONTHDAY=9');
    expect(buildRule(state({ freq: 'daily', ends: 'count', endCount: 10 }), start)).toBe(
      'FREQ=DAILY;INTERVAL=1;COUNT=10',
    );
    expect(buildRule(state({ freq: 'daily', ends: 'date', endDate: '2026-12-18' }), start)).toBe(
      'FREQ=DAILY;INTERVAL=1;UNTIL=20261218T000000Z',
    );
  });

  it('flags repeat settings the server would refuse', () => {
    expect(repeatProblem(state({ freq: 'weekly', weekdays: [] }), start)).toBe('weekdays');
    expect(repeatProblem(state({ freq: 'daily', interval: 0 }), start)).toBe('interval');
    expect(repeatProblem(state({ freq: 'daily', ends: 'date', endDate: '2026-10-01' }), start)).toBe('end_date');
    expect(repeatProblem(state({ freq: 'daily', ends: 'count', endCount: 0 }), start)).toBe('end_count');
    expect(repeatProblem(state({ freq: 'daily' }), start)).toBeNull();
  });

  it('previews weekly dates on the chosen weekdays', () => {
    const dates = previewDates(state({ freq: 'weekly', weekdays: [1, 3] }), start, 4);
    expect(dates).toEqual(['2026-10-12', '2026-10-14', '2026-10-19', '2026-10-21']);
  });

  it('previews every other day and stops at the count', () => {
    const dates = previewDates(state({ freq: 'daily', interval: 2, ends: 'count', endCount: 3 }), start);
    expect(dates).toEqual(['2026-10-09', '2026-10-11', '2026-10-13']);
  });

  it('previews the last day of the month and the nth weekday', () => {
    expect(previewDates(state({ freq: 'monthly', monthlyMode: 'last' }), start, 2)).toEqual([
      '2026-10-31',
      '2026-11-30',
    ]);
    expect(previewDates(state({ freq: 'monthly', monthlyMode: 'nth', nth: 2, nthWeekday: 5 }), start, 2)).toEqual([
      '2026-10-09',
      '2026-11-13',
    ]);
  });

  it('stops the preview at the end date', () => {
    const dates = previewDates(state({ freq: 'daily', ends: 'date', endDate: '2026-10-10' }), start);
    expect(dates).toEqual(['2026-10-09', '2026-10-10']);
  });
});
