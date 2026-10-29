import type { MockedObject } from "vitest";
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { of, throwError } from 'rxjs';
import { AuthService } from '../../../core/auth/auth.service';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { MyTaskItem } from '../../my-tasks/my-tasks.models';
import { MyTasksService } from '../../my-tasks/my-tasks.service';
import { Task } from '../../tasks/tasks.models';
import { TasksService } from '../../tasks/tasks.service';
import { TodayTaskComponent } from './today-task.component';
import { mockOf } from '../../../testing/mock-of';

function item(over: Partial<MyTaskItem> = {}): MyTaskItem {
    return {
        task_id: 'o1',
        title: 'Practise piano',
        category_emoji: null,
        category_colour: '#2563eb',
        part_of_day: 'after_school',
        due_date: '2026-10-09',
        due_at: '2026-10-09T15:00:00Z',
        expires_at: null,
        points: 3,
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
        version: 4,
        checklist: [
            { id: 's1', text: 'Scales', done: true },
            { id: 's2', text: 'Piece', done: false },
        ],
        ...over,
    };
}

function el(fixture: ComponentFixture<TodayTaskComponent>, testId: string): HTMLElement | null {
    return fixture.nativeElement.querySelector(`[data-testid="${testId}"]`);
}

describe('TodayTaskComponent', () => {
    let myTasks: MockedObject<MyTasksService>;
    let tasks: MockedObject<TasksService>;

    beforeEach(async () => {
        myTasks = mockOf<MyTasksService>({
            tickChecklist: vi.fn().mockName("MyTasksService.tickChecklist")
        });
        tasks = mockOf<TasksService>({
            complete: vi.fn().mockName("TasksService.complete")
        });
        tasks.complete.mockReturnValue(of({} as unknown as Task));

        await TestBed.configureTestingModule({
            imports: [TodayTaskComponent, getTranslocoTestingModule()],
            providers: [
                provideRouter([]),
                { provide: MyTasksService, useValue: myTasks },
                { provide: TasksService, useValue: tasks },
                { provide: AuthService, useValue: { currentUser: () => ({ user: { display_name: 'Ava' }, family: { member_id: 'm-ava' } }) } },
            ],
        }).compileComponents();
    });

    function create(over: Partial<MyTaskItem> = {}): ComponentFixture<TodayTaskComponent> {
        const fixture = TestBed.createComponent(TodayTaskComponent);
        fixture.componentRef.setInput('item', item(over));
        fixture.detectChanges();
        return fixture;
    }

    it('shows the steps and the count with an I am done button', () => {
        const fixture = create();
        expect(el(fixture, 'today-task')).not.toBeNull();
        expect(el(fixture, 'today-task-count')?.textContent).toContain('1');
        expect(fixture.nativeElement.querySelectorAll('[data-testid="today-task-step"]').length).toBe(2);
        expect(el(fixture, 'today-task-finish')).not.toBeNull();
        expect(el(fixture, 'today-task-complete')).toBeNull();
    });

    it('links the title to the task detail and remembers the tab it was opened from', () => {
        const fixture = create();
        const link = el(fixture, 'today-task-title') as HTMLAnchorElement;
        expect(link.getAttribute('href')).toBe('/my-tasks/o1?from=today');

        const fromTab = TestBed.createComponent(TodayTaskComponent);
        fromTab.componentRef.setInput('item', item());
        fromTab.componentRef.setInput('origin', 'past_due');
        fromTab.detectChanges();
        expect((el(fromTab, 'today-task-title') as HTMLAnchorElement).getAttribute('href')).toBe('/my-tasks/o1?from=past_due');
    });

    it('I am done is enabled even when steps are still open, because steps are optional', () => {

        const open = create();
        expect((el(open, 'today-task-finish') as HTMLButtonElement).disabled).toBe(false);

        const allDone = create({
            checklist: [
                { id: 's1', text: 'Scales', done: true },
                { id: 's2', text: 'Piece', done: true },
            ],
        });
        expect((el(allDone, 'today-task-finish') as HTMLButtonElement).disabled).toBe(false);
    });

    it('I am done completes the copy with steps still open', () => {
        const fixture = create();
        const changed = vi.fn().mockName('changed');
        fixture.componentInstance.changed.subscribe(changed);
        tasks.complete.mockReturnValue(of({} as unknown as Task));

        (el(fixture, 'today-task-finish') as HTMLButtonElement).click();
        fixture.detectChanges();
        expect(tasks.complete).not.toHaveBeenCalled();
        (el(fixture, 'task-confirm-ok') as HTMLButtonElement).click();

        expect(tasks.complete).toHaveBeenCalledWith('o1', 'm-ava');
        expect(changed).toHaveBeenCalled();
    });

    it('ticking a step saves it with the copy version and does not complete the task', () => {
        const fixture = create();
        const ticked = item({
            version: 5,
            checklist: [
                { id: 's1', text: 'Scales', done: true },
                { id: 's2', text: 'Piece', done: true },
            ],
        });
        myTasks.tickChecklist.mockReturnValue(of(ticked as unknown as Task));
        const changed = vi.fn().mockName('changed');
        fixture.componentInstance.changed.subscribe(changed);

        (fixture.nativeElement.querySelectorAll('[data-testid="today-task-step"]')[1] as HTMLButtonElement).click();
        fixture.detectChanges();

        expect(myTasks.tickChecklist).toHaveBeenCalledWith('o1', 's2', true, 4);
        expect(tasks.complete).not.toHaveBeenCalled();
        expect(changed).toHaveBeenCalled();
        expect(el(fixture, 'today-task-count')?.textContent).toContain('All done');
    });

    it('a task with no steps asks for confirmation before completing', () => {
        const fixture = create({ checklist: [] });
        expect(el(fixture, 'today-task-finish')).toBeNull();
        const changed = vi.fn().mockName('changed');
        fixture.componentInstance.changed.subscribe(changed);

        (el(fixture, 'today-task-complete') as HTMLButtonElement).click();
        fixture.detectChanges();

        expect(el(fixture, 'task-confirm')).not.toBeNull();
        expect(tasks.complete).not.toHaveBeenCalled();

        (el(fixture, 'task-confirm-ok') as HTMLButtonElement).click();

        expect(tasks.complete).toHaveBeenCalledWith('o1', 'm-ava');
        expect(myTasks.tickChecklist).not.toHaveBeenCalled();
        expect(changed).toHaveBeenCalled();
    });

    it('warns about unfinished steps in the confirm step but still allows the submit', () => {
        const fixture = create({
            checklist: [
                { id: 's1', text: 'Fill the bowl', done: true },
                { id: 's2', text: 'Fresh water', done: false },
            ],
        });
        (el(fixture, 'today-task-finish') as HTMLButtonElement).click();
        fixture.detectChanges();

        expect(el(fixture, 'task-confirm')?.textContent).toContain('still not done');
        (el(fixture, 'task-confirm-ok') as HTMLButtonElement).click();

        expect(tasks.complete).toHaveBeenCalledWith('o1', 'm-ava');
    });

    it('shows a translated error and emits changed when a tick fails', () => {
        const fixture = create();
        myTasks.tickChecklist.mockReturnValue(throwError(() => ({ status: 500 })));
        const changed = vi.fn().mockName('changed');
        fixture.componentInstance.changed.subscribe(changed);

        (fixture.nativeElement.querySelector('[data-testid="today-task-step"]') as HTMLButtonElement).click();
        fixture.detectChanges();

        expect(fixture.nativeElement.querySelector('p.error')).not.toBeNull();
        expect(changed).toHaveBeenCalled();
    });
});
