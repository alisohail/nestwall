import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { of, throwError } from 'rxjs';
import { getTranslocoTestingModule } from '../../../core/transloco/transloco-testing.module';
import { ApiError } from '../../../core/api/api-client.service';
import { FamilyService } from '../../family/family.service';
import { TaskSettingsResponse, TaskTemplate } from '../../tasks/tasks.models';
import { TasksService } from '../../tasks/tasks.service';
import { TaskTemplatesSettingsComponent } from './task-templates-settings.component';

type Categories = ReturnType<TasksService['categories']>;
type Templates = ReturnType<TasksService['templates']>;
type Settings = ReturnType<TasksService['settings']>;
type Members = ReturnType<FamilyService['listMembers']>;

const regular: TaskTemplate = {
  id: 't1',
  kind: 'regular',
  name: 'Feed the dog',
  category_id: null,
  title: 'Feed the dog',
  description: null,
  part_of_day: null,
  priority: 'normal',
  due_time: null,
  reminder_minutes_before: null,
  late_points_percent: null,
  default_assignee_ids: [],
  points: 5,
  approval_required: null,
  expires_after_minutes: null,
  penalty_value: null,
  penalty_unit: null,
  version: 1,
} as unknown as TaskTemplate;

const settings = {
  defaults: { points: null, missed_after: null, approval_required: null },
  part_of_day: {},
  version: 1,
} as unknown as TaskSettingsResponse;

describe('TaskTemplatesSettingsComponent', () => {
  let fixture: ComponentFixture<TaskTemplatesSettingsComponent>;
  let tasks: jasmine.SpyObj<TasksService>;

  async function build(): Promise<void> {
    fixture = TestBed.createComponent(TaskTemplatesSettingsComponent);
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

  beforeEach(async () => {
    tasks = jasmine.createSpyObj<TasksService>('TasksService', [
      'settings',
      'categories',
      'templates',
      'createTemplate',
      'updateTemplate',
      'deleteTemplate',
    ]);
    tasks.settings.and.returnValue(of(settings) as unknown as Settings);
    tasks.categories.and.returnValue(of({ categories: [] }) as unknown as Categories);
    tasks.templates.and.returnValue(of({ templates: [regular], next_cursor: null }) as unknown as Templates);

    const family = jasmine.createSpyObj<FamilyService>('FamilyService', ['listMembers']);
    family.listMembers.and.returnValue(of({ members: [] }) as unknown as Members);

    await TestBed.configureTestingModule({
      imports: [TaskTemplatesSettingsComponent, getTranslocoTestingModule()],
      providers: [
        provideRouter([]),
        { provide: TasksService, useValue: tasks },
        { provide: FamilyService, useValue: family },
      ],
    }).compileComponents();
  });

  it('should show the load error screen and retry reloads the defaults', async () => {
    tasks.settings.and.returnValue(throwError(() => ({ code: 'network', status: 0 }) as ApiError) as unknown as Settings);
    await build();

    expect(el('app-load-error')).not.toBeNull();

    tasks.settings.and.returnValue(of(settings) as unknown as Settings);
    await click('[data-testid="load-error-retry"]');

    expect(tasks.settings).toHaveBeenCalledTimes(2);
    expect(el('app-load-error')).toBeNull();
  });

  it('should show the list with no editor open', async () => {
    await build();
    expect(el('.split')?.classList).not.toContain('show-editor');
    expect(el('.tpl-page')?.classList).not.toContain('editing');
    expect(el('.back')).toBeNull();
  });

  it('should hand the view to the editor when a template is opened', async () => {
    await build();
    await click('.list-col .item');
    expect(el('.split')?.classList).toContain('show-editor');
    expect(el('.tpl-page')?.classList).toContain('editing');
    expect(el('.editor-col')?.textContent).toContain('Feed the dog');
  });

  it('should hand the view to the editor for a new template', async () => {
    await build();
    await click('.list-col .section-head .new-tpl');
    expect(el('.split')?.classList).toContain('show-editor');
    expect(el('.tpl-page')?.classList).toContain('editing');
  });

  it('should return to the list when Back is pressed', async () => {
    await build();
    await click('.list-col .item');
    await click('.editor-col .back');
    expect(el('.split')?.classList).not.toContain('show-editor');
    expect(el('.tpl-page')?.classList).not.toContain('editing');
  });
});
