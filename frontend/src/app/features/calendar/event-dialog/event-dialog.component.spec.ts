import type { MockedObject } from "vitest";
import { provideHttpClient, withXhr } from '@angular/common/http';
import { provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { of, throwError } from 'rxjs';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { FamilyService, MemberDetail } from '../../family/family.service';
import { CalendarImport, CalendarSource, EventDetail } from '../calendar.models';
import { CalendarService } from '../calendar.service';
import { EventDialogComponent } from './event-dialog.component';
import { buildRule, defaultRepeat } from '../../tasks/task-form/recurrence';
import { mockOf } from '../../../testing/mock-of';

const ava: MemberDetail = {
    id: 'm1',
    user_id: 'u1',
    display_name: 'Ava',
    role_id: 'r1',
    role_name: 'Parent',
    status: 'active',
    colour: '#ff0000',
    is_child: false,
    avatar_id: null,
    email: null,
    date_of_birth: null,
    gender: null,
};

const calendars: CalendarSource[] = [
    { id: 'c1', name: 'Family', colour: '#0D9488', event_count: 0 },
    { id: 'c2', name: 'Work', colour: '#2563EB', event_count: 0 },
];

const DATE = '2026-10-09';

const fileImport: CalendarImport = {
    id: 'i1',
    name: 'School',
    kind: 'file',
    file_name: 'school.ics',
    event_count: 3,
    skipped_count: 0,
    colour: '#7C3AED',
    audience: 'everyone',
    show_on_wall: true,
    member_ids: [],
    refresh_minutes: 60,
    paused: false,
    last_synced_at: null,
    last_sync_error: null,
    created_at: '2026-01-01T00:00:00Z',
};

const linkImport: CalendarImport = { ...fileImport, id: 'i2', name: 'Sports', kind: 'link', file_name: null };
const WEEKLY = buildRule({ ...defaultRepeat(DATE), freq: 'weekly', weekdays: [5] }, DATE) as string;

function savedEvent(overrides: Partial<EventDetail> = {}): EventDetail {
    return {
        id: 'ev1',
        title: 'Swim',
        description: null,
        location: null,
        all_day: false,
        time_zone: 'UTC',
        start_date: DATE,
        start_time: '16:00',
        end_date: DATE,
        end_time: '17:00',
        recurrence_rule: null,
        member_ids: ['m1'],
        calendar_id: 'c1',
        reminders: [],
        version: 3,
        exceptions: [],
        ...overrides,
    };
}

describe('EventDialogComponent', () => {
    let calendar: MockedObject<CalendarService>;
    let family: MockedObject<FamilyService>;

    beforeEach(async () => {
        calendar = mockOf<CalendarService>({
            createEvent: vi.fn().mockName("CalendarService.createEvent"),
            updateEvent: vi.fn().mockName("CalendarService.updateEvent"),
            editOccurrence: vi.fn().mockName("CalendarService.editOccurrence"),
            listSources: vi.fn().mockName("CalendarService.listSources"),
            listImports: vi.fn().mockName("CalendarService.listImports")
        });
        calendar.createEvent.mockReturnValue(of(savedEvent()));
        calendar.updateEvent.mockReturnValue(of(savedEvent()));
        calendar.editOccurrence.mockReturnValue(of(undefined));
        calendar.listSources.mockReturnValue(of({ items: calendars }));
        calendar.listImports.mockReturnValue(of({ items: [] }));
        family = mockOf<FamilyService>({
            listMembers: vi.fn().mockName("FamilyService.listMembers")
        });
        family.listMembers.mockReturnValue(of({ members: [ava] }));

        await TestBed.configureTestingModule({
            imports: [EventDialogComponent, getTranslocoTestingModule()],
            providers: [
                { provide: CalendarService, useValue: calendar },
                { provide: FamilyService, useValue: family },
                provideHttpClient(withXhr()),
                provideHttpClientTesting(),
            ],
        }).compileComponents();
    });

    function create(inputs: {
        event?: EventDetail | null;
        occurrence?: string | null;
        defaultDate?: string;
    } = {}): ComponentFixture<EventDialogComponent> {
        const fixture = TestBed.createComponent(EventDialogComponent);
        fixture.componentRef.setInput('zone', 'UTC');
        fixture.componentRef.setInput('defaultDate', inputs.defaultDate ?? DATE);
        fixture.componentRef.setInput('event', inputs.event ?? null);
        fixture.componentRef.setInput('occurrence', inputs.occurrence ?? null);
        fixture.detectChanges();
        return fixture;
    }

    function el<T extends HTMLElement = HTMLElement>(fixture: ComponentFixture<EventDialogComponent>, testId: string): T | null {
        return fixture.nativeElement.querySelector(`[data-testid="${testId}"]`);
    }

    function type(fixture: ComponentFixture<EventDialogComponent>, testId: string, value: string): void {
        const input = el<HTMLInputElement>(fixture, testId) as HTMLInputElement;
        input.value = value;
        input.dispatchEvent(new Event('input'));
        fixture.detectChanges();
    }

    function save(fixture: ComponentFixture<EventDialogComponent>): void {
        el(fixture, 'event-save')?.click();
        fixture.detectChanges();
    }

    /** Clicks a frequency in the repeat builder: 0 none, 1 daily, 2 weekly, 3 monthly, 4 yearly. */
    function pickFreq(fixture: ComponentFixture<EventDialogComponent>, index: number): void {
        const builder: HTMLElement = fixture.nativeElement.querySelector('app-repeat-builder');
        (builder.querySelectorAll('button')[index] as HTMLButtonElement).click();
        fixture.detectChanges();
    }

    it('creates a single timed event with the default times and the chosen person', () => {
        const fixture = create();

        type(fixture, 'event-title', '  Swim  ');
        el(fixture, 'event-person-m1')?.click();
        fixture.detectChanges();
        save(fixture);

        expect(calendar.createEvent).toHaveBeenCalledTimes(1);

        expect(calendar.createEvent).toHaveBeenCalledWith({
            title: 'Swim',
            description: null,
            location: null,
            all_day: false,
            time_zone: 'UTC',
            start_date: DATE,
            start_time: '09:00',
            end_date: DATE,
            end_time: '10:00',
            recurrence_rule: null,
            member_ids: ['m1'],
            calendar_id: 'c1',
            reminders: [],
        });
    });

    it('holds one preset at a time, so picking another replaces it', () => {
        const fixture = create();

        type(fixture, 'event-title', 'Swim');
        el(fixture, 'event-reminder-preset-15')?.click();
        el(fixture, 'event-reminder-preset-30')?.click();
        fixture.detectChanges();

        expect(el(fixture, 'event-reminder-preset-15')?.getAttribute('aria-pressed')).toBe('false');
        expect(el(fixture, 'event-reminder-preset-30')?.getAttribute('aria-pressed')).toBe('true');
        save(fixture);

        expect(vi.mocked(calendar.createEvent).mock.lastCall![0].reminders).toEqual([30]);
    });

    it('clicking the selected preset again clears the reminder', () => {
        const fixture = create();

        type(fixture, 'event-title', 'Swim');
        el(fixture, 'event-reminder-preset-15')?.click();
        el(fixture, 'event-reminder-preset-15')?.click();
        save(fixture);

        expect(vi.mocked(calendar.createEvent).mock.lastCall![0].reminders).toEqual([]);
    });

    it('a custom amount replaces the selected preset and is sent on save', () => {
        const fixture = create();

        type(fixture, 'event-title', 'Swim');
        el(fixture, 'event-reminder-preset-15')?.click();
        type(fixture, 'event-reminder-amount', '2');
        const unit = el<HTMLSelectElement>(fixture, 'event-reminder-unit') as HTMLSelectElement;
        unit.value = 'hours';
        unit.dispatchEvent(new Event('change'));
        fixture.detectChanges();

        expect(el(fixture, 'event-reminder-preset-15')?.getAttribute('aria-pressed')).toBe('false');
        save(fixture);

        expect(vi.mocked(calendar.createEvent).mock.lastCall![0].reminders).toEqual([120]);
    });

    it('picking a preset clears the custom amount', () => {
        const fixture = create();

        type(fixture, 'event-title', 'Swim');
        type(fixture, 'event-reminder-amount', '2');
        el(fixture, 'event-reminder-preset-30')?.click();
        fixture.detectChanges();

        expect((el(fixture, 'event-reminder-amount') as HTMLInputElement).value).toBe('');
        save(fixture);

        expect(vi.mocked(calendar.createEvent).mock.lastCall![0].reminders).toEqual([30]);
    });

    it('clearing the custom amount removes the reminder', () => {
        const fixture = create();

        type(fixture, 'event-title', 'Swim');
        type(fixture, 'event-reminder-amount', '2');
        type(fixture, 'event-reminder-amount', '');
        save(fixture);

        expect(vi.mocked(calendar.createEvent).mock.lastCall![0].reminders).toEqual([]);
    });

    it('warns about reminders already past for the start but still saves them', () => {
        const fixture = create({ defaultDate: '2020-01-01' });

        type(fixture, 'event-title', 'Old');
        el(fixture, 'event-reminder-preset-0')?.click();
        fixture.detectChanges();

        expect(el(fixture, 'event-reminders-past')).not.toBeNull();
        save(fixture);

        expect(vi.mocked(calendar.createEvent).mock.lastCall![0].reminders).toEqual([0]);
    });

    it('shows no past warning for a start in the future', () => {
        const fixture = create({ defaultDate: '2099-01-01' });

        el(fixture, 'event-reminder-preset-0')?.click();
        fixture.detectChanges();

        expect(el(fixture, 'event-reminders-past')).toBeNull();
    });

    it('loads a saved custom reminder into the amount field and sends it back on save', () => {
        const fixture = create({ event: savedEvent({ reminders: [90] }) });

        expect((el(fixture, 'event-reminder-amount') as HTMLInputElement).value).toBe('90');
        expect(el(fixture, 'event-reminder-preset-0')?.getAttribute('aria-pressed')).toBe('false');
        save(fixture);

        expect(calendar.updateEvent).toHaveBeenCalledWith('ev1', expect.objectContaining({ reminders: [90] }), 3);
    });

    it('accepts a reminder further out than four weeks', () => {
        const fixture = create();

        type(fixture, 'event-title', 'Trip');
        type(fixture, 'event-reminder-amount', '30');
        const unit = el<HTMLSelectElement>(fixture, 'event-reminder-unit') as HTMLSelectElement;
        unit.value = 'days';
        unit.dispatchEvent(new Event('change'));
        fixture.detectChanges();
        save(fixture);

        expect(el(fixture, 'event-error')).toBeNull();
        expect(vi.mocked(calendar.createEvent).mock.lastCall![0].reminders).toEqual([43200]);
    });

    it('shows no past warning for a repeating event, since later occurrences can still use the reminder', () => {
        const fixture = create({ event: savedEvent({ recurrence_rule: 'FREQ=DAILY', start_date: '2020-01-01' }) });

        el(fixture, 'event-reminder-preset-0')?.click();
        fixture.detectChanges();

        expect(el(fixture, 'event-reminders-past')).toBeNull();
    });

    it('groups imported calendars by type and creates an event on one with its id', () => {
        calendar.listImports.mockReturnValue(of({ items: [fileImport, linkImport] }));
        const fixture = create();

        const groups = Array.from(fixture.nativeElement.querySelectorAll('optgroup') as NodeListOf<HTMLOptGroupElement>).map((g) => g.label);
        expect(groups).toEqual(['Personal', 'Imported']);

        type(fixture, 'event-title', 'Match');
        const select = el<HTMLSelectElement>(fixture, 'event-calendar') as HTMLSelectElement;
        select.value = 'i1';
        select.dispatchEvent(new Event('change'));
        fixture.detectChanges();

        expect(el(fixture, 'event-people-locked')).not.toBeNull();
        expect((el(fixture, 'event-person-m1') as HTMLButtonElement).disabled).toBe(true);
        save(fixture);

        expect(vi.mocked(calendar.createEvent).mock.lastCall![0].calendar_id).toBe('i1');
    });

    it('does not offer linked calendars for a new event', () => {
        calendar.listImports.mockReturnValue(of({ items: [fileImport, linkImport] }));
        const fixture = create();

        const options = Array.from(fixture.nativeElement.querySelectorAll('option') as NodeListOf<HTMLOptionElement>).map((o) => o.value);
        expect(options).not.toContain('i2');
    });

    it('keeps the linked calendar listed when editing an event already on it', () => {
        calendar.listImports.mockReturnValue(of({ items: [fileImport, linkImport] }));
        const fixture = create({ event: savedEvent({ calendar_id: 'i2' }) });

        const options = Array.from(fixture.nativeElement.querySelectorAll('option') as NodeListOf<HTMLOptionElement>).map((o) => o.value);
        expect(options).toContain('i2');
    });

    it('keeps people editable on a manual calendar', () => {
        calendar.listImports.mockReturnValue(of({ items: [fileImport] }));
        const fixture = create();

        expect(el(fixture, 'event-people-locked')).toBeNull();
        expect((el(fixture, 'event-person-m1') as HTMLButtonElement).disabled).toBe(false);
    });

    it('sends an all-day event with no times', () => {
        const fixture = create();

        type(fixture, 'event-title', 'Holiday');
        (el(fixture, 'event-all-day') as HTMLInputElement).click();
        fixture.detectChanges();
        save(fixture);

        const body = vi.mocked(calendar.createEvent).mock.lastCall![0];
        expect(body.all_day).toBe(true);
        expect(body.start_time).toBeNull();
        expect(body.end_time).toBeNull();
    });

    it('sends a weekly repeat rule for a new series', () => {
        const fixture = create();

        type(fixture, 'event-title', 'Piano');
        pickFreq(fixture, 2);
        save(fixture);

        expect(vi.mocked(calendar.createEvent).mock.lastCall![0].recurrence_rule).toBe(buildRule({ ...defaultRepeat(DATE), freq: 'weekly' }, DATE));
    });

    it('sends the location, and no location when it is blank', () => {
        const fixture = create();

        type(fixture, 'event-title', 'Swim');
        type(fixture, 'event-location', '  Leisure centre  ');
        save(fixture);
        expect(vi.mocked(calendar.createEvent).mock.lastCall![0].location).toBe('Leisure centre');

        type(fixture, 'event-location', '   ');
        save(fixture);
        expect(vi.mocked(calendar.createEvent).mock.lastCall![0].location).toBeNull();
    });

    it('shows the saved location and sends the edited one', () => {
        const fixture = create({ event: savedEvent({ location: 'Pool' }) });

        expect((el<HTMLInputElement>(fixture, 'event-location') as HTMLInputElement).value).toBe('Pool');
        type(fixture, 'event-location', 'Pool hall');
        save(fixture);

        expect(calendar.updateEvent).toHaveBeenCalledTimes(1);

        expect(calendar.updateEvent).toHaveBeenCalledWith('ev1', expect.objectContaining({ location: 'Pool hall' }), 3);
    });

    it('preselects the saved calendar and sends it on save', () => {
        const fixture = create({ event: savedEvent({ calendar_id: 'c2' }) });

        expect((el<HTMLSelectElement>(fixture, 'event-calendar') as HTMLSelectElement).value).toBe('c2');
        type(fixture, 'event-title', 'Swim club');
        save(fixture);

        expect(calendar.updateEvent).toHaveBeenCalledTimes(1);

        expect(calendar.updateEvent).toHaveBeenCalledWith('ev1', expect.objectContaining({ calendar_id: 'c2' }), 3);
    });

    it('does not save when the title is empty', () => {
        const fixture = create();

        save(fixture);

        expect(calendar.createEvent).not.toHaveBeenCalled();
        expect(el(fixture, 'event-error')).not.toBeNull();
    });

    it('ends the next day when the end time is earlier than the start time', () => {
        const fixture = create();

        type(fixture, 'event-title', 'Party');
        type(fixture, 'event-start', '22:00');
        type(fixture, 'event-end', '01:00');
        save(fixture);

        const body = vi.mocked(calendar.createEvent).mock.lastCall![0];
        expect(body.start_date).toBe(DATE);
        expect(body.start_time).toBe('22:00');
        expect(body.end_date).toBe('2026-10-10');
        expect(body.end_time).toBe('01:00');
        expect(el(fixture, 'event-error')).toBeNull();
    });

    it('does not save when the end time equals the start time', () => {
        const fixture = create();

        type(fixture, 'event-title', 'Swim');
        type(fixture, 'event-start', '10:00');
        type(fixture, 'event-end', '10:00');
        save(fixture);

        expect(calendar.createEvent).not.toHaveBeenCalled();
        expect(el(fixture, 'event-error')).not.toBeNull();
    });

    it('edits a whole series with the loaded version as If-Match', () => {
        const event = savedEvent({ recurrence_rule: WEEKLY });
        const fixture = create({ event, occurrence: DATE });

        el(fixture, 'event-scope-series')?.click();
        fixture.detectChanges();
        type(fixture, 'event-title', 'Swim club');
        save(fixture);

        expect(calendar.updateEvent).toHaveBeenCalledTimes(1);

        expect(calendar.updateEvent).toHaveBeenCalledWith('ev1', expect.objectContaining({ title: 'Swim club', recurrence_rule: WEEKLY, member_ids: ['m1'] }), 3);
        expect(calendar.editOccurrence).not.toHaveBeenCalled();
    });

    it('changes only the opened occurrence, and sends no series fields', () => {
        const event = savedEvent({ recurrence_rule: WEEKLY });
        const fixture = create({ event, occurrence: DATE });

        expect(fixture.componentInstance.scope()).toBe('occurrence');
        type(fixture, 'event-title', 'Swim (moved)');
        type(fixture, 'event-date', '2026-10-16');
        save(fixture);

        expect(calendar.editOccurrence).toHaveBeenCalledTimes(1);

        expect(calendar.editOccurrence).toHaveBeenCalledWith('ev1', DATE, {
            title: 'Swim (moved)',
            description: null,
            location: null,
            start_date: '2026-10-16',
            start_time: '16:00',
            end_date: '2026-10-16',
            end_time: '17:00',
        }, 3);
        expect(calendar.updateEvent).not.toHaveBeenCalled();
    });

    it('shows the scope choice only for a repeating event opened from an occurrence', () => {
        expect(el(create(), 'event-scope-series')).toBeNull();
        expect(el(create({ event: savedEvent() }), 'event-scope-series')).toBeNull();
        expect(el(create({ event: savedEvent({ recurrence_rule: WEEKLY }), occurrence: DATE }), 'event-scope-series')).not.toBeNull();
    });

    it('disables the repeat choice while editing one occurrence', () => {
        const fixture = create({ event: savedEvent({ recurrence_rule: WEEKLY }), occurrence: DATE });

        expect(el<HTMLFieldSetElement>(fixture, 'event-repeat')?.disabled).toBe(true);
    });

    it('keeps a rule with an interval and an end date when the series is edited without changing it', () => {
        const rule = buildRule({ ...defaultRepeat(DATE), freq: 'weekly', interval: 2, weekdays: [5], ends: 'date', endDate: '2026-12-18' }, DATE) as string;
        const fixture = create({ event: savedEvent({ recurrence_rule: rule }) });

        type(fixture, 'event-title', 'Swim club');
        save(fixture);

        expect(calendar.updateEvent).toHaveBeenCalledTimes(1);

        expect(calendar.updateEvent).toHaveBeenCalledWith('ev1', expect.objectContaining({ recurrence_rule: rule }), 3);
    });

    it('keeps a rule the builder cannot show unchanged, until the user stops repeating', () => {
        const fixture = create({ event: savedEvent({ recurrence_rule: 'FREQ=DAILY' }) });

        expect(el(fixture, 'event-repeat-custom')).not.toBeNull();
        expect(fixture.nativeElement.querySelector('app-repeat-builder')).toBeNull();
        save(fixture);
        expect(vi.mocked(calendar.updateEvent).mock.lastCall![1].recurrence_rule).toBe('FREQ=DAILY');

        el(fixture, 'event-repeat-stop')?.click();
        fixture.detectChanges();
        expect(el(fixture, 'event-repeat-custom')).toBeNull();
        save(fixture);
        expect(vi.mocked(calendar.updateEvent).mock.lastCall![1].recurrence_rule).toBeNull();
    });

    it('does not save a repeat that is incomplete', () => {
        const fixture = create();

        type(fixture, 'event-title', 'Piano');
        pickFreq(fixture, 2);
        fixture.componentInstance.repeatState.update((state) => ({ ...state, weekdays: [] }));
        fixture.detectChanges();
        save(fixture);

        expect(calendar.createEvent).not.toHaveBeenCalled();
        expect(el(fixture, 'event-error')).not.toBeNull();
    });

    it('reports a version conflict and keeps the dialog open', () => {
        calendar.updateEvent.mockReturnValue(throwError(() => ({ status: 412, code: 'version_conflict' })));
        const fixture = create({ event: savedEvent() });
        const closed = vi.fn().mockName('closed');
        fixture.componentInstance.closed.subscribe(closed);

        type(fixture, 'event-title', 'Swim club');
        save(fixture);

        expect(el(fixture, 'event-error')).not.toBeNull();
        expect(closed).not.toHaveBeenCalled();
        expect(fixture.componentInstance.saving()).toBe(false);
    });

    it('emits saved after a successful save', () => {
        const fixture = create();
        const saved = vi.fn().mockName('saved');
        fixture.componentInstance.saved.subscribe(saved);

        type(fixture, 'event-title', 'Swim');
        save(fixture);

        expect(saved).toHaveBeenCalledTimes(1);
    });

    it('closes on Escape and on Cancel', () => {
        const fixture = create();
        const closed = vi.fn().mockName('closed');
        fixture.componentInstance.closed.subscribe(closed);

        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
        el(fixture, 'event-cancel')?.click();

        expect(closed).toHaveBeenCalledTimes(2);
    });

    it('lists every calendar in the Calendar select, and closes from the header X', () => {
        const fixture = create();
        const select = el(fixture, 'event-calendar') as HTMLSelectElement;

        expect(select.options.length).toBe(2);
        expect(select.options[0].textContent?.trim()).toBe('● Family');
        expect(select.options[1].textContent?.trim()).toBe('● Work');
        expect(select.options[0].style.color).not.toBe('');

        const closed = vi.fn().mockName('closed');
        fixture.componentInstance.closed.subscribe(closed);
        el(fixture, 'event-close')?.click();

        expect(closed).toHaveBeenCalledTimes(1);
    });
});
