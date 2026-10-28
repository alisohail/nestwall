import { Component, DestroyRef, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormsModule } from '@angular/forms';
import { RouterLink } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';
import { finalize } from 'rxjs';
import { ApiError } from '../../../core/api/api-client.service';
import { ApprovalMode, ShareSettings, ShareSettingsInput } from '../../sharing/sharing.models';
import { SharingService } from '../../sharing/sharing.service';
import { RewardsService } from '../../rewards/rewards.service';
import { isOptionalWhole, settingsErrorKey } from '../settings.format';
import { UnsavedChangesAware } from '../../../core/unsaved/unsaved-changes';

/** Upper bound for every points field, matching the backend `maxPoints`. */
const MAX_POINTS = 100000;

/** Parent settings for the sharing rules. Maps to the points settings mock (sharing card only). */
import { BannerComponent } from '../../../shared/ui/banner/banner.component';
import { LoadingComponent } from '../../../shared/ui/loading/loading.component';
import { SkeletonComponent } from '../../../shared/ui/skeleton/skeleton.component';

@Component({
    selector: 'app-points-settings',
    imports: [FormsModule, RouterLink, TranslocoModule, BannerComponent, LoadingComponent, SkeletonComponent],
    templateUrl: './points-settings.component.html',
    styleUrl: './points-settings.component.scss'
})
export class PointsSettingsComponent implements UnsavedChangesAware {
  private sharing = inject(SharingService);
  private rewards = inject(RewardsService);
  private destroyRef = inject(DestroyRef);
  /** The sharing rules and the spending default as last loaded or saved. */
  private baseline = '';
  private spendBaseline = '';

  readonly approvalModes: readonly ApprovalMode[] = ['never', 'above', 'always'];
  readonly maxPoints = MAX_POINTS;

  loadState = signal<'loading' | 'ready' | 'error'>('loading');
  loadError = signal<string | null>(null);
  current = signal<ShareSettings | null>(null);
  busy = signal(false);
  saved = signal(false);
  saveError = signal<string | null>(null);
  stale = signal(false);

  /** The spending card has its own load and save, separate from the sharing rules. */
  spendState = signal<'loading' | 'ready' | 'error'>('loading');
  spendBusy = signal(false);
  spendSaved = signal(false);
  spendError = signal<string | null>(null);
  approvalDefault = false;

  enabled = false;
  minPerShare: number | null = null;
  maxPerShare: number | null = null;
  dailyLimit: number | null = null;
  minBalance: number | null = null;
  approvalMode: ApprovalMode = 'never';
  approvalAbove: number | null = null;
  notifyRecipient = false;
  notifyParents = false;

  constructor() {
    this.load();
    this.loadSpending();
  }

  loadSpending(): void {
    this.spendState.set('loading');
    this.spendError.set(null);
    this.rewards
      .settings()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (s) => {
          this.approvalDefault = s.approval_required_default;
          this.spendBaseline = this.spendSnapshot();
          this.spendState.set('ready');
        },
        error: (err: ApiError) => {
          this.spendError.set(settingsErrorKey(err));
          this.spendState.set('error');
        },
      });
  }

  hasSpendingChanges(): boolean {
    return this.spendState() === 'ready' && this.spendSnapshot() !== this.spendBaseline;
  }

  saveSpending(): Promise<boolean> {
    if (this.spendBusy()) return Promise.resolve(false);
    this.spendSaved.set(false);
    this.spendError.set(null);
    this.spendBusy.set(true);
    return new Promise<boolean>((resolve) => {
      this.rewards
        .saveSettings({ approval_required_default: this.approvalDefault })
        .pipe(
          takeUntilDestroyed(this.destroyRef),
          finalize(() => this.spendBusy.set(false)),
        )
        .subscribe({
          next: (s) => {
            this.approvalDefault = s.approval_required_default;
            this.spendBaseline = this.spendSnapshot();
            this.spendSaved.set(true);
            resolve(true);
          },
          error: (err: ApiError) => {
            this.spendError.set(settingsErrorKey(err));
            resolve(false);
          },
        });
    });
  }

  private spendSnapshot(): string {
    return JSON.stringify([this.approvalDefault]);
  }

  load(): void {
    this.loadState.set('loading');
    this.loadError.set(null);
    this.stale.set(false);
    this.sharing
      .summary()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (s) => {
          this.current.set(s);
          this.fill(s);
          this.loadState.set('ready');
        },
        error: (err: ApiError) => {
          this.loadError.set(settingsErrorKey(err));
          this.loadState.set('error');
        },
      });
  }

  hasUnsavedChanges(): boolean {
    return this.hasSharingChanges() || this.hasSpendingChanges();
  }

  hasSharingChanges(): boolean {
    return this.loadState() === 'ready' && this.snapshot() !== this.baseline;
  }

  /** Saves whichever forms have edits. Stops at the first failure. */
  async saveChanges(): Promise<boolean> {
    if (this.hasSharingChanges() && !(await this.saveSharing())) return false;
    if (this.hasSpendingChanges() && !(await this.saveSpending())) return false;
    return true;
  }

  private fill(s: ShareSettings): void {
    this.enabled = s.enabled;
    this.minPerShare = s.min_per_share;
    this.maxPerShare = s.max_per_share;
    this.dailyLimit = s.daily_limit;
    this.minBalance = s.min_balance;
    this.approvalMode = s.approval_mode;
    this.approvalAbove = s.approval_above;
    this.notifyRecipient = s.notify_recipient;
    this.notifyParents = s.notify_parents;
    this.baseline = this.snapshot();
  }

  private snapshot(): string {
    return JSON.stringify([
      this.enabled,
      this.minPerShare,
      this.maxPerShare,
      this.dailyLimit,
      this.minBalance,
      this.approvalMode,
      this.approvalAbove,
      this.notifyRecipient,
      this.notifyParents,
    ]);
  }

  /** A required whole number inside the inclusive range. */
  private whole(value: number | null, min: number, max: number): boolean {
    return value !== null && isOptionalWhole(value, min, max);
  }

  valid(): boolean {
    return (
      this.whole(this.minPerShare, 1, MAX_POINTS) &&
      this.whole(this.maxPerShare, 1, MAX_POINTS) &&
      this.whole(this.dailyLimit, 1, MAX_POINTS) &&
      this.whole(this.minBalance, 0, MAX_POINTS) &&
      this.whole(this.approvalAbove, 0, MAX_POINTS)
    );
  }

  save(): void {
    void this.saveSharing();
  }

  saveSharing(): Promise<boolean> {
    const version = this.current()?.version;
    if (version === undefined || this.busy()) return Promise.resolve(false);
    this.saved.set(false);
    this.saveError.set(null);
    this.stale.set(false);
    if (!this.valid()) {
      this.saveError.set('settings.error.number');
      return Promise.resolve(false);
    }
    // The validator: the fewest per share may not be more than the most.
    if (this.minPerShare !== null && this.maxPerShare !== null && this.minPerShare > this.maxPerShare) {
      this.saveError.set('settings.points.error_range');
      return Promise.resolve(false);
    }

    const input: ShareSettingsInput = {
      enabled: this.enabled,
      min_per_share: this.minPerShare ?? 1,
      max_per_share: this.maxPerShare ?? 0,
      daily_limit: this.dailyLimit ?? 0,
      min_balance: this.minBalance ?? 0,
      approval_mode: this.approvalMode,
      approval_above: this.approvalAbove ?? 0,
      notify_recipient: this.notifyRecipient,
      notify_parents: this.notifyParents,
    };

    this.busy.set(true);
    return new Promise<boolean>((resolve) => {
      this.sharing
        .saveSettings(input, version)
        .pipe(
          takeUntilDestroyed(this.destroyRef),
          finalize(() => this.busy.set(false)),
        )
        .subscribe({
          next: (saved) => {
            this.current.set(saved);
            this.fill(saved);
            this.saved.set(true);
            resolve(true);
          },
          error: (err: ApiError) => {
            // A 412 means someone else changed the settings. Show a reload action instead of a generic error.
            if (err.status === 412) this.stale.set(true);
            this.saveError.set(settingsErrorKey(err));
            resolve(false);
          },
        });
    });
  }
}
