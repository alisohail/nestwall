import type { MockedObject } from "vitest";
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ActivatedRoute, convertToParamMap, provideRouter } from '@angular/router';
import { of, throwError } from 'rxjs';
import { AuthService } from '../../../core/auth/auth.service';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { mockOf } from '../../../testing/mock-of';
import { Task } from '../../tasks/tasks.models';
import { TasksService } from '../../tasks/tasks.service';
import { addDaysIso, todayIso } from '../../tasks/tasks.format';
import { MyTasksService } from '../my-tasks.service';
import { MyTaskComponent } from './my-task.component';

function makeTask(over: Partial<Task> = {}): Task {
    return {
        id: 'o1',
        template_id: null,
        title: 'Feed the cat',
        description: 'Dry food, then fresh water.',
        category_id: null,
        part_of_day: 'morning',
        priority: 'normal',
        due_date: todayIso(),
        due_time: null,
        due_at: '2026-10-09T08:00:00Z',
        expires_at: null,
        reminder_minutes_before: null,
        late_points_percent: null,
        archived: false,
        assignee_ids: ['m1'],
        assignees: [{ member_id: 'm1', status: 'to_do' }],
        checklist: [
            { id: 's1', text: 'Fill the bowl', done: false },
            { id: 's2', text: 'Fresh water', done: false },
        ],
        version: 3,
        points: 2,
        approval_required: true,
        expires_after_minutes: null,
        penalty_value: null,
        penalty_unit: null,
        ...over,
    };
}

describe('MyTaskComponent', () => {
    let tasks: MockedObject<TasksService>;
    let mine: MockedObject<MyTasksService>;

    async function setup(task: Task | null, error: unknown = null, manages = false, from: string | null = null): Promise<ComponentFixture<MyTaskComponent>> {
        tasks = mockOf<TasksService>({
            get: vi.fn().mockName("TasksService.get"),
            complete: vi.fn().mockName("TasksService.complete")
        });
        if (error)
            tasks.get.mockReturnValue(throwError(() => error));
        else
            tasks.get.mockReturnValue(of(task ?? makeTask()));
        tasks.complete.mockReturnValue(of(makeTask({ assignees: [{ member_id: 'm1', status: 'submitted' }] })));
        mine = mockOf<MyTasksService>({
            tickChecklist: vi.fn().mockName("MyTasksService.tickChecklist"),
            withdraw: vi.fn().mockName("MyTasksService.withdraw")
        });

        await TestBed.configureTestingModule({
            imports: [MyTaskComponent, getTranslocoTestingModule()],
            providers: [
                provideRouter([]),
                { provide: TasksService, useValue: tasks },
                { provide: MyTasksService, useValue: mine },
                {
                    provide: AuthService,
                    useValue: {
                        currentUser: () => ({ user: { is_child: !manages }, family: { member_id: 'm1' } }),
                        loadPermissions: () => of(undefined),
                        can: (code: string) => manages && code === 'tasks.manage',
                    },
                },
                {
                    provide: ActivatedRoute,
                    useValue: {
                        paramMap: of(convertToParamMap({ id: 'o1' })),
                        queryParamMap: of(convertToParamMap(from ? { from } : {})),
                        snapshot: { paramMap: convertToParamMap({ id: 'o1' }) },
                    },
                },
            ],
        }).compileComponents();

        const fixture = TestBed.createComponent(MyTaskComponent);
        fixture.detectChanges();
        return fixture;
    }

    function finishButton(fixture: ComponentFixture<MyTaskComponent>): HTMLButtonElement {
        return fixture.nativeElement.querySelector('[data-testid="task-finish"]') as HTMLButtonElement;
    }

    function stepButton(fixture: ComponentFixture<MyTaskComponent>, id: string): HTMLButtonElement {
        return fixture.nativeElement.querySelector(`[data-testid="step-${id}"]`) as HTMLButtonElement;
    }

    it('offers Undo on a submitted copy and sends it back to to do', async () => {
        const fixture = await setup(makeTask({ assignees: [{ member_id: 'm1', status: 'submitted' }] }));
        mine.withdraw.mockReturnValue(of(makeTask({ version: 4 })));
        const undo = fixture.nativeElement.querySelector('[data-testid="task-undo"]') as HTMLButtonElement;
        expect(undo).not.toBeNull();
        expect(undo.textContent?.trim()).toBe('Move to to-do');
        undo.click();
        fixture.detectChanges();
        expect(mine.withdraw).toHaveBeenCalledWith('o1', 3);
        expect(fixture.nativeElement.querySelector('[data-testid="task-undo"]')).toBeNull();
        expect(finishButton(fixture)).not.toBeNull();
    });

    it('shows the error when Undo fails and keeps the copy submitted', async () => {
        const fixture = await setup(makeTask({ assignees: [{ member_id: 'm1', status: 'submitted' }] }));
        mine.withdraw.mockReturnValue(throwError(() => ({ error: { code: 'invalid_transition' } })));
        (fixture.nativeElement.querySelector('[data-testid="task-undo"]') as HTMLButtonElement).click();
        fixture.detectChanges();
        expect(fixture.nativeElement.querySelector('[data-testid="task-action-error"]')).not.toBeNull();
        expect(fixture.nativeElement.querySelector('[data-testid="task-undo"]')).not.toBeNull();
    });

    it('enables "I\'m done!" while steps are still open, because steps are optional', async () => {
        const fixture = await setup(null);
        expect(finishButton(fixture).disabled).toBe(false);
        expect(stepButton(fixture, 's1')).not.toBeNull();
    });

    it('ticks a step with the task version and shows the response', async () => {
        const fixture = await setup(null);
        mine.tickChecklist.mockReturnValue(of(makeTask({ version: 4, checklist: [{ id: 's1', text: 'Fill the bowl', done: true }, { id: 's2', text: 'Fresh water', done: false }] })));
        stepButton(fixture, 's1').click();
        fixture.detectChanges();
        expect(mine.tickChecklist).toHaveBeenCalledWith('o1', 's1', true, 3);
        expect(stepButton(fixture, 's1').getAttribute('aria-pressed')).toBe('true');
    });

    it('finishes the copy once every step is done', async () => {
        const fixture = await setup(makeTask({
            checklist: [
                { id: 's1', text: 'Fill the bowl', done: true },
                { id: 's2', text: 'Fresh water', done: true },
            ],
        }));
        expect(finishButton(fixture).disabled).toBe(false);
        finishButton(fixture).click();
        fixture.detectChanges();
        expect(tasks.complete).not.toHaveBeenCalled();
        (fixture.nativeElement.querySelector('[data-testid="task-confirm-ok"]') as HTMLButtonElement).click();
        fixture.detectChanges();
        expect(tasks.complete).toHaveBeenCalledWith('o1', 'm1');
        expect(fixture.nativeElement.querySelector('[data-testid="task-submitted"]')).not.toBeNull();
    });

    it('replaces "I\'m done!" with the due day while the copy is not yet due', async () => {
        const fixture = await setup(makeTask({
            due_date: addDaysIso(todayIso(), 2),
            checklist: [
                { id: 's1', text: 'Fill the bowl', done: true },
                { id: 's2', text: 'Fresh water', done: true },
            ],
        }));
        expect(fixture.nativeElement.querySelector('[data-testid="task-finish"]')).toBeNull();
        expect(fixture.nativeElement.querySelector('[data-testid="task-not-due"]')).not.toBeNull();
        expect(tasks.complete).not.toHaveBeenCalled();
    });

    it('locks the steps of a child copy that is not yet due', async () => {
        const fixture = await setup(makeTask({ due_date: addDaysIso(todayIso(), 2) }));
        expect(stepButton(fixture, 's1').disabled).toBe(true);
        stepButton(fixture, 's1').click();
        expect(mine.tickChecklist).not.toHaveBeenCalled();
    });

    it('lets a parent act on a copy that is not yet due', async () => {
        const fixture = await setup(makeTask({
            due_date: addDaysIso(todayIso(), 2),
            checklist: [
                { id: 's1', text: 'Fill the bowl', done: true },
                { id: 's2', text: 'Fresh water', done: true },
            ],
        }), null, true);
        expect(stepButton(fixture, 's1').disabled).toBe(false);
        expect(finishButton(fixture).disabled).toBe(false);
        expect(fixture.nativeElement.querySelector('[data-testid="task-not-due"]')).toBeNull();
    });

    it('shows the not-found state when the copy does not exist', async () => {
        const fixture = await setup(null, { status: 404 });
        expect(fixture.nativeElement.querySelector('[data-testid="task-missing"]')).not.toBeNull();
    });

    it('shows the load error screen and retries the copy when the load fails', async () => {
        const fixture = await setup(null, { code: 'server', status: 500 });
        expect(fixture.nativeElement.querySelector('[data-testid="task-loading"]')).toBeNull();
        expect(fixture.nativeElement.querySelector('[data-testid="task-error"] app-load-error')).not.toBeNull();
        tasks.get.mockReturnValue(of(makeTask()));
        (fixture.nativeElement.querySelector('[data-testid="load-error-retry"]') as HTMLButtonElement).click();
        fixture.detectChanges();
        expect(tasks.get).toHaveBeenCalledTimes(2);
        expect(fixture.nativeElement.querySelector('[data-testid="task-error"]')).toBeNull();
    });

    it('goes back to the tab the task was opened from', async () => {
        const fixture = await setup(null, null, false, 'waiting');
        const back = fixture.nativeElement.querySelector('[data-testid="task-back"]') as HTMLAnchorElement;
        expect(back.getAttribute('href')).toBe('/my-tasks?tab=waiting');
    });

    it('goes back to Today when the task was opened without a tab', async () => {
        const fixture = await setup(null);
        const back = fixture.nativeElement.querySelector('[data-testid="task-back"]') as HTMLAnchorElement;
        expect(back.getAttribute('href')).toBe('/my-tasks?tab=today');
    });

    it('goes back to the Today page when the task was opened from it', async () => {
        const fixture = await setup(null, null, false, 'home');
        const back = fixture.nativeElement.querySelector('[data-testid="task-back"]') as HTMLAnchorElement;
        expect(back.getAttribute('href')).toBe('/today');
    });
});
