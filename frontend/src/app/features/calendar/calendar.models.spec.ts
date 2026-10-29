import { ALL_ITEMS, CalendarFilter, CalendarItem, passesFilter } from './calendar.models';

function item(overrides: Partial<CalendarItem>): CalendarItem {
    return {
        key: 'k',
        kind: 'event',
        title: 'Swim',
        occurrence_date: '2026-10-09',
        starts_at: '2026-10-09T16:00:00Z',
        ends_at: '2026-10-09T17:00:00Z',
        all_day: false,
        time_zone: 'UTC',
        member_ids: ['m1'],
        ...overrides,
    };
}

describe('passesFilter', () => {
    it('passes everything with the default filter', () => {
        expect(passesFilter(item({}), ALL_ITEMS)).toBe(true);
        expect(passesFilter(item({ kind: 'task' }), ALL_ITEMS)).toBe(true);
    });

    it('drops tasks when deadlines are off and keeps events', () => {
        const off: CalendarFilter = { members: null, hiddenCalendars: [], tasks: false };

        expect(passesFilter(item({}), off)).toBe(true);
        expect(passesFilter(item({ kind: 'task' }), off)).toBe(false);
        expect(passesFilter(item({ kind: 'task' }), { ...ALL_ITEMS, tasks: false })).toBe(false);
        expect(passesFilter(item({ kind: 'task' }), { ...ALL_ITEMS, tasks: true })).toBe(true);
    });

    it('drops items from a switched-off calendar and keeps the others', () => {
        const filter: CalendarFilter = { ...ALL_ITEMS, hiddenCalendars: ['cal1'] };

        expect(passesFilter(item({ calendar_id: 'cal1' }), filter)).toBe(false);
        expect(passesFilter(item({ calendar_id: 'cal2' }), filter)).toBe(true);
        expect(passesFilter(item({}), filter)).toBe(true);
    });

    it('keeps items for selected members and drops the rest', () => {
        const filter: CalendarFilter = { members: ['m2'], hiddenCalendars: [], tasks: true };

        expect(passesFilter(item({ member_ids: ['m1'] }), filter)).toBe(false);
        expect(passesFilter(item({ member_ids: ['m1', 'm2'] }), filter)).toBe(true);
    });

    it('always keeps family-wide items, even when every member is hidden', () => {
        const filter: CalendarFilter = { members: [], hiddenCalendars: [], tasks: true };

        expect(passesFilter(item({ member_ids: [] }), filter)).toBe(true);
    });
});
