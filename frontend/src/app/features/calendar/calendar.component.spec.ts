import { WritableSignal, signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Subject, of, throwError } from 'rxjs';
import { getTranslocoTestingModule } from '../../core/transloco/transloco-testing.module';
import { AuthService } from '../../core/auth/auth.service';
import { FamilyService, MemberDetail } from '../family/family.service';
import { Task } from '../tasks/tasks.models';
import { TasksService } from '../tasks/tasks.service';
import { CalendarComponent } from './calendar.component';
import { CalendarItem, CalendarRange, CalendarSummary, EventDetail } from './calendar.models';
import { CalendarService } from './calendar.service';
import { AvatarService } from '../../shared/avatar/avatar.service';

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

function event(title: string, overrides: Partial<CalendarItem> = {}): CalendarItem {
  return {
    key: `e-${title}`,
    kind: 'event',
    title,
    occurrence_date: '2026-10-09',
    starts_at: '2026-10-09T16:00:00Z',
    ends_at: '2026-10-09T17:00:00Z',
    all_day: false,
    time_zone: 'UTC',
    member_ids: ['m1'],
    ...overrides,
  };
}

function task(title: string): CalendarItem {
  return event(title, { key: `t-${title}`, kind: 'task', status: 'scheduled', member_ids: ['m1'], ends_at: '2026-10-09T16:00:00Z' });
}

function range(items: CalendarItem[], calendars: CalendarSummary[] = []): CalendarRange {
  return { from: '', to: '', time_zone: 'UTC', items, calendars };
}

function stored(overrides: Partial<EventDetail> = {}): EventDetail {
  return {
    id: 'ev1',
    title: 'Swim',
    description: null,
    location: null,
    all_day: false,
    time_zone: 'UTC',
    start_date: '2026-10-09',
    start_time: '16:00',
    end_date: '2026-10-09',
    end_time: '17:00',
    recurrence_rule: null,
    member_ids: ['m1'],
    calendar_id: 'cal-default',
    reminders: [],
    version: 2,
    exceptions: [],
    ...overrides,
  };
}

describe('CalendarComponent', () => {
  let calendar: jasmine.SpyObj<CalendarService>;
  let family: jasmine.SpyObj<FamilyService>;
  let tasks: jasmine.SpyObj<TasksService>;
  let isChild: WritableSignal<boolean>;
  let can: jasmine.Spy;
  let loadPermissions: jasmine.Spy;

  function create(): ComponentFixture<CalendarComponent> {
    const fixture = TestBed.createComponent(CalendarComponent);
    fixture.detectChanges();
    return fixture;
  }

  /** Task deadlines are hidden by default, so tests about them switch the toggle on first. */
  function showTasks(fixture: ComponentFixture<CalendarComponent>): void {
    (fixture.nativeElement.querySelector('[data-testid="cal-tasks"]') as HTMLInputElement).click();
    fixture.detectChanges();
  }

  function el(fixture: ComponentFixture<CalendarComponent>, testId: string): HTMLElement | null {
    return fixture.nativeElement.querySelector(`[data-testid="${testId}"]`);
  }

  function grid(fixture: ComponentFixture<CalendarComponent>): HTMLElement {
    return el(fixture, 'cal-grid') as HTMLElement;
  }

  function pills(fixture: ComponentFixture<CalendarComponent>): string[] {
    return Array.from(grid(fixture).querySelectorAll<HTMLElement>('.ev-pill')).map((p) => p.textContent?.trim() ?? '');
  }

  beforeEach(async () => {
    jasmine.clock().install();
    jasmine.clock().mockDate(new Date('2026-10-09T12:00:00Z'));

    calendar = jasmine.createSpyObj<CalendarService>('CalendarService', ['range', 'getEvent', 'deleteEvent', 'cancelOccurrence', 'exportFamily', 'listSources', 'listImports']);
    calendar.exportFamily.and.returnValue(of(new Blob(['BEGIN:VCALENDAR'], { type: 'text/calendar' })));
    calendar.listSources.and.returnValue(of({ items: [] }));
    calendar.listImports.and.returnValue(of({ items: [] }));
    calendar.range.and.returnValue(of(range([])));
    calendar.deleteEvent.and.returnValue(of(undefined));
    calendar.cancelOccurrence.and.returnValue(of(undefined));
    family = jasmine.createSpyObj<FamilyService>('FamilyService', ['listMembers']);
    family.listMembers.and.returnValue(of({ members: [ava] }));
    tasks = jasmine.createSpyObj<TasksService>('TasksService', ['get', 'archive', 'removePerson']);
    isChild = signal(false);
    can = jasmine.createSpy('can').and.returnValue(true);
    loadPermissions = jasmine.createSpy('loadPermissions').and.returnValue(of(undefined));

    await TestBed.configureTestingModule({
      imports: [CalendarComponent, getTranslocoTestingModule()],
      providers: [
        {
          provide: AuthService,
          useValue: {
            currentUser: signal({ user: { is_child: false }, family: { timezone: 'UTC' } }),
            isChild,
            can,
            loadPermissions,
          },
        },
        { provide: CalendarService, useValue: calendar },
        { provide: TasksService, useValue: tasks },
        { provide: FamilyService, useValue: family },
        { provide: AvatarService, useValue: jasmine.createSpyObj<AvatarService>('AvatarService', ['url']) },
      ],
    }).compileComponents();
  });

  afterEach(() => {
    jasmine.clock().uninstall();
  });

  it('loads the whole six-row grid for October 2026, including leading and trailing days', () => {
    const fixture = create();

    expect(el(fixture, 'cal-title')?.textContent).toContain('October');
    expect(el(fixture, 'cal-title')?.textContent).toContain('2026');
    expect(calendar.range).toHaveBeenCalledWith('2026-09-27', '2026-10-31', true);
    expect(grid(fixture).querySelectorAll('.day').length).toBe(35);
    expect(grid(fixture).querySelector('.day.today')?.getAttribute('data-date')).toBe('2026-10-09');
  });

  it('shows a skeleton in place of the grid while events load, then the events', () => {
    const pending = new Subject<CalendarRange>();
    calendar.range.and.returnValue(pending);

    const fixture = create();

    expect(el(fixture, 'cal-skeleton')).not.toBeNull();
    expect(el(fixture, 'cal-grid')).toBeNull();
    expect(fixture.nativeElement.querySelectorAll('.cal-skeleton .day').length).toBe(35);

    pending.next(range([event('Swim')]));
    pending.complete();
    fixture.detectChanges();

    expect(el(fixture, 'cal-skeleton')).toBeNull();
    expect(pills(fixture)).toEqual(['Swim']);
  });

  it('moves between months and reloads the new span', () => {
    const fixture = create();

    el(fixture, 'cal-next')?.click();
    fixture.detectChanges();
    expect(calendar.range).toHaveBeenCalledWith('2026-11-01', '2026-12-05', true);
    expect(el(fixture, 'cal-title')?.textContent).toContain('November');

    el(fixture, 'cal-today')?.click();
    fixture.detectChanges();
    expect(calendar.range).toHaveBeenCalledWith('2026-09-27', '2026-10-31', true);
  });

  it('shows up to three pills per day and a more link for the rest', () => {
    calendar.range.and.returnValue(
      of(range([event('Swim'), event('Piano'), event('Dentist'), event('Dinner'), event('Movie')])),
    );

    const fixture = create();

    expect(pills(fixture)).toEqual(['Swim', 'Piano', 'Dentist']);
    expect(grid(fixture).querySelector('.more')?.textContent?.trim()).toBe('+2 more');
  });

  it('colours a pill with the first member it belongs to', () => {
    calendar.range.and.returnValue(of(range([event('Swim')])));

    const fixture = create();
    const pill = grid(fixture).querySelector<HTMLElement>('.ev-pill');

    expect(pill?.style.getPropertyValue('--c')).toContain('#ff0000');
  });

  it('hides a calendar when its chip is switched off', () => {
    calendar.range.and.returnValue(
      of(
        range(
          [
            event('Swim', { calendar_id: 'cal-default', calendar_name: 'Default' }),
            event('Recital', { calendar_id: 'cal-school', calendar_name: 'School term' }),
            task('Bins'),
          ],
          [
            { id: 'cal-default', name: 'Default' },
            { id: 'cal-school', name: 'School term', colour: '#ff0000' },
          ],
        ),
      ),
    );

    const fixture = create();
    showTasks(fixture);
    const chips = () => [...fixture.nativeElement.querySelectorAll('[data-testid="cal-calendar"]')] as HTMLButtonElement[];
    expect(chips().map((c) => c.textContent?.trim())).toEqual(['Default', 'School term']);
    expect(pills(fixture).sort()).toEqual(['Bins', 'Recital', 'Swim']);

    chips()[1].click();
    fixture.detectChanges();

    expect(chips()[1].getAttribute('aria-pressed')).toBe('false');
    expect(pills(fixture).sort()).toEqual(['Bins', 'Swim']);
  });

  it('gives every visible calendar a chip, even with no items in the month on screen', () => {
    calendar.range.and.returnValue(
      of(
        range(
          [event('Swim', { calendar_id: 'cal-default', calendar_name: 'Default' })],
          [
            { id: 'cal-default', name: 'Default' },
            { id: 'cal-club', name: 'Sports club', colour: '#00ff00' },
          ],
        ),
      ),
    );

    const fixture = create();
    const chips = [...fixture.nativeElement.querySelectorAll('[data-testid="cal-calendar"]')] as HTMLButtonElement[];
    expect(chips.map((c) => c.textContent?.trim())).toEqual(['Default', 'Sports club']);
    expect(pills(fixture)).toEqual(['Swim']);
  });

  it('keeps the chips from the last good load when a month fails', () => {
    calendar.range.and.returnValue(of(range([], [{ id: 'cal-school', name: 'School term' }])));
    const fixture = create();
    const chipCount = () => fixture.nativeElement.querySelectorAll('[data-testid="cal-calendar"]').length;
    expect(chipCount()).toBe(1);

    calendar.range.and.returnValue(throwError(() => new Error('offline')));
    fixture.componentInstance.move(1);
    fixture.detectChanges();

    expect(chipCount()).toBe(1);
  });
  it('hides task deadlines by default and shows them once the task switch is turned on', () => {
    calendar.range.and.returnValue(of(range([event('Swim'), task('Bins')])));

    const fixture = create();
    expect((el(fixture, 'cal-tasks') as HTMLInputElement).checked).toBe(false);
    expect(pills(fixture)).toEqual(['Swim']);

    (el(fixture, 'cal-tasks') as HTMLInputElement).click();
    fixture.detectChanges();

    expect(pills(fixture)).toEqual(['Swim', 'Bins']);
  });

  it('hides items for a person who is switched off, but keeps family-wide items', () => {
    calendar.range.and.returnValue(of(range([event('Swim'), event('Holiday', { member_ids: [] })])));

    const fixture = create();
    expect(el(fixture, 'cal-person')).toBeNull();
    (el(fixture, 'cal-people-toggle') as HTMLButtonElement).click();
    fixture.detectChanges();

    const chip = el(fixture, 'cal-person') as HTMLButtonElement;
    expect(chip.getAttribute('aria-pressed')).toBe('true');

    chip.click();
    fixture.detectChanges();

    expect(chip.getAttribute('aria-pressed')).toBe('false');
    expect(pills(fixture)).toEqual(['Holiday']);
  });

  it('shows the filter-active badge only while a member is switched off', () => {
    calendar.range.and.returnValue(of(range([event('Swim')])));

    const fixture = create();
    expect(el(fixture, 'cal-filter-active')).toBeNull();
    (el(fixture, 'cal-people-toggle') as HTMLButtonElement).click();
    fixture.detectChanges();

    (el(fixture, 'cal-person') as HTMLButtonElement).click();
    fixture.detectChanges();
    expect(el(fixture, 'cal-filter-active')).not.toBeNull();

    (el(fixture, 'cal-person') as HTMLButtonElement).click();
    fixture.detectChanges();
    expect(el(fixture, 'cal-filter-active')).toBeNull();
  });

  it('shows an error with retry, and reloads when retry is pressed', () => {
    calendar.range.and.returnValue(throwError(() => new Error('down')));

    const fixture = create();
    expect(el(fixture, 'cal-error')).not.toBeNull();

    calendar.range.and.returnValue(of(range([event('Swim')])));
    (el(fixture, 'cal-retry') as HTMLButtonElement).click();
    fixture.detectChanges();

    expect(el(fixture, 'cal-error')).toBeNull();
    expect(pills(fixture)).toEqual(['Swim']);
  });

  it('shows a child the same calendar, without the people filter or new event button', () => {
    isChild.set(true);
    can.and.callFake((permission: string) => permission !== 'calendar.manage');
    calendar.range.and.returnValue(of(range([event('Swim')])));

    const fixture = create();

    expect(pills(fixture)).toEqual(['Swim']);
    expect(calendar.range).toHaveBeenCalled();
    expect(family.listMembers).not.toHaveBeenCalled();
    expect(el(fixture, 'cal-people-toggle')).toBeNull();
    expect(el(fixture, 'cal-new')).toBeNull();
  });

  it('hides edit and delete from a child in the event and task details', () => {
    isChild.set(true);
    can.and.callFake((permission: string) => permission !== 'calendar.manage');
    calendar.range.and.returnValue(of(range([event('Swim', { event_id: 'ev1' }), task('Bins')])));
    const fixture = create();
    showTasks(fixture);

    (grid(fixture).querySelector('button[data-testid="cal-event"]') as HTMLButtonElement).click();
    fixture.detectChanges();
    expect(el(fixture, 'event-detail')).not.toBeNull();
    expect(el(fixture, 'detail-edit')).toBeNull();
    expect(el(fixture, 'detail-delete')).toBeNull();

    (el(fixture, 'detail-close') as HTMLButtonElement).click();
    fixture.detectChanges();
    (grid(fixture).querySelector('button[data-testid="cal-task"]') as HTMLButtonElement).click();
    fixture.detectChanges();
    expect(el(fixture, 'detail-open-task')).toBeNull();
    expect(el(fixture, 'detail-delete')).toBeNull();
  });

  it('opens a new event dialog on today when the focused month shows today', () => {
    const fixture = create();

    (el(fixture, 'cal-new') as HTMLButtonElement).click();
    fixture.detectChanges();

    expect(el(fixture, 'event-dialog')).not.toBeNull();
    expect((el(fixture, 'event-date') as HTMLInputElement).value).toBe('2026-10-09');
  });

  it('makes every pill a button, so each can open its detail', () => {
    calendar.range.and.returnValue(of(range([event('Swim', { event_id: 'ev1' }), task('Bins')])));

    const fixture = create();
    showTasks(fixture);

    expect(grid(fixture).querySelectorAll('button.ev-pill').length).toBe(2);
    expect(grid(fixture).querySelectorAll('button[data-testid="cal-event"]').length).toBe(1);
    expect(grid(fixture).querySelectorAll('button[data-testid="cal-task"].task').length).toBe(1);
  });

  it('removes the Agenda view toggle, leaving Day, Week and Month', () => {
    const fixture = create();
    const labels = Array.from(fixture.nativeElement.querySelectorAll('.seg button') as NodeListOf<HTMLElement>).map(
      (b) => b.textContent?.trim(),
    );

    expect(labels).toEqual(['Day', 'Week', 'Month']);
  });

  it('opens an event pill in the detail dialog, and edits the event from there', () => {
    calendar.range.and.returnValue(of(range([event('Swim', { event_id: 'ev1', location: 'Leisure centre' })])));
    calendar.getEvent.and.returnValue(
      of({
        id: 'ev1',
        title: 'Swim',
        description: null,
        location: 'Leisure centre',
        all_day: false,
        time_zone: 'UTC',
        start_date: '2026-10-09',
        start_time: '16:00',
        end_date: '2026-10-09',
        end_time: '17:00',
        recurrence_rule: null,
        member_ids: ['m1'],
        calendar_id: 'cal-default',
        reminders: [],
        version: 2,
        exceptions: [],
      }),
    );

    const fixture = create();
    (grid(fixture).querySelector('button.ev-pill') as HTMLButtonElement).click();
    fixture.detectChanges();

    expect(el(fixture, 'event-dialog')).toBeNull();
    expect(el(fixture, 'detail-title')?.textContent).toContain('Swim');
    expect(el(fixture, 'detail-location')?.textContent).toContain('Leisure centre');

    (el(fixture, 'detail-edit') as HTMLButtonElement).click();
    fixture.detectChanges();

    expect(calendar.getEvent).toHaveBeenCalledWith('ev1');
    expect(el(fixture, 'event-detail')).toBeNull();
    expect((el(fixture, 'event-title') as HTMLInputElement).value).toBe('Swim');
  });

  it('shows a synced event without edit or delete, and names its calendar', () => {
    calendar.range.and.returnValue(
      of(range([event('Football', { event_id: 'ev2', calendar_id: 'cal1', calendar_name: 'School term', imported: true })])),
    );

    const fixture = create();
    (grid(fixture).querySelector('button.ev-pill') as HTMLButtonElement).click();
    fixture.detectChanges();

    expect(el(fixture, 'detail-title')?.textContent).toContain('Football');
    expect(el(fixture, 'detail-calendar')?.textContent).toContain('School term');
    expect(el(fixture, 'detail-synced')).not.toBeNull();
    expect(el(fixture, 'detail-edit')).toBeNull();
    expect(el(fixture, 'detail-delete')).toBeNull();
  });

  it('shows a task detail with a link to the task and no location or edit action', () => {
    calendar.range.and.returnValue(of(range([task('Bins')])));

    const fixture = create();
    showTasks(fixture);
    (el(fixture, 'cal-task') as HTMLButtonElement).click();
    fixture.detectChanges();

    expect(el(fixture, 'detail-title')?.textContent).toContain('Bins');
    expect(el(fixture, 'detail-when')?.textContent?.trim()).not.toContain('–');
    expect(el(fixture, 'detail-status')).not.toBeNull();
    expect(el(fixture, 'detail-open-task')).not.toBeNull();
    expect(el(fixture, 'detail-location')).toBeNull();
    expect(el(fixture, 'detail-edit')).toBeNull();
  });

  it('removes this person\'s copy of a task from its detail, as the Tasks list does', () => {
    calendar.range.and.returnValue(of(range([{ ...task('Bins'), task_id: 'occ1' }])));
    tasks.get.and.returnValue(of({ version: 4, repeats: false, assignees: [{ id: 'm1' }] } as unknown as Task));
    tasks.removePerson.and.returnValue(of({} as Task));

    const fixture = create();
    showTasks(fixture);
    (el(fixture, 'cal-task') as HTMLButtonElement).click();
    fixture.detectChanges();
    (el(fixture, 'detail-delete') as HTMLButtonElement).click();
    fixture.detectChanges();

    expect(el(fixture, 'delete-scope')).toBeNull();
    expect(el(fixture, 'remove-person')?.textContent).toContain('Bins');
    (el(fixture, 'remove-person-ok') as HTMLButtonElement).click();
    fixture.detectChanges();

    expect(tasks.get).toHaveBeenCalledWith('occ1');
    expect(tasks.removePerson).toHaveBeenCalledWith('occ1', 4, jasmine.any(String), 'occurrence');
    expect(tasks.archive).not.toHaveBeenCalled();
    expect(el(fixture, 'event-detail')).toBeNull();
    expect(calendar.range).toHaveBeenCalledTimes(2);
  });

  it('opens the day dialog from "+N more" with every item for that day, and picks one into the detail', () => {
    calendar.range.and.returnValue(
      of(range([event('Swim'), event('Piano'), event('Dentist'), event('Dinner'), event('Movie')])),
    );

    const fixture = create();
    const day = fixture.nativeElement.querySelector('.day[data-date="2026-10-09"]') as HTMLElement;
    (day.querySelector('[data-testid="cal-more"]') as HTMLButtonElement).click();
    fixture.detectChanges();

    const items = fixture.nativeElement.querySelectorAll('[data-testid="day-item"]') as NodeListOf<HTMLButtonElement>;
    expect(items.length).toBe(5);

    items[3].click();
    fixture.detectChanges();

    expect(el(fixture, 'day-close')).toBeNull();
    expect(el(fixture, 'detail-title')?.textContent).toContain('Dinner');
  });

  it('closes the day dialog with its close button', () => {
    calendar.range.and.returnValue(of(range([event('Swim'), event('Piano'), event('Dentist'), event('Dinner')])));

    const fixture = create();
    (grid(fixture).querySelector('[data-testid="cal-more"]') as HTMLButtonElement).click();
    fixture.detectChanges();
    expect(el(fixture, 'day-close')).not.toBeNull();

    (el(fixture, 'day-close') as HTMLButtonElement).click();
    fixture.detectChanges();

    expect(el(fixture, 'day-close')).toBeNull();
  });

  it('shows the people on an event as avatars rather than names', () => {
    calendar.range.and.returnValue(of(range([event('Swim', { event_id: 'ev1' })])));

    const fixture = create();
    (grid(fixture).querySelector('button.ev-pill') as HTMLButtonElement).click();
    fixture.detectChanges();

    const people = el(fixture, 'detail-people') as HTMLElement;
    expect(people.querySelectorAll('app-avatar').length).toBe(1);
    expect(people.textContent).not.toContain('Ava');
  });

  it('shows every member as an avatar for an item with no people', () => {
    family.listMembers.and.returnValue(of({ members: [ava, { ...ava, id: 'm2', display_name: 'Sam' }] }));
    calendar.range.and.returnValue(of(range([event('Holiday', { event_id: 'ev2', member_ids: [] })])));

    const fixture = create();
    (grid(fixture).querySelector('button.ev-pill') as HTMLButtonElement).click();
    fixture.detectChanges();

    expect((el(fixture, 'detail-people') as HTMLElement).querySelectorAll('app-avatar').length).toBe(2);
  });

  it('deletes an event from its detail after confirmation, using the latest version', () => {
    calendar.range.and.returnValue(of(range([event('Swim', { event_id: 'ev1' })])));
    calendar.getEvent.and.returnValue(of(stored({ version: 3 })));

    const fixture = create();
    (grid(fixture).querySelector('button.ev-pill') as HTMLButtonElement).click();
    fixture.detectChanges();
    (el(fixture, 'detail-delete') as HTMLButtonElement).click();
    fixture.detectChanges();

    expect(el(fixture, 'task-confirm')).not.toBeNull();
    expect(calendar.deleteEvent).not.toHaveBeenCalled();

    (el(fixture, 'task-confirm-ok') as HTMLButtonElement).click();
    fixture.detectChanges();

    expect(calendar.getEvent).toHaveBeenCalledWith('ev1');
    expect(calendar.deleteEvent).toHaveBeenCalledWith('ev1', 3);
    expect(el(fixture, 'event-detail')).toBeNull();
    expect(calendar.range.calls.count()).toBeGreaterThan(1);
  });

  it('keeps the event when the delete is cancelled', () => {
    calendar.range.and.returnValue(of(range([event('Swim', { event_id: 'ev1' })])));

    const fixture = create();
    (grid(fixture).querySelector('button.ev-pill') as HTMLButtonElement).click();
    fixture.detectChanges();
    (el(fixture, 'detail-delete') as HTMLButtonElement).click();
    fixture.detectChanges();
    (el(fixture, 'task-confirm-cancel') as HTMLButtonElement).click();
    fixture.detectChanges();

    expect(calendar.deleteEvent).not.toHaveBeenCalled();
    expect(el(fixture, 'task-confirm')).toBeNull();
    expect(el(fixture, 'event-detail')).not.toBeNull();
  });

  it('offers this event or all events when deleting a repeating event, with this event selected by default', () => {
    calendar.range.and.returnValue(of(range([event('Swim', { event_id: 'ev1', recurring: true })])));
    calendar.getEvent.and.returnValue(of(stored({ version: 3, recurrence_rule: 'FREQ=WEEKLY' })));

    const fixture = create();
    (grid(fixture).querySelector('button.ev-pill') as HTMLButtonElement).click();
    fixture.detectChanges();
    (el(fixture, 'detail-delete') as HTMLButtonElement).click();
    fixture.detectChanges();

    expect(el(fixture, 'task-confirm')).toBeNull();
    expect((el(fixture, 'delete-scope-occurrence') as HTMLInputElement).checked).toBe(true);
    expect((el(fixture, 'delete-scope-series') as HTMLInputElement).checked).toBe(false);

    (el(fixture, 'delete-scope-ok') as HTMLButtonElement).click();
    fixture.detectChanges();

    expect(calendar.cancelOccurrence).toHaveBeenCalledWith('ev1', '2026-10-09', 3);
    expect(calendar.deleteEvent).not.toHaveBeenCalled();
  });

  it('deletes the whole series when all events is chosen for a repeating event', () => {
    calendar.range.and.returnValue(of(range([event('Swim', { event_id: 'ev1', recurring: true })])));
    calendar.getEvent.and.returnValue(of(stored({ version: 3, recurrence_rule: 'FREQ=WEEKLY' })));

    const fixture = create();
    (grid(fixture).querySelector('button.ev-pill') as HTMLButtonElement).click();
    fixture.detectChanges();
    (el(fixture, 'detail-delete') as HTMLButtonElement).click();
    fixture.detectChanges();
    (el(fixture, 'delete-scope-series') as HTMLInputElement).click();
    fixture.detectChanges();
    (el(fixture, 'delete-scope-ok') as HTMLButtonElement).click();
    fixture.detectChanges();

    expect(calendar.deleteEvent).toHaveBeenCalledWith('ev1', 3);
    expect(calendar.cancelOccurrence).not.toHaveBeenCalled();
  });

  it('closes only the delete scope dialog on Escape and keeps the detail open', () => {
    calendar.range.and.returnValue(of(range([event('Swim', { event_id: 'ev1', recurring: true })])));

    const fixture = create();
    (grid(fixture).querySelector('button.ev-pill') as HTMLButtonElement).click();
    fixture.detectChanges();
    (el(fixture, 'detail-delete') as HTMLButtonElement).click();
    fixture.detectChanges();
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    fixture.detectChanges();

    expect(el(fixture, 'delete-scope')).toBeNull();
    expect(el(fixture, 'event-detail')).not.toBeNull();
  });

  it('puts the period and view controls in a toolbar above the filters and grid, and keeps New event in the header', () => {
    const fixture = create();
    const toolbar = fixture.nativeElement.querySelector('.toolbar') as HTMLElement;
    const cells = grid(fixture);

    expect(toolbar.contains(el(fixture, 'cal-prev'))).toBe(true);
    expect(toolbar.contains(el(fixture, 'cal-today'))).toBe(true);
    expect(toolbar.querySelector('.seg')).not.toBeNull();
    expect(toolbar.contains(el(fixture, 'cal-new'))).toBe(false);
    expect(toolbar.nextElementSibling?.classList.contains('filters')).toBe(true);
    expect(toolbar.nextElementSibling?.nextElementSibling).toBe(cells.closest('.cal-wrap'));
    expect(toolbar.compareDocumentPosition(cells) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it('opens a month and year picker from the heading and jumps to a far month', () => {
    const fixture = create();
    const start = fixture.componentInstance.focus();

    (el(fixture, 'cal-title') as HTMLButtonElement).click();
    fixture.detectChanges();
    expect(el(fixture, 'cal-picker')).not.toBeNull();
    expect(el(fixture, 'cal-picker-year')?.textContent?.trim()).toBe(String(start.year));

    (el(fixture, 'cal-picker-next-year') as HTMLButtonElement).click();
    (el(fixture, 'cal-picker-next-year') as HTMLButtonElement).click();
    fixture.detectChanges();
    expect(el(fixture, 'cal-picker-year')?.textContent?.trim()).toBe(String(start.year + 2));

    const months = fixture.nativeElement.querySelectorAll('[data-testid="cal-picker-month"]') as NodeListOf<HTMLButtonElement>;
    expect(months.length).toBe(12);
    months[2].click();
    fixture.detectChanges();

    expect(fixture.componentInstance.focus()).toEqual({ year: start.year + 2, month: 2 });
    expect(el(fixture, 'cal-picker')).toBeNull();
  });

  it('closes the month picker on Escape without changing the month', () => {
    const fixture = create();
    const start = fixture.componentInstance.focus();

    (el(fixture, 'cal-title') as HTMLButtonElement).click();
    fixture.detectChanges();
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    fixture.detectChanges();

    expect(el(fixture, 'cal-picker')).toBeNull();
    expect(fixture.componentInstance.focus()).toEqual(start);
  });

  describe('day and week views', () => {
    function viewButton(fixture: ComponentFixture<CalendarComponent>, view: 'day' | 'week' | 'month'): HTMLButtonElement {
      return el(fixture, `cal-view-${view}`) as HTMLButtonElement;
    }

    function click(fixture: ComponentFixture<CalendarComponent>, testId: string): void {
      (el(fixture, testId) as HTMLButtonElement).click();
      fixture.detectChanges();
    }

    it('shows the day view for today and loads only that day', () => {
      const fixture = create();

      viewButton(fixture, 'day').click();
      fixture.detectChanges();

      expect(calendar.range).toHaveBeenCalledWith('2026-10-09', '2026-10-09', true);
      expect(viewButton(fixture, 'day').getAttribute('aria-pressed')).toBe('true');
      expect(viewButton(fixture, 'month').getAttribute('aria-pressed')).toBe('false');
      expect(el(fixture, 'cal-title')?.textContent).toContain('October 9, 2026');
      expect(el(fixture, 'cal-grid')).toBeNull();
      expect(el(fixture, 'cal-day')).not.toBeNull();
    });

    it('lists all-day items first, then timed items in start order', () => {
      calendar.range.and.returnValue(
        of(
          range([
            event('Dentist', { starts_at: '2026-10-09T15:00:00Z', ends_at: '2026-10-09T16:00:00Z' }),
            event('Holiday', { all_day: true, starts_at: '2026-10-09T00:00:00Z', ends_at: '2026-10-09T23:59:00Z' }),
          ]),
        ),
      );
      const fixture = create();

      viewButton(fixture, 'day').click();
      fixture.detectChanges();

      const rows = Array.from(el(fixture, 'cal-day')?.querySelectorAll<HTMLElement>('.day-row') ?? []);
      expect(rows.map((r) => r.querySelector('.title')?.textContent?.trim())).toEqual(['Holiday', 'Dentist']);
      expect(rows[0].querySelector('.time')?.textContent?.trim()).toBe('All day');
    });

    it('shows an empty state when the day has nothing planned', () => {
      const fixture = create();

      viewButton(fixture, 'day').click();
      fixture.detectChanges();

      expect(el(fixture, 'cal-day-empty')).not.toBeNull();
    });

    it('moves one day at a time in the day view', () => {
      const fixture = create();
      viewButton(fixture, 'day').click();
      fixture.detectChanges();

      click(fixture, 'cal-next');
      expect(calendar.range).toHaveBeenCalledWith('2026-10-10', '2026-10-10', true);

      click(fixture, 'cal-prev');
      click(fixture, 'cal-prev');
      expect(calendar.range).toHaveBeenCalledWith('2026-10-08', '2026-10-08', true);
    });

    it('shows Sunday to Saturday as seven columns in the week view', () => {
      const fixture = create();

      viewButton(fixture, 'week').click();
      fixture.detectChanges();

      expect(calendar.range).toHaveBeenCalledWith('2026-10-04', '2026-10-10', true);
      const days = el(fixture, 'cal-week')?.querySelectorAll('.wday') ?? [];
      expect(days.length).toBe(7);
      expect(Array.from(days).map((d) => d.getAttribute('data-date'))).toEqual([
        '2026-10-04',
        '2026-10-05',
        '2026-10-06',
        '2026-10-07',
        '2026-10-08',
        '2026-10-09',
        '2026-10-10',
      ]);
      expect(el(fixture, 'cal-week')?.querySelector('.wday.today')?.getAttribute('data-date')).toBe('2026-10-09');
    });

    it('places each item in the column for its day', () => {
      calendar.range.and.returnValue(
        of(range([event('Swim', { occurrence_date: '2026-10-07' }), event('Dentist', { occurrence_date: '2026-10-09' })])),
      );
      const fixture = create();

      viewButton(fixture, 'week').click();
      fixture.detectChanges();

      const swimDay = el(fixture, 'cal-week')?.querySelector('.wday[data-date="2026-10-07"]');
      const dentistDay = el(fixture, 'cal-week')?.querySelector('.wday[data-date="2026-10-09"]');
      expect(swimDay?.querySelector('.slot')?.textContent?.trim()).toBe('Swim');
      expect(swimDay?.textContent).not.toContain('Dentist');
      expect(dentistDay?.querySelector('.slot')?.textContent?.trim()).toBe('Dentist');
    });

    it('opens the day dialog from the "more" link when a week day is full', () => {
      const many = ['A', 'B', 'C', 'D', 'E', 'F', 'G'].map((t) => event(t, { key: `k-${t}` }));
      calendar.range.and.returnValue(of(range(many)));
      const fixture = create();

      viewButton(fixture, 'week').click();
      fixture.detectChanges();
      expect(el(fixture, 'cal-week')?.querySelectorAll('.wday[data-date="2026-10-09"] .slot').length).toBe(6);

      click(fixture, 'cal-more');

      expect(fixture.nativeElement.querySelector('app-day-dialog')).not.toBeNull();
    });

    it('opens the detail when a week item is clicked', () => {
      calendar.range.and.returnValue(of(range([event('Swim')])));
      const fixture = create();

      viewButton(fixture, 'week').click();
      fixture.detectChanges();
      (el(fixture, 'cal-week')?.querySelector('.slot') as HTMLButtonElement).click();

      expect(fixture.componentInstance.detail()?.title).toBe('Swim');
    });

    it('moves seven days at a time in the week view', () => {
      const fixture = create();
      viewButton(fixture, 'week').click();
      fixture.detectChanges();

      click(fixture, 'cal-prev');

      expect(calendar.range).toHaveBeenCalledWith('2026-09-27', '2026-10-03', true);
      expect(el(fixture, 'cal-title')?.textContent).toContain('Sep 27');
    });

    it('starts the day view on today when the focused month is the current one', () => {
      const fixture = create();

      viewButton(fixture, 'day').click();
      fixture.detectChanges();

      expect(calendar.range).toHaveBeenCalledWith('2026-10-09', '2026-10-09', true);
    });

    it('starts the day view on the 1st when the focused month is another month', () => {
      const fixture = create();
      click(fixture, 'cal-next');

      viewButton(fixture, 'day').click();
      fixture.detectChanges();

      expect(calendar.range).toHaveBeenCalledWith('2026-11-01', '2026-11-01', true);
    });

    it('returns to the month that holds the day in view', () => {
      const fixture = create();
      viewButton(fixture, 'week').click();
      fixture.detectChanges();
      click(fixture, 'cal-next');

      viewButton(fixture, 'month').click();
      fixture.detectChanges();

      expect(fixture.componentInstance.focus()).toEqual({ year: 2026, month: 9 });
      expect(calendar.range).toHaveBeenCalledWith('2026-09-27', '2026-10-31', true);
      expect(el(fixture, 'cal-grid')).not.toBeNull();
    });

    it('moves to the first of the picked month when a day or week view is open', () => {
      const fixture = create();
      viewButton(fixture, 'week').click();
      fixture.detectChanges();

      fixture.componentInstance.pickMonth(10);
      fixture.detectChanges();

      expect(calendar.range).toHaveBeenCalledWith('2026-11-01', '2026-11-07', true);
    });

    it('defaults a new event to the day in view', () => {
      const fixture = create();
      viewButton(fixture, 'day').click();
      fixture.detectChanges();

      fixture.componentInstance.openNew();

      expect(fixture.componentInstance.dialogDate()).toBe('2026-10-09');
    });
  });

  describe('export', () => {
    let createObjectURL: jasmine.Spy;
    let revokeObjectURL: jasmine.Spy;
    let anchorClick: jasmine.Spy;

    beforeEach(() => {
      createObjectURL = spyOn(URL, 'createObjectURL').and.returnValue('blob:export');
      revokeObjectURL = spyOn(URL, 'revokeObjectURL');
      anchorClick = spyOn(HTMLAnchorElement.prototype, 'click');
    });

    it('loads permissions on open so the export button can be gated', () => {
      create();

      expect(loadPermissions).toHaveBeenCalled();
    });

    it('shows the export button to a caller with calendar.view', () => {
      const fixture = create();

      expect(el(fixture, 'cal-export')).not.toBeNull();
    });

    it('hides the export button from a caller without calendar.view', () => {
      can.and.returnValue(false);
      const fixture = create();

      expect(el(fixture, 'cal-export')).toBeNull();
    });

    it('downloads the family calendar as nestwall-family.ics', () => {
      const fixture = create();

      el(fixture, 'cal-export')?.click();
      jasmine.clock().tick(0);

      expect(calendar.exportFamily).toHaveBeenCalled();
      expect(createObjectURL).toHaveBeenCalled();
      expect(anchorClick).toHaveBeenCalled();
      expect(revokeObjectURL).toHaveBeenCalledWith('blob:export');
    });

    it('shows an error when the export fails', () => {
      calendar.exportFamily = jasmine.createSpy('exportFamily').and.returnValue(throwError(() => ({ code: 'server_error' })));
      const fixture = create();

      el(fixture, 'cal-export')?.click();
      fixture.detectChanges();

      expect(el(fixture, 'cal-export-error')?.textContent).toContain('The calendar could not be exported.');
      expect(createObjectURL).not.toHaveBeenCalled();
    });
  });
});

