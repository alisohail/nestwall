import { Component, DestroyRef, HostListener, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormsModule } from '@angular/forms';
import { TranslocoModule, TranslocoService } from '@ngneat/transloco';
import { finalize, forkJoin } from 'rxjs';
import { ApiError } from '../../../core/api/api-client.service';
import { UnsavedChangesAware } from '../../../core/unsaved/unsaved-changes';
import { TaskCategory, TaskSettingsResponse, PartOfDayTimes } from '../../tasks/tasks.models';
import { TasksService } from '../../tasks/tasks.service';
import {
  CATEGORY_EMOJIS,
  CATEGORY_EMOJI_MAX,
  CategoryFormValue,
  ExpiryUnit,
  categoryBody,
  categoryToForm,
  expiryParts,
  isExpiryValid,
  isOptionalWhole,
  isSingleEmoji,
  settingsBody,
  settingsErrorKey,
  timesInOrder,
} from '../settings.format';

/** Task categories (name, emoji, colour, defaults) and the part-of-day times used by the kid dashboard. */
import { BannerComponent } from '../../../shared/ui/banner/banner.component';
import { ColourPickerComponent, COLOUR_PALETTE } from '../../../shared/ui/colour-picker/colour-picker.component';
import { LoadErrorComponent } from '../../../shared/ui/load-error/load-error.component';
import { LoadingComponent } from '../../../shared/ui/loading/loading.component';

@Component({
    selector: 'app-task-categories-settings',
    imports: [FormsModule, TranslocoModule, BannerComponent, LoadingComponent, LoadErrorComponent, ColourPickerComponent],
    templateUrl: './task-categories-settings.component.html',
    styleUrl: './task-categories-settings.component.scss'
})
export class TaskCategoriesSettingsComponent implements UnsavedChangesAware {
  private service = inject(TasksService);
  private transloco = inject(TranslocoService);
  private destroyRef = inject(DestroyRef);
  /** The part-of-day times as last loaded or saved. Anything else counts as unsaved. */
  private baseline = '';

  readonly emojis = CATEGORY_EMOJIS;
  readonly expiryUnits: ExpiryUnit[] = ['days', 'hours'];
  /** Skill badges a category can earn: the stored badge key and the name key of the built-in badge. */
  readonly skillBadges: readonly { key: string; nameKey: string }[] = [
    { key: 'tidy_pro', nameKey: 'settings.badges.builtin.k_tidy' },
    { key: 'homework_hero', nameKey: 'settings.badges.builtin.k_hw' },
    { key: 'pet_pal', nameKey: 'settings.badges.builtin.k_pet' },
    { key: 'green_thumb', nameKey: 'settings.badges.builtin.k_green' },
  ];

  loadState = signal<'loading' | 'ready' | 'error'>('loading');
  loadError = signal<ApiError | null>(null);
  categories = signal<TaskCategory[]>([]);
  settings = signal<TaskSettingsResponse | null>(null);

  // Category modal. `editing` is the stored category, or null when adding.
  modalOpen = signal(false);
  editing = signal<TaskCategory | null>(null);
  form: CategoryFormValue = this.blankForm();
  submitted = signal(false);
  saving = signal(false);
  modalError = signal<string | null>(null);

  // Part of day card
  partTimes: PartOfDayTimes = { morning_start: '', after_school_start: '', evening_start: '' };
  partSaving = signal(false);
  partSaved = signal(false);
  partError = signal<string | null>(null);

  constructor() {
    this.load();
  }

  load(): void {
    this.loadState.set('loading');
    this.loadError.set(null);
    forkJoin({ categories: this.service.categories(), settings: this.service.settings() })
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: ({ categories, settings }) => {
          this.categories.set(categories.categories);
          this.applySettings(settings);
          this.loadState.set('ready');
        },
        error: (err: ApiError) => {
          this.loadError.set(err);
          this.loadState.set('error');
        },
      });
  }

  private applySettings(settings: TaskSettingsResponse): void {
    this.settings.set(settings);
    this.partTimes = { ...settings.part_of_day };
    this.baseline = this.snapshot();
  }

  // Category list

  /** Expiry label for a category, for example "2 days". */
  expiryLabel(category: TaskCategory): string | null {
    const parts = expiryParts(category.expires_after_minutes);
    if (parts.value === null) return null;
    return `${parts.value} ${this.transloco.translate('settings.unit.' + parts.unit)}`;
  }

  approvalLabel(category: TaskCategory): string | null {
    if (category.approval_required === null) return null;
    return category.approval_required
      ? this.transloco.translate('settings.common.approval_required')
      : this.transloco.translate('settings.common.approval_not_required');
  }

  /** Name of the skill badge the category earns, or null when it earns none. */
  skillBadgeLabel(category: TaskCategory): string | null {
    const badge = this.skillBadges.find((b) => b.key === category.badge_key);
    return badge ? this.transloco.translate(badge.nameKey) : null;
  }

  hasDefaults(category: TaskCategory): boolean {
    return category.points !== null || category.approval_required !== null || category.expires_after_minutes !== null;
  }

  // Modal

  private blankForm(): CategoryFormValue {
    return {
      name: '',
      emoji: CATEGORY_EMOJIS[0],
      colour: COLOUR_PALETTE[0],
      points: null,
      approval_required: null,
      expiry_value: null,
      expiry_unit: 'days',
      badge_key: null,
    };
  }

  openNew(): void {
    this.editing.set(null);
    this.form = this.blankForm();
    this.openModal();
  }

  openEdit(category: TaskCategory): void {
    this.editing.set(category);
    this.form = categoryToForm(category);
    this.openModal();
  }

  private openModal(): void {
    this.submitted.set(false);
    this.modalError.set(null);
    this.modalOpen.set(true);
  }

  closeModal(): void {
    if (this.saving()) return;
    this.modalOpen.set(false);
  }

  @HostListener('document:keydown.escape')
  onEscape(): void {
    this.closeModal();
  }

  /** The first problem with the modal values, as a translation key. Null when valid. */
  validationKey(value: CategoryFormValue): string | null {
    const name = value.name.trim();
    if (name === '') return 'settings.error.required';
    if (name.length > 80) return 'settings.error.too_long';
    const emoji = value.emoji.trim();
    if (emoji === '') return 'settings.error.required';
    if (!isSingleEmoji(emoji)) return 'settings.error.emoji_invalid';
    if ([...emoji].length > CATEGORY_EMOJI_MAX) return 'settings.error.emoji_too_long';
    if (!isOptionalWhole(value.points, 0, Number.MAX_SAFE_INTEGER)) return 'settings.error.number';
    if (!isExpiryValid(value.expiry_value, value.expiry_unit)) return 'settings.error.expiry';
    return null;
  }

  save(): void {
    if (this.saving()) return;
    this.submitted.set(true);
    this.modalError.set(null);
    const problem = this.validationKey(this.form);
    if (problem) {
      this.modalError.set(problem);
      return;
    }
    const stored = this.editing();
    const body = categoryBody(this.form, stored);
    const request$ = stored
      ? this.service.updateCategory(stored.id, body, stored.version)
      : this.service.createCategory(body);
    this.saving.set(true);
    request$
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => this.saving.set(false)),
      )
      .subscribe({
        next: () => {
          this.modalOpen.set(false);
          this.reloadCategories();
        },
        error: (err: ApiError) => this.modalError.set(settingsErrorKey(err)),
      });
  }

  private reloadCategories(): void {
    this.service
      .categories()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (list) => this.categories.set(list.categories),
        error: (err: ApiError) => this.loadError.set(err),
      });
  }

  // Part of day

  hasUnsavedChanges(): boolean {
    return this.loadState() === 'ready' && this.snapshot() !== this.baseline;
  }

  savePartOfDay(): void {
    void this.saveChanges();
  }

  saveChanges(): Promise<boolean> {
    const current = this.settings();
    if (!current || this.partSaving()) return Promise.resolve(false);
    this.partSaved.set(false);
    this.partError.set(null);
    const times = this.partTimes;
    if (!times.morning_start || !times.after_school_start || !times.evening_start) {
      this.partError.set('settings.error.time_required');
      return Promise.resolve(false);
    }
    if (!timesInOrder(times)) {
      this.partError.set('settings.error.out_of_order');
      return Promise.resolve(false);
    }
    this.partSaving.set(true);
    return new Promise<boolean>((resolve) => {
      this.service
        .updateSettings(settingsBody(current, { partOfDay: { ...times } }), current.version)
        .pipe(
          takeUntilDestroyed(this.destroyRef),
          finalize(() => this.partSaving.set(false)),
        )
        .subscribe({
          next: (saved) => {
            this.applySettings(saved);
            this.partSaved.set(true);
            resolve(true);
          },
          error: (err: ApiError) => {
            this.partError.set(settingsErrorKey(err));
            resolve(false);
          },
        });
    });
  }

  private snapshot(): string {
    return JSON.stringify(this.partTimes);
  }
}
