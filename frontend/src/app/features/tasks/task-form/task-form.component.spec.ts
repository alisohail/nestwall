import type { Mock } from "vitest";
import { provideHttpClient, withXhr } from '@angular/common/http';
import { provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Router, provideRouter } from '@angular/router';
import { of } from 'rxjs';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { FamilyService } from '../../family/family.service';
import { TasksService } from '../tasks.service';
import { TaskFormComponent } from './task-form.component';
import type { Task } from '../tasks.models';

describe('TaskFormComponent points', () => {
    let fixture: ComponentFixture<TaskFormComponent>;
    let component: TaskFormComponent;
    let create: Mock;

    beforeEach(async () => {
        create = vi.fn().mockName('create').mockReturnValue(of({ id: 't1' }));
        const tasks = {
            categories: () => of({ categories: [] }),
            templates: () => of({ templates: [], next_cursor: null }),
            create,
        } as unknown as TasksService;

        await TestBed.configureTestingModule({
            imports: [TaskFormComponent, getTranslocoTestingModule()],
            providers: [
                provideRouter([]),
                provideHttpClient(withXhr()),
                provideHttpClientTesting(),
                { provide: TasksService, useValue: tasks },
                { provide: FamilyService, useValue: { listMembers: () => of({ members: [] }) } as unknown as FamilyService },
            ],
        }).compileComponents();

        vi.spyOn(TestBed.inject(Router), 'navigateByUrl').mockResolvedValue(true);
        fixture = TestBed.createComponent(TaskFormComponent);
        component = fixture.componentInstance;
        fixture.detectChanges();
        await fixture.whenStable();
        fixture.detectChanges();
    });

    function pointsInput(): HTMLInputElement {
        return fixture.nativeElement.querySelector('input[formcontrolname="points"]') as HTMLInputElement;
    }

    it('starts at 0 instead of a placeholder', () => {
        expect(component.f.points.value).toBe(0);
        expect(pointsInput().value).toBe('0');
        expect(pointsInput().placeholder).toBe('');
    });

    it('asks which copies to change before saving a repeating task', async () => {
        const update = vi.fn().mockReturnValue(of({}));
        (TestBed.inject(TasksService) as unknown as { update: typeof update }).update = update;
        component.task.set({ id: 't1', version: 3, repeats: true, title: 'Feed the cat', checklist: [], assignees: [] } as unknown as Task);
        const saving = (component as unknown as { saveEdit: () => Promise<boolean> }).saveEdit();
        expect(component.scopePrompt()).toBe(true);
        expect(update).not.toHaveBeenCalled();
        component.settleScope('series');
        await expect(saving).resolves.toBe(true);
        expect(update).toHaveBeenCalledWith('t1', expect.any(Object), 3, 'series');
    });

    it('does not save a repeating task when the scope prompt is cancelled', async () => {
        const update = vi.fn().mockReturnValue(of({}));
        (TestBed.inject(TasksService) as unknown as { update: typeof update }).update = update;
        component.task.set({ id: 't1', version: 3, repeats: true, title: 'Feed the cat', checklist: [], assignees: [] } as unknown as Task);
        const saving = (component as unknown as { saveEdit: () => Promise<boolean> }).saveEdit();
        component.settleScope(null);
        await expect(saving).resolves.toBe(false);
        expect(update).not.toHaveBeenCalled();
    });

    it('opens the scope popup when a repeating task is saved from the form', async () => {
        Object.defineProperty(component, 'editing', { value: true });
        const update = vi.fn().mockReturnValue(of({}));
        (TestBed.inject(TasksService) as unknown as { update: typeof update }).update = update;
        const task = {
            id: 't1',
            version: 3,
            repeats: true,
            title: 'Feed the cat',
            description: null,
            category_id: null,
            due_date: '2026-11-02',
            due_time: null,
            start_time: null,
            priority: 'normal',
            part_of_day: null,
            points: 5,
            reminder_minutes: null,
            expires_after_minutes: null,
            penalty_unit: null,
            penalty_value: null,
            checklist: [],
            assignees: [{ member_id: 'm1', status: 'to_do' }],
        } as unknown as Task;
        component.task.set(task);
        (component as unknown as { fillFromTask: (t: unknown) => void }).fillFromTask(task);
        component.save();
        await fixture.whenStable();
        expect(component.saveError()).toBeNull();
        expect(component.scopePrompt()).toBe(true);
        expect(update).not.toHaveBeenCalled();
        component.settleScope(null);
    });

    it('preselects the assignees when editing an assigned task', () => {
        const task = {
            id: 't1',
            title: 'Feed the cat',
            due_date: '2026-11-02',
            priority: 'normal',
            points: 5,
            checklist: [],
            assignees: [{ member_id: 'm1' }, { member_id: 'm2' }],
        } as unknown;
        (component as unknown as { fillFromTask: (t: unknown) => void }).fillFromTask(task);
        expect(component.f.assignees.value).toEqual(['m1', 'm2']);
    });

    it('rejects an empty points value as required', () => {
        component.f.points.setValue(null);
        expect(component.f.points.hasError('required')).toBe(true);
    });

    it('rejects points above the maximum', () => {
        component.f.points.setValue(1001);
        expect(component.f.points.hasError('max')).toBe(true);
    });

    it('accepts 0 as a valid points value', () => {
        component.f.points.setValue(0);
        expect(component.f.points.valid).toBe(true);
    });

    it('blocks save and shows the field error when points are cleared', () => {
        component.f.title.setValue('Make bed');
        component.f.assignees.setValue(['k1']);
        component.f.points.setValue(null);
        component.save();
        fixture.detectChanges();
        expect(create).not.toHaveBeenCalled();
        expect(fixture.nativeElement.querySelector('input[formcontrolname="points"]').getAttribute('aria-invalid')).toBe('true');
    });

    it('adds to a numeric points value instead of joining digits', () => {
        component.f.points.setValue('2' as unknown as number);
        component.bumpPoints(1);
        expect(component.f.points.value).toBe(3);
    });

    it('sends 0 points when the value is 0', () => {
        component.f.title.setValue('Make bed');
        component.f.assignees.setValue(['k1']);
        component.save();
        expect(create).toHaveBeenCalledWith(expect.objectContaining({ points: 0 }));
    });
});
