import type { MockedObject } from "vitest";
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { throwError } from 'rxjs';
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
            get: vi.fn().mockName("TasksService.get")
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

    it('does not offer skip or mark as missed from the popup', () => {
        const c = fixture.componentInstance;
        c.task.set({ id: 't1', title: 'Read', archived: false, version: 2, due_date: '2026-10-09', assignees: [{ member_id: 'm1', status: 'to_do' }] } as unknown as Task);

        const el = fixture.nativeElement as HTMLElement;
        expect(el.querySelector('[data-testid="task-skip"]')).toBeNull();
        expect(el.querySelector('[data-testid="task-mark-missed"]')).toBeNull();
    });
});
