import type { MockedObject } from "vitest";
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideHttpClient, withXhr } from '@angular/common/http';
import { provideHttpClientTesting } from '@angular/common/http/testing';
import { provideRouter } from '@angular/router';
import { of, throwError } from 'rxjs';
import { getTranslocoTestingModule } from '../../core/transloco/transloco-testing.module';
import { mockOf } from '../../testing/mock-of';
import { BonusService } from '../bonus/bonus.service';
import { BonusClaim, BonusTask } from '../bonus/bonus.models';
import { CalendarItem, CalendarRange } from '../calendar/calendar.models';
import { CalendarService } from '../calendar/calendar.service';
import { FamilyService, MemberDetail } from '../family/family.service';
import { Task } from '../tasks/tasks.models';
import { TasksService } from '../tasks/tasks.service';
import { DashboardComponent } from './dashboard.component';
import { ParentDashboard } from './dashboard.models';
import { DashboardService } from './dashboard.service';
import { WallDeviceView, WallDevicesService } from '../settings/wall-devices/wall-devices.service';

const member = {
    member_id: 'm1',
    member_name: 'Ava',
    member_colour: '#ff0000',
    avatar_id: null,
};

const dashboard: ParentDashboard = {
    date: '2026-10-09',
    timezone: 'UTC',
    summary: { today_total: 4, today_done: 1, overdue: 1, waiting_approval: 1, waiting_reward_requests: 1, points_week: 5 },
    today: [
        {
            ...member,
            task_id: 't1',
            title: 'Feed the cat',
            due_at: '2026-10-09T08:00:00Z',
            priority: 'normal',
            status: 'to_do',
        },
    ],
    people: [{ ...member, total: 4, done: 1, week_total: 6, week_done: 2, points_week: 5, overdue: 1 }],
    approvals: [
        {
            ...member,
            task_id: 't3',
            title: 'Empty dishwasher',
            completed_at: '2026-10-09T09:00:00Z',
            completed_late: false,
        },
    ],
    reward_requests: [
        {
            ...member,
            redemption_id: 'r1',
            reward_name: 'Pick dessert',
            reward_icon: '🍦',
            cost: 40,
            requested_at: '2026-10-09T18:40:00Z',
        },
    ],
    activity: [
        {
            ...member,
            action: 'task.completed',
            task_id: 't3',
            task_title: 'Empty dishwasher',
            created_at: '2026-10-09T09:00:00Z',
        },
    ],
};

const emptyDashboard: ParentDashboard = {
    date: '2026-10-09',
    timezone: 'UTC',
    summary: { today_total: 0, today_done: 0, overdue: 0, waiting_approval: 0, waiting_reward_requests: 0, points_week: 0 },
    today: [],
    people: [],
    approvals: [],
    reward_requests: [],
    activity: [],
};

function el(fixture: ComponentFixture<DashboardComponent>, testId: string): HTMLElement | null {
    return fixture.nativeElement.querySelector(`[data-testid="${testId}"]`);
}

/** Timed events, one an hour from 8am on the given day. */
function dayEvents(day: string, count: number): CalendarItem[] {
    return Array.from({ length: count }, (_, i) => {
        const hh = String(8 + i).padStart(2, '0');
        return {
            key: `ev-${day}-${i}`,
            kind: 'event',
            title: `Event ${i + 1}`,
            occurrence_date: day,
            starts_at: `${day}T${hh}:00:00Z`,
            ends_at: `${day}T${hh}:30:00Z`,
            all_day: false,
            time_zone: 'UTC',
            member_ids: [],
        } as CalendarItem;
    });
}

/** A timed event on the given day that starts at `start` and runs for an hour. */
function timed(key: string, day: string, start: Date, memberIds: string[] = [], title = key): CalendarItem {
    return {
        key,
        kind: 'event',
        title,
        occurrence_date: day,
        starts_at: start.toISOString(),
        ends_at: new Date(start.getTime() + 3600000).toISOString(),
        all_day: false,
        time_zone: 'UTC',
        member_ids: memberIds,
    } as CalendarItem;
}

/** A time `hours` from now, so a test event today is still ahead (negative: already over). */
function inHours(hours: number): Date {
    return new Date(Date.now() + hours * 3600000);
}

/** Timed events today, one an hour, all still ahead of now. */
function upcomingToday(count: number): CalendarItem[] {
    const today = new Date().toISOString().slice(0, 10);
    return Array.from({ length: count }, (_, i) => timed(`ev-${i}`, today, inHours(i + 1), [], `Event ${i + 1}`));
}

function familyMember(id: string, name: string): MemberDetail {
    return { id, display_name: name, colour: '#ff0000', avatar_id: null } as MemberDetail;
}

function titles(fixture: ComponentFixture<DashboardComponent>, testId: string): string[] {
    return Array.from(el(fixture, testId)?.querySelectorAll('.tt') ?? []).map((n) => n.textContent?.trim() ?? '');
}

function choose(fixture: ComponentFixture<DashboardComponent>, testId: string, value: string): void {
    const option = `${testId}-option-${value || 'all'}`;
    if (!el(fixture, option)) {
        (el(fixture, testId) as HTMLElement).click();
        fixture.detectChanges();
    }
    (el(fixture, option) as HTMLElement).click();
    fixture.detectChanges();
}

function menuLabels(fixture: ComponentFixture<DashboardComponent>, testId: string): string[] {
    return Array.from(fixture.nativeElement.querySelectorAll(`[data-testid^="${testId}-option-"]`) as NodeListOf<HTMLElement>).map((b) => b.textContent?.trim() ?? '');
}

function claim(overrides: Partial<BonusClaim>): BonusClaim {
    return {
        id: 'c1',
        bonus_task_id: 'b1',
        family_member_id: 'm1',
        status: 'claimed',
        claimed_at: '2026-10-09T16:02:00Z',
        released_at: null,
        completed_at: null,
        decided_at: null,
        decided_by_member_id: null,
        decision_note: null,
        version: 1,
        ...overrides,
    };
}

function bonusTask(overrides: Partial<BonusTask>): BonusTask {
    return {
        id: 'b1',
        template_id: null,
        title: 'Wash the car',
        description: null,
        points: 20,
        claim_mode: 'first_come',
        approval_required: true,
        day: '2026-10-09',
        show_from: null,
        ends_at: '2026-10-09T23:59:00Z',
        state: 'open',
        cancelled_at: null,
        version: 1,
        editable: false,
        eligible_member_ids: ['m1'],
        claims: [],
        ...overrides,
    };
}

describe('DashboardComponent', () => {
    let dashboards: MockedObject<DashboardService>;
    let tasks: MockedObject<TasksService>;
    let bonus: MockedObject<BonusService>;
    let calendar: MockedObject<CalendarService>;
    let family: MockedObject<FamilyService>;
    let wallDevices: MockedObject<WallDevicesService>;

    beforeEach(async () => {
        dashboards = mockOf<DashboardService>({
            parent: vi.fn().mockName("DashboardService.parent")
        });
        dashboards.parent.mockReturnValue(of(dashboard));
        tasks = mockOf<TasksService>({
            approve: vi.fn().mockName("TasksService.approve")
        });
        tasks.approve.mockReturnValue(of({} as Task));
        bonus = mockOf<BonusService>({
            list: vi.fn().mockName("BonusService.list")
        });
        bonus.list.mockReturnValue(of({ tasks: [] }));
        calendar = mockOf<CalendarService>({
            range: vi.fn().mockName("CalendarService.range")
        });
        calendar.range.mockReturnValue(of({ from: '', to: '', time_zone: 'UTC', items: [], calendars: [] } as CalendarRange));
        family = mockOf<FamilyService>({
            listMembers: vi.fn().mockName("FamilyService.listMembers")
        });
        family.listMembers.mockReturnValue(of({ members: [] }));
        wallDevices = mockOf<WallDevicesService>({
            list: vi.fn().mockName("WallDevicesService.list")
        });
        wallDevices.list.mockReturnValue(of([]));

        await TestBed.configureTestingModule({
            imports: [DashboardComponent, getTranslocoTestingModule()],
            providers: [
                provideRouter([]),
                provideHttpClient(withXhr()),
                provideHttpClientTesting(),
                { provide: DashboardService, useValue: dashboards },
                { provide: TasksService, useValue: tasks },
                { provide: BonusService, useValue: bonus },
                { provide: CalendarService, useValue: calendar },
                { provide: FamilyService, useValue: family },
                { provide: WallDevicesService, useValue: wallDevices },
            ],
        }).compileComponents();
    });

    function create(): ComponentFixture<DashboardComponent> {
        const fixture = TestBed.createComponent(DashboardComponent);
        fixture.detectChanges();
        return fixture;
    }

    it('renders the band, stats, people, approvals and activity', () => {
        const fixture = create();

        expect(dashboards.parent).toHaveBeenCalledTimes(1);
        expect(el(fixture, 'band-today')?.textContent).toContain('No events today.');
        expect(el(fixture, 'stat-done')?.textContent).toContain('1 / 4');
        expect(el(fixture, 'stat-done')?.textContent).toContain('25%');
        expect(el(fixture, 'stat-overdue')?.textContent).toContain('(1 due)');
        expect(el(fixture, 'stat-waiting')?.querySelector('.stat-value')?.textContent?.trim()).toBe('2');
        expect(el(fixture, 'people')?.querySelectorAll('[data-testid="person-row"]').length).toBe(1);
        expect(el(fixture, 'approvals')?.querySelectorAll('[data-testid="approval-row"]').length).toBe(1);
        expect(el(fixture, 'activity')?.querySelectorAll('[data-testid="activity-row"]').length).toBe(1);
        expect(el(fixture, 'activity')?.textContent).toContain('Ava finished Empty dishwasher');
    });

    it('lists each person overdue copies in the Overdue card, even when they are not due today', () => {
        const ben = { ...member, member_id: 'm2', member_name: 'Ben' };
        dashboards.parent.mockReturnValue(of({
            ...dashboard,
            summary: { ...dashboard.summary, overdue: 3 },
            today: [],
            people: [
                { ...member, total: 0, done: 0, week_total: 0, week_done: 0, points_week: 0, overdue: 2 },
                { ...ben, total: 0, done: 0, week_total: 0, week_done: 0, points_week: 0, overdue: 1 },
            ],
        }));
        const fixture = create();

        expect(el(fixture, 'stat-overdue')?.querySelector('.stat-value')?.textContent?.trim()).toBe('3');
        expect(el(fixture, 'stat-overdue')?.textContent).toContain('(2 due)');
        expect(el(fixture, 'stat-overdue')?.textContent).toContain('(1 due)');
        expect(el(fixture, 'stat-overdue')?.querySelectorAll('.stat-chip').length).toBe(2);
        expect(el(fixture, 'stat-overdue')?.querySelectorAll('.meter i').length).toBe(2);
    });

    it('shows avatars instead of names for who an event is for', () => {
        const today = new Date().toISOString().slice(0, 10);
        calendar.range.mockReturnValue(of({
            from: today,
            to: today,
            time_zone: 'UTC',
            items: [timed('Swim', today, inHours(1), ['m1']), timed('Dinner', today, inHours(2))],
        } as CalendarRange));
        family.listMembers.mockReturnValue(of({ members: [{ id: 'm1', display_name: 'Ava', colour: '#ff0000', avatar_id: null } as MemberDetail] }));
        const fixture = create();

        const rows = el(fixture, 'band-today')?.querySelectorAll('.ab-ev') ?? [];
        expect(rows.length).toBe(2);
        expect(rows[0].querySelectorAll('.faces app-avatar').length).toBe(1);
        expect(rows[0].querySelector('.who')?.textContent).not.toContain('Ava');
        expect(rows[1].querySelector('.face.everyone')).not.toBeNull();
    });

    it('opens a view-only event popup in place when a band row is clicked', () => {
        const today = new Date().toISOString().slice(0, 10);
        calendar.range.mockReturnValue(of({ from: today, to: today, time_zone: 'UTC', items: [timed('Swim', today, inHours(1), ['m1'])], calendars: [] } as CalendarRange));
        family.listMembers.mockReturnValue(of({ members: [{ id: 'm1', display_name: 'Ava', colour: '#ff0000', avatar_id: null } as MemberDetail] }));
        const fixture = create();

        const row = el(fixture, 'band-today')?.querySelector<HTMLButtonElement>('.ab-ev');
        expect(row).toBeTruthy();
        row?.click();
        fixture.detectChanges();

        expect(el(fixture, 'event-detail')).not.toBeNull();
        expect(el(fixture, 'detail-edit')).toBeNull();
        expect(el(fixture, 'detail-delete')).toBeNull();
    });

    it('caps today at five rows, with More for the rest and the chip counting all', () => {
        const today = new Date().toISOString().slice(0, 10);
        calendar.range.mockReturnValue(of({ from: today, to: today, time_zone: 'UTC', items: upcomingToday(7) } as CalendarRange));
        const fixture = create();

        expect(el(fixture, 'band-today')?.querySelectorAll('.ab-ev').length).toBe(5);
        expect(el(fixture, 'band-today')?.querySelector('.badge')?.textContent).toContain('7 events');
        expect(el(fixture, 'band-today-more')?.textContent).toContain('More (2)');
        expect(el(fixture, 'band-coming-more')).toBeNull();
    });

    it('caps coming up at five rows and shows More for the rest', () => {
        const tomorrow = new Date(Date.now() + 86400000).toISOString().slice(0, 10);
        calendar.range.mockReturnValue(of({ from: tomorrow, to: tomorrow, time_zone: 'UTC', items: dayEvents(tomorrow, 8), calendars: [] } as CalendarRange));
        const fixture = create();

        expect(el(fixture, 'band-coming')?.querySelectorAll('.ab-ev').length).toBe(5);
        expect(el(fixture, 'band-coming-more')?.textContent).toContain('More (3)');
        expect(el(fixture, 'band-today-more')).toBeNull();
    });

    it('opens the full list from More, and a row in it opens the view-only popup over the list', () => {
        const today = new Date().toISOString().slice(0, 10);
        calendar.range.mockReturnValue(of({ from: today, to: today, time_zone: 'UTC', items: upcomingToday(7) } as CalendarRange));
        const fixture = create();

        el(fixture, 'band-today-more')?.click();
        fixture.detectChanges();
        expect(el(fixture, 'band-more')).not.toBeNull();
        expect(el(fixture, 'band-more-list')?.querySelectorAll('.ab-ev').length).toBe(7);

        el(fixture, 'band-more-list')?.querySelectorAll<HTMLButtonElement>('.ab-ev')[6].click();
        fixture.detectChanges();
        expect(el(fixture, 'event-detail')).not.toBeNull();
        expect(el(fixture, 'detail-edit')).toBeNull();

        el(fixture, 'band-more-close')?.click();
        fixture.detectChanges();
        expect(el(fixture, 'band-more')).toBeNull();
    });

    it('shows ended events dimmed when the list has room', () => {
        const today = new Date().toISOString().slice(0, 10);
        calendar.range.mockReturnValue(of({
            from: today,
            to: today,
            time_zone: 'UTC',
            items: [
                timed('done-early', today, inHours(-3)),
                timed('done-late', today, inHours(-2)),
                timed('soon', today, inHours(1)),
                timed('later', today, inHours(2)),
                timed('last', today, inHours(3)),
            ],
        } as CalendarRange));
        const fixture = create();

        expect(titles(fixture, 'band-today')).toEqual(['done-early', 'done-late', 'soon', 'later', 'last']);
        const rows = el(fixture, 'band-today')?.querySelectorAll('.ab-ev') ?? [];
        expect(rows[0].classList).toContain('past');
        expect(rows[1].classList).toContain('past');
        expect(rows[2].classList).not.toContain('past');
        expect(el(fixture, 'band-today')?.querySelector('.ab-ev.next .tt')?.textContent?.trim()).toBe('soon');
        expect(el(fixture, 'band-today-more')).toBeNull();
    });

    it('hides the ended events first when the list is full, and More counts them', () => {
        const today = new Date().toISOString().slice(0, 10);
        calendar.range.mockReturnValue(of({
            from: today,
            to: today,
            time_zone: 'UTC',
            items: [
                timed('done-early', today, inHours(-3)),
                timed('done-late', today, inHours(-2)),
                timed('soon', today, inHours(1)),
                timed('later', today, inHours(2)),
                timed('last', today, inHours(3)),
                timed('fourth', today, inHours(4)),
                timed('fifth', today, inHours(5)),
            ],
        } as CalendarRange));
        const fixture = create();

        expect(titles(fixture, 'band-today')).toEqual(['soon', 'later', 'last', 'fourth', 'fifth']);
        expect(el(fixture, 'band-today-more')?.textContent).toContain('More (2)');

        el(fixture, 'band-today-more')?.click();
        fixture.detectChanges();
        const full = el(fixture, 'band-more-list')?.querySelectorAll('.ab-ev') ?? [];
        expect(full.length).toBe(7);
        expect(full[0].classList).toContain('past');
        expect(full[6].classList).not.toContain('past');
    });

    it('filters each card by person and keeps events for everyone', () => {
        const today = new Date().toISOString().slice(0, 10);
        const tomorrow = new Date(Date.now() + 86400000).toISOString().slice(0, 10);
        calendar.range.mockReturnValue(of({
            from: today,
            to: tomorrow,
            time_zone: 'UTC',
            items: [
                timed('ava-today', today, inHours(1), ['m1']),
                timed('noah-today', today, inHours(2), ['m2']),
                timed('all-today', today, inHours(3)),
                timed('ava-tomorrow', tomorrow, inHours(25), ['m1']),
                timed('noah-tomorrow', tomorrow, inHours(26), ['m2']),
            ],
        } as CalendarRange));
        family.listMembers.mockReturnValue(of({ members: [familyMember('m1', 'Ava'), familyMember('m2', 'Noah')] }));
        const fixture = create();

        choose(fixture, 'band-today-person', 'm1');
        expect(titles(fixture, 'band-today')).toEqual(['ava-today', 'all-today']);
        expect(titles(fixture, 'band-coming')).toEqual(['ava-tomorrow', 'noah-tomorrow']);

        choose(fixture, 'band-coming-person', 'm2');
        expect(titles(fixture, 'band-coming')).toEqual(['noah-tomorrow']);
        expect(titles(fixture, 'band-today')).toEqual(['ava-today', 'all-today']);

        choose(fixture, 'band-today-person', '');
        expect(titles(fixture, 'band-today')).toEqual(['ava-today', 'noah-today', 'all-today']);
    });

    it('filters a card by calendar', () => {
        const today = new Date().toISOString().slice(0, 10);
        calendar.range.mockReturnValue(of({
            from: today,
            to: today,
            time_zone: 'UTC',
            items: [
                { ...timed('family-event', today, inHours(1)), calendar_id: 'family', calendar_name: 'Family' } as CalendarItem,
                { ...timed('school-event', today, inHours(2)), calendar_id: 'school', calendar_name: 'School' } as CalendarItem,
            ],
        } as CalendarRange));
        const fixture = create();

        (el(fixture, 'band-today-calendar') as HTMLElement).click();
        fixture.detectChanges();
        expect(menuLabels(fixture, 'band-today-calendar')).toEqual(['All calendars', 'Family', 'School']);

        choose(fixture, 'band-today-calendar', 'school');
        expect(titles(fixture, 'band-today')).toEqual(['school-event']);
        expect(el(fixture, 'band-today-calendar')?.classList.contains('is-active')).toBe(true);

        choose(fixture, 'band-today-calendar', 'family');
        expect(titles(fixture, 'band-today')).toEqual(['family-event', 'school-event']);
        expect(menuLabels(fixture, 'band-today-calendar')).toEqual(['All calendars', 'Family', 'School']);

        choose(fixture, 'band-today-calendar', 'school');
        expect(titles(fixture, 'band-today')).toEqual(['family-event']);

        choose(fixture, 'band-today-calendar', 'family');
        expect(titles(fixture, 'band-today')).toEqual(['family-event', 'school-event']);
        expect(el(fixture, 'band-today-calendar')?.classList.contains('is-active')).toBe(false);

        choose(fixture, 'band-today-calendar', 'school');
        choose(fixture, 'band-today-calendar', 'all');
        expect(titles(fixture, 'band-today')).toEqual(['family-event', 'school-event']);
        expect(el(fixture, 'band-today-calendar')?.classList.contains('is-active')).toBe(false);
    });

    it('lets a card pick several people, and All clears them', () => {
        const today = new Date().toISOString().slice(0, 10);
        calendar.range.mockReturnValue(of({
            from: today,
            to: today,
            time_zone: 'UTC',
            items: [
                timed('ava-today', today, inHours(1), ['m1']),
                timed('noah-today', today, inHours(2), ['m2']),
                timed('all-today', today, inHours(3)),
            ],
        } as CalendarRange));
        family.listMembers.mockReturnValue(of({ members: [familyMember('m1', 'Ava'), familyMember('m2', 'Noah')] }));
        const fixture = create();

        choose(fixture, 'band-today-person', 'm1');
        choose(fixture, 'band-today-person', 'm2');
        expect(titles(fixture, 'band-today')).toEqual(['ava-today', 'noah-today', 'all-today']);
        expect(el(fixture, 'band-today-person')?.classList.contains('is-active')).toBe(true);

        choose(fixture, 'band-today-person', 'm1');
        expect(titles(fixture, 'band-today')).toEqual(['noah-today', 'all-today']);

        choose(fixture, 'band-today-person', '');
        expect(titles(fixture, 'band-today')).toEqual(['ava-today', 'noah-today', 'all-today']);
        expect(el(fixture, 'band-today-person')?.classList.contains('is-active')).toBe(false);
    });

    it('says a filter matches nothing, with the filtered empty state', () => {
        const today = new Date().toISOString().slice(0, 10);
        calendar.range.mockReturnValue(of({ from: today, to: today, time_zone: 'UTC', items: [timed('ava-swim', today, inHours(1), ['m1'])], calendars: [] } as CalendarRange));
        family.listMembers.mockReturnValue(of({ members: [familyMember('m1', 'Ava'), familyMember('m2', 'Noah')] }));
        const fixture = create();

        choose(fixture, 'band-today-person', 'm2');
        expect(el(fixture, 'band-today')?.textContent).toContain('No events match these filters.');
        expect(el(fixture, 'band-today')?.textContent).not.toContain('No events today.');

        choose(fixture, 'band-today-person', '');
        expect(titles(fixture, 'band-today')).toEqual(['ava-swim']);
    });

    it('keeps an ended event on the card when nothing is left today, with no More', () => {
        const today = new Date().toISOString().slice(0, 10);
        calendar.range.mockReturnValue(of({ from: today, to: today, time_zone: 'UTC', items: [timed('ended', today, inHours(-3))], calendars: [] } as CalendarRange));
        const fixture = create();

        expect(titles(fixture, 'band-today')).toEqual(['ended']);
        expect(el(fixture, 'band-today')?.querySelector('.ab-ev')?.classList).toContain('past');
        expect(el(fixture, 'band-today')?.textContent).not.toContain('have passed');
        expect(el(fixture, 'band-today-more')).toBeNull();
    });

    it('shows empty states when the family has nothing yet', () => {
        dashboards.parent.mockReturnValue(of(emptyDashboard));
        const fixture = create();

        expect(el(fixture, 'band-today')?.textContent).toContain('No events today.');
        expect(el(fixture, 'band-coming')?.textContent).toContain('Nothing else in the next 7 days.');
        expect(el(fixture, 'people')?.textContent).toContain('Add family members');
        expect(el(fixture, 'approvals-empty')?.textContent).toContain('Nothing is waiting for approval.');
        expect(el(fixture, 'activity-empty')?.textContent).toContain('No activity yet.');
    });

    it('shows an alert when loading fails and retry reloads the dashboard', () => {
        dashboards.parent.mockReturnValue(throwError(() => new Error('boom')));
        const fixture = create();

        const alert = el(fixture, 'dash-error');
        expect(alert?.querySelector('[role="alert"]')).not.toBeNull();

        dashboards.parent.mockReturnValue(of(dashboard));
        alert?.querySelector('button')?.click();
        fixture.detectChanges();

        expect(dashboards.parent).toHaveBeenCalledTimes(2);
        expect(el(fixture, 'dash-error')).toBeNull();
        expect(el(fixture, 'band-today')?.textContent).toContain('No events today.');
    });

    it('counts reward requests in the waiting total and links the tile to approvals', () => {
        const fixture = create();

        const tile = el(fixture, 'stat-waiting');
        expect(tile?.getAttribute('href')).toBe('/approvals');
        expect(tile?.textContent).toContain('Task (1)');
        expect(tile?.textContent).toContain('Rewards (1)');
        expect(el(fixture, 'approvals-all')?.textContent).toContain('See all 2');
        expect(el(fixture, 'approvals-all')?.getAttribute('href')).toBe('/approvals');
    });

    it('lists reward requests with a review link', () => {
        const fixture = create();

        const row = el(fixture, 'reward-row');
        expect(row?.textContent).toContain('Reward: Pick dessert');
        expect(row?.textContent).toContain('40 points');
        expect(el(fixture, 'review-btn')?.getAttribute('href')).toBe('/approvals');
    });

    it('shows points earned this week on the tile and per person', () => {
        const fixture = create();

        expect(el(fixture, 'stat-points')?.querySelector('.stat-value')?.textContent?.trim()).toBe('5');
        expect(el(fixture, 'stat-points')?.textContent).toContain('Ava 5');
        expect(el(fixture, 'person-points')?.textContent).toContain('5');
    });

    it('shows a status chip per person from their progress and overdue copies', () => {
        const ben = { ...member, member_id: 'm2', member_name: 'Ben' };
        dashboards.parent.mockReturnValue(of({
            ...dashboard,
            today: [
                {
                    ...member,
                    task_id: 't9',
                    title: 'Late copy',
                    due_at: '2000-01-01T08:00:00Z',
                    priority: 'normal',
                    status: 'to_do',
                },
            ],
            people: [
                { ...member, total: 2, done: 1, week_total: 2, week_done: 1, points_week: 0, overdue: 0 },
                { ...ben, total: 2, done: 2, week_total: 2, week_done: 2, points_week: 0, overdue: 0 },
                { ...ben, member_id: 'm3', member_name: 'Cy', total: 0, done: 0, week_total: 0, week_done: 0, points_week: 0, overdue: 0 },
            ],
        }));
        const fixture = create();

        const chips = fixture.nativeElement.querySelectorAll('[data-testid="person-status"]') as NodeListOf<HTMLElement>;
        expect(chips.length).toBe(2);
        expect(chips[0].textContent).toContain('1 overdue');
        expect(chips[0].classList).toContain('err');
        expect(chips[1].textContent).toContain('Done for today');
        expect(chips[1].classList).toContain('teal');
    });

    it('shows on track for a person with items still to do and nothing overdue', () => {
        dashboards.parent.mockReturnValue(of({
            ...dashboard,
            today: [],
            people: [{ ...member, total: 3, done: 1, week_total: 3, week_done: 1, points_week: 0, overdue: 0 }],
        }));
        const fixture = create();

        const chip = el(fixture, 'person-status');
        expect(chip?.textContent).toContain('All on track');
        expect(chip?.classList).toContain('ok');
    });

    it('shows a status pill on each activity row', () => {
        dashboards.parent.mockReturnValue(of({
            ...dashboard,
            activity: [{ ...member, action: 'task.skipped', task_id: 't4', task_title: 'Recycling', created_at: '2026-10-09T10:00:00Z' }],
        }));
        const fixture = create();

        const row = el(fixture, 'activity')?.querySelector('[data-testid="activity-row"]');
        expect(row?.textContent).toContain('Missed');
        expect(row?.querySelector('.badge')?.classList).toContain('err');
    });

    it('switches Today by person between today and this week counts', () => {
        const fixture = create();
        const people = (): HTMLElement | null => el(fixture, 'people');
        const toggle = (testId: string): void => {
            (el(fixture, testId) as HTMLButtonElement).click();
            fixture.detectChanges();
        };

        expect(el(fixture, 'scope-today')?.getAttribute('aria-pressed')).toBe('true');
        expect(el(fixture, 'person-count')?.textContent).toContain('1 of 4 done');
        expect(el(fixture, 'person-status')).not.toBeNull();

        toggle('scope-week');

        expect(el(fixture, 'scope-week')?.getAttribute('aria-pressed')).toBe('true');
        expect(el(fixture, 'scope-week')?.classList).toContain('on');
        expect(el(fixture, 'scope-today')?.getAttribute('aria-pressed')).toBe('false');
        expect(el(fixture, 'person-count')?.textContent).toContain('2 of 6 done');
        expect(people()?.querySelector('.bar')?.getAttribute('aria-valuemax')).toBe('6');
        expect(people()?.querySelector('.bar')?.getAttribute('aria-valuenow')).toBe('2');
        expect(el(fixture, 'person-status')).toBeNull();
        expect(el(fixture, 'person-points')?.textContent).toContain('5');

        toggle('scope-today');

        expect(el(fixture, 'person-count')?.textContent).toContain('1 of 4 done');
        expect(el(fixture, 'person-status')).not.toBeNull();
    });

    it('shows nothing due this week for a person with no copies in the week', () => {
        dashboards.parent.mockReturnValue(of({
            ...dashboard,
            people: [{ ...member, total: 0, done: 0, week_total: 0, week_done: 0, points_week: 0, overdue: 0 }],
        }));
        const fixture = create();
        (el(fixture, 'scope-week') as HTMLButtonElement).click();
        fixture.detectChanges();

        expect(el(fixture, 'person-count')?.textContent).toContain('Nothing due this week');
    });

    it('shows the kitchen tablet as not paired yet', () => {
        const fixture = create();

        expect(el(fixture, 'kitchen-tablet')?.textContent).toContain('No kitchen tablet is paired yet.');
    });

    it('shows the paired kitchen tablet by name', () => {
        const paired: WallDeviceView = {
            id: 'w1', name: 'Kitchen wall', status: 'online', paired_at: '2026-10-03T10:00:00Z', last_seen_at: null,
        };
        wallDevices.list.mockReturnValue(of([{ ...paired, id: 'w0', name: 'Old wall', paired_at: null, status: 'pending' }, paired]));

        const fixture = create();

        expect(el(fixture, 'kitchen-tablet')?.textContent).toContain('Kitchen wall is paired');
        expect(el(fixture, 'kitchen-tablet')?.textContent).not.toContain('No kitchen tablet');
    });

    it('hides the kitchen tablet card when the wall list fails', () => {
        wallDevices.list.mockReturnValue(throwError(() => new Error('boom')));

        const fixture = create();

        expect(el(fixture, 'kitchen-tablet')).toBeNull();
    });

    it('approves a copy and reloads the dashboard', () => {
        const fixture = create();

        (el(fixture, 'approve-btn') as HTMLButtonElement).click();
        fixture.detectChanges();

        expect(tasks.approve).toHaveBeenCalledWith('t3', 'm1');
        expect(dashboards.parent).toHaveBeenCalledTimes(2);
    });

    it('shows an action error when approval fails', () => {
        tasks.approve.mockReturnValue(throwError(() => new Error('boom')));
        const fixture = create();

        (el(fixture, 'approve-btn') as HTMLButtonElement).click();
        fixture.detectChanges();

        const alert = el(fixture, 'dash-action-error');
        expect(alert?.querySelector('[role="alert"]')).not.toBeNull();
        expect(dashboards.parent).toHaveBeenCalledTimes(1);
    });

    it('shows today\'s bonus tasks with their status and hides cancelled ones', async () => {
        bonus.list.mockReturnValue(of({
            tasks: [
                bonusTask({
                    id: 'b1',
                    title: 'Wash the car',
                    points: 20,
                    claims: [claim({ id: 'c1', family_member_id: 'm1', status: 'claimed' })],
                }),
                bonusTask({
                    id: 'b2',
                    title: 'Fold the laundry',
                    points: 10,
                    claims: [claim({ id: 'c2', family_member_id: 'm1', status: 'submitted' })],
                }),
                bonusTask({ id: 'b3', title: 'Water the garden', points: 8, state: 'cancelled' }),
            ],
        }));
        const fixture = create();

        expect(bonus.list).toHaveBeenCalledWith('today');
        const rows = fixture.nativeElement.querySelectorAll('[data-testid="dash-bonus-row"]') as NodeListOf<HTMLElement>;
        expect(rows.length).toBe(2);
        expect(rows[0].textContent).toContain('Wash the car');
        expect(rows[0].textContent).toContain('+20');
        expect(rows[0].textContent).toContain('In progress');
        expect(rows[1].textContent).toContain('Fold the laundry');
        expect(rows[1].textContent).toContain('1 to approve');
        expect(fixture.nativeElement.textContent).not.toContain('Water the garden');
        expect(el(fixture, 'dash-bonus-publish')?.getAttribute('href')).toBe('/bonus/new');
    });

    it('shows the empty message when no bonus task is set for today', () => {
        const fixture = create();

        expect(el(fixture, 'dash-bonus-empty')?.textContent).toContain('No bonus tasks today.');
    });

    it('shows a bonus error without hiding the rest of the dashboard', () => {
        bonus.list.mockReturnValue(throwError(() => new Error('boom')));
        const fixture = create();

        expect(el(fixture, 'dash-bonus-error')?.textContent).toContain('Bonus tasks could not load.');
        expect(el(fixture, 'band-today')?.textContent).toContain('No events today.');
    });
});
