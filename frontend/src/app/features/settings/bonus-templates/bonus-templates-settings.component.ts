import { ChangeDetectorRef, Component, DestroyRef, ElementRef, ViewChild, computed, inject, signal, ChangeDetectionStrategy } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormsModule } from '@angular/forms';
import { RouterLink, RouterLinkActive } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';
import { TimeInputComponent } from '../../../shared/ui/time-input/time-input.component';
import { finalize, forkJoin } from 'rxjs';
import { ApiError } from '../../../core/api/api-client.service';
import { UnsavedChangesAware } from '../../../core/unsaved/unsaved-changes';
import {
  BonusClaimMode,
  BonusDefaults,
  TaskCategory,
  TaskSettingsResponse,
  TaskTemplate,
  TaskTemplateRequest,
} from '../../tasks/tasks.models';
import { TasksService } from '../../tasks/tasks.service';
import {
  ApprovalChoice,
  approvalChoice,
  approvalValue,
  isOptionalWhole,
  settingsBody,
  settingsErrorKey,
} from '../settings.format';

const NAME_MAX = 80;
const TITLE_MAX = 200;
const DESCRIPTION_MAX = 2000;
const POINTS_MIN = 1;
const POINTS_MAX = 100000;

/** Editable bonus template values. Bonus templates have no penalty fields. */
interface BonusTemplateForm {
  name: string;
  category_id: string | null;
  title: string;
  description: string;
  points: number | null;
  approval_required: boolean;
}

function emptyForm(): BonusTemplateForm {
  return { name: '', category_id: null, title: '', description: '', points: null, approval_required: true };
}

function formOf(template: TaskTemplate): BonusTemplateForm {
  return {
    name: template.name,
    category_id: template.category_id,
    title: template.title ?? '',
    description: template.description ?? '',
    points: template.points,
    approval_required: template.approval_required ?? true,
  };
}

/** Bonus task templates. Each template prefills a bonus task when it is published. Changing one never changes published tasks. */
import { BannerComponent } from '../../../shared/ui/banner/banner.component';
import { LoadErrorComponent } from '../../../shared/ui/load-error/load-error.component';
import { LoadingComponent } from '../../../shared/ui/loading/loading.component';

@Component({
    selector: 'app-bonus-templates-settings',
    imports: [FormsModule, RouterLink, RouterLinkActive, TranslocoModule, BannerComponent, LoadingComponent, LoadErrorComponent, TimeInputComponent],
    templateUrl: './bonus-templates-settings.component.html',
    changeDetection: ChangeDetectionStrategy.Eager,
    styleUrl: './bonus-templates-settings.component.scss'
})
export class BonusTemplatesSettingsComponent implements UnsavedChangesAware {
  private service = inject(TasksService);
  private destroyRef = inject(DestroyRef);
  private cdr = inject(ChangeDetectorRef);
  /** The values as last loaded or saved. Anything else counts as unsaved. */
  private defaultsBaseline = '';
  private editorBaseline = '';

  readonly nameMax = NAME_MAX;
  readonly titleMax = TITLE_MAX;
  readonly descriptionMax = DESCRIPTION_MAX;
  readonly pointsMin = POINTS_MIN;
  readonly pointsMax = POINTS_MAX;

  loadState = signal<'loading' | 'ready' | 'error'>('loading');
  loadError = signal<ApiError | null>(null);
  settings = signal<TaskSettingsResponse | null>(null);
  categories = signal<TaskCategory[]>([]);
  templates = signal<TaskTemplate[]>([]);
  nextCursor = signal<string | null>(null);
  loadingMore = signal(false);
  listError = signal<string | null>(null);

  // Bonus defaults card. Ends is '' (no default), end_of_day or an HH:MM time.
  defaultsPoints: number | null = null;
  defaultsClaimMode: BonusClaimMode | null = null;
  defaultsShowFrom = '';
  defaultsEnds = '';
  defaultsApproval: ApprovalChoice = 'unset';
  defaultsBusy = signal(false);
  defaultsSaved = signal(false);
  defaultsError = signal<string | null>(null);

  // Editor. `current` is the stored template being edited, or null when creating.
  creating = signal(false);
  current = signal<TaskTemplate | null>(null);
  editing = computed(() => this.creating() || this.current() !== null);
  form: BonusTemplateForm = emptyForm();
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
    forkJoin({
      settings: this.service.settings(),
      categories: this.service.categories(),
      templates: this.service.templates(null, 'bonus'),
    })
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: ({ settings, categories, templates }) => {
          this.settings.set(settings);
          this.loadDefaults(settings);
          this.categories.set(categories.categories);
          this.templates.set(templates.templates);
          this.nextCursor.set(templates.next_cursor);
          this.loadState.set('ready');
        },
        error: (err: ApiError) => {
          this.loadError.set(err);
          this.loadState.set('error');
        },
      });
  }

  private loadDefaults(settings: TaskSettingsResponse): void {
    const defaults = settings.bonus_defaults;
    this.defaultsPoints = defaults.points;
    this.defaultsClaimMode = defaults.claim_mode;
    this.defaultsShowFrom = defaults.show_from ?? '';
    this.defaultsEnds = defaults.ends ?? '';
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
    return JSON.stringify([
      this.defaultsPoints,
      this.defaultsClaimMode,
      this.defaultsShowFrom,
      this.defaultsEnds,
      this.defaultsApproval,
    ]);
  }

  private editorSnapshot(): string {
    return JSON.stringify(this.form);
  }

  saveDefaults(): void {
    void this.saveDefaultsChanges();
  }

  private saveDefaultsChanges(): Promise<boolean> {
    const current = this.settings();
    if (!current || this.defaultsBusy()) return Promise.resolve(false);
    this.defaultsSaved.set(false);
    this.defaultsError.set(null);
    if (!isOptionalWhole(this.defaultsPoints, POINTS_MIN, POINTS_MAX)) {
      this.defaultsError.set('settings.error.number');
      return Promise.resolve(false);
    }
    this.defaultsBusy.set(true);
    const bonusDefaults: BonusDefaults = {
      points: this.defaultsPoints,
      claim_mode: this.defaultsClaimMode,
      show_from: this.defaultsShowFrom || null,
      ends: this.defaultsEnds || null,
      approval_required: approvalValue(this.defaultsApproval),
    };
    return new Promise<boolean>((resolve) => {
      this.service
        .updateSettings(settingsBody(current, { bonusDefaults }), current.version)
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


  loadMore(): void {
    const cursor = this.nextCursor();
    if (!cursor || this.loadingMore()) return;
    this.loadingMore.set(true);
    this.listError.set(null);
    this.service
      .templates(cursor, 'bonus')
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

  // Editor

  newTemplate(): void {
    this.current.set(null);
    this.creating.set(true);
    this.form = emptyForm();
    this.editorBaseline = this.editorSnapshot();
    this.resetEditor();
    this.showEditorOnNarrow();
  }

  edit(template: TaskTemplate): void {
    this.creating.set(false);
    this.current.set(template);
    this.form = formOf(template);
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

  /** The first problem with the editor values, as a translation key. Null when the values are valid. */
  validationKey(value: BonusTemplateForm): string | null {
    const name = value.name.trim();
    if (name === '') return 'settings.error.required';
    if (name.length > NAME_MAX) return 'settings.error.too_long';
    if (value.title.length > TITLE_MAX || value.description.length > DESCRIPTION_MAX) return 'settings.error.too_long';
    if (!isOptionalWhole(value.points, POINTS_MIN, POINTS_MAX)) return 'settings.error.number';
    return null;
  }

  /** Body for create or update. Only the bonus fields are sent. */
  private bodyOf(value: BonusTemplateForm): TaskTemplateRequest {
    return {
      name: value.name.trim(),
      category_id: value.category_id,
      title: value.title.trim() || null,
      description: value.description.trim() || null,
      points: value.points,
      approval_required: value.approval_required,
    };
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
    const body = this.bodyOf(this.form);
    const stored = this.current();
    const request$ = stored
      ? this.service.updateTemplate(stored.id, body, stored.version)
      : this.service.createTemplate({ ...body, kind: 'bonus' });
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
            this.form = formOf(saved);
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
