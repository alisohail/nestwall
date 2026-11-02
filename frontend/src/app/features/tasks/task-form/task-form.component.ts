import { Component, DestroyRef, computed, inject, signal, ChangeDetectionStrategy } from '@angular/core';
import { takeUntilDestroyed, toSignal } from '@angular/core/rxjs-interop';
import { NonNullableFormBuilder, ReactiveFormsModule, Validators } from '@angular/forms';
import { ActivatedRoute, Router, RouterLink } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';
import { Observable, catchError, concatMap, forkJoin, from, map, of, toArray } from 'rxjs';
import { AuthService } from '../../../core/auth/auth.service';
import { AvatarComponent } from '../../../shared/avatar/avatar.component';
import { refreshOnFormChange } from '../../../shared/forms/refresh-on-form-change';
import { BannerComponent } from '../../../shared/ui/banner/banner.component';
import { ConfirmDialogComponent } from '../../../shared/ui/confirm-dialog/confirm-dialog.component';
import { ErrorStateComponent } from '../../../shared/ui/error-state/error-state.component';
import { LoadingComponent } from '../../../shared/ui/loading/loading.component';
import { FamilyService, MemberDetail } from '../../family/family.service';
import { formatDay, formatTime, taskErrorKey, todayIso } from '../tasks.format';
import { ChecklistItem, PartOfDay, PenaltyUnit, Priority, Task, TaskCategory, TaskPatchRequest, TaskRequest, TaskTemplate, TaskTemplateRequest } from '../tasks.models';
import { TasksService } from '../tasks.service';
import { RepeatBuilderComponent } from './repeat-builder/repeat-builder.component';
import { TimeInputComponent } from '../../../shared/ui/time-input/time-input.component';
import { RepeatState, buildRule, defaultRepeat, repeatProblem } from './recurrence';
import { UnsavedChangesAware } from '../../../core/unsaved/unsaved-changes';
import { REMINDER_OPTIONS } from '../../settings/settings.format';

type ExpiryUnit = 'days' | 'hours';
type LoadState = 'loading' | 'ready' | 'error';

const MINUTES_PER_HOUR = 60;
const MINUTES_PER_DAY = 1440;
const CHECKLIST_MAX = 200;
/** One checklist step in the edit form. A new step has no id until the task is saved. */
interface EditStep {
  id: string | null;
  text: string;
}
const TEMPLATE_CHIPS = 5;

/** Splits stored minutes into the largest whole unit (days, else hours). */
function expiryParts(minutes: number | null): { value: number | null; unit: ExpiryUnit } {
  if (minutes === null) return { value: null, unit: 'days' };
  if (minutes % MINUTES_PER_DAY === 0) return { value: minutes / MINUTES_PER_DAY, unit: 'days' };
  return { value: minutes / MINUTES_PER_HOUR, unit: 'hours' };
}

/** Create and edit form for a task. Repeats are only set on create. */
@Component({
    selector: 'app-task-form',
    imports: [
        ReactiveFormsModule,
        RouterLink,
        TranslocoModule,
        AvatarComponent,
        BannerComponent,
        ConfirmDialogComponent,
        ErrorStateComponent,
        LoadingComponent,
        RepeatBuilderComponent,
        TimeInputComponent,
    ],
    templateUrl: './task-form.component.html',
    changeDetection: ChangeDetectionStrategy.Eager,
    styleUrl: './task-form.component.scss'
})
export class TaskFormComponent implements UnsavedChangesAware {
  private fb = inject(NonNullableFormBuilder);
  private service = inject(TasksService);
  private family = inject(FamilyService);
  private auth = inject(AuthService);
  private route = inject(ActivatedRoute);
  private router = inject(Router);
  private destroyRef = inject(DestroyRef);
  /** The values as last loaded or saved. Anything else counts as unsaved. */
  private baseline = '';

  readonly taskId = this.route.snapshot.paramMap.get('id');
  /** The copy the form edits and ticks: the member named in ?member=, else the first assignee's copy. */
  readonly copyMemberId = this.route.snapshot.queryParamMap.get('member');
  readonly editing = this.taskId !== null;
  readonly partialChecklist = this.route.snapshot.queryParamMap.get('partial') === '1';
  /** The Tasks tab the edit was opened from, so Back, Cancel and Save return to it. */
  readonly backTab = this.route.snapshot.queryParamMap.get('tab');
  readonly backQuery = this.backTab ? { tab: this.backTab } : null;
  readonly today = todayIso(new Date(), this.auth.currentUser()?.family?.timezone);

  readonly priorities: Priority[] = ['low', 'normal', 'high'];
  readonly partsOfDay: PartOfDay[] = ['anytime', 'morning', 'after_school', 'evening'];
  readonly expiryUnits: ExpiryUnit[] = ['days', 'hours'];
  readonly penaltyUnits: PenaltyUnit[] = ['percent', 'points'];
  readonly checklistMax = CHECKLIST_MAX;

  loadState = signal<LoadState>('loading');
  saving = signal(false);
  saveError = signal<string | null>(null);
  pendingStep = signal<string | null>(null);
  task = signal<Task | null>(null);
  members = signal<MemberDetail[]>([]);
  categories = signal<TaskCategory[]>([]);
  templates = signal<TaskTemplate[]>([]);
  templatesExpanded = signal(false);
  selectedTemplateId = signal<string | null>(null);

  visibleTemplates = computed(() => (this.templatesExpanded() ? this.templates() : this.templates().slice(0, TEMPLATE_CHIPS)));

  activeMembers = computed(() => this.members().filter((m) => m.status === 'active'));
  /** Set while the Save popup asks whether a repeating task's edit applies to this task or the whole series. */
  scopePrompt = signal(false);
  scopeChoice = signal<'occurrence' | 'series'>('occurrence');
  private scopeResolver: ((scope: 'occurrence' | 'series' | null) => void) | null = null;

  /** Resolves with the chosen scope, or null when the user cancels. */
  private chooseScope(): Promise<'occurrence' | 'series' | null> {
    this.scopeChoice.set('occurrence');
    this.scopePrompt.set(true);
    return new Promise((resolve) => {
      this.scopeResolver = resolve;
    });
  }

  settleScope(scope: 'occurrence' | 'series' | null): void {
    this.scopePrompt.set(false);
    const resolve = this.scopeResolver;
    this.scopeResolver = null;
    resolve?.(scope);
  }

  checklistLocked = computed(() => (this.task()?.assignees ?? []).some((a) => a.status !== 'to_do' && a.status !== 'rejected'));

  form = this.fb.group({
    title: ['', [Validators.required, Validators.maxLength(200)]],
    description: ['', Validators.maxLength(2000)],
    category_id: [''],
    priority: ['normal' as Priority],
    part_of_day: ['anytime' as PartOfDay],
    due_date: [this.today, Validators.required],
    start_time: [''],
    due_time: [''],
    reminder_minutes_before: [null as number | null],
    assignees: [[] as string[], Validators.required],
    points: [0 as number | null, [Validators.required, Validators.min(0), Validators.max(1000)]],
    approval_required: [true],
    expires_value: [null as number | null, Validators.min(1)],
    expires_unit: ['days' as ExpiryUnit],
    late_points_percent: [null as number | null, [Validators.min(0), Validators.max(100)]],
    penalty_value: [null as number | null, Validators.min(0)],
    penalty_unit: ['percent' as PenaltyUnit],
  });
  readonly f = this.form.controls;
  readonly reminderOptions = REMINDER_OPTIONS;
  /** Programmatic patches (template or saved task loads) must refresh validity in the view. */
  private refreshOnForm = refreshOnFormChange(this.form);

  formValue = toSignal(this.form.valueChanges.pipe(map(() => this.form.getRawValue())), { initialValue: this.form.getRawValue() });
  dueDate = toSignal(this.f.due_date.valueChanges, { initialValue: this.today });

  repeat = signal<RepeatState>(defaultRepeat(this.today));
  repeatError = computed(() => (this.repeat().freq === 'none' ? null : repeatProblem(this.repeat(), this.dueDate())));

  draftItems = signal<string[]>([]);
  newItemText = signal('');
  checklistError = signal<string | null>(null);
  /** Edit mode: the checklist as staged in the form. It is saved with the rest of the task. */
  editItems = signal<EditStep[]>([]);
  /** Index of a saved step waiting for the user to confirm its removal. */
  pendingRemove = signal<number | null>(null);


  templateOpen = signal(false);
  templateName = signal('');
  templateSaving = signal(false);
  templateSaved = signal(false);

  previewName = computed(() => this.members().find((m) => m.id === this.formValue().assignees[0])?.display_name ?? '');
  previewSteps = computed(() => (this.editing ? this.editItems().length : this.draftItems().length));

  constructor() {
    this.f.category_id.valueChanges.pipe(takeUntilDestroyed(this.destroyRef)).subscribe((id) => {
      if (!this.editing) this.applyCategory(id);
    });
    this.load();
  }

  load(): void {
    this.loadState.set('loading');
    const task$: Observable<Task | null> = this.taskId
      ? this.service.get(this.taskId, this.copyMemberId ?? undefined)
      : of(null);
    const templates$ = this.editing
      ? of({ templates: [] as TaskTemplate[], next_cursor: null })
      : this.service.templates().pipe(catchError(() => of({ templates: [] as TaskTemplate[], next_cursor: null })));
    forkJoin({ members: this.family.listMembers(), categories: this.service.categories(), task: task$, templates: templates$ })
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: ({ members, categories, task, templates }) => {
          this.members.set(members.members);
          this.categories.set(categories.categories);
          this.templates.set(templates.templates);
          if (task) {
            this.task.set(task);
            this.fillFromTask(task);
          }
          this.baseline = this.snapshot();
          this.loadState.set('ready');
        },
        error: () => this.loadState.set('error'),
      });
  }

  /** Pre-fills only the fields the template sets. Category defaults apply first, then the template overrides them. */
  applyTemplate(template: TaskTemplate): void {
    this.selectedTemplateId.set(template.id);
    if (template.category_id !== null) this.f.category_id.setValue(template.category_id);
    const expiry = template.expires_after_minutes === null ? null : expiryParts(template.expires_after_minutes);
    this.form.patchValue({
      ...(template.title !== null ? { title: template.title } : {}),
      ...(template.description !== null ? { description: template.description } : {}),
      ...(template.part_of_day !== null ? { part_of_day: template.part_of_day } : {}),
      ...(template.priority !== null ? { priority: template.priority } : {}),
      ...(template.start_time !== null ? { start_time: template.start_time } : {}),
      ...(template.due_time !== null ? { due_time: template.due_time } : {}),
      ...(template.reminder_minutes_before !== null ? { reminder_minutes_before: template.reminder_minutes_before } : {}),
      ...(template.late_points_percent !== null ? { late_points_percent: template.late_points_percent } : {}),
      ...(template.default_assignee_ids.length > 0 ? { assignees: [...template.default_assignee_ids] } : {}),
      ...(template.points !== null ? { points: template.points } : {}),
      ...(template.approval_required !== null ? { approval_required: template.approval_required } : {}),
      ...(expiry ? { expires_value: expiry.value, expires_unit: expiry.unit } : {}),
      ...(template.penalty_value !== null ? { penalty_value: template.penalty_value, penalty_unit: template.penalty_unit ?? 'percent' } : {}),
    });
    if (template.repeat_state) {
      this.repeat.set({ ...defaultRepeat(this.today), ...(template.repeat_state as Partial<RepeatState>) });
    }
    if (template.checklist && template.checklist.length > 0) {
      this.draftItems.set([...template.checklist]);
    }
  }

  toggleAssignee(id: string): void {
    const current = this.f.assignees.value;
    this.f.assignees.setValue(current.includes(id) ? current.filter((x) => x !== id) : [...current, id]);
    this.f.assignees.markAsTouched();
  }

  bumpPoints(delta: number): void {
    const current = Number(this.f.points.value ?? 0) || 0;
    this.f.points.setValue(Math.max(0, current + delta));
  }

  inputValue(event: Event): string {
    return (event.target as HTMLInputElement).value;
  }

  addItem(): void {
    const text = this.newItemText().trim();
    if (!text || text.length > CHECKLIST_MAX) return;
    if (!this.editing) {
      this.draftItems.update((items) => [...items, text]);
    } else {
      this.editItems.update((items) => [...items, { id: null, text }]);
    }
    this.newItemText.set('');
  }

  removeDraft(index: number): void {
    this.draftItems.update((items) => items.filter((_, i) => i !== index));
  }

  renameStep(index: number, value: string): void {
    const text = value.trim();
    const step = this.editItems()[index];
    if (!step || !text || text === step.text || text.length > CHECKLIST_MAX) return;
    this.editItems.update((items) => items.map((s, i) => (i === index ? { ...s, text } : s)));
  }

  /** A saved step asks first. A step that is not saved yet can go straight away. */
  askRemoveStep(index: number): void {
    if (this.editItems()[index]?.id === null) this.removeStep(index);
    else this.pendingRemove.set(index);
  }

  removeStep(index: number): void {
    this.pendingRemove.set(null);
    this.editItems.update((items) => items.filter((_, i) => i !== index));
  }

  confirmRemove(): void {
    const index = this.pendingRemove();
    if (index !== null) this.removeStep(index);
  }

  /** Done state comes from the server copy, since ticks save straight away. */
  stepDone(step: EditStep): boolean {
    return this.task()?.checklist?.find((item) => item.id === step.id)?.done ?? false;
  }

  toggleStep(step: EditStep): void {
    const item = this.task()?.checklist?.find((c) => c.id === step.id);
    if (item) this.toggleItem(item);
  }

  /** Ticks or unticks a step on the copy the form shows, so only that person's checklist changes. */
  toggleItem(item: ChecklistItem): void {
    const task = this.task();
    const memberId = this.copyMemberId ?? task?.assignees[0]?.member_id;
    if (!task || !memberId || this.checklistLocked() || this.pendingStep()) return;
    this.pendingStep.set(item.id);
    this.service.tickChecklist(task.id, item.id, !item.done, task.version, memberId).subscribe({
      next: (updated) => {
        this.pendingStep.set(null);
        this.applyChecklist(updated);
      },
      error: (err) => {
        this.pendingStep.set(null);
        this.checklistError.set(taskErrorKey(err));
      },
    });
  }

  save(): void {
    void this.saveChanges().then((ok) => {
      if (ok) void this.router.navigate(['/tasks'], { queryParams: this.backQuery });
    });
  }

  hasUnsavedChanges(): boolean {
    return this.loadState() === 'ready' && this.snapshot() !== this.baseline;
  }

  saveChanges(): Promise<boolean> {
    if (this.saving()) return Promise.resolve(false);
    this.saveError.set(null);
    this.form.markAllAsTouched();
    if (this.form.invalid) {
      this.saveError.set('tasks.form.error.invalid');
      return Promise.resolve(false);
    }
    return this.editing ? this.saveEdit() : this.saveCreate();
  }

  saveTemplate(): void {
    const name = this.templateName().trim();
    if (!name) return;
    const v = this.form.getRawValue();
    const body: TaskTemplateRequest = {
      name,
      category_id: v.category_id || null,
      title: v.title.trim() || null,
      description: v.description.trim() || null,
      part_of_day: v.part_of_day,
      priority: v.priority,
      start_time: v.start_time || null,
      due_time: v.due_time || null,
      reminder_minutes_before: v.reminder_minutes_before,
      late_points_percent: v.late_points_percent,
      default_assignee_ids: v.assignees,
      points: v.points,
      approval_required: v.approval_required,
      expires_after_minutes: this.expiryMinutes(v.expires_value, v.expires_unit),
      penalty_value: v.penalty_value,
      penalty_unit: v.penalty_value === null ? null : v.penalty_unit,
      repeat_state: this.repeat().freq === 'none' ? null : { ...this.repeat() },
      checklist: [...this.draftItems()],
    };
    this.templateSaving.set(true);
    this.service.createTemplate(body).subscribe({
      next: () => {
        this.templateSaving.set(false);
        this.templateOpen.set(false);
        this.templateName.set('');
        this.templateSaved.set(true);
      },
      error: (err) => {
        this.templateSaving.set(false);
        this.saveError.set(taskErrorKey(err));
      },
    });
  }

  private saveCreate(): Promise<boolean> {
    const repeatProblemKey = this.repeatError();
    if (repeatProblemKey) {
      this.saveError.set(`tasks.repeat.problem.${repeatProblemKey}`);
      return Promise.resolve(false);
    }
    const base = this.requestBody();
    if (this.repeat().freq !== 'none') {
      const rule = buildRule(this.repeat(), this.f.due_date.value);
      if (!rule) {
        this.saveError.set('tasks.repeat.problem.interval');
        return Promise.resolve(false);
      }
      this.begin();
      return new Promise<boolean>((resolve) => {
        this.service.createRepeat({ ...base, recurrence_rule: rule, checklist: [...this.draftItems()] }).subscribe({
          next: () => resolve(this.finish()),
          error: (err) => resolve(this.fail(err)),
        });
      });
    }
    this.begin();
    return new Promise<boolean>((resolve) => {
      this.service.create(base).subscribe({
        next: (created) => resolve(this.addDraftItems(created.id)),
        error: (err) => resolve(this.fail(err)),
      });
    });
  }

  /** Adds queued checklist items one at a time after the task exists. */
  private addDraftItems(taskId: string): Promise<boolean> {
    const items = this.draftItems();
    if (items.length === 0) return Promise.resolve(this.finish());
    return new Promise<boolean>((resolve) => {
      from(items)
        .pipe(
          concatMap((text) => this.service.addChecklistItem(taskId, text)),
          toArray(),
        )
        .subscribe({
          next: () => resolve(this.finish()),
          error: () => {
            resolve(false);
            void this.router.navigate(['/tasks', taskId, 'edit'], { queryParams: { partial: 1, tab: this.backTab ?? undefined } });
          },
        });
    });
  }

  private async saveEdit(): Promise<boolean> {
    const task = this.task();
    if (!task) return false;
    const scope = task.repeats ? await this.chooseScope() : null;
    if (task.repeats && !scope) return false;
    this.begin();
    return new Promise<boolean>((resolve) => {
      this.service.update(task.id, this.patchBody(), task.version, scope ?? undefined).subscribe({
        next: () => resolve(this.finish()),
        error: (err) => resolve(this.fail(err)),
      });
    });
  }

  private applyChecklist(updated: Task): void {
    this.task.set(updated);
    this.checklistError.set(null);
  }

  private applyCategory(id: string): void {
    const category = this.categories().find((c) => c.id === id);
    if (!category) return;
    const expiry = expiryParts(category.expires_after_minutes);
    this.form.patchValue({
      points: category.points,
      approval_required: category.approval_required ?? true,
      expires_value: expiry.value,
      expires_unit: expiry.unit,
      penalty_value: category.penalty_value,
      penalty_unit: category.penalty_unit ?? 'percent',
    });
  }

  private fillFromTask(task: Task): void {
    const expiry = expiryParts(task.expires_after_minutes);
    this.form.patchValue({
      title: task.title,
      description: task.description ?? '',
      category_id: task.category_id ?? '',
      priority: task.priority,
      part_of_day: task.part_of_day ?? 'anytime',
      due_date: task.due_date,
      start_time: task.start_time ?? '',
      due_time: task.due_time ?? '',
      reminder_minutes_before: task.reminder_minutes_before,
      points: task.points,
      approval_required: task.approval_required ?? true,
      expires_value: expiry.value,
      expires_unit: expiry.unit,
      late_points_percent: task.late_points_percent,
      penalty_value: task.penalty_value,
      penalty_unit: task.penalty_unit ?? 'percent',
      assignees: task.assignees.map((a) => a.member_id),
    });
    this.editItems.set((task.checklist ?? []).map((item) => ({ id: item.id, text: item.text })));
  }

  private expiryMinutes(value: number | null, unit: ExpiryUnit): number | null {
    if (value === null) return null;
    return value * (unit === 'days' ? MINUTES_PER_DAY : MINUTES_PER_HOUR);
  }

  private requestBody(): TaskRequest {
    const v = this.form.getRawValue();
    return {
      title: v.title.trim(),
      description: v.description.trim() || null,
      category_id: v.category_id || null,
      part_of_day: v.part_of_day,
      priority: v.priority,
      due_date: v.due_date,
      due_time: v.due_time || null,
      reminder_minutes_before: v.reminder_minutes_before,
      late_points_percent: v.late_points_percent,
      assignee_ids: v.assignees,
      points: v.points,
      approval_required: v.approval_required,
      expires_after_minutes: this.expiryMinutes(v.expires_value, v.expires_unit),
      penalty_value: v.penalty_value,
      penalty_unit: v.penalty_value === null ? null : v.penalty_unit,
    };
  }

  private patchBody(): TaskPatchRequest {
    const v = this.form.getRawValue();
    const patch: TaskPatchRequest = {
      title: v.title.trim(),
      description: v.description.trim() || null,
      category_id: v.category_id || null,
      part_of_day: v.part_of_day,
      priority: v.priority,
      due_date: v.due_date,
      start_time: v.start_time || null,
      due_time: v.due_time || null,
      reminder_minutes_before: v.reminder_minutes_before,
      late_points_percent: v.late_points_percent,
      assignee_ids: v.assignees,
      approval_required: v.approval_required,
      expires_after_minutes: this.expiryMinutes(v.expires_value, v.expires_unit),
      penalty_value: v.penalty_value,
      penalty_unit: v.penalty_value === null ? null : v.penalty_unit,
    };
    if (v.points !== null) patch.points = v.points;
    if (this.checklistChanged()) {
      patch.checklist = this.editItems().map((step) => ({ id: step.id, text: step.text.trim() }));
    }
    return patch;
  }

  /** True when the staged steps differ from the saved checklist (text, order, or added and removed steps). */
  private checklistChanged(): boolean {
    const saved = this.task()?.checklist ?? [];
    const staged = this.editItems();
    return saved.length !== staged.length || saved.some((item, i) => item.id !== staged[i].id || item.text !== staged[i].text);
  }

  private begin(): void {
    this.saving.set(true);
    this.saveError.set(null);
  }

  private finish(): boolean {
    this.saving.set(false);
    this.baseline = this.snapshot();
    return true;
  }

  private fail(err: unknown): boolean {
    this.saving.set(false);
    this.saveError.set(taskErrorKey(err));
    return false;
  }

  private snapshot(): string {
    return JSON.stringify({ form: this.form.getRawValue(), draft: this.draftItems(), steps: this.editItems(), repeat: this.repeat() });
  }

  readonly formatDay = formatDay;
  readonly formatTime = formatTime;
}
