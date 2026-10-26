import { DatePipe } from '@angular/common';
import { Component, DestroyRef, OnInit, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { RouterLink } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';
import { forkJoin } from 'rxjs';
import { AuthService } from '../../core/auth/auth.service';
import { BannerComponent } from '../../shared/ui/banner/banner.component';
import { EmptyStateComponent } from '../../shared/ui/empty-state/empty-state.component';
import { ApiError } from '../../core/api/api-client.service';
import { LoadErrorComponent } from '../../shared/ui/load-error/load-error.component';
import { IconComponent } from '../../shared/ui/icon/icon.component';
import { LoadingComponent } from '../../shared/ui/loading/loading.component';
import { MyPointsResponse, RedemptionListItem, Reward } from './rewards.models';
import { RewardsService } from './rewards.service';
import { newIdempotencyKey } from './idempotency-key';
import { RewardsAdminComponent } from './admin/rewards-admin.component';

type LoadState = 'loading' | 'ready' | 'error';
type Filter = 'all' | 'now' | 'saving';

/** Requests listed on this page. The full history is on the requests page. */
const RECENT_REQUESTS = 3;
/** Every pending request is needed for the Requested badges, so this is the API maximum. */
const PENDING_LIMIT = 200;

interface Notice {
  key: string;
  params?: Record<string, unknown>;
}

@Component({
  selector: 'app-rewards',
  standalone: true,
  imports: [
    TranslocoModule,
    DatePipe,
    RouterLink,
    RewardsAdminComponent,
    BannerComponent,
    EmptyStateComponent,
    LoadErrorComponent,
    IconComponent,
    LoadingComponent,
  ],
  templateUrl: './rewards.component.html',
  styleUrl: './rewards.component.scss',
})
export class RewardsComponent implements OnInit {
  protected auth = inject(AuthService);
  private api = inject(RewardsService);
  private destroyRef = inject(DestroyRef);

  loadState = signal<LoadState>('loading');
  loadError = signal<ApiError | null>(null);
  rewards = signal<Reward[]>([]);
  points = signal<MyPointsResponse | null>(null);
  /** The most recent requests, any status. */
  requests = signal<RedemptionListItem[]>([]);
  pendingRequests = signal<RedemptionListItem[]>([]);

  filter = signal<Filter>('all');
  readonly filters: Filter[] = ['all', 'now', 'saving'];

  /** Reward whose Request call is in flight. Blocks a second tap. */
  busyRewardId = signal<string | null>(null);
  cancelTarget = signal<RedemptionListItem | null>(null);
  cancelBusy = signal(false);
  /** i18n key of the last failed action, shown as a banner. */
  actionError = signal<string | null>(null);
  notice = signal<Notice | null>(null);

  /** Spendable points. The ledger balance already has pending requests taken out. */
  balance = computed(() => this.points()?.balance ?? 0);
  reserved = computed(() => this.points()?.reserved ?? 0);

  /** The cheapest active reward the child cannot yet afford. */
  goal = computed<Reward | null>(() => {
    const balance = this.balance();
    const saving = this.rewards()
      .filter((r) => r.cost > balance)
      .sort((a, b) => a.cost - b.cost);
    return saving[0] ?? null;
  });

  goalPercent = computed(() => {
    const goal = this.goal();
    if (!goal) return 100;
    return Math.min(100, Math.round((this.balance() / goal.cost) * 100));
  });

  visibleRewards = computed(() => {
    const balance = this.balance();
    // A requested reward has its points held back, so it can look unaffordable.
    const requested = new Set(this.pendingRequests().map((r) => r.reward_id));
    switch (this.filter()) {
      case 'now':
        return this.rewards().filter((r) => r.cost <= balance || requested.has(r.id));
      case 'saving':
        return this.rewards().filter((r) => r.cost > balance && !requested.has(r.id));
      default:
        return this.rewards();
    }
  });

  ngOnInit(): void {
    if (this.auth.isChild()) this.load();
  }

  load(): void {
    if (this.loadState() !== 'ready') this.loadState.set('loading');
    this.loadError.set(null);
    forkJoin({
      rewards: this.api.list(),
      points: this.api.myPoints(),
      requests: this.api.redemptionPage({ limit: RECENT_REQUESTS }),
      pending: this.api.redemptionPage({ status: 'pending', limit: PENDING_LIMIT }),
    })
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: ({ rewards, points, requests, pending }) => {
          this.rewards.set(rewards.rewards);
          this.points.set(points);
          this.requests.set(requests.redemptions);
          this.pendingRequests.set(pending.redemptions);
          this.loadState.set('ready');
        },
        error: (err: ApiError) => {
          this.loadError.set(err);
          this.loadState.set('error');
        },
      });
  }

  /** The child's pending request for this reward, if any. */
  pendingFor(rewardId: string): RedemptionListItem | undefined {
    return this.pendingRequests().find((r) => r.reward_id === rewardId);
  }

  request(reward: Reward): void {
    if (this.busyRewardId()) return;
    this.busyRewardId.set(reward.id);
    this.actionError.set(null);
    this.api
      .redeem(reward.id, newIdempotencyKey())
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => {
          this.busyRewardId.set(null);
          this.showNotice({ key: 'rewards.request.sent' });
          this.load();
        },
        error: (err: unknown) => {
          this.busyRewardId.set(null);
          this.actionError.set(this.errorKey(err));
          this.load();
        },
      });
  }

  openCancel(item: RedemptionListItem): void {
    this.cancelTarget.set(item);
  }

  closeCancel(): void {
    if (!this.cancelBusy()) this.cancelTarget.set(null);
  }

  confirmCancel(): void {
    const target = this.cancelTarget();
    if (!target || this.cancelBusy()) return;
    this.cancelBusy.set(true);
    this.actionError.set(null);
    this.api
      .cancel(target.id, target.version)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => {
          this.cancelBusy.set(false);
          this.cancelTarget.set(null);
          this.showNotice({ key: 'rewards.cancel.done', params: { count: target.cost } });
          this.load();
        },
        error: (err: unknown) => {
          this.cancelBusy.set(false);
          this.cancelTarget.set(null);
          this.actionError.set(this.errorKey(err));
          this.load();
        },
      });
  }

  dismissNotice(): void {
    this.notice.set(null);
  }

  private showNotice(notice: Notice): void {
    this.notice.set(notice);
    setTimeout(() => {
      if (this.notice() === notice) this.notice.set(null);
    }, 3200);
  }

  /** Maps an API error code to an i18n key. Unknown codes use the generic message. */
  private errorKey(err: unknown): string {
    const code = (err as { code?: string } | null)?.code ?? '';
    switch (code) {
      case 'insufficient_points':
      case 'outside_window':
      case 'limit_reached':
      case 'reward_unavailable':
      case 'not_pending':
        return `rewards.error.${code}`;
      default:
        return 'rewards.error.generic';
    }
  }
}
