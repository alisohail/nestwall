import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { of, throwError } from 'rxjs';
import { ApiError } from '../../../core/api/api-client.service';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { TaskTemplate } from '../../tasks/tasks.models';
import { TasksService } from '../../tasks/tasks.service';
import { BonusTemplatesSettingsComponent } from './bonus-templates-settings.component';

type Categories = ReturnType<TasksService['categories']>;
type Templates = ReturnType<TasksService['templates']>;
type Create = ReturnType<TasksService['createTemplate']>;
type Update = ReturnType<TasksService['updateTemplate']>;
type Remove = ReturnType<TasksService['deleteTemplate']>;
type Settings = ReturnType<TasksService['settings']>;
type UpdateSettings = ReturnType<TasksService['updateSettings']>;

const settingsResponse = {
  version: 1,
  bonus_defaults: {
    points: null,
    claim_mode: null,
    show_from: null,
    ends: null,
    approval_required: null,
  },
};

const bonus: TaskTemplate = {
  id: 't1',
  kind: 'bonus',
  name: 'Wash the car',
  category_id: null,
  title: 'Wash the car',
  description: null,
  part_of_day: null,
  priority: 'normal',
  due_time: null,
  reminder_minutes_before: null,
  late_points_percent: null,
  default_assignee_ids: [],
  points: 10,
  approval_required: true,
  expires_after_minutes: null,
  penalty_value: null,
  penalty_unit: null,
  version: 3,
};

describe('BonusTemplatesSettingsComponent', () => {
  let fixture: ComponentFixture<BonusTemplatesSettingsComponent>;
  let tasks: jasmine.SpyObj<TasksService>;

  /** Form fields wire ngModel on a microtask, so wait before typing. */
  async function build(): Promise<void> {
    fixture = TestBed.createComponent(BonusTemplatesSettingsComponent);
    fixture.detectChanges();
    await fixture.whenStable();
    fixture.detectChanges();
  }

  function el<T extends Element = HTMLElement>(selector: string): T | null {
    return fixture.nativeElement.querySelector(selector) as T | null;
  }

  async function click(selector: string): Promise<void> {
    el<HTMLElement>(selector)?.click();
    fixture.detectChanges();
    await fixture.whenStable();
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

  beforeEach(async () => {
    tasks = jasmine.createSpyObj<TasksService>('TasksService', [
      'categories',
      'templates',
      'settings',
      'updateSettings',
      'createTemplate',
      'updateTemplate',
      'deleteTemplate',
    ]);
    tasks.categories.and.returnValue(of({ categories: [] }) as unknown as Categories);
    tasks.templates.and.returnValue(of({ templates: [bonus], next_cursor: null }) as unknown as Templates);
    tasks.settings.and.returnValue(of(settingsResponse) as unknown as Settings);
    tasks.updateSettings.and.returnValue(of(settingsResponse) as unknown as UpdateSettings);
    tasks.createTemplate.and.returnValue(of({ ...bonus, id: 't2' }) as unknown as Create);
    tasks.updateTemplate.and.returnValue(of(bonus) as unknown as Update);
    tasks.deleteTemplate.and.returnValue(of(undefined) as unknown as Remove);

    await TestBed.configureTestingModule({
      imports: [BonusTemplatesSettingsComponent, getTranslocoTestingModule()],
      providers: [provideRouter([]), { provide: TasksService, useValue: tasks }],
    }).compileComponents();
  });

  it('should show the full-page load error and retry the load when loading fails', async () => {
    tasks.templates.and.returnValue(throwError(() => ({ code: 'internal', status: 500 }) as ApiError) as unknown as Templates);
    await build();
    expect(el('app-loading')).toBeNull();
    expect(el('app-load-error')).toBeTruthy();

    tasks.templates.and.returnValue(of({ templates: [bonus], next_cursor: null }) as unknown as Templates);
    await click('[data-testid="load-error-retry"]');
    expect(tasks.templates).toHaveBeenCalledTimes(2);
    expect(el('app-load-error')).toBeNull();
    expect(el('[data-testid="bonus-tpl-t1"]')).toBeTruthy();
  });

  it('should load bonus templates only', async () => {
    await build();
    expect(tasks.templates).toHaveBeenCalledWith(null, 'bonus');
    expect(el('[data-testid="bonus-tpl-t1"]')).toBeTruthy();
  });

  it('should save the bonus task defaults with the settings version', async () => {
    await build();
    typeInto('#bdef-points', '25');
    await click('[data-testid="bonus-defaults-save"]');
    expect(tasks.updateSettings).toHaveBeenCalledWith(
      jasmine.objectContaining({
        bonus_defaults: {
          points: 25,
          claim_mode: null,
          show_from: null,
          ends: null,
          approval_required: null,
        },
      }),
      settingsResponse.version,
    );
  });

  it('should save bonus approval as required, not required or not set', async () => {
    await build();
    tasks.updateSettings.and.callFake(
      (body) => of({ ...settingsResponse, bonus_defaults: body.bonus_defaults }) as unknown as UpdateSettings,
    );
    await click('.defaults .seg button:nth-child(2)');
    await click('[data-testid="bonus-defaults-save"]');
    expect(tasks.updateSettings.calls.mostRecent().args[0].bonus_defaults.approval_required).toBe(true);

    await click('.defaults .seg button:nth-child(3)');
    await click('[data-testid="bonus-defaults-save"]');
    expect(tasks.updateSettings.calls.mostRecent().args[0].bonus_defaults.approval_required).toBe(false);

    await click('.defaults .seg button:nth-child(1)');
    await click('[data-testid="bonus-defaults-save"]');
    expect(tasks.updateSettings.calls.mostRecent().args[0].bonus_defaults.approval_required).toBeNull();
  });

  it('should link the Regular and Bonus switch to both pages', async () => {
    await build();
    expect(el<HTMLAnchorElement>('[data-testid="kind-regular"]')?.getAttribute('href')).toBe('/settings/admin/templates');
    expect(el<HTMLAnchorElement>('[data-testid="kind-bonus"]')?.getAttribute('href')).toBe('/settings/admin/bonus-templates');
  });

  it('should create a bonus template with only bonus fields', async () => {
    await build();
    await click('[data-testid="bonus-tpl-new"]');
    typeInto('[data-testid="bonus-tpl-name"]', 'Wash the car');
    await click('[data-testid="bonus-tpl-save"]');
    expect(tasks.createTemplate).toHaveBeenCalledWith({
      name: 'Wash the car',
      category_id: null,
      title: null,
      description: null,
      points: null,
      approval_required: true,
      kind: 'bonus',
    });
  });

  it('should block save and not call the API when the name is blank', async () => {
    await build();
    await click('[data-testid="bonus-tpl-new"]');
    await click('[data-testid="bonus-tpl-save"]');
    expect(tasks.createTemplate).not.toHaveBeenCalled();
  });

  it('should update an existing template with its version', async () => {
    await build();
    await click('[data-testid="bonus-tpl-t1"]');
    typeInto('[data-testid="bonus-tpl-name"]', 'Wash the bike');
    await click('[data-testid="bonus-tpl-save"]');
    expect(tasks.updateTemplate).toHaveBeenCalledWith(
      't1',
      {
        name: 'Wash the bike',
        category_id: null,
        title: 'Wash the car',
        description: null,
        points: 10,
        approval_required: true,
      },
      3,
    );
  });

  it('should delete a template with its version', async () => {
    await build();
    await click('[data-testid="bonus-tpl-t1"]');
    await click('[data-testid="bonus-tpl-delete"]');
    expect(tasks.deleteTemplate).toHaveBeenCalledWith('t1', 3);
  });
});
