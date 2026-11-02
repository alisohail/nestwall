import type { MockedObject } from "vitest";
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { of, throwError } from 'rxjs';
import { AuthService } from '../../../core/auth/auth.service';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { TasksService } from '../tasks.service';
import { Task } from '../tasks.models';
import { mockOf } from '../../../testing/mock-of';
import { TaskDetailComponent } from './task-detail.component';

describe('TaskDetailComponent', () => {
    let fixture: ComponentFixture<TaskDetailComponent>;
    let service: MockedObject<TasksService>;

    beforeEach(async () => {
        service = mockOf<TasksService>({
            get: vi.fn().mockName("TasksService.get"),
            markMissed: vi.fn().mockName("TasksService.markMissed")
        });
        service.get.mockReturnValue(throwError(() => ({ code: 'server', status: 500 })));

        await TestBed.configureTestingModule({
            imports: [TaskDetailComponent, getTranslocoTestingModule()],
            providers: [
                { provide: TasksService, useValue: service },
                { provide: AuthService, useValue: { currentUser: () => null, can: () => false } },
            ],
        }).compileComponents();

        fixture = TestBed.createComponent(TaskDetailComponent);
        fixture.componentRef.setInput('taskId', 't1');
        fixture.componentRef.setInput('memberId', 'm1');
        fixture.detectChanges();
        await fixture.whenStable();
        fixture.detectChanges();
    });

    it('shows the load error screen instead of the loader when the load fails, and retry loads again', async () => {
        const el = fixture.nativeElement as HTMLElement;
        expect(el.querySelector('app-loading')).toBeNull();
        expect(el.querySelector('app-load-error')).not.toBeNull();
        expect(service.get).toHaveBeenCalledTimes(1);

        (el.querySelector('[data-testid="load-error-retry"]') as HTMLButtonElement).click();
        fixture.detectChanges();
        await fixture.whenStable();
        fixture.detectChanges();

        expect(service.get).toHaveBeenCalledTimes(2);
        expect(el.querySelector('app-load-error')).not.toBeNull();
    });

    it('offers mark as missed only while some copy is still open', () => {
        const c = fixture.componentInstance;
        c.task.set({ id: 't1', archived: false, version: 2, assignees: [{ member_id: 'm1', status: 'to_do' }] } as unknown as Task);
        expect(c.canMarkMissed()).toBe(true);

        c.task.set({ id: 't1', archived: false, version: 2, assignees: [{ member_id: 'm1', status: 'approved' }] } as unknown as Task);
        expect(c.canMarkMissed()).toBe(false);
    });

    it('marks the task missed with its version and tells the list to refresh', () => {
        const c = fixture.componentInstance;
        const changed = vi.fn();
        c.changed.subscribe(changed);
        service.markMissed.mockReturnValue(of({} as Task));
        c.task.set({ id: 't1', archived: false, version: 2, assignees: [{ member_id: 'm1', status: 'to_do' }] } as unknown as Task);

        c.markMissed();

        expect(service.markMissed).toHaveBeenCalledWith('t1', 2);
        expect(changed).toHaveBeenCalledTimes(1);
    });
});
