import type { MockedObject } from "vitest";
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { of, throwError } from 'rxjs';
import { AuthService } from '../../core/auth/auth.service';
import { getTranslocoTestingModule } from '../../core/transloco/transloco-testing.module';
import { AvatarService } from '../../shared/avatar/avatar.service';
import { CalendarItem, CalendarRange } from '../calendar/calendar.models';
import { CalendarService } from '../calendar/calendar.service';
import { MyTaskItem, MyTasksResponse } from '../my-tasks/my-tasks.models';
import { MyTasksService } from '../my-tasks/my-tasks.service';
import { BadgesService } from '../badges/badges.service';
import { MyBadges } from '../badges/badges.models';
import { BonusService } from '../bonus/bonus.service';
import { MyPointsResponse, Reward } from '../rewards/rewards.models';
import { RewardsService } from '../rewards/rewards.service';
import { TasksService } from '../tasks/tasks.service';
import { WeatherService } from '../weather/weather.service';
import { TodayComponent } from './today.component';
import { mockOf } from '../../testing/mock-of';

function item(over: Partial<MyTaskItem> = {}): MyTaskItem {
    return {
        task_id: 'o1',
        title: 'Feed the cat',
        category_emoji: null,
        category_colour: null,
        part_of_day: 'morning',
        due_date: '2026-10-09',
        due_at: '2026-10-09T08:00:00Z',
        expires_at: null,
        points: 2,
        approval_required: false,
        status: 'to_do',
        past_due: false,
        completed_at: null,
        completed_late: false,
        late_points_percent: null,
        penalty_value: null,
        penalty_unit: null,
        penalty_points: null,
        decided_at: null,
        decided_by_name: null,
        version: 1,
        ...over,
    };
}

const day: MyTasksResponse = {
    date: '2026-10-09',
    timezone: 'UTC',
    now: '2026-10-09T07:00:00Z',
    streak_days: 3,
    summary: {
        today_total: 2,
        today_done: 1,
        next_up: item({ task_id: 'o2', title: 'Homework', part_of_day: 'after_school', due_at: '2026-10-09T15:00:00Z' }),
    },
    today: [item(), item({ task_id: 'o2', title: 'Homework', part_of_day: 'after_school', due_at: '2026-10-09T15:00:00Z' })],
    catch_up: [],
    waiting: [],
    expired: [],
};

const emptyDay: MyTasksResponse = {
    ...day,
    streak_days: 0,
    summary: { today_total: 0, today_done: 0, next_up: null },
    today: [],
};

function reward(over: Partial<Reward> = {}): Reward {
    return {
        id: 'r1',
        name: 'Movie night',
        icon: '🍿',
        cost: 50,
        status: 'active',
        archived: false,
        in_window: true,
        version: 1,
        ...over,
    } as Reward;
}

function points(over: Partial<MyPointsResponse> = {}): MyPointsResponse {
    return {
        balance: 40,
        reserved: 0,
        today: 6,
        week: [0, 0, 0, 0, 0, 0, 6],
        tasks_done: [0, 0, 0, 0, 0, 0, 0],
        tasks_missed: [0, 0, 0, 0, 0, 0, 0],
        bonus: [0, 0, 0, 0, 0, 0, 0],
        week_start: '2026-10-05',
        timezone: 'UTC',
        ...over,
    } as MyPointsResponse;
}

function el(fixture: ComponentFixture<TodayComponent>, testId: string): HTMLElement | null {
    return fixture.nativeElement.querySelector(`[data-testid="${testId}"]`);
}

function calendarItem(over: Partial<CalendarItem> = {}): CalendarItem {
    return {
        key: 'piano',
        kind: 'event',
        title: 'Piano lesson',
        occurrence_date: '2026-10-09',
        starts_at: '2026-10-09T16:00:00Z',
        ends_at: '2026-10-09T17:00:00Z',
        all_day: false,
        time_zone: 'UTC',
        member_ids: ['ava'],
        ...over,
    };
}

function calendarDay(items: CalendarItem[]): CalendarRange {
    return { from: '2026-10-09', to: '2026-10-09', time_zone: 'UTC', items, calendars: [] };
}

function badgesOn(over: Partial<MyBadges> = {}): MyBadges {
    return {
        enabled: true,
        show_on_kid_dashboard: true,
        earned_count: 2,
        total_count: 24,
        groups: [
            {
                group: 'streaks',
                earned: 1,
                total: 1,
                badges: [
                    { key: 's3', earned: true, earned_at: '2026-10-01T08:00:00Z', progress: 3, target: 3 },
                    { key: 's7', earned: false, earned_at: null, progress: 3, target: 7 },
                    { key: 's14', earned: false, earned_at: null, progress: 3, target: 14 },
                ],
            },
        ],
        from_parents: [],
        ...over,
    };
}

describe('TodayComponent', () => {
    let service: MockedObject<MyTasksService>;
    let badges: MockedObject<BadgesService>;
    let bonus: MockedObject<BonusService>;
    let tasks: MockedObject<TasksService>;
    let rewards: MockedObject<RewardsService>;
    let calendar: MockedObject<CalendarService>;
    let weather: MockedObject<WeatherService>;

    beforeEach(async () => {
        service = mockOf<MyTasksService>({
            mine: vi.fn().mockName("MyTasksService.mine")
        });
        service.mine.mockReturnValue(of(day));
        badges = mockOf<BadgesService>({
            mine: vi.fn().mockName("BadgesService.mine")
        });
        badges.mine.mockReturnValue(of(badgesOn()));
        bonus = mockOf<BonusService>({
            list: vi.fn().mockName("BonusService.list"),
            claim: vi.fn().mockName("BonusService.claim"),
            release: vi.fn().mockName("BonusService.release"),
            complete: vi.fn().mockName("BonusService.complete")
        });
        bonus.list.mockReturnValue(of({ tasks: [] }));
        tasks = mockOf<TasksService>({
            complete: vi.fn().mockName("TasksService.complete")
        });
        rewards = mockOf<RewardsService>({
            list: vi.fn().mockName("RewardsService.list"),
            myPoints: vi.fn().mockName("RewardsService.myPoints")
        });
        rewards.myPoints.mockReturnValue(of(points()));
        rewards.list.mockReturnValue(of({ rewards: [reward({ id: 'r0', name: 'Sweets', cost: 30 }), reward()] }));
        calendar = mockOf<CalendarService>({
            range: vi.fn().mockName("CalendarService.range")
        });
        calendar.range.mockReturnValue(of(calendarDay([calendarItem(), calendarItem({ key: 'trip', title: 'Field trip', all_day: true })])));
        weather = mockOf<WeatherService>({
            forecast: vi.fn().mockName("WeatherService.forecast")
        });
        weather.forecast.mockReturnValue(of({ configured: false, location_name: null, temperature_unit: 'c', animation: 'calm', forecast: null }));

        await TestBed.configureTestingModule({
            imports: [TodayComponent, getTranslocoTestingModule()],
            providers: [
                provideRouter([]),
                { provide: MyTasksService, useValue: service },
                { provide: BadgesService, useValue: badges },
                { provide: BonusService, useValue: bonus },
                { provide: TasksService, useValue: tasks },
                { provide: RewardsService, useValue: rewards },
                { provide: CalendarService, useValue: calendar },
                { provide: AuthService, useValue: { currentUser: () => ({ user: { display_name: 'Ava' }, family: null }) } },
                { provide: WeatherService, useValue: weather },
                { provide: AvatarService, useValue: {
                        url: vi.fn().mockName("AvatarService.url")
                    } },
            ],
        }).compileComponents();
    });

    function create(): ComponentFixture<TodayComponent> {
        const fixture = TestBed.createComponent(TodayComponent);
        fixture.detectChanges();
        return fixture;
    }

    it('shows the streak, the hero and the next task', () => {
        const fixture = create();
        expect(el(fixture, 'today-streak')?.textContent).toContain('3');
        expect(el(fixture, 'today-hero')).not.toBeNull();
        expect(el(fixture, 'today-next-up')?.textContent).toContain('Homework');
    });

    it('greets the child by time of day with an exclamation mark', () => {
        const fixture = create();
        const heading = (fixture.nativeElement as HTMLElement).querySelector('h1')?.textContent ?? '';
        expect(heading).toMatch(/Good (morning|afternoon|evening), Ava!/);
    });

    it('lists today open copies as task cards and shows no tomorrow section', () => {
        const fixture = create();
        expect(el(fixture, 'today-list')?.querySelectorAll('app-today-task').length).toBe(2);
        expect(el(fixture, 'today-tomorrow')).toBeNull();
        expect(el(fixture, 'today-catch-up')).toBeNull();
        expect(el(fixture, 'today-all-done')).toBeNull();
    });

    it('shows the all done card when everything due today is finished', () => {
        service.mine.mockReturnValue(of({ ...day, today: [], summary: { today_total: 2, today_done: 2, next_up: null } }));
        const fixture = create();
        expect(el(fixture, 'today-all-done')?.textContent).toContain('All done for today!');
        expect(el(fixture, 'today-all-done')?.textContent).toContain('Ava');
        expect(el(fixture, 'today-list')).toBeNull();
    });

    it('keeps the list instead of the all done card while a catch up copy is open', () => {
        service.mine.mockReturnValue(of({ ...day, today: [], catch_up: [item({ task_id: 'c1', title: 'Bins' })], summary: { today_total: 2, today_done: 2, next_up: null } }));
        const fixture = create();
        expect(el(fixture, 'today-all-done')).toBeNull();
        expect(el(fixture, 'today-catch-up')?.querySelectorAll('app-task-row').length).toBe(1);
    });


    it('shows the kid bonus section above today list', () => {
        const fixture = create();
        expect(bonus.list).toHaveBeenCalledWith('today');
        expect(el(fixture, 'kid-bonus')).not.toBeNull();
    });

    it('shows the empty state when nothing is due today', () => {
        service.mine.mockReturnValue(of(emptyDay));
        const fixture = create();
        expect(el(fixture, 'today-empty')).not.toBeNull();
        expect(el(fixture, 'today-tomorrow')).toBeNull();
    });

    it('shows the load error screen with a retry when the load fails', () => {
        service.mine.mockReturnValue(throwError(() => ({ code: 'server', status: 500 })));
        const fixture = create();
        expect(el(fixture, 'today-loading')).toBeNull();
        expect(el(fixture, 'today-error')?.querySelector('app-load-error')).not.toBeNull();
        service.mine.mockReturnValue(of(day));
        (el(fixture, 'load-error-retry') as HTMLButtonElement).click();
        fixture.detectChanges();
        expect(service.mine).toHaveBeenCalledTimes(2);
        expect(el(fixture, 'today-error')).toBeNull();
        expect(el(fixture, 'today-list')).not.toBeNull();
    });

    it('shows the badges pill when badges are on and the kid showcase is on', () => {
        const fixture = create();
        const pill = el(fixture, 'today-badges');
        expect(pill).not.toBeNull();
        expect(pill?.getAttribute('href')).toContain('/me/badges');
        expect(pill?.textContent).toContain('2');
    });

    it('hides the badges pill when the kid showcase is off', () => {
        badges.mine.mockReturnValue(of(badgesOn({ show_on_kid_dashboard: false })));
        const fixture = create();
        expect(el(fixture, 'today-badges')).toBeNull();
        expect(el(fixture, 'today-streak')).not.toBeNull();
    });

    it('hides the badges pill when badges are switched off', () => {
        badges.mine.mockReturnValue(of(badgesOn({ enabled: false })));
        const fixture = create();
        expect(el(fixture, 'today-badges')).toBeNull();
    });

    it('shows the next streak badge with the days left', () => {
        const fixture = create();
        const footer = el(fixture, 'today-next-badge');
        expect(footer?.textContent).toContain('Next badge: 7 day streak');
        expect(footer?.textContent).toContain('4 more days');
        expect(footer?.querySelector('[role="progressbar"]')?.getAttribute('aria-valuenow')).toBe('43');
    });

    it('says 1 more day when one day is left to the next badge', () => {
        service.mine.mockReturnValue(of({ ...day, streak_days: 6 }));
        const fixture = create();
        expect(el(fixture, 'today-next-badge')?.textContent).toContain('1 more day');
    });

    it('hides the next badge when the kid showcase is off or badges are off', () => {
        badges.mine.mockReturnValue(of(badgesOn({ show_on_kid_dashboard: false })));
        expect(el(create(), 'today-next-badge')).toBeNull();
        badges.mine.mockReturnValue(of(badgesOn({ enabled: false })));
        expect(el(create(), 'today-next-badge')).toBeNull();
    });

    it('hides the next badge when the streak has passed every badge', () => {
        service.mine.mockReturnValue(of({ ...day, streak_days: 20 }));
        const fixture = create();
        expect(el(fixture, 'today-next-badge')).toBeNull();
    });

    it('shows the all done hero with the streak safe line when nothing is left today', () => {
        service.mine.mockReturnValue(of({ ...day, today: [], summary: { today_total: 2, today_done: 2, next_up: null } }));
        const fixture = create();
        expect(el(fixture, 'today-hero')?.textContent).toContain('All done!');
        expect(el(fixture, 'today-hero')?.textContent).toContain('Streak safe. See you tomorrow!');
        expect(el(fixture, 'today-everything-done')?.textContent).toContain('Everything done');
        expect(el(fixture, 'today-next-up')).toBeNull();
    });

    it('shows the all done hero on an empty day too', () => {
        service.mine.mockReturnValue(of(emptyDay));
        const fixture = create();
        expect(el(fixture, 'today-hero')?.textContent).toContain('All done!');
    });

    it('shows the past due chip when a catch up copy is open', () => {
        service.mine.mockReturnValue(of({ ...day, catch_up: [item({ task_id: 'c1', title: 'Bins' })] }));
        const fixture = create();
        expect(el(fixture, 'today-past-due')?.textContent).toContain('1 past due');
    });

    it('shows the points balance in the header and points today in the hero', () => {
        const fixture = create();
        expect(el(fixture, 'today-points')?.textContent).toContain('40 points');
        expect(el(fixture, 'today-points-today')?.textContent).toContain('6 points today');
    });

    it('shows the points earned today from this week, not the day index', () => {
        rewards.myPoints.mockReturnValue(of(points({ today: 2, week: [1, 2, 4, 0, 0, 0, 0] })));
        const fixture = create();
        expect(el(fixture, 'today-points-today')?.textContent).toContain('4 points today');
    });

    it('shows the cheapest reward the balance has not reached yet', () => {
        rewards.list.mockReturnValue(of({ rewards: [reward({ id: 'r0', name: 'Sweets', cost: 30 }), reward({ cost: 80, name: 'Bike day' }), reward()] }));
        const fixture = create();
        const card = el(fixture, 'today-reward');
        expect(card?.textContent).toContain('Movie night');
        expect(card?.textContent).toContain('10 to go');
        expect(card?.textContent).toContain('40 / 50');
        expect(card?.querySelector('[role="progressbar"]')?.getAttribute('aria-valuenow')).toBe('80');
    });

    it('does not show a day estimate for the next reward', () => {
        const fixture = create();
        expect(el(fixture, 'today-reward')?.textContent).not.toContain('about');
    });

    it('shows the two most expensive rewards the balance covers as pills', () => {
        rewards.list.mockReturnValue(of({ rewards: [reward({ id: 'r0', name: 'Sweets', cost: 30 }), reward({ id: 'r2', name: 'Stickers', cost: 10 }), reward({ id: 'r3', name: 'Candy', cost: 20 }), reward()] }));
        const fixture = create();
        const pills = Array.from(el(fixture, 'today-reward')?.querySelectorAll('.pill') ?? []).map((pill) => pill.textContent?.trim());
        expect(pills).toEqual(['🍿 Candy · 20', '🍿 Sweets · 30']);
    });

    it('says keep going when the balance covers no reward yet', () => {
        rewards.myPoints.mockReturnValue(of(points({ balance: 20, week: [0, 0, 0, 0, 0, 0, 6] })));
        const fixture = create();
        expect(el(fixture, 'today-reward-close')?.textContent).toContain("Keep going, you're getting close!");
        expect(el(fixture, 'today-reward')?.querySelector('.pill')).toBeNull();
    });

    it('skips inactive and archived rewards for the next reward', () => {
        rewards.list.mockReturnValue(of({ rewards: [reward({ name: 'Old', status: 'inactive' }), reward({ name: 'Gone', archived: true })] }));
        const fixture = create();
        expect(el(fixture, 'today-reward-none')).not.toBeNull();
    });

    it('shows the every reward message when the balance has reached every reward', () => {
        rewards.myPoints.mockReturnValue(of(points({ balance: 100 })));
        const fixture = create();
        expect(el(fixture, 'today-reward-all')?.textContent).toContain('You can get every reward!');
        expect(el(fixture, 'today-reward-none')).toBeNull();
        expect(el(fixture, 'today-reward')?.querySelector('[role="progressbar"]')).toBeNull();
    });

    it('keeps the page usable when the points and rewards loads fail', () => {
        rewards.myPoints.mockReturnValue(throwError(() => ({ status: 500 })));
        rewards.list.mockReturnValue(throwError(() => ({ status: 500 })));
        const fixture = create();
        expect(el(fixture, 'today-points')).toBeNull();
        expect(el(fixture, 'today-reward-none')).not.toBeNull();
        expect(el(fixture, 'today-list')).not.toBeNull();
    });

    it('shows the weather tile and this week card with the done, missed and bonus counts', () => {
        rewards.myPoints.mockReturnValue(of(points({ tasks_done: [1, 2, 0, 0, 0, 0, 0], tasks_missed: [0, 1, 0, 0, 0, 0, 0], bonus: [0, 0, 0, 0, 0, 0, 3] })));
        const fixture = create();
        expect(el(fixture, 'weather-tile')).not.toBeNull();
        expect(el(fixture, 'today-week-placeholder')?.textContent).toContain('This week');
        expect(el(fixture, 'today-week-placeholder')?.textContent).not.toContain('Coming soon');
        expect(el(fixture, 'today-week-done')?.textContent).toContain('3');
        expect(el(fixture, 'today-week-missed')?.textContent).toContain('1');
        expect(el(fixture, 'today-week-bonus')?.textContent).toContain('3');
    });

    it('shows the hovered slice count in the donut centre, and done by default', () => {
        rewards.myPoints.mockReturnValue(of(points({ tasks_done: [1, 2, 0, 0, 0, 0, 0], tasks_missed: [0, 1, 0, 0, 0, 0, 0], bonus: [0, 0, 0, 0, 0, 0, 3] })));
        const fixture = create();
        expect(el(fixture, 'today-week-centre-value')?.textContent?.trim()).toBe('3');
        el(fixture, 'today-week-missed')?.dispatchEvent(new Event('mouseenter'));
        fixture.detectChanges();
        expect(el(fixture, 'today-week-centre-value')?.textContent?.trim()).toBe('1');
        expect(el(fixture, 'today-week-centre-label')?.textContent).toContain('missed');
        el(fixture, 'today-week-missed')?.dispatchEvent(new Event('mouseleave'));
        fixture.detectChanges();
        expect(el(fixture, 'today-week-centre-value')?.textContent?.trim()).toBe('3');
    });

    it("lists today's events with their times and links to the calendar", () => {
        const fixture = create();
        expect(calendar.range).toHaveBeenCalledWith('2026-10-09', '2026-10-09', false);
        const text = el(fixture, 'today-events')?.textContent ?? '';
        expect(text).toContain('Piano lesson');
        expect(text).toContain('Field trip');
        expect(text).toContain('All day');
        expect(el(fixture, 'today-calendar-link')?.getAttribute('href')).toBe('/calendar');
    });

    it('says nothing is on today when there are no events', () => {
        calendar.range.mockReturnValue(of(calendarDay([])));
        const fixture = create();
        expect(el(fixture, 'today-events')).toBeNull();
        expect(el(fixture, 'today-calendar-empty')?.textContent).toContain('No events today.');
    });

    it('shows an error for the calendar when its events fail to load', () => {
        calendar.range.mockReturnValue(throwError(() => new Error('down')));
        const fixture = create();
        expect(el(fixture, 'today-calendar-error')).not.toBeNull();
        expect(el(fixture, 'today-events')).toBeNull();
    });
});
