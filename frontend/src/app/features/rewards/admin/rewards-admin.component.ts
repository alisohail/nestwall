import { Component, DestroyRef, OnInit, computed, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { TranslocoModule, TranslocoService } from '@ngneat/transloco';
import { FamilyService, MemberDetail } from '../../family/family.service';
import { ApiError } from '../../../core/api/api-client.service';
import { AvatarComponent } from '../../../shared/avatar/avatar.component';
import { BannerComponent } from '../../../shared/ui/banner/banner.component';
import { EmptyStateComponent } from '../../../shared/ui/empty-state/empty-state.component';
import { LoadErrorComponent } from '../../../shared/ui/load-error/load-error.component';
import { LoadingComponent } from '../../../shared/ui/loading/loading.component';
import { LimitPeriod, Reward, RewardInput, RewardStatus } from '../rewards.models';
import { RewardsService } from '../rewards.service';

type LoadState = 'loading' | 'ready' | 'error';
type RewardSort = 'name' | 'points-asc' | 'points-desc';

/** Editable fields as the form holds them. Numbers may be empty while typing. */
interface Draft {
  name: string;
  icon: string;
  cost: number | null;
  description: string;
  status: RewardStatus;
  approval: boolean;
  limitPeriod: LimitPeriod | '';
  limitCount: number | null;
  from: string;
  until: string;
  days: string[];
  eligible: string[];
}

const DAY_KEYS = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'];
const MAX_ICON = 16;
const MAX_POINTS = 100000;
const MAX_LIMIT = 1000;

@Component({
  selector: 'app-rewards-admin',
  standalone: true,
  imports: [
    FormsModule,
    TranslocoModule,
    AvatarComponent,
    BannerComponent,
    EmptyStateComponent,
    LoadErrorComponent,
    LoadingComponent,
  ],
  templateUrl: './rewards-admin.component.html',
  styleUrl: './rewards-admin.component.scss',
})
export class RewardsAdminComponent implements OnInit {
  private api = inject(RewardsService);
  private family = inject(FamilyService);
  private transloco = inject(TranslocoService);
  private destroyRef = inject(DestroyRef);

  readonly dayKeys = DAY_KEYS;
  readonly statuses: RewardStatus[] = ['active', 'inactive'];
  readonly periods: LimitPeriod[] = ['day', 'week', 'total'];
  readonly sorts: RewardSort[] = ['name', 'points-asc', 'points-desc'];

  loadState = signal<LoadState>('loading');
  loadError = signal<ApiError | null>(null);
  rewards = signal<Reward[]>([]);
  sort = signal<RewardSort>('name');
  sortedRewards = computed(() => {
    const list = [...this.rewards()];
    switch (this.sort()) {
      case 'points-asc':
        return list.sort((a, b) => a.cost - b.cost || a.name.localeCompare(b.name));
      case 'points-desc':
        return list.sort((a, b) => b.cost - a.cost || a.name.localeCompare(b.name));
      default:
        return list.sort((a, b) => a.name.localeCompare(b.name));
    }
  });
  children = signal<MemberDetail[]>([]);

  /** True while the form is open. formTarget is the reward being edited, or null for a new one. */
  formOpen = signal(false);
  formTarget = signal<Reward | null>(null);
  formError = signal<string | null>(null);
  /** Blocks a second tap while a save or delete is in flight. */
  busy = signal(false);
  notice = signal<string | null>(null);
  /** Reward waiting for the delete confirm on its card. */
  deleteTarget = signal<Reward | null>(null);
  deleteError = signal<string | null>(null);

  /** Family default that a new reward's approval toggle starts from. Loaded once in ngOnInit. */
  private approvalDefault = false;
  draft: Draft = this.emptyDraft();

  ngOnInit(): void {
    this.load();
    this.api
      .settings()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (s) => (this.approvalDefault = s.approval_required_default),
        error: () => (this.approvalDefault = false),
      });
    this.family
      .listMembers()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: ({ members }) => this.children.set(members.filter((m) => m.is_child && m.status === 'active')),
        error: () => this.children.set([]),
      });
  }

  load(): void {
    if (this.loadState() !== 'ready') this.loadState.set('loading');
    this.loadError.set(null);
    this.api
      .list()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: ({ rewards }) => {
          this.rewards.set(rewards.filter((r) => !r.archived));
          this.loadState.set('ready');
        },
        error: (err: ApiError) => {
          this.loadError.set(err);
          this.loadState.set('error');
        },
      });
  }

  openNew(): void {
    this.open(null);
  }

  openEdit(reward: Reward): void {
    this.open(reward);
  }

  closeForm(): void {
    if (!this.busy()) this.formOpen.set(false);
  }

  save(): void {
    if (this.busy()) return;
    const key = this.validationKey();
    if (key) {
      this.formError.set(key);
      return;
    }
    const target = this.formTarget();
    const input = this.toInput();
    this.busy.set(true);
    this.formError.set(null);
    const request = target ? this.api.update(target.id, input, target.version) : this.api.create(input);
    request.pipe(takeUntilDestroyed(this.destroyRef)).subscribe({
      next: () => {
        this.busy.set(false);
        this.formOpen.set(false);
        this.notice.set(target ? 'rewards.admin.saved' : 'rewards.admin.created');
        this.load();
      },
      error: (err: unknown) => {
        this.busy.set(false);
        this.formError.set(this.errorKey(err));
        if (this.errorCode(err) === 'version_conflict') this.load();
      },
    });
  }

  askDelete(reward: Reward): void {
    this.deleteError.set(null);
    this.deleteTarget.set(reward);
  }

  cancelDelete(): void {
    if (!this.busy()) this.deleteTarget.set(null);
  }

  /** Archives the reward from the card. Past requests keep their history. */
  confirmDelete(): void {
    const target = this.deleteTarget();
    if (!target || this.busy()) return;
    this.busy.set(true);
    this.deleteError.set(null);
    this.api
      .archive(target.id, target.version)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => {
          this.busy.set(false);
          this.deleteTarget.set(null);
          this.notice.set('rewards.admin.deleted');
          this.load();
        },
        error: (err: unknown) => {
          this.busy.set(false);
          const stale = this.errorCode(err) === 'version_conflict';
          this.deleteError.set(this.errorKey(err));
          if (stale) {
            this.deleteTarget.set(null);
            this.load();
          }
        },
      });
  }

  setField<K extends 'name' | 'icon' | 'description'>(key: K, value: string): void {
    this.draft[key] = value;
  }

  toggleDay(day: string): void {
    this.draft.days = this.draft.days.includes(day)
      ? this.draft.days.filter((d) => d !== day)
      : [...this.draft.days, day];
  }

  isDayOn(day: string): boolean {
    return this.draft.days.includes(day);
  }

  toggleChild(id: string): void {
    this.draft.eligible = this.draft.eligible.includes(id)
      ? this.draft.eligible.filter((c) => c !== id)
      : [...this.draft.eligible, id];
  }

  isChildOn(id: string): boolean {
    return this.draft.eligible.includes(id);
  }

  setSort(event: Event): void {
    this.sort.set((event.target as HTMLSelectElement).value as RewardSort);
  }

  statusBadge(reward: Reward): { key: string; cls: string } {
    if (reward.status === 'inactive') return { key: 'rewards.admin.status.paused', cls: '' };
    if (reward.approval_required) return { key: 'rewards.admin.status.approval', cls: 'amber' };
    return { key: 'rewards.admin.status.active', cls: 'ok' };
  }

  /** A short summary of the limit, or an empty string when there is none. */
  limitText(reward: Reward): string {
    if (!reward.limit) return '';
    return this.transloco.translate(`rewards.admin.limit.${reward.limit.period}`, { count: reward.limit.count });
  }

  daysText(reward: Reward): string {
    if (!reward.available_days || reward.available_days.length === 0) {
      return this.transloco.translate('rewards.admin.days.every');
    }
    return reward.available_days.map((d) => this.transloco.translate(`rewards.admin.days.${d}`)).join(', ');
  }

  childrenText(reward: Reward): string {
    if (reward.eligible_member_ids.length === 0) return this.transloco.translate('rewards.admin.eligible.all');
    const names = reward.eligible_member_ids
      .map((id) => this.children().find((c) => c.id === id)?.display_name)
      .filter((n): n is string => !!n);
    return names.length ? names.join(', ') : this.transloco.translate('rewards.admin.eligible.all');
  }

  /** Returns the i18n key of the first invalid field, or null when the form can be saved. */
  validationKey(): string | null {
    const d = this.draft;
    if (!d.name.trim()) return 'rewards.admin.error.name';
    const icon = d.icon.trim();
    if (!icon || [...icon].length > MAX_ICON) return 'rewards.admin.error.icon';
    if (!Number.isInteger(d.cost) || (d.cost ?? 0) < 1 || (d.cost ?? 0) > MAX_POINTS) return 'rewards.admin.error.cost';
    if (d.limitPeriod) {
      if (!Number.isInteger(d.limitCount) || (d.limitCount ?? 0) < 1 || (d.limitCount ?? 0) > MAX_LIMIT) {
        return 'rewards.admin.error.limit';
      }
    }
    if (d.from && d.until && d.from > d.until) return 'rewards.admin.error.window';
    return null;
  }

  private open(reward: Reward | null): void {
    this.formTarget.set(reward);
    this.formError.set(null);
    this.draft = reward ? this.draftFrom(reward) : this.emptyDraft();
    this.formOpen.set(true);
  }

  private emptyDraft(): Draft {
    return {
      name: '',
      icon: '🎁',
      cost: 10,
      description: '',
      status: 'active',
      approval: this.approvalDefault,
      limitPeriod: '',
      limitCount: 1,
      from: '',
      until: '',
      days: [],
      eligible: [],
    };
  }

  private draftFrom(r: Reward): Draft {
    return {
      name: r.name,
      icon: r.icon,
      cost: r.cost,
      description: r.description ?? '',
      status: r.status,
      approval: r.approval_required,
      limitPeriod: r.limit?.period ?? '',
      limitCount: r.limit?.count ?? 1,
      from: r.available_from ?? '',
      until: r.available_until ?? '',
      days: [...(r.available_days ?? [])],
      eligible: [...r.eligible_member_ids],
    };
  }

  /** Builds the request body. Every day selected, or none, means every day, so it is sent as null. */
  private toInput(): RewardInput {
    const d = this.draft;
    const days = d.days.length > 0 && d.days.length < DAY_KEYS.length ? DAY_KEYS.filter((k) => d.days.includes(k)) : null;
    return {
      name: d.name.trim(),
      description: d.description.trim() || null,
      icon: d.icon.trim(),
      cost: d.cost ?? 0,
      status: d.status,
      limit_period: d.limitPeriod || null,
      limit_count: d.limitPeriod ? d.limitCount : null,
      approval_required: d.approval,
      available_from: d.from || null,
      available_until: d.until || null,
      available_days: days,
      eligible_member_ids: [...d.eligible],
    };
  }

  private errorCode(err: unknown): string {
    return (err as { code?: string } | null)?.code ?? '';
  }

  private errorKey(err: unknown): string {
    const code = this.errorCode(err);
    if (code === 'version_conflict') return 'rewards.admin.error.stale';
    if (code === 'validation_failed') return 'rewards.admin.error.invalid';
    return 'rewards.admin.error.generic';
  }
}
