import { Component, DestroyRef, OnInit, computed, inject, signal } from '@angular/core';
import { DatePipe } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { TranslocoModule } from '@ngneat/transloco';
import { Subscription, finalize } from 'rxjs';
import { ApiError } from '../../core/api/api-client.service';
import { AuthService } from '../../core/auth/auth.service';
import { BADGE_NOTE_MAX, CustomBadge } from '../badges/badges.models';
import { BadgesService } from '../badges/badges.service';
import { badgeErrorKey } from '../badges/badge-errors';
import { BannerComponent } from '../../shared/ui/banner/banner.component';
import { EmptyStateComponent } from '../../shared/ui/empty-state/empty-state.component';
import { ErrorStateComponent } from '../../shared/ui/error-state/error-state.component';
import { LoadingComponent } from '../../shared/ui/loading/loading.component';
import { SkeletonComponent } from '../../shared/ui/skeleton/skeleton.component';
import { FamilyService, MemberDetail } from '../family/family.service';
import { AvatarComponent } from '../../shared/avatar/avatar.component';
import { newIdempotencyKey } from '../rewards/idempotency-key';
import { ManualPointsType, MemberBalance, PointsEntry, PointsEntryType, Reward } from '../rewards/rewards.models';
import { RewardsService } from '../rewards/rewards.service';

type LoadState = 'loading' | 'ready' | 'error';

/** What a parent sees for one child: balance, points held, and the reward they are saving for. */
interface BalanceCard {
  id: string;
  name: string;
  colour: string;
  avatarId: string | null;
  balance: number;
  reserved: number;
  percent: number;
  goal: Reward | null;
}

const ENTRY_TYPES: PointsEntryType[] = [
  'task_award',
  'bonus_task_award',
  'bonus',
  'penalty',
  'redemption',
  'redemption_reversal',
  'reversal',
  'adjustment',
  'transfer_out',
  'transfer_in',
];
const MANUAL_TYPES: ManualPointsType[] = ['bonus', 'penalty', 'adjustment'];
const PAGE_SIZE = 50;
const MAX_POINTS = 100000;
const MAX_REASON = 200;

@Component({
  selector: 'app-points',
  standalone: true,
  imports: [
    AvatarComponent,
    DatePipe,
    FormsModule,
    TranslocoModule,
    BannerComponent,
    EmptyStateComponent,
    ErrorStateComponent,
    LoadingComponent,
    SkeletonComponent,
  ],
  templateUrl: './points.component.html',
  styleUrl: './points.component.scss',
})
export class PointsComponent implements OnInit {
  private api = inject(RewardsService);
  private family = inject(FamilyService);
  private auth = inject(AuthService);
  private badges = inject(BadgesService);
  private destroyRef = inject(DestroyRef);
  private inflight?: Subscription;

  readonly entryTypes = ENTRY_TYPES;
  readonly manualTypes = MANUAL_TYPES;
  readonly noteMax = BADGE_NOTE_MAX;
  /** Parents see the whole family, with filters and actions. Children see only their own entries. */
  readonly isParent = computed(() => !this.auth.isChild());

  loadState = signal<LoadState>('loading');
  entries = signal<PointsEntry[]>([]);
  nextCursor = signal<string | null>(null);
  loadingMore = signal(false);
  children = signal<MemberDetail[]>([]);
  balances = signal<MemberBalance[]>([]);
  rewards = signal<Reward[]>([]);
  balanceError = signal(false);
  memberFilter = signal('');
  typeFilter = signal<PointsEntryType | ''>('');
  notice = signal<string | null>(null);

  /** One card per active child. The goal is the cheapest active reward the child can see that is still out of reach. */
  readonly balanceCards = computed<BalanceCard[]>(() => {
    const byMember = new Map(this.balances().map((b) => [b.member_id, b]));
    const open = this.rewards().filter((r) => r.status === 'active' && !r.archived);
    return this.children().map((child) => {
      const found = byMember.get(child.id);
      const balance = found?.balance ?? 0;
      const goal =
        open
          .filter(
            (r) =>
              r.cost > Math.max(balance, 0) &&
              (r.eligible_member_ids.length === 0 || r.eligible_member_ids.includes(child.id)),
          )
          .sort((a, b) => a.cost - b.cost)[0] ?? null;
      return {
        id: child.id,
        name: child.display_name,
        colour: child.colour,
        avatarId: child.avatar_id,
        balance,
        reserved: found?.reserved ?? 0,
        percent: goal ? Math.min(100, Math.max(0, Math.round((balance / goal.cost) * 100))) : 100,
        goal,
      };
    });
  });

  // Bonus, penalty or correction form.
  manualOpen = signal(false);
  manualType = signal<ManualPointsType>('bonus');
  manualMember = signal('');
  manualError = signal<string | null>(null);
  manualBusy = signal(false);
  manualPoints: number | null = 10;
  manualReason = '';
  private manualKey = newIdempotencyKey();

  // Award a badge by hand. The badge list is loaded when the dialog opens.
  awardOpen = signal(false);
  awardBadges = signal<CustomBadge[]>([]);
  awardBadgesLoading = signal(false);
  awardBadgesError = signal<string | null>(null);
  awardMemberId: string | null = null;
  awardBadgeId: string | null = null;
  awardNote = '';
  awardSubmitted = signal(false);
  awardBusy = signal(false);
  awardError = signal<string | null>(null);
  private awardKey = newIdempotencyKey();

  // Reverse a penalty.
  reverseTarget = signal<PointsEntry | null>(null);
  reverseError = signal<string | null>(null);
  reverseBusy = signal(false);
  reverseReason = '';

  ngOnInit(): void {
    if (this.isParent()) {
      this.family
        .listMembers()
        .pipe(takeUntilDestroyed(this.destroyRef))
        .subscribe({
          next: ({ members }) => this.children.set(members.filter((m) => m.is_child && m.status === 'active')),
          error: () => this.children.set([]),
        });
      this.loadBalances();
      this.loadRewards();
    }
    this.load();
  }

  /** Reloads each child's balance. Called after a change, since balances move with the ledger. */
  loadBalances(): void {
    this.api
      .memberBalances()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => {
          this.balances.set(res.members);
          this.balanceError.set(false);
        },
        error: () => this.balanceError.set(true),
      });
  }

  private loadRewards(): void {
    this.api
      .list()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => this.rewards.set(res.rewards),
        error: () => this.balanceError.set(true),
      });
  }

  /** Reloads the first page with the current filters. A newer call cancels an older one still in flight. */
  load(): void {
    this.inflight?.unsubscribe();
    if (this.entries().length === 0) this.loadState.set('loading');
    this.inflight = this.api
      .entryPage(this.query())
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (page) => {
          this.entries.set(page.entries);
          this.nextCursor.set(page.next_cursor);
          this.loadState.set('ready');
        },
        error: () => this.loadState.set('error'),
      });
  }

  loadMore(): void {
    const cursor = this.nextCursor();
    if (!cursor || this.loadingMore()) return;
    this.loadingMore.set(true);
    this.api
      .entryPage({ ...this.query(), cursor })
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (page) => {
          this.entries.update((list) => [...list, ...page.entries]);
          this.nextCursor.set(page.next_cursor);
          this.loadingMore.set(false);
        },
        error: () => {
          this.loadingMore.set(false);
          this.notice.set('points.error.more');
        },
      });
  }

  setMember(id: string): void {
    if (this.memberFilter() === id) return;
    this.memberFilter.set(id);
    this.load();
  }

  setType(value: string): void {
    this.typeFilter.set(value as PointsEntryType | '');
    this.load();
  }

  memberName(id: string): string {
    return this.member(id)?.display_name ?? '';
  }

  member(id: string): MemberDetail | undefined {
    return this.children().find((c) => c.id === id);
  }

  amountText(entry: PointsEntry): string {
    return entry.amount > 0 ? `+${entry.amount}` : `${entry.amount}`;
  }

  /** Tone for the type badge. Gains use the ok tone, losses the err tone. */
  typeTone(type: PointsEntryType): string {
    switch (type) {
      case 'task_award':
      case 'bonus_task_award':
        return 'badge ok';
      case 'bonus':
        return 'badge amber';
      case 'penalty':
        return 'badge err';
      case 'redemption':
      case 'redemption_reversal':
      case 'transfer_in':
      case 'transfer_out':
        return 'badge teal';
      default:
        return 'badge';
    }
  }

  canReverse(entry: PointsEntry): boolean {
    return this.isParent() && entry.type === 'penalty';
  }

  openManual(): void {
    this.manualType.set('bonus');
    this.manualMember.set(this.children()[0]?.id ?? '');
    this.manualPoints = 10;
    this.manualReason = '';
    this.manualError.set(null);
    this.manualKey = newIdempotencyKey();
    this.manualOpen.set(true);
  }

  closeManual(): void {
    if (!this.manualBusy()) this.manualOpen.set(false);
  }

  setManualType(type: ManualPointsType): void {
    this.manualType.set(type);
  }

  /** Returns the i18n key of the hint under the points field. */
  manualHint(): string {
    return this.manualType() === 'adjustment' ? 'points.manual.hint.adjustment' : 'points.manual.hint.signed';
  }

  submitManual(): void {
    if (this.manualBusy()) return;
    const key = this.manualValidationKey();
    if (key) {
      this.manualError.set(key);
      return;
    }
    this.manualBusy.set(true);
    this.manualError.set(null);
    this.api
      .postPoints(
        this.manualMember(),
        { type: this.manualType(), points: Number(this.manualPoints), reason: this.manualReason.trim() },
        this.manualKey,
      )
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => {
          this.manualBusy.set(false);
          this.manualOpen.set(false);
          this.notice.set(res.replayed ? 'points.notice.replayed' : 'points.notice.saved');
          this.load();
          this.loadBalances();
        },
        error: (err: unknown) => {
          this.manualBusy.set(false);
          this.manualError.set(this.manualErrorKey(err));
          // A coded error ends this attempt. A new key stops a changed form from reusing the old one.
          if (this.errorCode(err)) this.manualKey = newIdempotencyKey();
        },
      });
  }

  openAward(): void {
    this.awardMemberId = null;
    this.awardBadgeId = null;
    this.awardNote = '';
    this.awardKey = newIdempotencyKey();
    this.awardSubmitted.set(false);
    this.awardError.set(null);
    this.awardBadgesError.set(null);
    this.awardBadges.set([]);
    this.awardBadgesLoading.set(true);
    this.awardOpen.set(true);
    this.badges
      .get()
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => this.awardBadgesLoading.set(false)),
      )
      .subscribe({
        next: (config) => this.awardBadges.set(config.custom.filter((c) => c.enabled)),
        error: (err: ApiError) => this.awardBadgesError.set(badgeErrorKey(err)),
      });
  }

  closeAward(): void {
    if (!this.awardBusy()) this.awardOpen.set(false);
  }

  submitAward(): void {
    if (this.awardBusy()) return;
    this.awardSubmitted.set(true);
    this.awardError.set(null);
    const memberId = this.awardMemberId;
    const badgeId = this.awardBadgeId;
    const note = this.awardNote.trim();
    if (!memberId || !badgeId) {
      this.awardError.set(!memberId ? 'settings.badges.error.child' : 'settings.badges.error.badge');
      return;
    }
    if (note === '' || [...note].length > BADGE_NOTE_MAX) {
      this.awardError.set('settings.badges.error.note');
      return;
    }
    this.awardBusy.set(true);
    this.badges
      .award({ member_id: memberId, badge_id: badgeId, note }, this.awardKey)
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => this.awardBusy.set(false)),
      )
      .subscribe({
        next: () => this.awardOpen.set(false),
        error: (err: ApiError) => this.awardError.set(badgeErrorKey(err)),
      });
  }

  askReverse(entry: PointsEntry): void {
    this.reverseTarget.set(entry);
    this.reverseReason = '';
    this.reverseError.set(null);
  }

  closeReverse(): void {
    if (!this.reverseBusy()) this.reverseTarget.set(null);
  }

  confirmReverse(): void {
    const target = this.reverseTarget();
    if (!target || this.reverseBusy()) return;
    const reason = this.reverseReason.trim();
    if (!reason || [...reason].length > MAX_REASON) {
      this.reverseError.set('points.error.reason');
      return;
    }
    this.reverseBusy.set(true);
    this.reverseError.set(null);
    this.api
      .reversePenalty(target.id, reason)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => {
          this.reverseBusy.set(false);
          this.reverseTarget.set(null);
          this.notice.set('points.notice.reversed');
          this.load();
          this.loadBalances();
        },
        error: (err: unknown) => {
          this.reverseBusy.set(false);
          this.reverseError.set(this.reverseErrorKey(err));
        },
      });
  }

  private query() {
    return {
      member_id: this.isParent() && this.memberFilter() ? this.memberFilter() : undefined,
      type: this.typeFilter() || undefined,
      limit: PAGE_SIZE,
    };
  }

  /** Returns the i18n key of the first invalid field, or null when the form can be sent. */
  private manualValidationKey(): string | null {
    if (!this.manualMember()) return 'points.error.member';
    const points = Number(this.manualPoints ?? 0);
    if (!Number.isInteger(points)) return 'points.error.points';
    if (this.manualType() === 'adjustment') {
      if (points === 0 || Math.abs(points) > MAX_POINTS) return 'points.error.adjust';
    } else if (points < 1 || points > MAX_POINTS) {
      return 'points.error.points';
    }
    const reason = this.manualReason.trim();
    if (!reason || [...reason].length > MAX_REASON) return 'points.error.reason';
    return null;
  }

  private errorCode(err: unknown): string {
    return (err as { code?: string } | null)?.code ?? '';
  }

  private manualErrorKey(err: unknown): string {
    switch (this.errorCode(err)) {
      case 'member_not_found':
        return 'points.error.memberNotFound';
      case 'nothing_to_penalise':
        return 'points.error.nothing';
      case 'idempotency_conflict':
        return 'points.error.conflict';
      case 'validation_failed':
        return 'points.error.invalid';
      default:
        return 'points.error.generic';
    }
  }

  private reverseErrorKey(err: unknown): string {
    switch (this.errorCode(err)) {
      case 'already_reversed':
        return 'points.error.alreadyReversed';
      case 'not_a_penalty':
        return 'points.error.notPenalty';
      case 'entry_not_found':
        return 'points.error.notFound';
      default:
        return 'points.error.generic';
    }
  }
}
