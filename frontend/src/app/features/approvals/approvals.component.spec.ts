import { provideHttpClient } from '@angular/common/http';
import { provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { NEVER, of, throwError } from 'rxjs';
import { getTranslocoTestingModule } from '../../core/transloco/transloco-testing.module';
import { BonusTask } from '../bonus/bonus.models';
import { BonusService } from '../bonus/bonus.service';
import { FamilyService } from '../family/family.service';
import { RedemptionListItem } from '../rewards/rewards.models';
import { RewardsService } from '../rewards/rewards.service';
import { TaskListItem } from '../tasks/tasks.models';
import { TasksService } from '../tasks/tasks.service';
import { ApprovalsComponent } from './approvals.component';

type ListReturn = ReturnType<TasksService['list']>;
type PageReturn = ReturnType<RewardsService['redemptionPage']>;
type ApproveReturn = ReturnType<TasksService['approve']>;
type RedeemReturn = ReturnType<RewardsService['approveRedemption']>;
type ListBonusReturn = ReturnType<BonusService['list']>;
type BonusDecisionReturn = ReturnType<BonusService['approve']>;

function task(id: string, over: Partial<TaskListItem> = {}): TaskListItem {
  return {
    id,
    member_id: 'm1',
    title: `Task ${id}`,
    points: 20,
    due_at: '2026-10-12T17:00:00Z',
    version: 1,
    ...over,
  } as unknown as TaskListItem;
}

/** A bonus task with one claim by Noah (m1) that is submitted and waiting for a decision. */
function bonusTask(): BonusTask {
  return {
    id: 'b1',
    title: 'Fold the laundry',
    points: 10,
    claims: [
      {
        id: 'c1',
        bonus_task_id: 'b1',
        family_member_id: 'm1',
        status: 'submitted',
        claimed_at: '2026-10-12T15:00:00Z',
        completed_at: '2026-10-12T16:20:00Z',
        version: 3,
      },
    ],
  } as unknown as BonusTask;
}

function request(id: string, over: Partial<RedemptionListItem> = {}): RedemptionListItem {
  return {
    id,
    reward_id: 'r1',
    member_id: 'm1',
    status: 'pending',
    cost: 40,
    requested_at: '2026-10-12T09:00:00Z',
    decided_at: null,
    decided_by_member_id: null,
    decision_reason: null,
    version: 4,
    reward_name: 'Movie night',
    reward_icon: '🎬',
    member_name: 'Mia',
    ...over,
  } as unknown as RedemptionListItem;
}

describe('ApprovalsComponent', () => {
  let fixture: ComponentFixture<ApprovalsComponent>;
  let tasks: jasmine.SpyObj<TasksService>;
  let rewards: jasmine.SpyObj<RewardsService>;
  let bonus: jasmine.SpyObj<BonusService>;

  function build(): void {
    fixture = TestBed.createComponent(ApprovalsComponent);
    fixture.detectChanges();
  }

  function el<T extends Element = HTMLElement>(selector: string): T | null {
    return fixture.nativeElement.querySelector(selector) as T | null;
  }

  function click(selector: string): void {
    el<HTMLElement>(selector)?.click();
    fixture.detectChanges();
  }

  function typeInto(selector: string, value: string): void {
    const field = el<HTMLTextAreaElement>(selector);
    if (field) {
      field.value = value;
      field.dispatchEvent(new Event('input'));
    }
    fixture.detectChanges();
  }

  /** Makes the redemption endpoint return the pending requests or the history page, depending on status. */
  function setRedemptions(pending: RedemptionListItem[], history: RedemptionListItem[] = []): void {
    rewards.redemptionPage.and.callFake((query) => {
      const items = query.status === 'pending' ? pending : history;
      return of({ redemptions: items, next_cursor: null }) as unknown as PageReturn;
    });
  }

  beforeEach(async () => {
    tasks = jasmine.createSpyObj<TasksService>('TasksService', ['list', 'approve', 'reject']);
    tasks.list.and.returnValue(of({ tasks: [task('t1'), task('t2', { version: 2 })], next_cursor: null }) as unknown as ListReturn);
    tasks.approve.and.returnValue(of({}) as unknown as ApproveReturn);
    tasks.reject.and.returnValue(of({}) as unknown as ApproveReturn);

    bonus = jasmine.createSpyObj<BonusService>('BonusService', ['list', 'approve', 'reject']);
    bonus.list.and.returnValue(of({ tasks: [] }) as unknown as ListBonusReturn);
    bonus.approve.and.returnValue(of({}) as unknown as BonusDecisionReturn);
    bonus.reject.and.returnValue(of({}) as unknown as BonusDecisionReturn);

    rewards = jasmine.createSpyObj<RewardsService>('RewardsService', ['redemptionPage', 'approveRedemption', 'rejectRedemption']);
    setRedemptions([]);
    rewards.approveRedemption.and.returnValue(of({}) as unknown as RedeemReturn);
    rewards.rejectRedemption.and.returnValue(of({}) as unknown as RedeemReturn);

    await TestBed.configureTestingModule({
      imports: [ApprovalsComponent, getTranslocoTestingModule()],
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        { provide: TasksService, useValue: tasks },
        { provide: RewardsService, useValue: rewards },
        { provide: BonusService, useValue: bonus },
        {
          provide: FamilyService,
          useValue: {
            listMembers: () =>
              of({
                members: [
                  { id: 'm1', display_name: 'Noah', colour: '#2a9d8f', avatar_id: null, is_child: true, status: 'active' },
                  { id: 'm2', display_name: 'Ava', colour: '#e76f51', avatar_id: null, is_child: true, status: 'active' },
                ],
              }),
          } as unknown as FamilyService,
        },
      ],
    }).compileComponents();
  });

  it('should show the loading state until the tasks arrive', () => {
    tasks.list.and.returnValue(NEVER as unknown as ListReturn);
    build();
    expect(el('[data-testid="tasks-loading"]')).toBeTruthy();
    expect(el('[data-testid="task-list"]')).toBeNull();
  });

  it('should list submitted tasks with a count badge', () => {
    build();
    expect(el('[data-testid="count-tasks"]')?.textContent?.trim()).toBe('2');
    expect(el('[data-testid="task-t1-m1"]')).toBeTruthy();
    expect(tasks.list).toHaveBeenCalledWith({ status: 'submitted', limit: 200 });
  });

  it('should show the checklist of a submitted task with its done steps ticked', () => {
    tasks.list.and.returnValue(
      of({
        tasks: [
          task('t1', {
            checklist: [
              { id: 'c1', text: 'Dishes', done: true },
              { id: 'c2', text: 'Wipe counters', done: false },
            ],
          }),
        ],
        next_cursor: null,
      }) as unknown as ListReturn,
    );
    build();
    const steps = el('[data-testid="task-checklist"]')?.querySelectorAll('li');
    expect(steps?.length).toBe(2);
    expect(steps?.[0].querySelector('.box')?.classList.contains('on')).toBeTrue();
    expect(steps?.[1].querySelector('.box')?.classList.contains('on')).toBeFalse();
  });

  it('should switch between tabs and keep aria-selected in step', () => {
    build();
    expect(el('[data-testid="panel-tasks"]')).toBeTruthy();
    click('[data-testid="tab-requests"]');
    expect(el('[data-testid="panel-requests"]')).toBeTruthy();
    expect(el('[data-testid="panel-tasks"]')).toBeNull();
    expect(el('[data-testid="tab-requests"]')?.getAttribute('aria-selected')).toBe('true');
    expect(el('[data-testid="tab-tasks"]')?.getAttribute('aria-selected')).toBe('false');
  });

  it('should approve a task with its member and version, then reload the list', () => {
    build();
    click('[data-testid="task-approve"]');
    expect(tasks.approve).toHaveBeenCalledWith('t1', 'm1', 1);
    expect(tasks.list.calls.count()).toBe(2);
    expect(el('[data-testid="approvals-notice"]')).toBeTruthy();
  });

  it('should require a note before sending a task back', () => {
    build();
    click('[data-testid="task-send-back"]');
    click('[data-testid="send-back-submit"]');
    expect(el('[data-testid="send-back-error"]')).toBeTruthy();
    expect(tasks.reject).not.toHaveBeenCalled();
  });

  it('should send a task back with the trimmed note', () => {
    build();
    click('[data-testid="task-send-back"]');
    typeInto('[data-testid="send-back-note"]', '  Redo the bathroom  ');
    click('[data-testid="send-back-submit"]');
    expect(tasks.reject).toHaveBeenCalledWith('t1', 'm1', 'Redo the bathroom', 1);
  });

  it('should confirm approve all, approve each task and leave reward requests alone', () => {
    build();
    click('[data-testid="approve-all"]');
    expect(el('[data-testid="approve-all-dialog"]')).toBeTruthy();
    expect(el('[data-testid="confirm-list"]')?.children.length).toBe(2);
    click('[data-testid="approve-all-confirm"]');
    expect(tasks.approve).toHaveBeenCalledTimes(2);
    expect(tasks.approve).toHaveBeenCalledWith('t1', 'm1', 1);
    expect(tasks.approve).toHaveBeenCalledWith('t2', 'm1', 2);
    expect(rewards.approveRedemption).not.toHaveBeenCalled();
  });

  it('should show the changed message and reload when a task was changed elsewhere', () => {
    tasks.approve.and.returnValue(throwError(() => ({ code: 'stale', status: 409 })) as unknown as ApproveReturn);
    build();
    click('[data-testid="task-approve"]');
    expect(el('[data-testid="approvals-error"]')?.textContent).toContain('This item changed');
    expect(tasks.list.calls.count()).toBe(2);
  });

  it('should show the error state for the task list with a retry', () => {
    tasks.list.and.returnValue(throwError(() => ({ status: 500 })) as unknown as ListReturn);
    build();
    expect(el('[data-testid="tasks-error"]')).toBeTruthy();
    click('[data-testid="tasks-error"] [data-testid="error-state-retry"]');
    expect(tasks.list.calls.count()).toBe(2);
  });

  it('should approve a reward request with its version', () => {
    setRedemptions([request('rq1')]);
    build();
    click('[data-testid="tab-requests"]');
    click('[data-testid="request-approve"]');
    expect(rewards.approveRedemption).toHaveBeenCalledWith('rq1', 4);
  });

  it('should require a reason before declining a request', () => {
    setRedemptions([request('rq1')]);
    build();
    click('[data-testid="tab-requests"]');
    click('[data-testid="request-decline"]');
    click('[data-testid="decline-submit"]');
    expect(el('[data-testid="decline-error"]')).toBeTruthy();
    expect(rewards.rejectRedemption).not.toHaveBeenCalled();
  });

  it('should decline a request with the trimmed reason', () => {
    setRedemptions([request('rq1')]);
    build();
    click('[data-testid="tab-requests"]');
    click('[data-testid="request-decline"]');
    typeInto('[data-testid="decline-reason"]', ' Not this week ');
    click('[data-testid="decline-submit"]');
    expect(rewards.rejectRedemption).toHaveBeenCalledWith('rq1', 'Not this week', 4);
  });

  it('should load the next page of history with its cursor', () => {
    rewards.redemptionPage.and.callFake((query) => {
      if (query.status === 'pending') {
        return of({ redemptions: [], next_cursor: null }) as unknown as PageReturn;
      }
      const next = query.cursor ? null : 'c2';
      const id = query.cursor ? 'h2' : 'h1';
      return of({
        redemptions: [request(id, { status: 'approved', decided_at: '2026-10-11T10:00:00Z' })],
        next_cursor: next,
      }) as unknown as PageReturn;
    });
    build();
    click('[data-testid="tab-history"]');
    expect(el('[data-testid="history-table"]')).toBeTruthy();
    click('[data-testid="history-more"]');
    const last = rewards.redemptionPage.calls.mostRecent().args[0];
    expect(last.cursor).toBe('c2');
    expect(last.status).toBe('approved,rejected');
  });

  it('should show the history empty state when nothing is decided yet', () => {
    build();
    click('[data-testid="tab-history"]');
    expect(el('[data-testid="history-empty"]')).toBeTruthy();
  });

  it('should show a submitted bonus claim with a BONUS chip and include it in approve all', () => {
    bonus.list.and.returnValue(of({ tasks: [bonusTask()] }) as unknown as ListBonusReturn);
    build();
    expect(bonus.list).toHaveBeenCalledWith('today');
    expect(el('[data-testid="count-tasks"]')?.textContent?.trim()).toBe('3');
    expect(el('[data-testid="bonus-c1"]')).toBeTruthy();
    expect(el('[data-testid="bonus-chip"]')?.textContent?.trim()).toContain('BONUS');
    click('[data-testid="approve-all"]');
    expect(el('[data-testid="confirm-list"]')?.children.length).toBe(3);
    click('[data-testid="approve-all-confirm"]');
    expect(tasks.approve).toHaveBeenCalledTimes(2);
    expect(bonus.approve).toHaveBeenCalledWith('c1', 3);
    expect(bonus.list.calls.count()).toBe(2);
  });

  it('should approve a bonus claim with its version, then reload', () => {
    bonus.list.and.returnValue(of({ tasks: [bonusTask()] }) as unknown as ListBonusReturn);
    build();
    click('[data-testid="bonus-approve"]');
    expect(bonus.approve).toHaveBeenCalledWith('c1', 3);
    expect(bonus.list.calls.count()).toBe(2);
    expect(el('[data-testid="approvals-notice"]')).toBeTruthy();
  });

  it('should send a bonus claim back with the trimmed note', () => {
    bonus.list.and.returnValue(of({ tasks: [bonusTask()] }) as unknown as ListBonusReturn);
    build();
    click('[data-testid="bonus-send-back"]');
    typeInto('[data-testid="send-back-note"]', '  Fold them properly  ');
    click('[data-testid="send-back-submit"]');
    expect(bonus.reject).toHaveBeenCalledWith('c1', 3, 'Fold them properly');
  });

  it('should send a bonus claim back without a note', () => {
    bonus.list.and.returnValue(of({ tasks: [bonusTask()] }) as unknown as ListBonusReturn);
    build();
    click('[data-testid="bonus-send-back"]');
    click('[data-testid="send-back-submit"]');
    expect(bonus.reject).toHaveBeenCalledWith('c1', 3, '');
    expect(el('[data-testid="send-back-error"]')).toBeNull();
  });

  it('should show how many checklist steps are done', () => {
    tasks.list.and.returnValue(
      of({
        tasks: [
          task('t1', {
            checklist: [
              { id: 'c1', text: 'Dishes', done: true },
              { id: 'c2', text: 'Wipe counters', done: false },
            ],
          }),
        ],
        next_cursor: null,
      }) as unknown as ListReturn,
    );
    build();
    expect(el('[data-testid="task-checklist-progress"]')?.textContent?.trim()).toBe('1 of 2 steps done');
  });

  it('should filter tasks and bonus claims by person and approve only that person', () => {
    tasks.list.and.returnValue(
      of({ tasks: [task('t1'), task('t2', { member_id: 'm2', version: 2 })], next_cursor: null }) as unknown as ListReturn,
    );
    bonus.list.and.returnValue(of({ tasks: [bonusTask()] }) as unknown as ListBonusReturn);
    build();
    expect(el('[data-testid="member-chip-all"]')?.classList.contains('on')).toBeTrue();
    expect(el('[data-testid="count-tasks"]')?.textContent?.trim()).toBe('3');

    click('[data-testid="member-chip-m2"]');
    expect(el('[data-testid="member-chip-m2"]')?.classList.contains('on')).toBeTrue();
    expect(el('[data-testid="task-t2-m2"]')).toBeTruthy();
    expect(el('[data-testid="task-t1-m1"]')).toBeNull();
    expect(el('[data-testid="bonus-c1"]')).toBeNull();
    expect(el('[data-testid="approve-all"]')?.textContent).toContain('Approve all 1');

    click('[data-testid="member-chip-m1"]');
    expect(el('[data-testid="task-t1-m1"]')).toBeTruthy();
    expect(el('[data-testid="task-t2-m2"]')).toBeNull();
    expect(el('[data-testid="bonus-c1"]')).toBeTruthy();

    click('[data-testid="member-chip-m2"]');
    click('[data-testid="approve-all"]');
    expect(el('[data-testid="confirm-list"]')?.children.length).toBe(1);
    click('[data-testid="approve-all-confirm"]');
    expect(tasks.approve).toHaveBeenCalledOnceWith('t2', 'm2', 2);
    expect(bonus.approve).not.toHaveBeenCalled();
  });

  it('should show the person filter on the reward requests and history tabs and reload them for that person', () => {
    setRedemptions([request('rq1', { member_id: 'm2' })]);
    build();
    click('[data-testid="tab-requests"]');
    expect(el('[data-testid="member-filter"]')).toBeTruthy();
    click('[data-testid="member-chip-m2"]');
    const pending = rewards.redemptionPage.calls.allArgs().filter(([q]) => q.status === 'pending');
    expect(pending[pending.length - 1][0].member_id).toBe('m2');
    click('[data-testid="tab-history"]');
    click('[data-testid="member-chip-all"]');
    const history = rewards.redemptionPage.calls.allArgs().filter(([q]) => q.status === 'approved,rejected');
    expect(history[history.length - 1][0].member_id).toBeUndefined();
  });
});
