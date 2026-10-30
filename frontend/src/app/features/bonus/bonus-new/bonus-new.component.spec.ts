import type { MockedObject } from "vitest";
import { provideHttpClient, withXhr } from '@angular/common/http';
import { provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Router, provideRouter } from '@angular/router';
import { NEVER, of } from 'rxjs';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { FamilyService } from '../../family/family.service';
import { DashboardService } from '../../dashboard/dashboard.service';
import { TasksService } from '../../tasks/tasks.service';
import { BonusDefaults, TaskTemplate } from '../../tasks/tasks.models';
import { BonusService } from '../bonus.service';
import { BonusNewComponent } from './bonus-new.component';
import { mockOf } from '../../../testing/mock-of';

type CreateReturn = ReturnType<BonusService['create']>;

const noBonusDefaults: BonusDefaults = {
    points: null,
    claim_mode: null,
    show_from: null,
    ends: null,
    approval_required: null,
};

const carTemplate = {
    id: 'tpl-car',
    kind: 'bonus',
    name: 'Wash the car',
    title: null,
    description: 'Use the hose',
    points: 25,
    approval_required: false,
} as unknown as TaskTemplate;

describe('BonusNewComponent', () => {
    let fixture: ComponentFixture<BonusNewComponent>;
    let api: MockedObject<BonusService>;
    let tasks: MockedObject<TasksService>;
    let router: Router;
    let listMembers: () => ReturnType<FamilyService['listMembers']>;
    let familyZone: string;

    /** Form fields inside a form wire their ngModel on a microtask, so wait before typing. */
    async function build(): Promise<void> {
        fixture = TestBed.createComponent(BonusNewComponent);
        fixture.detectChanges();
        await fixture.whenStable();
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
        const field = el<HTMLInputElement | HTMLTextAreaElement>(selector);
        if (field) {
            field.value = value;
            field.dispatchEvent(new Event('input'));
        }
        fixture.detectChanges();
    }

    /** Picks a 24-hour time through the time-input menus, in the default 12-hour style. */
    function setTime(selector: string, value: string): void {
        const [hour, minute] = value.split(':').map(Number);
        const host = el<HTMLElement>(selector);
        const pick = (menu: string, menuValue: string): void => {
            const select = host?.querySelector<HTMLSelectElement>(`[data-testid="${menu}"]`);
            if (select) {
                select.value = menuValue;
                select.dispatchEvent(new Event('change'));
            }
        };
        pick('time-hour', String((hour % 12) || 12).padStart(2, '0'));
        pick('time-minute', String(minute).padStart(2, '0'));
        pick('time-period', hour >= 12 ? 'PM' : 'AM');
        fixture.detectChanges();
    }

    beforeEach(async () => {
        api = mockOf<BonusService>({
            list: vi.fn().mockName("BonusService.list"),
            create: vi.fn().mockName("BonusService.create"),
            approve: vi.fn().mockName("BonusService.approve"),
            reject: vi.fn().mockName("BonusService.reject")
        });
        api.create.mockReturnValue(of({}) as unknown as CreateReturn);
        tasks = mockOf<TasksService>({
            createTemplate: vi.fn().mockName("TasksService.createTemplate"),
            templates: vi.fn().mockName("TasksService.templates"),
            settings: vi.fn().mockName("TasksService.settings")
        });
        tasks.createTemplate.mockReturnValue(of({}) as unknown as ReturnType<TasksService['createTemplate']>);
        tasks.templates.mockReturnValue(of({ templates: [carTemplate], next_cursor: null }) as unknown as ReturnType<TasksService['templates']>);
        tasks.settings.mockReturnValue(of({ bonus_defaults: noBonusDefaults }) as unknown as ReturnType<TasksService['settings']>);
        familyZone = 'UTC';
        listMembers = () => of({
            members: [
                { id: 'k1', display_name: 'Noah', colour: '#2a9d8f', avatar_id: null, is_child: true, status: 'active' },
                { id: 'a1', display_name: 'Sam', colour: '#264653', avatar_id: null, is_child: false, status: 'active' },
            ],
        }) as unknown as ReturnType<FamilyService['listMembers']>;

        await TestBed.configureTestingModule({
            imports: [BonusNewComponent, getTranslocoTestingModule()],
            providers: [
                provideRouter([]),
                provideHttpClient(withXhr()),
                provideHttpClientTesting(),
                { provide: BonusService, useValue: api },
                { provide: TasksService, useValue: tasks },
                { provide: FamilyService, useValue: { listMembers: () => listMembers() } as unknown as FamilyService },
                {
                    provide: DashboardService,
                    useValue: {
                        parent: () => of({ date: '2026-10-13', timezone: familyZone }),
                    } as unknown as DashboardService,
                },
            ],
        }).compileComponents();

        router = TestBed.inject(Router);
        vi.spyOn(router, 'navigateByUrl').mockResolvedValue(true);
    });

    it('should list only active children as kid chips', async () => {
        await build();
        expect(el('[data-testid="bonus-kid-k1"]')).toBeTruthy();
        expect(el('[data-testid="bonus-kid-a1"]')).toBeNull();
    });

    it('should show the loading state while the children load', async () => {
        listMembers = () => NEVER;
        await build();
        expect(el('[data-testid="bonus-new-form"]')).toBeTruthy();
        expect(fixture.nativeElement.textContent).toContain('Loading children');
    });

    it('should block publish and not call the API when no kid is chosen', async () => {
        await build();
        typeInto('[data-testid="bonus-title"]', 'Wash the car');
        click('[data-testid="bonus-kid-k1"]');
        click('[data-testid="bonus-publish"]');
        expect(api.create).not.toHaveBeenCalled();
        expect(fixture.nativeElement.textContent).toContain('Choose at least one child');
    });

    it('should block publish when the title is empty', async () => {
        await build();
        click('[data-testid="bonus-kid-k1"]');
        click('[data-testid="bonus-publish"]');
        expect(api.create).not.toHaveBeenCalled();
        expect(fixture.nativeElement.textContent).toContain('Add a title');
    });

    it('should publish a valid task with the chosen kid and defaults', async () => {
        await build();
        typeInto('[data-testid="bonus-title"]', '  Wash the car  ');
        click('[data-testid="bonus-publish"]');
        await fixture.whenStable();
        expect(api.create).toHaveBeenCalledWith({
            title: 'Wash the car',
            description: null,
            points: 10,
            claim_mode: 'first_come',
            approval_required: true,
            eligible_member_ids: ['k1'],
            day: '2026-10-13',
            show_from: null,
            ends_at: null,
        });
        expect(router.navigateByUrl).toHaveBeenCalledWith('/bonus');
    });

    it('should start the form from the family bonus defaults', async () => {
        tasks.settings.mockReturnValue(of({
            bonus_defaults: {
                points: 25,
                claim_mode: 'everyone_once',
                show_from: '08:00',
                ends: '20:00',
                approval_required: false,
            },
        }) as unknown as ReturnType<TasksService['settings']>);
        await build();
        typeInto('[data-testid="bonus-title"]', 'Read a chapter');
        click('[data-testid="bonus-publish"]');
        const body = vi.mocked(api.create).mock.lastCall![0];
        expect(body.points).toBe(25);
        expect(body.claim_mode).toBe('everyone_once');
        expect(body.approval_required).toBe(false);
        expect(body.show_from).toBe('2026-10-13T08:00:00Z');
        expect(body.ends_at).toBe('2026-10-13T20:00:00Z');
    });

    it('should leave the end of the day when the default ends at end of day', async () => {
        tasks.settings.mockReturnValue(of({ bonus_defaults: { ...noBonusDefaults, ends: 'end_of_day' } }) as unknown as ReturnType<TasksService['settings']>);
        await build();
        typeInto('[data-testid="bonus-title"]', 'Read a chapter');
        click('[data-testid="bonus-publish"]');
        expect(vi.mocked(api.create).mock.lastCall![0].ends_at).toBeNull();
    });

    it('should publish for tomorrow when that day is chosen', async () => {
        await build();
        typeInto('[data-testid="bonus-title"]', 'Read a chapter');
        click('[data-testid="bonus-day-tomorrow"]');
        click('[data-testid="bonus-publish"]');
        expect(vi.mocked(api.create).mock.lastCall![0].day).toBe('2026-10-14');
    });

    it('should send the show and end times as instants in the family time zone', async () => {
        familyZone = 'America/New_York';
        await build();
        typeInto('[data-testid="bonus-title"]', 'Read a chapter');
        setTime('[data-testid="bonus-show"]', '09:00');
        setTime('[data-testid="bonus-ends"]', '18:30');
        click('[data-testid="bonus-publish"]');
        const body = vi.mocked(api.create).mock.lastCall![0];
        expect(body.show_from).toBe('2026-10-13T13:00:00Z');
        expect(body.ends_at).toBe('2026-10-13T22:30:00Z');
    });

    it('should block an end time before the show time', async () => {
        await build();
        typeInto('[data-testid="bonus-title"]', 'Read a chapter');
        setTime('[data-testid="bonus-show"]', '10:00');
        setTime('[data-testid="bonus-ends"]', '09:00');
        click('[data-testid="bonus-publish"]');
        expect(api.create).not.toHaveBeenCalled();
        expect(fixture.nativeElement.textContent).toContain('Pick a later end time');
    });

    it('should send everyone_once when that mode is chosen', async () => {
        await build();
        typeInto('[data-testid="bonus-title"]', 'Read a chapter');
        click('[data-testid="bonus-mode-everyone-once"]');
        click('[data-testid="bonus-publish"]');
        expect(vi.mocked(api.create).mock.lastCall![0].claim_mode).toBe('everyone_once');
    });

    it('should save the values as a bonus template without publishing', async () => {
        await build();
        typeInto('[data-testid="bonus-title"]', 'Wash the car');
        click('[data-testid="bonus-save-template"]');
        expect(tasks.createTemplate).toHaveBeenCalledWith({
            kind: 'bonus',
            name: 'Wash the car',
            title: 'Wash the car',
            description: null,
            points: 10,
            approval_required: true,
        });
        expect(api.create).not.toHaveBeenCalled();
        expect(el('[data-testid="bonus-template-saved"]')).toBeTruthy();
    });

    it('should not save a template when the title is empty', async () => {
        await build();
        click('[data-testid="bonus-save-template"]');
        expect(tasks.createTemplate).not.toHaveBeenCalled();
        expect(el('[data-testid="bonus-template-error"]')).toBeTruthy();
    });

    it('should offer saved bonus templates as starting points', async () => {
        await build();
        expect(tasks.templates).toHaveBeenCalledWith(null, 'bonus');
        expect(el('[data-testid="bonus-template-tpl-car"]')?.textContent?.trim()).toBe('Wash the car');
    });

    it('should pre-fill from a template and send its id when published', async () => {
        await build();
        click('[data-testid="bonus-template-tpl-car"]');
        await fixture.whenStable();
        fixture.detectChanges();
        expect(el<HTMLInputElement>('[data-testid="bonus-title"]')?.value).toBe('Wash the car');
        expect(el<HTMLTextAreaElement>('[data-testid="bonus-description"]')?.value).toBe('Use the hose');
        expect(el<HTMLInputElement>('[data-testid="bonus-points"]')?.value).toBe('25');
        expect(el<HTMLInputElement>('[data-testid="bonus-approval"]')?.checked).toBe(false);
        click('[data-testid="bonus-publish"]');
        expect(vi.mocked(api.create).mock.lastCall![0].template_id).toBe('tpl-car');
        expect(vi.mocked(api.create).mock.lastCall![0].points).toBe(25);
    });
});
