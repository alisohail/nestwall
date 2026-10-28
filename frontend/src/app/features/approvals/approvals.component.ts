import { Component, DestroyRef, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormsModule } from '@angular/forms';
import { TranslocoModule } from '@ngneat/transloco';
import { Observable, catchError, concatMap, from, map, of, toArray } from 'rxjs';
import { ApiError } from '../../core/api/api-client.service';
import { AvatarComponent } from '../../shared/avatar/avatar.component';
import { BannerComponent } from '../../shared/ui/banner/banner.component';
import { EmptyStateComponent } from '../../shared/ui/empty-state/empty-state.component';
import { ErrorStateComponent } from '../../shared/ui/error-state/error-state.component';
import { IconComponent } from '../../shared/ui/icon/icon.component';
import { LoadingComponent } from '../../shared/ui/loading/loading.component';
import { SkeletonComponent } from '../../shared/ui/skeleton/skeleton.component';
import { BonusClaim, BonusTask } from '../bonus/bonus.models';
import { TimeFormatService } from '../../core/time-format/time-format.service';
import { BonusService } from '../bonus/bonus.service';
import { FamilyService, MemberDetail } from '../family/family.service';
import { RedemptionListItem } from '../rewards/rewards.models';
import { RewardsService } from '../rewards/rewards.service';
import { TaskListItem } from '../tasks/tasks.models';
import { TasksService } from '../tasks/tasks.service';

export type ApprovalTab = 'tasks' | 'requests' | 'history';
type LoadState = 'loading' | 'ready' | 'error';

/** A translation key with its parameters, shown in the notice banner. */
export interface ApprovalNotice {
  key: string;
  params?: Record<string, string | number>;
}

/** A submitted bonus claim with the task it belongs to. */
export interface BonusReviewItem {
  task: BonusTask;
  claim: BonusClaim;
}

/** Upper bound of one review list. The API caps list pages at 200. */
const LIST_LIMIT = 200;
const HISTORY_PAGE = 20;
/** Matches the server limits: a send-back note up to 500 characters, a decline reason up to 200. */
const NOTE_MAX = 500;
const REASON_MAX = 200;
/** A bonus send-back note is optional and up to 200 characters. */
const BONUS_NOTE_MAX = 200;

/** Parent review queue: task completions, reward requests and the decision history. */
@Component({
  selector: 'app-approvals',
  standalone: true,
  imports: [
    FormsModule,
    TranslocoModule,
    AvatarComponent,
    BannerComponent,
    EmptyStateComponent,
    ErrorStateComponent,
    IconComponent,
    LoadingComponent,
    SkeletonComponent,
  ],
  templateUrl: './approvals.component.html',
  styleUrl: './approvals.component.scss',
})
export class ApprovalsComponent {
  private tasksApi = inject(TasksService);
  private bonusApi = inject(BonusService);
  private rewardsApi = inject(RewardsService);
  private familyApi = inject(FamilyService);
  private destroyRef = inject(DestroyRef);
  private timeFormat = inject(TimeFormatService);

  readonly maxNote = NOTE_MAX;
  readonly maxReason = REASON_MAX;

  readonly tab = signal<ApprovalTab>('tasks');
  readonly members = signal<MemberDetail[]>([]);
  private memberById = computed(() => new Map(this.members().map((m) => [m.id, m])));

  readonly tasks = signal<TaskListItem[]>([]);
  readonly tasksState = signal<LoadState>('loading');
  readonly tasksHasMore = signal(false);

  readonly bonusItems = signal<BonusReviewItem[]>([]);
  readonly bonusState = signal<LoadState>('loading');
  /** Count on the Tasks tab: regular completions plus submitted bonus claims. */
  readonly reviewCount = computed(() => this.tasks().length + this.bonusItems().length);

  /** Person chosen in the filter chips. Empty means everyone. */
  readonly memberFilter = signal('');
  readonly activeMembers = computed(() => this.members().filter((m) => m.status === 'active'));
  readonly visibleTasks = computed(() => {
    const id = this.memberFilter();
    return id ? this.tasks().filter((item) => item.member_id === id) : this.tasks();
  });
  readonly visibleBonus = computed(() => {
    const id = this.memberFilter();
    return id ? this.bonusItems().filter((item) => item.claim.family_member_id === id) : this.bonusItems();
  });
  /** Items the list and Approve all act on, after the person filter. */
  readonly visibleCount = computed(() => this.visibleTasks().length + this.visibleBonus().length);

  readonly requests = signal<RedemptionListItem[]>([]);
  readonly requestsState = signal<LoadState>('loading');

  readonly history = signal<RedemptionListItem[]>([]);
  readonly historyState = signal<LoadState>('loading');
  readonly historyCursor = signal<string | null>(null);
  readonly historyMoreBusy = signal(false);

  /** Key of the single row or dialog action in progress. Blocks a second send of the same request. */
  readonly busy = signal<string | null>(null);
  /** True while a batch of task approvals runs. */
  readonly bulkBusy = signal(false);
  readonly notice = signal<ApprovalNotice | null>(null);
  readonly error = signal<string | null>(null);

  readonly confirmAllOpen = signal(false);
  readonly sendBackFor = signal<TaskListItem | null>(null);
  readonly sendBackBonusFor = signal<BonusReviewItem | null>(null);
  /** Title shown in the send-back dialog, for either kind of item. */
  readonly sendBackTitle = computed(() => this.sendBackFor()?.title ?? this.sendBackBonusFor()?.task.title ?? null);
  readonly sendBackMax = computed(() => (this.sendBackBonusFor() ? BONUS_NOTE_MAX : this.maxNote));
  readonly sendBackNote = signal('');
  readonly sendBackError = signal<string | null>(null);
  readonly declineFor = signal<RedemptionListItem | null>(null);
  readonly declineReason = signal('');
  readonly declineError = signal<string | null>(null);

  /** Task actions are off while any approval or send-back is running. */
  readonly actionsDisabled = computed(() => this.busy() !== null || this.bulkBusy());
  /** Approve all needs the full queue on screen; a partial batch would leave some copies behind. */
  readonly canApproveAll = computed(
    () =>
      this.visibleCount() > 0 &&
      !this.tasksHasMore() &&
      !this.actionsDisabled() &&
      this.tasksState() === 'ready' &&
      this.bonusState() === 'ready',
  );

  constructor() {
    this.familyApi
      .listMembers()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => this.members.set(res.members),
        error: () => this.members.set([]),
      });
    this.loadTasks();
    this.loadBonus();
    this.loadRequests();
    this.loadHistory(true);
  }

  setTab(tab: ApprovalTab): void {
    this.tab.set(tab);
  }

  memberOf(id: string): MemberDetail | undefined {
    return this.memberById().get(id);
  }

  memberName(id: string): string {
    return this.memberOf(id)?.display_name ?? '';
  }

  /** Stable key for a task copy: each member has their own copy of a task. */
  taskKey(item: TaskListItem): string {
    return `task:${item.id}:${item.member_id}`;
  }

  setMember(id: string): void {
    this.memberFilter.set(id);
    this.loadRequests();
    this.loadHistory(true);
  }

  checklistDone(item: TaskListItem): number {
    return (item.checklist ?? []).filter((c) => c.done).length;
  }

  bonusKey(item: BonusReviewItem): string {
    return `bonus:${item.claim.id}`;
  }

  /** Loads the bonus claims submitted today. Only submitted claims need a decision. */
  loadBonus(): void {
    if (this.bonusState() !== 'ready') this.bonusState.set('loading');
    this.bonusApi
      .list('today')
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => {
          this.bonusItems.set(
            res.tasks.flatMap((task) =>
              (task.claims ?? []).filter((claim) => claim.status === 'submitted').map((claim) => ({ task, claim })),
            ),
          );
          this.bonusState.set('ready');
        },
        error: () => this.bonusState.set('error'),
      });
  }

  requestKey(item: RedemptionListItem): string {
    return `request:${item.id}`;
  }

  /** Loads the open task completions. Existing rows stay on screen while a refresh runs. */
  loadTasks(): void {
    if (this.tasksState() !== 'ready') this.tasksState.set('loading');
    this.tasksApi
      .list({ status: 'submitted', limit: LIST_LIMIT })
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => {
          this.tasks.set(res.tasks);
          this.tasksHasMore.set(res.next_cursor !== null);
          this.tasksState.set('ready');
        },
        error: () => this.tasksState.set('error'),
      });
  }

  /** Loads the pending reward requests for the person chosen in the filter. */
  loadRequests(): void {
    if (this.requestsState() !== 'ready') this.requestsState.set('loading');
    this.rewardsApi
      .redemptionPage({ status: 'pending', limit: LIST_LIMIT, member_id: this.memberFilter() || undefined })
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (page) => {
          this.requests.set(page.redemptions);
          this.requestsState.set('ready');
        },
        error: () => this.requestsState.set('error'),
      });
  }

  /** Loads the decided reward requests. reset starts again from the newest; otherwise the next page is appended. */
  loadHistory(reset: boolean): void {
    if (reset) {
      if (this.historyState() !== 'ready') this.historyState.set('loading');
    } else {
      this.historyMoreBusy.set(true);
    }
    const cursor = reset ? undefined : (this.historyCursor() ?? undefined);
    this.rewardsApi
      .redemptionPage({
        status: 'approved,rejected',
        limit: HISTORY_PAGE,
        cursor,
        member_id: this.memberFilter() || undefined,
      })
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (page) => {
          this.history.set(reset ? page.redemptions : [...this.history(), ...page.redemptions]);
          this.historyCursor.set(page.next_cursor);
          this.historyState.set('ready');
          this.historyMoreBusy.set(false);
        },
        error: () => {
          this.historyMoreBusy.set(false);
          if (reset) this.historyState.set('error');
          else this.error.set('approvals.error.load');
        },
      });
  }

  approveTask(item: TaskListItem): void {
    if (this.actionsDisabled()) return;
    this.clearMessages();
    this.busy.set(this.taskKey(item));
    this.tasksApi
      .approve(item.id, item.member_id, item.version)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => {
          this.busy.set(null);
          this.notice.set({
            key: 'approvals.notice.taskApproved',
            params: { title: item.title },
          });
          this.loadTasks();
        },
        error: (err) => {
          this.busy.set(null);
          this.error.set(this.errorKey(err));
          if (this.isStale(err)) this.loadTasks();
        },
      });
  }

  /** Opens the send-back dialog for a task copy. */
  openSendBack(item: TaskListItem): void {
    this.sendBackBonusFor.set(null);
    this.sendBackFor.set(item);
    this.sendBackNote.set('');
    this.sendBackError.set(null);
  }

  /** Opens the send-back dialog for a bonus claim. The note is optional. */
  openSendBackBonus(item: BonusReviewItem): void {
    this.sendBackFor.set(null);
    this.sendBackBonusFor.set(item);
    this.sendBackNote.set('');
    this.sendBackError.set(null);
  }

  closeSendBack(): void {
    if (this.busy() !== null) return;
    this.sendBackFor.set(null);
    this.sendBackBonusFor.set(null);
  }

  /** Sends a task copy back with a required note. The child can try again. */
  submitSendBack(): void {
    const bonus = this.sendBackBonusFor();
    if (bonus) {
      this.submitBonusSendBack(bonus);
      return;
    }
    const item = this.sendBackFor();
    if (!item || this.busy() !== null) return;
    const note = this.sendBackNote().trim();
    if (!note) {
      this.sendBackError.set('approvals.error.noteRequired');
      return;
    }
    if (note.length > NOTE_MAX) {
      this.sendBackError.set('approvals.error.noteTooLong');
      return;
    }
    this.clearMessages();
    this.busy.set(this.taskKey(item));
    this.sendBackError.set(null);
    this.tasksApi
      .reject(item.id, item.member_id, note, item.version)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => {
          this.busy.set(null);
          this.sendBackFor.set(null);
          this.notice.set({
            key: 'approvals.notice.sentBack',
            params: { title: item.title },
          });
          this.loadTasks();
        },
        error: (err) => {
          this.busy.set(null);
          if (this.isStale(err)) {
            this.sendBackFor.set(null);
            this.error.set(this.errorKey(err));
            this.loadTasks();
          } else {
            this.sendBackError.set(this.errorKey(err));
          }
        },
      });
  }

  /** Approves a submitted bonus claim and pays its points. Approve all also covers bonus claims. */
  approveBonus(item: BonusReviewItem): void {
    if (this.actionsDisabled()) return;
    this.clearMessages();
    this.busy.set(this.bonusKey(item));
    this.bonusApi
      .approve(item.claim.id, item.claim.version)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => {
          this.busy.set(null);
          this.notice.set({
            key: 'approvals.notice.bonusApproved',
            params: {
              title: item.task.title,
              name: this.memberName(item.claim.family_member_id),
            },
          });
          this.loadBonus();
        },
        error: (err) => {
          this.busy.set(null);
          this.error.set(this.errorKey(err));
          if (this.isStale(err)) this.loadBonus();
        },
      });
  }

  /** Sends a bonus claim back. The note is optional, so the child can try again today. */
  private submitBonusSendBack(item: BonusReviewItem): void {
    if (this.busy() !== null) return;
    const note = this.sendBackNote().trim();
    if (note.length > BONUS_NOTE_MAX) {
      this.sendBackError.set('approvals.error.bonusNoteTooLong');
      return;
    }
    this.clearMessages();
    this.busy.set(this.bonusKey(item));
    this.sendBackError.set(null);
    this.bonusApi
      .reject(item.claim.id, item.claim.version, note)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => {
          this.busy.set(null);
          this.sendBackBonusFor.set(null);
          this.notice.set({
            key: 'approvals.notice.bonusSentBack',
            params: {
              title: item.task.title,
              name: this.memberName(item.claim.family_member_id),
            },
          });
          this.loadBonus();
        },
        error: (err) => {
          this.busy.set(null);
          if (this.isStale(err)) {
            this.sendBackBonusFor.set(null);
            this.error.set(this.errorKey(err));
            this.loadBonus();
          } else {
            this.sendBackError.set(this.errorKey(err));
          }
        },
      });
  }

  /** Approves every loaded task copy and bonus claim, one request at a time. Reward requests are never part of this. */
  approveAll(): void {
    const tasks = this.visibleTasks();
    const bonus = this.visibleBonus();
    if (!this.canApproveAll()) return;
    this.confirmAllOpen.set(false);
    this.clearMessages();
    this.bulkBusy.set(true);
    const jobs: (() => Observable<unknown>)[] = [
      ...tasks.map((item) => () => this.tasksApi.approve(item.id, item.member_id, item.version)),
      ...bonus.map((item) => () => this.bonusApi.approve(item.claim.id, item.claim.version)),
    ];
    from(jobs)
      .pipe(
        concatMap((job) =>
          job().pipe(
            map(() => true),
            catchError(() => of(false)),
          ),
        ),
        toArray(),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe((results) => {
        const approved = results.filter(Boolean).length;
        this.bulkBusy.set(false);
        this.notice.set({
          key: 'approvals.notice.allApproved',
          params: { count: approved },
        });
        if (approved < results.length) this.error.set('approvals.error.someFailed');
        this.loadTasks();
        this.loadBonus();
      });
  }

  approveRequest(item: RedemptionListItem): void {
    if (this.actionsDisabled() || this.busy() !== null) return;
    this.clearMessages();
    this.busy.set(this.requestKey(item));
    this.rewardsApi
      .approveRedemption(item.id, item.version)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => {
          this.busy.set(null);
          this.notice.set({
            key: 'approvals.notice.requestApproved',
            params: { name: item.reward_name },
          });
          this.loadRequests();
          this.loadHistory(true);
        },
        error: (err) => {
          this.busy.set(null);
          this.error.set(this.errorKey(err));
          if (this.isStale(err)) this.loadRequests();
        },
      });
  }

  openDecline(item: RedemptionListItem): void {
    this.declineFor.set(item);
    this.declineReason.set('');
    this.declineError.set(null);
  }

  closeDecline(): void {
    if (this.busy() !== null) return;
    this.declineFor.set(null);
  }

  /** Declines a reward request with a required reason. The points go back to the child. */
  submitDecline(): void {
    const item = this.declineFor();
    if (!item || this.busy() !== null) return;
    const reason = this.declineReason().trim();
    if (!reason) {
      this.declineError.set('approvals.error.reasonRequired');
      return;
    }
    if (reason.length > REASON_MAX) {
      this.declineError.set('approvals.error.reasonTooLong');
      return;
    }
    this.clearMessages();
    this.busy.set(this.requestKey(item));
    this.declineError.set(null);
    this.rewardsApi
      .rejectRedemption(item.id, reason, item.version)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => {
          this.busy.set(null);
          this.declineFor.set(null);
          this.notice.set({
            key: 'approvals.notice.requestDeclined',
            params: { name: item.reward_name },
          });
          this.loadRequests();
          this.loadHistory(true);
        },
        error: (err) => {
          this.busy.set(null);
          if (this.isStale(err)) {
            this.declineFor.set(null);
            this.error.set(this.errorKey(err));
            this.loadRequests();
          } else {
            this.declineError.set(this.errorKey(err));
          }
        },
      });
  }

  /** Short date and time such as "Fri 9 Oct, 5:00 pm" from an instant. */
  when(iso: string): string {
    return new Intl.DateTimeFormat(undefined, this.timeFormat.intl({
      weekday: 'short',
      day: 'numeric',
      month: 'short',
      hour: 'numeric',
      minute: '2-digit',
    })).format(new Date(iso));
  }

  private clearMessages(): void {
    this.notice.set(null);
    this.error.set(null);
  }

  private isStale(err: unknown): boolean {
    const status = (err as Partial<ApiError> | null)?.status;
    return status === 409 || status === 412;
  }

  /** Translation key for a failed decision. Stale versions mean someone else changed the item. */
  private errorKey(err: unknown): string {
    switch ((err as Partial<ApiError> | null)?.status) {
      case 0:
        return 'approvals.error.network';
      case 403:
        return 'approvals.error.forbidden';
      case 409:
      case 412:
        return 'approvals.error.changed';
      default:
        return 'approvals.error.generic';
    }
  }
}
