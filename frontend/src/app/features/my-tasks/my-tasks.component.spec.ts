import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter, Router } from '@angular/router';
import { of, throwError } from 'rxjs';
import { AuthService } from '../../core/auth/auth.service';
import { getTranslocoTestingModule } from '../../core/transloco/transloco-testing.module';
import { BonusTask } from '../bonus/bonus.models';
import { BonusService } from '../bonus/bonus.service';
import { Task } from '../tasks/tasks.models';
import { TasksService } from '../tasks/tasks.service';
import { MyTaskItem, MyTasksPage, MyTasksResponse } from './my-tasks.models';
import { MyTasksService } from './my-tasks.service';
import { MyTasksComponent } from './my-tasks.component';

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
    decided_at: null,
    decided_by_name: null,
    late_points_percent: null,
    penalty_value: null,
    penalty_unit: null,
    penalty_points: null,
    version: 1,
    ...over,
  };
}

function bonusTask(): BonusTask {
  return {
    id: 'b1',
    template_id: null,
    title: 'Wash the car',
    description: null,
    points: 3,
    claim_mode: 'first_come',
    approval_required: false,
    day: '2026-10-09',
    show_from: null,
    ends_at: '2026-10-09T20:00:00Z',
    state: 'open',
    cancelled_at: null,
    version: 1,
    editable: false,
    eligible_member_ids: ['m1'],
    my_claim: null,
    taken_by_other: false,
  };
}

const data: MyTasksResponse = {
  date: '2026-10-09',
  timezone: 'UTC',
  now: '2026-10-09T07:00:00Z',
  streak_days: 2,
  summary: { today_total: 1, today_done: 0, next_up: null },
  today: [],
  catch_up: [item({ task_id: 'p1', title: 'Wash car', past_due: true, due_date: '2026-10-07', due_at: '2026-10-07T17:00:00Z' })],
  waiting: [item({ task_id: 'w1', title: 'Empty dishwasher', status: 'submitted' })],
  expired: [],
};

/** The first page of each paged list, unless a test supplies its own. */
const comingDefault: MyTasksPage = {
  tasks: [
    item({ task_id: 'c1', title: 'Homework', due_date: '2026-10-10', due_at: '2026-10-10T16:00:00Z', part_of_day: 'after_school' }),
    item({ task_id: 'c2', title: 'Bike', due_date: '2026-10-11', due_at: '2026-10-11T16:00:00Z', part_of_day: 'anytime' }),
  ],
  next_cursor: null,
};
const doneDefault: MyTasksPage = { tasks: [item({ task_id: 'd1', title: 'Make bed', status: 'approved' })], next_cursor: null };

interface Pages {
  coming?: MyTasksPage;
  done?: MyTasksPage;
}

describe('MyTasksComponent', () => {
  let service: jasmine.SpyObj<MyTasksService>;
  let bonus: jasmine.SpyObj<BonusService>;
  let tasks: jasmine.SpyObj<TasksService>;
  let router: Router;

  async function setup(
    tab: string | null,
    response: MyTasksResponse = data,
    pages: Pages = {},
    failure: { code: string; status: number } | null = null,
  ): Promise<ComponentFixture<MyTasksComponent>> {
    service = jasmine.createSpyObj<MyTasksService>('MyTasksService', ['mine', 'withdraw', 'coming', 'done']);
    service.mine.and.returnValue(failure ? throwError(() => failure) : of(response));
    service.withdraw.and.returnValue(of({ id: 'w1' } as Task));
    service.coming.and.returnValue(of(pages.coming ?? comingDefault));
    service.done.and.returnValue(of(pages.done ?? doneDefault));
    bonus = jasmine.createSpyObj<BonusService>('BonusService', ['list', 'claim', 'release', 'complete']);
    bonus.list.and.returnValue(of({ tasks: [bonusTask()] }));
    tasks = jasmine.createSpyObj<TasksService>('TasksService', ['complete']);

    await TestBed.configureTestingModule({
      imports: [MyTasksComponent, getTranslocoTestingModule()],
      providers: [
        provideRouter([]),
        { provide: MyTasksService, useValue: service },
        { provide: BonusService, useValue: bonus },
        { provide: TasksService, useValue: tasks },
        { provide: AuthService, useValue: { currentUser: () => ({ user: { display_name: 'Ava' }, family: null }) } },
      ],
    }).compileComponents();

    router = TestBed.inject(Router);
    if (tab) await router.navigate([], { queryParams: { tab } });
    const fixture = TestBed.createComponent(MyTasksComponent);
    fixture.detectChanges();
    return fixture;
  }

  function tabButton(fixture: ComponentFixture<MyTasksComponent>, name: string): HTMLButtonElement {
    return fixture.nativeElement.querySelector(`[data-testid="tab-${name}"]`) as HTMLButtonElement;
  }

  it('shows the load error screen and retries the load when the tasks cannot be loaded', async () => {
    const fixture = await setup(null, data, {}, { code: 'server', status: 500 });
    expect(fixture.nativeElement.querySelector('[data-testid="my-tasks-loading"]')).toBeNull();
    expect(fixture.nativeElement.querySelector('app-load-error')).not.toBeNull();
    service.mine.and.returnValue(of(data));
    (fixture.nativeElement.querySelector('[data-testid="load-error-retry"]') as HTMLButtonElement).click();
    fixture.detectChanges();
    expect(service.mine.calls.count()).toBe(2);
    expect(fixture.nativeElement.querySelector('app-load-error')).toBeNull();
    expect(tabButton(fixture, 'today')).not.toBeNull();
  });

  it('opens on Today by default and shows the all done card when nothing is pending', async () => {
    const fixture = await setup(null);
    expect(tabButton(fixture, 'today').getAttribute('aria-selected')).toBe('true');
    expect(fixture.nativeElement.querySelector('app-all-done')).not.toBeNull();
    expect(fixture.nativeElement.querySelectorAll('app-today-task').length).toBe(0);
  });

  it('lists the open copies due today as rows grouped by part of day', async () => {
    const today: MyTasksResponse = {
      ...data,
      today: [item({ task_id: 'o1', title: 'Feed the cat' }), item({ task_id: 'o2', title: 'Brush teeth', part_of_day: 'evening' })],
      summary: { today_total: 2, today_done: 0, next_up: null },
    };
    const fixture = await setup(null, today);
    expect(fixture.nativeElement.querySelectorAll('app-today-task').length).toBe(2);
    expect(fixture.nativeElement.querySelectorAll('h2.day').length).toBe(2);
    expect(fixture.nativeElement.querySelector('app-all-done')).toBeNull();
  });

  it('opens on Coming up when the query string asks for it and groups by day', async () => {
    const fixture = await setup('coming');
    expect(tabButton(fixture, 'coming').getAttribute('aria-selected')).toBe('true');
    expect(fixture.nativeElement.querySelectorAll('app-task-row').length).toBe(2);
    expect(fixture.nativeElement.querySelectorAll('h2.day').length).toBe(2);
  });

  it('opens the tab named in the query string', async () => {
    const fixture = await setup('waiting');
    expect(tabButton(fixture, 'waiting').getAttribute('aria-selected')).toBe('true');
    expect(fixture.nativeElement.querySelectorAll('app-task-row').length).toBe(1);
  });

  it('shows when a waiting copy was finished and that a parent is checking it', async () => {
    const waiting: MyTasksResponse = {
      ...data,
      waiting: [item({ task_id: 'w1', title: 'Empty dishwasher', status: 'submitted', completed_at: new Date().toISOString() })],
    };
    const fixture = await setup('waiting', waiting);
    const row = fixture.nativeElement.querySelector('[data-testid="my-task-row"]') as HTMLElement;
    expect(row.querySelector('.meta')?.textContent).toContain('Submitted today');
    expect(fixture.nativeElement.querySelector('[data-testid="my-task-checking"]')?.textContent?.trim()).toBe('A parent is checking');
  });

  const emptyResponse: MyTasksResponse = { ...data, catch_up: [], waiting: [] };
  const noPage: MyTasksPage = { tasks: [], next_cursor: null };

  it('shows an empty card on the Past due tab when nothing is past due', async () => {
    const fixture = await setup('past_due', emptyResponse);
    expect(fixture.nativeElement.querySelector('[data-testid="empty-state"]')).not.toBeNull();
  });

  it('shows an empty card on the Coming up tab when nothing is coming up', async () => {
    const fixture = await setup('coming', emptyResponse, { coming: noPage });
    expect(fixture.nativeElement.querySelector('[data-testid="empty-state"]')).not.toBeNull();
  });

  it('shows an empty card on the Waiting tab when nothing is waiting', async () => {
    const fixture = await setup('waiting', emptyResponse);
    expect(fixture.nativeElement.querySelector('[data-testid="empty-state"]')).not.toBeNull();
  });

  it('shows an empty card on the Done tab when nothing is done', async () => {
    const fixture = await setup('done', emptyResponse, { done: noPage });
    expect(fixture.nativeElement.querySelector('[data-testid="empty-state"]')).not.toBeNull();
  });

  it('names the parent who approved a done copy', async () => {
    const fixture = await setup('done', data, {
      done: { tasks: [item({ task_id: 'd1', title: 'Make bed', status: 'approved', decided_at: '2026-10-09T09:00:00Z', decided_by_name: 'Sam' })], next_cursor: null },
    });
    const row = fixture.nativeElement.querySelector('[data-testid="my-task-row"]') as HTMLElement;
    expect(row.querySelector('.meta')?.textContent).toContain('Approved by Sam');
  });

  it('does not load Coming up or Done until their tab opens', async () => {
    await setup(null);
    expect(service.coming).not.toHaveBeenCalled();
    expect(service.done).not.toHaveBeenCalled();
  });

  it('loads the first page of Coming up when its tab opens', async () => {
    await setup('coming');
    expect(service.coming).toHaveBeenCalledOnceWith(null);
  });

  it('shows one page at a time and moves between pages with Next and Previous', async () => {
    const homework = item({ task_id: 'c1', title: 'Homework', due_date: '2026-10-10', due_at: '2026-10-10T16:00:00Z' });
    const bike = item({ task_id: 'c2', title: 'Bike', due_date: '2026-10-11', due_at: '2026-10-11T16:00:00Z' });
    const fixture = await setup('coming', data, { coming: { tasks: [homework], next_cursor: 'cur1' } });
    const prev = fixture.nativeElement.querySelector('[data-testid="my-tasks-coming-prev"]') as HTMLButtonElement;
    const next = fixture.nativeElement.querySelector('[data-testid="my-tasks-coming-next"]') as HTMLButtonElement;
    expect(fixture.nativeElement.querySelectorAll('app-task-row').length).toBe(1);
    expect(prev.disabled).toBeTrue();
    expect(next.disabled).toBeFalse();

    service.coming.and.returnValue(of({ tasks: [bike], next_cursor: null }));
    next.click();
    fixture.detectChanges();

    expect(service.coming).toHaveBeenCalledWith('cur1');
    expect(fixture.nativeElement.querySelectorAll('app-task-row').length).toBe(1);
    expect(fixture.nativeElement.querySelector('[data-testid="my-tasks-coming-page"]').textContent).toContain('2');
    expect(next.disabled).toBeTrue();
    expect(prev.disabled).toBeFalse();

    service.coming.and.returnValue(of({ tasks: [homework], next_cursor: 'cur1' }));
    prev.click();
    fixture.detectChanges();

    expect(service.coming).toHaveBeenCalledWith(null);
    expect(fixture.nativeElement.querySelector('[data-testid="my-tasks-coming-page"]').textContent).toContain('1');
    expect(prev.disabled).toBeTrue();
  });

  it('keeps the page on screen and offers a retry when Next fails', async () => {
    const homework = item({ task_id: 'c1', title: 'Homework', due_date: '2026-10-10', due_at: '2026-10-10T16:00:00Z' });
    const fixture = await setup('coming', data, { coming: { tasks: [homework], next_cursor: 'cur1' } });
    service.coming.and.returnValue(throwError(() => ({ status: 500 })));
    (fixture.nativeElement.querySelector('[data-testid="my-tasks-coming-next"]') as HTMLButtonElement).click();
    fixture.detectChanges();

    expect(fixture.nativeElement.querySelector('[data-testid="my-tasks-coming-error"]')).not.toBeNull();
    expect(fixture.nativeElement.querySelectorAll('app-task-row').length).toBe(1);
  });

  it('shows the bonus cards on the Bonus tab with the open count', async () => {
    const fixture = await setup(null);
    expect(tabButton(fixture, 'bonus').querySelector('.badge')?.textContent?.trim()).toBe('1');
    tabButton(fixture, 'bonus').click();
    fixture.detectChanges();
    const panel = fixture.nativeElement.querySelector('[data-testid="my-tasks-bonus"]') as HTMLElement;
    expect(panel.hidden).toBeFalse();
    expect(panel.querySelectorAll('[data-testid="kid-bonus-card"]').length).toBe(1);
    expect(bonus.list).toHaveBeenCalledWith('today');
  });

  it('offers Undo on each waiting copy and sends it back to to do', async () => {
    const fixture = await setup('waiting');
    const undo = fixture.nativeElement.querySelector('[data-testid="my-task-undo"]') as HTMLButtonElement;
    expect(undo).not.toBeNull();
    expect(undo.textContent?.trim()).toBe('Move to to-do');

    undo.click();
    fixture.detectChanges();

    expect(service.withdraw).toHaveBeenCalledWith('w1', 1);
    expect(service.mine.calls.count()).toBe(2);
  });

  it('shows the error when Undo fails and keeps the copy on the waiting list', async () => {
    const fixture = await setup('waiting');
    service.withdraw.and.returnValue(throwError(() => ({ error: { code: 'invalid_transition' } })));

    (fixture.nativeElement.querySelector('[data-testid="my-task-undo"]') as HTMLButtonElement).click();
    fixture.detectChanges();

    const error = fixture.nativeElement.querySelector('[data-testid="my-tasks-withdraw-error"]') as HTMLElement;
    expect(error).not.toBeNull();
    expect(error.textContent?.trim().length).toBeGreaterThan(0);
  });

  it('keeps the chosen tab in the URL', async () => {
    const fixture = await setup(null);
    const navigate = spyOn(router, 'navigate').and.resolveTo(true);
    tabButton(fixture, 'done').click();
    expect(navigate).toHaveBeenCalledWith([], jasmine.objectContaining({ queryParams: { tab: 'done' } }));
  });
});
