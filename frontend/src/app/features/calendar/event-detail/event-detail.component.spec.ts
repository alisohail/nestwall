import { TestBed } from '@angular/core/testing';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import type { MonthPill } from '../calendar.component';
import type { CalendarItem } from '../calendar.models';
import { EventDetailComponent } from './event-detail.component';

function pill(item: Partial<CalendarItem>, task = false): MonthPill {
    const full: CalendarItem = {
        key: 'k',
        kind: task ? 'task' : 'event',
        title: 'Dentist',
        occurrence_date: '2030-01-15',
        starts_at: '2030-01-15T09:00:00Z',
        ends_at: '2030-01-15T10:00:00Z',
        all_day: false,
        time_zone: 'UTC',
        member_ids: [],
        ...item,
    };
    return { key: 'k', title: 'Dentist', task, colour: '#336699', item: full, eventId: task ? null : 'ev1', date: '2030-01-15', recurring: false };
}

function create(p: MonthPill) {
    const fixture = TestBed.createComponent(EventDetailComponent);
    fixture.componentRef.setInput('pill', p);
    fixture.detectChanges();
    return fixture;
}

describe('EventDetailComponent reminders', () => {
    beforeEach(() => TestBed.configureTestingModule({ imports: [EventDetailComponent, getTranslocoTestingModule()] }));

    it('lists each reminder of an event in words', () => {
        const fixture = create(pill({ reminders: [0, 15, 90, 1440] }));
        const lines = Array.from((fixture.nativeElement as HTMLElement).querySelectorAll('[data-testid="detail-reminders"] div'))
            .map((el) => el.textContent?.trim());
        expect(lines).toEqual(['At start', '15 min before', '90 min before', '1 d before']);
    });

    it('hides the reminder row when an event has none', () => {
        const fixture = create(pill({ reminders: [] }));
        expect((fixture.nativeElement as HTMLElement).querySelector('[data-testid="detail-reminders"]')).toBeNull();
    });

    it('shows no reminder row for a task', () => {
        const fixture = create(pill({ reminders: [15] }, true));
        expect((fixture.nativeElement as HTMLElement).querySelector('[data-testid="detail-reminders"]')).toBeNull();
    });
});

describe('EventDetailComponent edit gate', () => {
    beforeEach(() => TestBed.configureTestingModule({ imports: [EventDetailComponent, getTranslocoTestingModule()] }));

    it('lets a parent edit and delete an event on a manual calendar', () => {
        const fixture = create(pill({ calendar_id: 'cal-default', calendar_name: 'Default', imported: false }));
        const el = fixture.nativeElement as HTMLElement;
        expect(el.querySelector('[data-testid="detail-edit"]')).not.toBeNull();
        expect(el.querySelector('[data-testid="detail-delete"]')).not.toBeNull();
        expect(el.querySelector('[data-testid="detail-synced"]')).toBeNull();
    });

    it('makes an imported event read-only', () => {
        const fixture = create(pill({ calendar_id: 'imp1', calendar_name: 'Google', imported: true }));
        const el = fixture.nativeElement as HTMLElement;
        expect(el.querySelector('[data-testid="detail-edit"]')).toBeNull();
        expect(el.querySelector('[data-testid="detail-delete"]')).toBeNull();
        expect(el.querySelector('[data-testid="detail-synced"]')).not.toBeNull();
    });
});
