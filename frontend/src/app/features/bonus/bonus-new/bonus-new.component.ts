import { Component, DestroyRef, computed, inject, signal, ChangeDetectionStrategy } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormsModule } from '@angular/forms';
import { Router, RouterLink } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';
import { catchError, finalize, of } from 'rxjs';
import { ApiError } from '../../../core/api/api-client.service';
import { AvatarComponent } from '../../../shared/avatar/avatar.component';
import { BannerComponent } from '../../../shared/ui/banner/banner.component';
import { SkeletonComponent } from '../../../shared/ui/skeleton/skeleton.component';
import { TimeInputComponent } from '../../../shared/ui/time-input/time-input.component';
import { FamilyService, MemberDetail } from '../../family/family.service';
import { DashboardService } from '../../dashboard/dashboard.service';
import { BonusClaimMode } from '../bonus.models';
import { clockOptions } from '../../../core/time-format/clock-style';
import { BonusService } from '../bonus.service';
import { TasksService } from '../../tasks/tasks.service';
import { BonusDefaults, TaskTemplate } from '../../tasks/tasks.models';
import { UnsavedChangesAware } from '../../../core/unsaved/unsaved-changes';

const TITLE_MAX = 200;
const TEMPLATE_CHIPS = 5;
const TEMPLATE_NAME_MAX = 80;
const DESCRIPTION_MAX = 2000;
const POINTS_MIN = 1;
const POINTS_MAX = 100000;
const NUMBERS = new Intl.NumberFormat(undefined);
const DAY_PATTERN = /^\d{4}-\d{2}-\d{2}$/;

/** Adds whole days to a family-local YYYY-MM-DD day. */
function addDays(day: string, n: number): string {
  const d = new Date(`${day}T00:00:00Z`);
  d.setUTCDate(d.getUTCDate() + n);
  return d.toISOString().slice(0, 10);
}

/** Offset of a time zone from UTC, in milliseconds, at an instant. */
function zoneOffsetMs(instantMs: number, timeZone: string): number {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone,
    hourCycle: 'h23',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).formatToParts(new Date(instantMs));
  const part = (type: string): number => Number(parts.find((p) => p.type === type)?.value);
  const wall = Date.UTC(part('year'), part('month') - 1, part('day'), part('hour'), part('minute'), part('second'));
  return wall - Math.floor(instantMs / 1000) * 1000;
}

/** RFC3339 instant of an HH:mm time on a family-local day, in the family's time zone. */
function zonedInstant(day: string, hhmm: string, timeZone: string): string {
  const [y, m, d] = day.split('-').map(Number);
  const [h, min] = hhmm.split(':').map(Number);
  const wall = Date.UTC(y, m - 1, d, h, min);
  const first = wall - zoneOffsetMs(wall, timeZone);
  const instant = wall - zoneOffsetMs(first, timeZone);
  return new Date(instant).toISOString().replace(/\.\d{3}Z$/, 'Z');
}

/** Time such as "4:00 pm" for an HH:mm value. */
function clock(hhmm: string): string {
  const [h, m] = hhmm.split(':').map(Number);
  return new Intl.DateTimeFormat(undefined, clockOptions({ hour: 'numeric', minute: '2-digit' })).format(new Date(2000, 0, 1, h, m));
}

/** Publishes a bonus task for one day, with an optional show-from time and end time. */
@Component({
    selector: 'app-bonus-new',
    imports: [FormsModule, RouterLink, TranslocoModule, AvatarComponent, BannerComponent, SkeletonComponent, TimeInputComponent],
    templateUrl: './bonus-new.component.html',
    changeDetection: ChangeDetectionStrategy.Eager,
    styleUrl: './bonus-new.component.scss'
})
export class BonusNewComponent implements UnsavedChangesAware {
  private api = inject(BonusService);
  private familyApi = inject(FamilyService);
  private dashboardApi = inject(DashboardService);
  private tasks = inject(TasksService);
  private router = inject(Router);
  private destroyRef = inject(DestroyRef);
  /** The values as last loaded or saved. Anything else counts as unsaved. */
  private baseline = '';

  readonly titleMax = TITLE_MAX;
  readonly descriptionMax = DESCRIPTION_MAX;
  readonly pointsMin = POINTS_MIN;
  readonly pointsMax = POINTS_MAX;
  readonly pointsRangeParams = { min: NUMBERS.format(POINTS_MIN), max: NUMBERS.format(POINTS_MAX) };

  readonly title = signal('');
  readonly description = signal('');
  readonly points = signal<number | null>(10);
  readonly mode = signal<BonusClaimMode>('first_come');
  readonly approvalRequired = signal(true);
  readonly selected = signal<string[]>([]);

  /** Family time zone and family-local today, from the parent dashboard. */
  readonly zone = signal<{ timezone: string; today: string } | null>(null);
  readonly zoneState = signal<'loading' | 'ready' | 'error'>('loading');
  readonly today = computed(() => this.zone()?.today ?? '');
  readonly tomorrow = computed(() => (this.today() ? addDays(this.today(), 1) : ''));

  /** Chosen day as family-local YYYY-MM-DD. Starts on today. */
  readonly day = signal('');
  /** HH:mm on the chosen day. Empty means show now. */
  readonly showAt = signal('');
  /** HH:mm on the chosen day. Empty means the end of the day. */
  readonly endsAt = signal('');

  readonly dayError = computed(() => {
    const d = this.day();
    if (!DAY_PATTERN.test(d)) return 'bonus.new.error.dayInvalid';
    if (this.today() && d < this.today()) return 'bonus.new.error.dayPast';
    return null;
  });

  /** The end must be after the show time and after midnight. */
  readonly windowError = computed(() => {
    const show = this.showAt();
    const ends = this.endsAt();
    if (ends && (ends === '00:00' || (show && ends <= show))) return 'bonus.new.error.endsTooEarly';
    return null;
  });

  readonly windowPreview = computed<{ key: string; params: Record<string, string | undefined> }>(() => {
    const show = this.showAt();
    const ends = this.endsAt();
    if (show && ends) return { key: 'bonus.window.range', params: { from: clock(show), to: clock(ends) } };
    if (show) return { key: 'bonus.window.from', params: { from: clock(show) } };
    if (ends) return { key: 'bonus.until', params: { time: clock(ends) } };
    return { key: 'bonus.new.windowEndOfDay', params: {} };
  });

  readonly dayPreview = computed(() => {
    const d = this.day();
    if (!DAY_PATTERN.test(d)) return '';
    return new Intl.DateTimeFormat(undefined, { weekday: 'short', day: 'numeric', month: 'short', timeZone: 'UTC' }).format(
      new Date(`${d}T00:00:00Z`),
    );
  });

  readonly members = signal<MemberDetail[]>([]);
  readonly kidsState = signal<'loading' | 'ready' | 'error'>('loading');
  readonly kids = computed(() => this.members().filter((m) => m.is_child && m.status === 'active'));

  readonly attempted = signal(false);
  readonly saving = signal(false);
  readonly saveError = signal<string | null>(null);
  readonly templateSaving = signal(false);
  readonly templateSaved = signal(false);
  readonly templateError = signal<string | null>(null);

  /** Saved bonus templates offered as starting points. Empty when none exist or they fail to load. */
  readonly templates = signal<TaskTemplate[]>([]);
  readonly templatesExpanded = signal(false);
  readonly selectedTemplateId = signal<string | null>(null);
  readonly visibleTemplates = computed(() =>
    this.templatesExpanded() ? this.templates() : this.templates().slice(0, TEMPLATE_CHIPS),
  );

  readonly titleError = computed(() => {
    const t = this.title().trim();
    if (!t) return 'bonus.new.error.titleRequired';
    if (t.length > TITLE_MAX) return 'bonus.new.error.titleTooLong';
    return null;
  });

  readonly descriptionError = computed(() =>
    this.description().length > DESCRIPTION_MAX ? 'bonus.new.error.descriptionTooLong' : null,
  );

  readonly pointsError = computed(() => {
    const p = this.points();
    if (p === null || !Number.isInteger(p)) return 'bonus.new.error.pointsInvalid';
    if (p < POINTS_MIN || p > POINTS_MAX) return 'bonus.new.error.pointsRange';
    return null;
  });

  readonly kidsError = computed(() => (this.selected().length === 0 ? 'bonus.new.error.kidsRequired' : null));

  readonly valid = computed(
    () =>
      !this.titleError() &&
      !this.descriptionError() &&
      !this.pointsError() &&
      !this.kidsError() &&
      !this.dayError() &&
      !this.windowError(),
  );

  /** Preview: the first selected kid, in the order of the kids list. */
  readonly previewName = computed(() => {
    const chosen = this.kids().find((k) => this.selected().includes(k.id));
    return chosen?.display_name ?? null;
  });

  readonly pointsPreview = computed(() => NUMBERS.format(this.points() ?? 0));

  constructor() {
    this.familyApi
      .listMembers()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => {
          this.members.set(res.members);
          this.kidsState.set('ready');
          if (this.selected().length === 0 && !this.selectedTemplateId()) {
            this.selected.set(this.kids().map((k) => k.id));
            if (this.zoneState() === 'ready') this.baseline = this.snapshot();
          }
        },
        error: () => this.kidsState.set('error'),
      });

    this.dashboardApi
      .parent()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => {
          this.zone.set({ timezone: res.timezone, today: res.date });
          this.day.set(res.date);
          this.zoneState.set('ready');
          this.baseline = this.snapshot();
        },
        error: () => this.zoneState.set('error'),
      });

    this.tasks
      .templates(null, 'bonus')
      .pipe(
        catchError(() => of({ templates: [] as TaskTemplate[], next_cursor: null })),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe((res) => this.templates.set(res.templates));

    this.tasks
      .settings()
      .pipe(
        catchError(() => of(null)),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe((res) => {
        if (res) this.applyDefaults(res.bonus_defaults);
        if (this.zoneState() === 'ready') this.baseline = this.snapshot();
      });
  }

  hasUnsavedChanges(): boolean {
    return this.zoneState() === 'ready' && this.snapshot() !== this.baseline;
  }

  private snapshot(): string {
    return JSON.stringify([
      this.title(),
      this.description(),
      this.points(),
      this.mode(),
      this.approvalRequired(),
      this.selected(),
      this.day(),
      this.showAt(),
      this.endsAt(),
    ]);
  }

  /**
   * Starts the form from the family bonus defaults. Runs once when the settings load. A template
   * already picked keeps its values, so the defaults are skipped in that case.
   */
  private applyDefaults(defaults: BonusDefaults): void {
    if (this.selectedTemplateId()) return;
    if (defaults.points !== null) this.points.set(defaults.points);
    if (defaults.claim_mode !== null) this.mode.set(defaults.claim_mode);
    if (defaults.approval_required !== null) this.approvalRequired.set(defaults.approval_required);
    if (defaults.show_from !== null) this.showAt.set(defaults.show_from);
    if (defaults.ends !== null) this.endsAt.set(defaults.ends === 'end_of_day' ? '' : defaults.ends);
  }

  /** Pre-fills only the fields the template sets. The chosen template is sent with the publish. */
  applyTemplate(template: TaskTemplate): void {
    this.selectedTemplateId.set(template.id);
    const title = template.title ?? template.name;
    this.title.set(title);
    if (template.description !== null) this.description.set(template.description);
    if (template.points !== null) this.points.set(template.points);
    if (template.approval_required !== null) this.approvalRequired.set(template.approval_required);
  }

  isSelected(id: string): boolean {
    return this.selected().includes(id);
  }

  toggleKid(id: string): void {
    this.selected.update((ids) => (ids.includes(id) ? ids.filter((x) => x !== id) : [...ids, id]));
  }

  submit(): void {
    void this.saveChanges().then((ok) => {
      if (ok) void this.router.navigateByUrl('/bonus');
    });
  }

  saveChanges(): Promise<boolean> {
    this.attempted.set(true);
    this.saveError.set(null);
    if (!this.valid() || this.saving()) return Promise.resolve(false);
    const zone = this.zone();
    if (!zone) {
      this.saveError.set('bonus.new.zoneError');
      return Promise.resolve(false);
    }
    const description = this.description().trim();
    const day = this.day();
    const show = this.showAt();
    const ends = this.endsAt();
    const templateId = this.selectedTemplateId();
    this.saving.set(true);
    return new Promise<boolean>((resolve) => {
      this.api
        .create({
          title: this.title().trim(),
          description: description || null,
          points: this.points() ?? 0,
          claim_mode: this.mode(),
          approval_required: this.approvalRequired(),
          eligible_member_ids: this.selected(),
          day,
          show_from: show ? zonedInstant(day, show, zone.timezone) : null,
          ends_at: ends ? zonedInstant(day, ends, zone.timezone) : null,
          ...(templateId ? { template_id: templateId } : {}),
        })
        .pipe(takeUntilDestroyed(this.destroyRef))
        .subscribe({
          next: () => {
            this.saving.set(false);
            this.baseline = this.snapshot();
            resolve(true);
          },
          error: (err) => {
            this.saving.set(false);
            this.saveError.set(this.errorKey(err));
            resolve(false);
          },
        });
    });
  }

  /** Saves the form values as a reusable bonus template. Nothing is published. */
  saveAsTemplate(): void {
    this.templateSaved.set(false);
    this.templateError.set(null);
    if (this.templateSaving()) return;
    const problem = this.titleError() ?? this.descriptionError() ?? this.pointsError();
    if (problem) {
      this.templateError.set(problem);
      return;
    }
    const title = this.title().trim();
    if (title.length > TEMPLATE_NAME_MAX) {
      this.templateError.set('bonus.new.templateTooLong');
      return;
    }
    this.templateSaving.set(true);
    this.tasks
      .createTemplate({
        kind: 'bonus',
        name: title,
        title,
        description: this.description().trim() || null,
        points: this.points(),
        approval_required: this.approvalRequired(),
      })
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => this.templateSaving.set(false)),
      )
      .subscribe({
        next: () => this.templateSaved.set(true),
        error: (err) => this.templateError.set(this.errorKey(err)),
      });
  }

  /** Translation key for a failed publish. */
  private errorKey(err: unknown): string {
    const apiErr = err as Partial<ApiError> | null;
    switch (apiErr?.status) {
      case 0:
        return 'bonus.error.network';
      case 400:
        return this.validationKey(apiErr?.params);
      case 403:
        return 'bonus.error.forbidden';
      default:
        return 'bonus.error.generic';
    }
  }

  /** Translation key for a 400 field error from the server. */
  private validationKey(params: Record<string, string> | undefined): string {
    const field = params?.['field'];
    const reason = params?.['reason'];
    if (field === 'day') return reason === 'in_past' ? 'bonus.new.error.dayPast' : 'bonus.new.error.dayInvalid';
    if (field === 'show_from' && reason === 'after_end') return 'bonus.new.error.endsTooEarly';
    if (field === 'ends_at' && reason === 'in_past') return 'bonus.new.error.endsPast';
    if (field === 'ends_at' && reason === 'before_day') return 'bonus.new.error.endsTooEarly';
    return 'bonus.new.error.invalid';
  }
}
