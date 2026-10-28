import type { MockedObject } from "vitest";
import { HttpErrorResponse, provideHttpClient, withXhr } from '@angular/common/http';
import { provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { of, throwError } from 'rxjs';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { MemberDetail } from '../../family/family.service';
import { Task } from '../tasks.models';
import { TasksService } from '../tasks.service';
import { mockOf } from '../../../testing/mock-of';
import { RemovePersonConfirmComponent } from './remove-person-confirm.component';

describe('RemovePersonConfirmComponent', () => {
    let tasks: MockedObject<TasksService>;
    let fixture: ComponentFixture<RemovePersonConfirmComponent>;

    const ava: MemberDetail = {
        id: 'm1',
        display_name: 'Ava',
        colour: 'teal',
        avatar_id: null,
    } as unknown as MemberDetail;
    const sam: MemberDetail = { ...ava, id: 'm2', display_name: 'Sam' };

    function task(overrides: {
        assignees: {
            id: string;
        }[];
        repeats?: boolean;
    }): Task {
        return { version: 4, repeats: false, title: 'Bins', ...overrides } as unknown as Task;
    }

    function el(testId: string): HTMLElement | null {
        return fixture.nativeElement.querySelector(`[data-testid="${testId}"]`);
    }

    function create(people: MemberDetail[] = [ava]): void {
        fixture = TestBed.createComponent(RemovePersonConfirmComponent);
        fixture.componentRef.setInput('taskId', 'occ1');
        fixture.componentRef.setInput('title', 'Bins');
        fixture.componentRef.setInput('people', people);
        fixture.detectChanges();
    }

    beforeEach(() => {
        tasks = mockOf<TasksService>({
            get: vi.fn().mockName("TasksService.get"),
            removePerson: vi.fn().mockName("TasksService.removePerson")
        });
        TestBed.configureTestingModule({
            imports: [RemovePersonConfirmComponent, getTranslocoTestingModule()],
            providers: [
                { provide: TasksService, useValue: tasks },
                provideHttpClient(withXhr()),
                provideHttpClientTesting(),
            ],
        });
    });

    it('loads the task and says the task is removed when this person is the only one on the day', () => {
        tasks.get.mockReturnValue(of(task({ assignees: [{ id: 'm1' }] })));

        create();

        expect(tasks.get).toHaveBeenCalledWith('occ1');
        expect(el('remove-person-picker')).toBeNull();
        expect(el('remove-scope')).toBeNull();
        expect(el('remove-person-message')?.textContent).toContain('archived for everyone');
    });

    it('asks which day or all repeats for a repeating task, with this day selected by default', () => {
        tasks.get.mockReturnValue(of(task({ repeats: true, assignees: [{ id: 'm1' }, { id: 'm2' }] })));

        create();

        expect(el('remove-scope')).not.toBeNull();
        expect((el('remove-scope-occurrence') as HTMLInputElement).checked).toBe(true);
        expect((el('remove-scope-series') as HTMLInputElement).checked).toBe(false);
    });

    it('removes this day for the chosen person when this day is selected', () => {
        tasks.get.mockReturnValue(of(task({ repeats: true, assignees: [{ id: 'm1' }, { id: 'm2' }] })));
        tasks.removePerson.mockReturnValue(of({} as Task));
        create([ava, sam]);

        (el('remove-person-ok') as HTMLButtonElement).click();

        expect(tasks.removePerson).toHaveBeenCalledWith('occ1', 4, 'm1', 'occurrence');
    });

    it('removes all repeats for the picked person when all repeats is selected', () => {
        tasks.get.mockReturnValue(of(task({ repeats: true, assignees: [{ id: 'm1' }, { id: 'm2' }] })));
        tasks.removePerson.mockReturnValue(of({} as Task));
        create([ava, sam]);

        (el('remove-scope-series') as HTMLInputElement).click();
        fixture.detectChanges();
        (fixture.nativeElement.querySelector('input[name="remove-person-for"][value="m2"]') as HTMLInputElement).click();
        fixture.detectChanges();
        (el('remove-person-ok') as HTMLButtonElement).click();

        expect(tasks.removePerson).toHaveBeenCalledWith('occ1', 4, 'm2', 'series');
    });

    it('shows a picker only when more than one person can be removed', () => {
        tasks.get.mockReturnValue(of(task({ assignees: [{ id: 'm1' }, { id: 'm2' }] })));

        create([ava, sam]);

        expect(el('remove-person-picker')).not.toBeNull();
        expect(el('remove-person-picker')?.querySelectorAll('input[type="radio"]').length).toBe(2);
    });

    it('emits removed after the copy is removed', () => {
        tasks.get.mockReturnValue(of(task({ assignees: [{ id: 'm1' }, { id: 'm2' }] })));
        tasks.removePerson.mockReturnValue(of({} as Task));
        create([ava, sam]);
        const removed = vi.fn().mockName('removed');
        fixture.componentInstance.removed.subscribe(removed);

        (el('remove-person-ok') as HTMLButtonElement).click();

        expect(removed).toHaveBeenCalled();
        expect(el('remove-person-error')).toBeNull();
    });

    it('shows the error and stays open when the removal fails', () => {
        tasks.get.mockReturnValue(of(task({ assignees: [{ id: 'm1' }, { id: 'm2' }] })));
        tasks.removePerson.mockReturnValue(throwError(() => new HttpErrorResponse({ status: 412 })));
        create([ava, sam]);
        const removed = vi.fn().mockName('removed');
        fixture.componentInstance.removed.subscribe(removed);

        (el('remove-person-ok') as HTMLButtonElement).click();
        fixture.detectChanges();

        expect(removed).not.toHaveBeenCalled();
        expect(el('remove-person')).not.toBeNull();
        expect(el('remove-person-error')).not.toBeNull();
    });

    it('shows a load error and does not allow removal when the task cannot be loaded', () => {
        tasks.get.mockReturnValue(throwError(() => new HttpErrorResponse({ status: 404 })));

        create();

        expect(el('remove-person-load-error')).not.toBeNull();
        expect((el('remove-person-ok') as HTMLButtonElement).disabled).toBe(true);
        expect(tasks.removePerson).not.toHaveBeenCalled();
    });

    it('emits cancelled on Escape', () => {
        tasks.get.mockReturnValue(of(task({ assignees: [{ id: 'm1' }] })));
        create();
        const cancelled = vi.fn().mockName('cancelled');
        fixture.componentInstance.cancelled.subscribe(cancelled);

        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));

        expect(cancelled).toHaveBeenCalled();
    });
});
