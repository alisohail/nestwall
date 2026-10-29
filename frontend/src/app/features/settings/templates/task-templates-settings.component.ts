import { ChangeDetectorRef, Component, DestroyRef, ElementRef, ViewChild, computed, inject, signal, ChangeDetectionStrategy } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormsModule } from '@angular/forms';
import { RouterLink, RouterLinkActive } from '@angular/router';
import { TranslocoModule, TranslocoService } from '@ngneat/transloco';
import { finalize, forkJoin } from 'rxjs';
import { ApiError } from '../../../core/api/api-client.service';
import { UnsavedChangesAware } from '../../../core/unsaved/unsaved-changes';
import { AvatarComponent } from '../../../shared/avatar/avatar.component';
import { FamilyService, MemberDetail } from '../../family/family.service';
import { TaskCategory, TaskSettingsResponse, TaskTemplate, MissedAfterUnit, PenaltyUnit } from '../../tasks/tasks.models';
import { TasksService } from '../../tasks/tasks.service';
import {
  ApprovalChoice,
  ExpiryUnit,
  REMINDER_OPTIONS,
  TEMPLATE_PREFILL_FIELD_COUNT,
  TemplateFormValue,
  approvalChoice,
  approvalValue,
  emptyTemplateForm,
  isExpiryValid,
  isOptionalWhole,
  missedAfterOf,
  prefillCount,
  settingsBody,
  settingsErrorKey,
  templateBody,
  templateToForm,
} from '../settings.format';

/** Regular task templates and family defaults. Each template prefills only the fields it sets. */
import { BannerComponent } from '../../../shared/ui/banner/banner.component';
import { LoadErrorComponent } from '../../../shared/ui/load-error/load-error.component';
import { LoadingComponent } from '../../../shared/ui/loading/loading.component';

@Component({
    selector: 'app-task-templates-settings',
    imports: [FormsModule, RouterLink, RouterLinkActive, TranslocoModule, AvatarComponent, BannerComponent, LoadingComponent, LoadErrorComponent],
    templateUrl: './task-templates-settings.component.html',
    changeDetection: ChangeDetectionStrategy.Eager,
    styleUrl: './task-templates-settings.component.scss'
})
export class TaskTemplatesSettingsComponent implements UnsavedChangesAware {
  private service = inject(TasksService);
  private family = inject(FamilyService);
  private transloco = inject(TranslocoService);
  private destroyRef = inject(DestroyRef);
  private cdr = inject(ChangeDetectorRef);
  /** The values as last loaded or saved. Anything else counts as unsaved. */
  private defaultsBaseline = '';
  private editorBaseline = '';

  readonly reminderOptions = REMINDER_OPTIONS;
  readonly missedUnits: MissedAfterUnit[] = ['days', 'weeks', 'months'];
  readonly expiryUnits: ExpiryUnit[] = ['days', 'hours'];
  readonly penaltyUnits: PenaltyUnit[] = ['percent', 'points'];
  readonly prefillTotal = TEMPLATE_PREFILL_FIELD_COUNT;

  loadState = signal<'loading' | 'ready' | 'error'>('loading');
  loadError = signal<string | null>(null);
  loadApiError = signal<ApiError | null>(null);
  settings = signal<TaskSettingsResponse | null>(null);
  categories = signal<TaskCategory[]>([]);
  members = signal<MemberDetail[]>([]);
  templates = signal<TaskTemplate[]>([]);
  nextCursor = signal<string | null>(null);
  loadingMore = signal(false);
  listError = signal<string | null>(null);

  // Regular task defaults card. Penalty fields are not on screen, but they are kept and sent back on save.
  defaultsPoints: number | null = null;
  defaultsMissedValue: number | null = null;
  defaultsMissedUnit: MissedAfterUnit = 'days';
  defaultsApproval: ApprovalChoice = 'unset';
  defaultsBusy = signal(false);
  defaultsSaved = signal(false);
  defaultsError = signal<string | null>(null);

  // Editor. `current` is the stored template being edited, or null when creating.
  creating = signal(false);
  current = signal<TaskTemplate | null>(null);
  editing = computed(() => this.creating() || this.current() !== null);
  form: TemplateFormValue = emptyTemplateForm();
  submitted = signal(false);
  saving = signal(false);
  saved = signal(false);
  deleting = signal(false);
  editorError = signal<string | null>(null);

  @ViewChild('editorCol') editorCol?: ElementRef<HTMLElement>;

  constructor() {
    this.load();
  }

  load(): void {
    this.loadState.set('loading');
    this.loadError.set(null);
    this.loadApiError.set(null);
    forkJoin({
      settings: this.service.settings(),
      categories: this.service.categories(),
      members: this.family.listMembers(),
      templates: this.service.templates(),
    })
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: ({ settings, categories, members, templates }) => {
          this.settings.set(settings);
          this.loadDefaults(settings);
          this.categories.set(categories.categories);
          this.members.set(members.members.filter((m) => m.status === 'active'));
          this.templates.set(templates.templates);
          this.nextCursor.set(templates.next_cursor);
          this.loadState.set('ready');
        },
        error: (err: ApiError) => {
          this.loadError.set(settingsErrorKey(err));
          this.loadApiError.set(err);
          this.loadState.set('error');
        },
      });
  }

  // Regular task defaults

  private loadDefaults(settings: TaskSettingsResponse): void {
    const defaults = settings.defaults;
    this.defaultsPoints = defaults.points;
    this.defaultsMissedValue = defaults.missed_after?.value ?? null;
    this.defaultsMissedUnit = defaults.missed_after?.unit ?? 'days';
    this.defaultsApproval = approvalChoice(defaults.approval_required);
    this.defaultsBaseline = this.defaultsSnapshot();
    this.cdr.markForCheck();
  }

  hasUnsavedChanges(): boolean {
    return this.loadState() === 'ready' && (this.defaultsDirty() || this.editorDirty());
  }

  defaultsDirty(): boolean {
    return this.defaultsSnapshot() !== this.defaultsBaseline;
  }

  editorDirty(): boolean {
    return this.editing() && this.editorSnapshot() !== this.editorBaseline;
  }

  async saveChanges(): Promise<boolean> {
    if (this.defaultsDirty() && !(await this.saveDefaultsChanges())) return false;
    if (this.editorDirty() && !(await this.saveEditor())) return false;
    return true;
  }

  private defaultsSnapshot(): string {
    return JSON.stringify([this.defaultsPoints, this.defaultsMissedValue, this.defaultsMissedUnit, this.defaultsApproval]);
  }

  private editorSnapshot(): string {
    return JSON.stringify(this.form);
  }

  defaultsValid(): boolean {
    const points = this.defaultsPoints;
    const pointsOk = points === null || isOptionalWhole(points, 0, Number.MAX_SAFE_INTEGER);
    return pointsOk && isOptionalWhole(this.defaultsMissedValue, 1, 999);
  }

  saveDefaults(): void {
    void this.saveDefaultsChanges();
  }

  private saveDefaultsChanges(): Promise<boolean> {
    const current = this.settings();
    if (!current || this.defaultsBusy()) return Promise.resolve(false);
    this.defaultsSaved.set(false);
    this.defaultsError.set(null);
    if (!this.defaultsValid()) {
      this.defaultsError.set('settings.error.number');
      return Promise.resolve(false);
    }
    this.defaultsBusy.set(true);
    const body = settingsBody(current, {
      defaults: {
        points: this.defaultsPoints,
        approval_required: approvalValue(this.defaultsApproval),
        missed_after: missedAfterOf(this.defaultsMissedValue, this.defaultsMissedUnit),
      },
    });
    return new Promise<boolean>((resolve) => {
      this.service
        .updateSettings(body, current.version)
        .pipe(
          takeUntilDestroyed(this.destroyRef),
          finalize(() => this.defaultsBusy.set(false)),
        )
        .subscribe({
          next: (saved) => {
            this.settings.set(saved);
            this.loadDefaults(saved);
            this.defaultsSaved.set(true);
            resolve(true);
          },
          error: (err: ApiError) => {
            this.defaultsError.set(settingsErrorKey(err));
            resolve(false);
          },
        });
    });
  }

  // Template list

  loadMore(): void {
    const cursor = this.nextCursor();
    if (!cursor || this.loadingMore()) return;
    this.loadingMore.set(true);
    this.listError.set(null);
    this.service
      .templates(cursor)
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => this.loadingMore.set(false)),
      )
      .subscribe({
        next: (page) => {
          this.templates.update((list) => [...list, ...page.templates]);
          this.nextCursor.set(page.next_cursor);
        },
        error: (err: ApiError) => this.listError.set(settingsErrorKey(err)),
      });
  }

  categoryFor(id: string | null): TaskCategory | undefined {
    return id ? this.categories().find((c) => c.id === id) : undefined;
  }

  categoryLabel(id: string | null): string {
    return this.categoryFor(id)?.name ?? this.transloco.translate('settings.templates.no_category');
  }

  // Editor

  newTemplate(): void {
    this.current.set(null);
    this.creating.set(true);
    this.form = emptyTemplateForm();
    this.editorBaseline = this.editorSnapshot();
    this.resetEditor();
    this.showEditorOnNarrow();
  }

  edit(template: TaskTemplate): void {
    this.creating.set(false);
    this.current.set(template);
    this.form = templateToForm(template);
    this.editorBaseline = this.editorSnapshot();
    this.resetEditor();
    this.showEditorOnNarrow();
  }

  closeEditor(): void {
    this.creating.set(false);
    this.current.set(null);
    this.resetEditor();
  }

  /** On narrow screens the editor replaces the list, so bring it to the top of the view. */
  private showEditorOnNarrow(): void {
    if (!window.matchMedia('(max-width: 1100px)').matches) return;
    setTimeout(() => this.editorCol?.nativeElement.scrollIntoView({ block: 'start' }));
  }

  private resetEditor(): void {
    this.submitted.set(false);
    this.saved.set(false);
    this.editorError.set(null);
  }

  prefilledCount(): number {
    return prefillCount(templateBody(this.form));
  }

  toggleAssignee(memberId: string): void {
    const ids = this.form.default_assignee_ids;
    this.form.default_assignee_ids = ids.includes(memberId) ? ids.filter((id) => id !== memberId) : [...ids, memberId];
  }

  /** The first problem with the editor values, as a translation key. Null when the values are valid. */
  validationKey(value: TemplateFormValue): string | null {
    const name = value.name.trim();
    if (name === '') return 'settings.error.required';
    if (name.length > 80) return 'settings.error.too_long';
    if (value.title.length > 200 || value.description.length > 2000) return 'settings.error.too_long';
    const max = Number.MAX_SAFE_INTEGER;
    if (!isOptionalWhole(value.points, 0, max)) return 'settings.error.number';
    const penaltyMax = value.penalty_unit === 'percent' ? 100 : max;
    if (!isOptionalWhole(value.penalty_value, 0, penaltyMax)) return 'settings.error.number';
    if (!isOptionalWhole(value.late_points_percent, 0, 100)) return 'settings.error.number';
    if (!isExpiryValid(value.expiry_value, value.expiry_unit)) return 'settings.error.expiry';
    return null;
  }

  save(): void {
    void this.saveEditor();
  }

  private saveEditor(): Promise<boolean> {
    if (this.saving()) return Promise.resolve(false);
    this.submitted.set(true);
    this.saved.set(false);
    this.editorError.set(null);
    const problem = this.validationKey(this.form);
    if (problem) {
      this.editorError.set(problem);
      return Promise.resolve(false);
    }
    const body = templateBody(this.form);
    const stored = this.current();
    const request$ = stored
      ? this.service.updateTemplate(stored.id, body, stored.version)
      : this.service.createTemplate(body);
    this.saving.set(true);
    return new Promise<boolean>((resolve) => {
      request$
        .pipe(
          takeUntilDestroyed(this.destroyRef),
          finalize(() => this.saving.set(false)),
        )
        .subscribe({
          next: (saved) => {
            this.templates.update((list) =>
              stored ? list.map((t) => (t.id === saved.id ? saved : t)) : [saved, ...list],
            );
            this.current.set(saved);
            this.creating.set(false);
            this.form = templateToForm(saved);
            this.editorBaseline = this.editorSnapshot();
            this.cdr.markForCheck();
            this.submitted.set(false);
            this.saved.set(true);
            resolve(true);
          },
          error: (err: ApiError) => {
            this.editorError.set(settingsErrorKey(err));
            resolve(false);
          },
        });
    });
  }

  remove(): void {
    const stored = this.current();
    if (!stored || this.deleting()) return;
    this.deleting.set(true);
    this.editorError.set(null);
    this.service
      .deleteTemplate(stored.id, stored.version)
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => this.deleting.set(false)),
      )
      .subscribe({
        next: () => {
          this.templates.update((list) => list.filter((t) => t.id !== stored.id));
          this.closeEditor();
        },
        error: (err: ApiError) => this.editorError.set(settingsErrorKey(err)),
      });
  }
}
