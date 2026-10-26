import { Component, DestroyRef, HostListener, OnDestroy, OnInit, TemplateRef, ViewChild, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormsModule } from '@angular/forms';
import { TranslocoModule } from '@ngneat/transloco';
import { finalize } from 'rxjs';
import { ApiError } from '../../../core/api/api-client.service';
import {
  BADGE_DESCRIPTION_MAX,
  BADGE_NAME_MAX,
  BADGE_PICTURES,
  BUILT_IN_EMOJI,
  BadgeConfig,
  BadgeGroup,
  BadgeGroupKey,
  BadgeSettings,
  CustomBadge,
  CustomBadgeRequest,
} from '../../badges/badges.models';
import { badgeErrorKey } from '../../badges/badge-errors';
import { BadgesService } from '../../badges/badges.service';
import { blankToNull, isSingleEmoji, settingsErrorKey } from '../settings.format';
import { SettingsHeaderService } from '../settings-header.service';

type LoadState = 'loading' | 'ready' | 'error';

interface CustomFormValue {
  name: string;
  emoji: string;
  description: string;
  enabled: boolean;
}

const GROUP_ORDER: readonly BadgeGroupKey[] = ['streaks', 'points', 'tasks', 'skills', 'timing', 'kindness'];

function blankForm(): CustomFormValue {
  return { name: '', emoji: BADGE_PICTURES[0], description: '', enabled: true };
}

function length(text: string): number {
  return [...text].length;
}

import { BannerComponent } from '../../../shared/ui/banner/banner.component';
import { LoadErrorComponent } from '../../../shared/ui/load-error/load-error.component';
import { LoadingComponent } from '../../../shared/ui/loading/loading.component';

@Component({
  selector: 'app-badges-settings',
  standalone: true,
  imports: [FormsModule, TranslocoModule, BannerComponent, LoadingComponent, LoadErrorComponent],
  templateUrl: './badges-settings.component.html',
  styleUrl: './badges-settings.component.scss',
})
export class BadgesSettingsComponent implements OnInit, OnDestroy {
  private service = inject(BadgesService);
  private destroyRef = inject(DestroyRef);
  private header = inject(SettingsHeaderService);

  @ViewChild('headerActions', { static: true }) private headerActions!: TemplateRef<unknown>;

  readonly pictures = BADGE_PICTURES;
  readonly nameMax = BADGE_NAME_MAX;
  readonly descriptionMax = BADGE_DESCRIPTION_MAX;

  loadState = signal<LoadState>('loading');
  loadError = signal<ApiError | null>(null);
  config = signal<BadgeConfig | null>(null);
  busy = signal(false);
  saveError = signal<string | null>(null);
  /** True after a 412, so the page offers a reload instead of another write. */
  stale = signal(false);

  // Custom badge modal
  modalOpen = signal(false);
  editing = signal<CustomBadge | null>(null);
  form: CustomFormValue = blankForm();
  submitted = signal(false);
  saving = signal(false);
  modalError = signal<string | null>(null);

  ngOnInit(): void {
    this.header.actions.set(this.headerActions);
    this.load();
  }

  ngOnDestroy(): void {
    if (this.header.actions() === this.headerActions) {
      this.header.actions.set(null);
    }
  }

  load(): void {
    this.loadState.set('loading');
    this.loadError.set(null);
    this.saveError.set(null);
    this.stale.set(false);
    this.service
      .get()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (cfg) => {
          this.config.set(this.ordered(cfg));
          this.loadState.set('ready');
        },
        error: (err: ApiError) => {
          this.loadError.set(err);
          this.loadState.set('error');
        },
      });
  }

  /** Silent reload after a successful write that changes the list (create or edit). */
  private refresh(): void {
    this.service
      .get()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (cfg) => this.config.set(this.ordered(cfg)),
        error: (err: ApiError) => this.saveError.set(badgeErrorKey(err)),
      });
  }

  /** The API returns groups in a fixed order; this keeps it stable even if that changes. */
  private ordered(cfg: BadgeConfig): BadgeConfig {
    const rank = (g: BadgeGroupKey): number => GROUP_ORDER.indexOf(g);
    const groups: BadgeGroup[] = [...cfg.groups].sort((a, b) => rank(a.group) - rank(b.group));
    return { ...cfg, groups };
  }

  emojiFor(key: string): string {
    return BUILT_IN_EMOJI[key] ?? '🏅';
  }

  /** Family switch. The checkbox is reverted by hand if the write fails, because the bound value did not change. */
  toggleSetting(key: keyof BadgeSettings, event: Event): void {
    const input = event.target as HTMLInputElement;
    const cfg = this.config();
    const value = input.checked;
    if (!cfg || this.busy()) {
      input.checked = !value;
      return;
    }
    this.busy.set(true);
    this.saveError.set(null);
    this.stale.set(false);
    this.service
      .saveSettings({ ...cfg.settings, [key]: value }, cfg.version)
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => this.busy.set(false)),
      )
      .subscribe({
        next: (saved) => this.config.set(this.ordered(saved)),
        error: (err: ApiError) => {
          input.checked = !value;
          this.fail(err);
        },
      });
  }

  toggleGroup(group: BadgeGroup, event: Event): void {
    const input = event.target as HTMLInputElement;
    const cfg = this.config();
    const value = input.checked;
    if (!cfg || this.busy()) {
      input.checked = !value;
      return;
    }
    this.busy.set(true);
    this.saveError.set(null);
    this.stale.set(false);
    this.service
      .setGroup(group.group, value, cfg.version)
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => this.busy.set(false)),
      )
      .subscribe({
        next: (saved) => this.config.set(this.ordered(saved)),
        error: (err: ApiError) => {
          input.checked = !value;
          this.fail(err);
        },
      });
  }

  private fail(err: ApiError): void {
    this.stale.set(err.status === 412);
    this.saveError.set(badgeErrorKey(err));
  }

  // Custom badges

  openNew(): void {
    this.editing.set(null);
    this.form = blankForm();
    this.openModal();
  }

  openEdit(badge: CustomBadge): void {
    this.editing.set(badge);
    this.form = { name: badge.name, emoji: badge.emoji, description: badge.description ?? '', enabled: badge.enabled };
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

  private customProblem(value: CustomFormValue): string | null {
    const name = value.name.trim();
    if (name === '') return 'settings.error.required';
    if (length(name) > BADGE_NAME_MAX) return 'settings.error.too_long';
    const emoji = value.emoji.trim();
    if (emoji === '') return 'settings.badges.error.emoji';
    if (!isSingleEmoji(emoji)) return 'settings.error.emoji_invalid';
    if (length(value.description.trim()) > BADGE_DESCRIPTION_MAX) return 'settings.error.too_long';
    return null;
  }

  saveCustom(): void {
    if (this.saving()) return;
    this.submitted.set(true);
    this.modalError.set(null);
    const problem = this.customProblem(this.form);
    if (problem) {
      this.modalError.set(problem);
      return;
    }
    const body: CustomBadgeRequest = {
      name: this.form.name.trim(),
      emoji: this.form.emoji.trim(),
      description: blankToNull(this.form.description.trim()),
    };
    const stored = this.editing();
    const request$ = stored
      ? this.service.updateCustom(stored.id, { ...body, enabled: this.form.enabled }, stored.version)
      : this.service.createCustom(body);

    this.saving.set(true);
    request$
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => this.saving.set(false)),
      )
      .subscribe({
        next: () => {
          this.modalOpen.set(false);
          this.refresh();
        },
        error: (err: ApiError) => this.modalError.set(badgeErrorKey(err)),
      });
  }

  @HostListener('document:keydown.escape')
  onEscape(): void {
    this.closeModal();
  }
}
