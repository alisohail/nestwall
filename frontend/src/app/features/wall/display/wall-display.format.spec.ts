import { addDays, dateKey, formatDayKey, formatDue, greetingPeriod, isNightTime, minutesUntil, usableZone } from './wall-display.format';

describe('wall display format helpers', () => {
    describe('usableZone', () => {
        it('keeps a valid IANA zone', () => {
            expect(usableZone('Europe/Paris')).toBe('Europe/Paris');
        });

        it('drops an unknown zone', () => {
            expect(usableZone('Not/AZone')).toBeUndefined();
        });
    });

    describe('dateKey', () => {
        it('uses the family zone for the calendar day', () => {
            // 23:30 UTC on 1 May is already 2 May in Auckland (UTC+12 in May).
            const instant = new Date('2024-05-01T23:30:00Z');
            expect(dateKey(instant, 'Pacific/Auckland')).toBe('2024-05-02');
            expect(dateKey(instant, 'UTC')).toBe('2024-05-01');
        });
    });

    describe('addDays', () => {
        it('crosses a leap day', () => {
            expect(addDays('2024-02-28', 2)).toBe('2024-03-01');
        });

        it('crosses a year boundary', () => {
            expect(addDays('2024-12-31', 1)).toBe('2025-01-01');
        });

        it('moves backwards', () => {
            expect(addDays('2024-03-01', -1)).toBe('2024-02-29');
        });
    });

    describe('greetingPeriod', () => {
        it('picks morning before noon in the family zone', () => {
            expect(greetingPeriod(new Date('2024-05-01T08:00:00Z'), 'UTC')).toBe('morning');
        });

        it('picks afternoon from noon until 18:00', () => {
            expect(greetingPeriod(new Date('2024-05-01T13:00:00Z'), 'UTC')).toBe('afternoon');
        });

        it('picks evening from 18:00', () => {
            expect(greetingPeriod(new Date('2024-05-01T19:00:00Z'), 'UTC')).toBe('evening');
        });
    });

    describe('isNightTime', () => {
        it('is day before 21:00 and from 06:30 onwards', () => {
            expect(isNightTime(new Date('2024-05-01T12:00:00Z'), 'UTC')).toBe(false);
            expect(isNightTime(new Date('2024-05-01T20:59:00Z'), 'UTC')).toBe(false);
            expect(isNightTime(new Date('2024-05-01T06:30:00Z'), 'UTC')).toBe(false);
            expect(isNightTime(new Date('2024-05-01T06:45:00Z'), 'UTC')).toBe(false);
        });

        it('is night from 21:00 through midnight until 06:29', () => {
            expect(isNightTime(new Date('2024-05-01T21:00:00Z'), 'UTC')).toBe(true);
            expect(isNightTime(new Date('2024-05-01T23:59:00Z'), 'UTC')).toBe(true);
            expect(isNightTime(new Date('2024-05-02T00:00:00Z'), 'UTC')).toBe(true);
            expect(isNightTime(new Date('2024-05-02T06:29:00Z'), 'UTC')).toBe(true);
        });

        it('reads the hour in the family zone, not the browser zone', () => {
            // 10:00 UTC is 22:00 in Auckland (UTC+12 in May): night there.
            expect(isNightTime(new Date('2024-05-01T10:00:00Z'), 'Pacific/Auckland')).toBe(true);
            // 19:00 UTC is 07:00 the next day in Auckland: day there.
            expect(isNightTime(new Date('2024-05-01T19:00:00Z'), 'Pacific/Auckland')).toBe(false);
        });
    });

    describe('formatDayKey', () => {
        it('shows weekday, day and month', () => {
            // 10 October 2024 was a Thursday.
            const label = formatDayKey('2024-10-10', 'en-US');
            expect(label).toContain('Thu');
            expect(label).toContain('Oct');
            expect(label).toContain('10');
        });
    });

    describe('formatDue', () => {
        it('shows only the time for tasks due on the family day', () => {
            const label = formatDue('2024-05-01T10:00:00Z', '2024-05-01', 'UTC', 'en-US');
            expect(label).toContain('10:00');
            expect(label).not.toContain('Wed');
        });

        it('adds the weekday for tasks due on another day', () => {
            // 1 May 2024 was a Wednesday.
            const label = formatDue('2024-05-01T10:00:00Z', '2024-05-02', 'UTC', 'en-US');
            expect(label).toContain('Wed');
            expect(label).toContain('10:00');
        });
    });

    describe('minutesUntil', () => {
        it('counts whole minutes until a start time', () => {
            expect(minutesUntil('2024-05-01T10:00:00Z', new Date('2024-05-01T09:30:00Z'))).toBe(30);
        });

        it('rounds partial minutes down', () => {
            expect(minutesUntil('2024-05-01T10:00:00Z', new Date('2024-05-01T09:58:30Z'))).toBe(1);
        });

        it('goes negative once the event has started', () => {
            expect(minutesUntil('2024-05-01T10:00:00Z', new Date('2024-05-01T10:05:00Z'))).toBe(-5);
        });
    });
});
