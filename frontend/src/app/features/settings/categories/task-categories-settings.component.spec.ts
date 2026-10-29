import type { MockedObject } from "vitest";
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { of, throwError } from 'rxjs';
import { ApiError } from '../../../core/api/api-client.service';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { mockOf } from '../../../testing/mock-of';
import { TaskCategory, TaskSettingsResponse } from '../../tasks/tasks.models';
import { TasksService } from '../../tasks/tasks.service';
import { TaskCategoriesSettingsComponent } from './task-categories-settings.component';

type Categories = ReturnType<TasksService['categories']>;
type Settings = ReturnType<TasksService['settings']>;
type Create = ReturnType<TasksService['createCategory']>;

const settings = {
    defaults: { points: null, missed_after: null, approval_required: null },
    part_of_day: { morning_start: '07:00', after_school_start: '15:00', evening_start: '18:00' },
    version: 1,
} as unknown as TaskSettingsResponse;

const cleaning = {
    id: 'c1',
    name: 'Cleaning',
    emoji: '🧹',
    colour: '#3b82f6',
    points: null,
    approval_required: null,
    expires_after_minutes: null,
    sort_order: 1,
    version: 1,
} as unknown as TaskCategory;

describe('TaskCategoriesSettingsComponent', () => {
    let fixture: ComponentFixture<TaskCategoriesSettingsComponent>;
    let component: TaskCategoriesSettingsComponent;
    let tasks: MockedObject<TasksService>;

    async function build(): Promise<void> {
        fixture = TestBed.createComponent(TaskCategoriesSettingsComponent);
        component = fixture.componentInstance;
        fixture.detectChanges();
        await fixture.whenStable();
        fixture.detectChanges();
    }

    function el<T extends Element = HTMLElement>(selector: string): T | null {
        return fixture.nativeElement.querySelector(selector) as T | null;
    }

    beforeEach(async () => {
        tasks = mockOf<TasksService>({
            settings: vi.fn().mockName("TasksService.settings"),
            categories: vi.fn().mockName("TasksService.categories"),
            createCategory: vi.fn().mockName("TasksService.createCategory"),
            updateCategory: vi.fn().mockName("TasksService.updateCategory")
        });
        tasks.settings.mockReturnValue(of(settings) as unknown as Settings);
        tasks.categories.mockReturnValue(of({ categories: [cleaning] }) as unknown as Categories);
        tasks.createCategory.mockReturnValue(of(cleaning) as unknown as Create);

        await TestBed.configureTestingModule({
            imports: [TaskCategoriesSettingsComponent, getTranslocoTestingModule()],
            providers: [provideRouter([]), { provide: TasksService, useValue: tasks }],
        }).compileComponents();
    });

    it('should show the full-page load error and retry the load when loading fails', async () => {
        tasks.settings.mockReturnValue(throwError(() => ({ code: 'internal', status: 500 }) as ApiError) as unknown as Settings);
        await build();
        expect(el('app-loading')).toBeNull();
        expect(el('app-load-error')).toBeTruthy();

        tasks.settings.mockReturnValue(of(settings) as unknown as Settings);
        el<HTMLButtonElement>('[data-testid="load-error-retry"]')?.click();
        fixture.detectChanges();
        await fixture.whenStable();
        fixture.detectChanges();
        expect(tasks.settings).toHaveBeenCalledTimes(2);
        expect(el('app-load-error')).toBeNull();
        expect(component.categories().length).toBe(1);
    });

    it('should wrap the preset pictures so they stay inside the dialog', async () => {
        await build();
        component.openNew();
        fixture.detectChanges();
        expect(el('.emoji-row')?.classList).toContain('wrap');
        expect(el('.emoji-row')?.querySelectorAll('.emoji-btn').length).toBe(component.emojis.length);
    });

    it('should show the typed picture in the custom input and keep it on the form', async () => {
        await build();
        component.openNew();
        fixture.detectChanges();
        const input = el<HTMLInputElement>('#cat-emoji-custom');
        expect(input).not.toBeNull();
        input!.value = '🧑🏽‍🍳';
        input!.dispatchEvent(new Event('input'));
        fixture.detectChanges();
        expect(component.form.emoji).toBe('🧑🏽‍🍳');
    });

    it('should reject a blank picture', async () => {
        await build();
        component.openNew();
        expect(component.validationKey({ ...component.form, name: 'Dishes', emoji: '   ' })).toBe('settings.error.required');
    });

    it('should reject a picture with more than one emoji', async () => {
        await build();
        component.openNew();
        expect(component.validationKey({ ...component.form, name: 'Dishes', emoji: '🙂🙂' })).toBe('settings.error.emoji_invalid');
    });

    it('should reject plain text in the picture', async () => {
        await build();
        component.openNew();
        expect(component.validationKey({ ...component.form, name: 'Dishes', emoji: 'OK' })).toBe('settings.error.emoji_invalid');
    });

    it('should reject a single emoji chain longer than the limit', async () => {
        await build();
        component.openNew();
        const tooLong = '🧑' + '\u200D🍳'.repeat(9);
        expect(component.validationKey({ ...component.form, name: 'Dishes', emoji: tooLong })).toBe('settings.error.emoji_too_long');
    });

    it('should accept one emoji with a skin tone, a joined sequence or a flag', async () => {
        await build();
        component.openNew();
        for (const emoji of ['🧑🏽‍🍳', '👨‍👩‍👧', '🇬🇧', '❤️', '👍🏾']) {
            expect(component.validationKey({ ...component.form, name: 'Dishes', emoji })).toBeNull();
        }
    });

    it('should save the trimmed custom picture', async () => {
        await build();
        component.openNew();
        component.form = { ...component.form, name: ' Dishes ', emoji: '  🧑🏽‍🍳  ' };
        component.save();
        expect(tasks.createCategory).toHaveBeenCalledWith(expect.objectContaining({ name: 'Dishes', emoji: '🧑🏽‍🍳' }));
    });

    it('should show the skill badge a category earns', async () => {
        tasks.categories.mockReturnValue(of({ categories: [{ ...cleaning, badge_key: 'pet_pal' }] }) as unknown as Categories);
        await build();
        expect(el('.item')?.textContent).toContain('Pet pal');
    });

    it('should save the skill badge picked in the form', async () => {
        await build();
        component.openNew();
        component.form = { ...component.form, name: 'Pets', badge_key: 'pet_pal' };
        component.save();
        expect(tasks.createCategory).toHaveBeenCalledWith(expect.objectContaining({ name: 'Pets', badge_key: 'pet_pal' }));
    });
});
