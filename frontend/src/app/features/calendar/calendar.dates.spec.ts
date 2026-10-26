import { addDays, countdown, dayTitle, monthGrid, shiftMonth, weekDays, weekTitle } from './calendar.dates';

describe('calendar dates', () => {
  describe('addDays', () => {
    it('rolls over month and year boundaries in both directions', () => {
      expect(addDays('2026-10-31', 1)).toBe('2026-11-01');
      expect(addDays('2026-01-01', -1)).toBe('2025-12-31');
      expect(addDays('2026-10-09', 0)).toBe('2026-10-09');
    });
  });

  describe('weekDays', () => {
    it('returns Sunday to Saturday of the week that holds the date', () => {
      const days = weekDays('2026-10-09');

      expect(days.map((d) => d.iso)).toEqual([
        '2026-10-04',
        '2026-10-05',
        '2026-10-06',
        '2026-10-07',
        '2026-10-08',
        '2026-10-09',
        '2026-10-10',
      ]);
      expect(days[5]).toEqual({ iso: '2026-10-09', day: 9, inMonth: true });
    });

    it('can start in the previous month', () => {
      const days = weekDays('2026-10-01');

      expect(days[0].iso).toBe('2026-09-27');
      expect(days[6].iso).toBe('2026-10-03');
    });
  });

  describe('week and day titles', () => {
    it('shows the year once when the week stays in one year', () => {
      const title = weekTitle('2026-10-09');

      expect(title).toContain('Oct 4');
      expect(title).toContain('Oct 10, 2026');
    });

    it('shows both years when the week crosses a year', () => {
      const title = weekTitle('2026-12-30');

      expect(title).toContain('Dec 27, 2026');
      expect(title).toContain('Jan 2, 2027');
    });

    it('names the weekday and the full date for a day', () => {
      const title = dayTitle('2026-10-18');

      expect(title).toContain('Sunday');
      expect(title).toContain('October 18, 2026');
    });
  });

  describe('monthGrid', () => {
    it('starts on the Sunday before the 1st and fills five weeks for October 2026', () => {
      const grid = monthGrid(2026, 9);

      expect(grid.length).toBe(35);
      expect(grid[0]).toEqual({ iso: '2026-09-27', day: 27, inMonth: false });
      expect(grid[4]).toEqual({ iso: '2026-10-01', day: 1, inMonth: true });
      expect(grid[34]).toEqual({ iso: '2026-10-31', day: 31, inMonth: true });
    });

    it('uses six weeks when the month needs them', () => {
      const grid = monthGrid(2026, 7);

      expect(grid.length).toBe(42);
      expect(grid[0].iso).toBe('2026-07-26');
      expect(grid[41].iso).toBe('2026-09-05');
    });
  });

  describe('shiftMonth', () => {
    it('rolls over the year in both directions', () => {
      expect(shiftMonth(2026, 11, 1)).toEqual({ year: 2027, month: 0 });
      expect(shiftMonth(2026, 0, -1)).toEqual({ year: 2025, month: 11 });
    });
  });

  describe('countdown', () => {
    const now = Date.parse('2026-10-09T12:00:00Z');

    it('shows hours and minutes when more than an hour is left', () => {
      expect(countdown('2026-10-09T13:30:00Z', now)).toEqual({
        key: 'calendar.band.in_hours',
        params: { hours: 1, minutes: 30 },
      });
    });

    it('shows whole minutes under an hour, rounding up', () => {
      expect(countdown('2026-10-09T12:25:00Z', now)).toEqual({
        key: 'calendar.band.in_minutes',
        params: { minutes: 25 },
      });
      expect(countdown('2026-10-09T12:00:10Z', now)).toEqual({
        key: 'calendar.band.in_minutes',
        params: { minutes: 1 },
      });
    });

    it('returns null once the time has passed', () => {
      expect(countdown('2026-10-09T11:59:00Z', now)).toBeNull();
      expect(countdown('2026-10-09T12:00:00Z', now)).toBeNull();
    });
  });
});
