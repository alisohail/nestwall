import { Component, DestroyRef, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { NgTemplateOutlet } from '@angular/common';
import { FormsModule } from '@angular/forms';
import { RouterLink } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';
import { ApiError } from '../../core/api/api-client.service';
import { AvatarComponent } from '../../shared/avatar/avatar.component';
import { BannerComponent } from '../../shared/ui/banner/banner.component';
import { EmptyStateComponent } from '../../shared/ui/empty-state/empty-state.component';
import { ErrorStateComponent } from '../../shared/ui/error-state/error-state.component';
import { LoadingComponent } from '../../shared/ui/loading/loading.component';
import { FamilyService, MemberDetail } from '../family/family.service';
import { BonusClaim, BonusScope, BonusTask, BonusTaskEditRequest } from './bonus.models';
import { TimeFormatService } from '../../core/time-format/time-format.service';
import { BonusService } from './bonus.service';

type LoadState = 'loading' | 'ready' | 'error';

/** Matches the server limit for a decision note. */
const NOTE_MAX = 200;
/** Limits of the edit panel. They match the new-task form and the server. */
const TITLE_MAX = 200;
const DESCRIPTION_MAX = 2000;
const POINTS_MIN = 1;
const POINTS_MAX = 100000;
const DAY_PATTERN = /^\d{4}-\d{2}-\d{2}$/;
const LIVE_STATUSES = ['claimed', 'submitted', 'approved'];

/** One kid's line on a bonus card. claim is null when the kid has not taken the task. */
export interface BonusKidRow {
  memberId: string;
  member: MemberDetail | undefined;
  name: string;
  claim: BonusClaim | null;
  /** Translation key for the status badge. */
  statusKey: string;
  /** Translation key and params for the detail line. */
  detailKey: string;
  detailParams: Record<string, string>;
}

export interface BonusCardView {
  task: BonusTask;
  rows: BonusKidRow[];
}

/** One line of the Scheduled or Past table. */
export interface BonusListRow {
  task: BonusTask;
  dayLabel: string;
  windowKey: string;
  windowParams: Record<string, string | undefined>;
  resultKey: string;
  resultParams: Record<string, string | undefined>;
  /** Kids whose claim was approved. Shown as avatars in the result cell. */
  doneMembers: MemberDetail[];
}

/** Parent Bonus tasks page. Today's tasks with the claim state and the approval actions. */
@Component({
  selector: 'app-bonus',
  standalone: true,
  imports: [
    FormsModule,
    NgTemplateOutlet,
    RouterLink,
    TranslocoModule,
    AvatarComponent,
    BannerComponent,
    EmptyStateComponent,
    ErrorStateComponent,
    LoadingComponent,
  ],
  templateUrl: './bonus.component.html',
  styleUrl: './bonus.component.scss',
})
export class BonusComponent {
  private api = inject(BonusService);
  private familyApi = inject(FamilyService);
  private destroyRef = inject(DestroyRef);
  private timeFormat = inject(TimeFormatService);

  readonly maxNote = NOTE_MAX;
  readonly titleMax = TITLE_MAX;
  readonly descriptionMax = DESCRIPTION_MAX;
  readonly pointsMin = POINTS_MIN;
  readonly pointsMax = POINTS_MAX;

  readonly members = signal<MemberDetail[]>([]);
  private memberById = computed(() => new Map(this.members().map((m) => [m.id, m])));

  readonly tasks = signal<BonusTask[]>([]);
  readonly state = signal<LoadState>('loading');

  /** Tabs in display order. */
  readonly scopes: BonusScope[] = ['today', 'scheduled', 'past'];
  readonly scope = signal<BonusScope>('today');

  /** Each task with the rows the card shows. */
  readonly cards = computed<BonusCardView[]>(() => this.tasks().map((task) => this.toCard(task)));

  /** Each task with the cells the Scheduled and Past tables show. */
  readonly listRows = computed<BonusListRow[]>(() => this.tasks().map((task) => this.toListRow(task)));

  /** Key of the single action in progress. Blocks a second send of the same decision. */
  readonly busy = signal<string | null>(null);
  readonly notice = signal<{ key: string; params?: Record<string, string> } | null>(null);
  readonly error = signal<string | null>(null);

  /** Claim being sent back, with its note. */
  readonly sendBackFor = signal<string | null>(null);
  readonly sendBackNote = signal('');
  readonly sendBackError = signal<string | null>(null);

  /** Task open in the edit panel, if any. editMode 'cancel' shows the cancel confirmation. */
  readonly editing = signal<BonusTask | null>(null);
  readonly editMode = signal<'edit' | 'cancel'>('edit');
  readonly editTitle = signal('');
  readonly editDescription = signal('');
  readonly editPoints = signal<number | null>(null);
  readonly editDay = signal('');
  readonly editError = signal<string | null>(null);

  constructor() {
    this.familyApi
      .listMembers()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({ next: (res) => this.members.set(res.members), error: () => this.members.set([]) });
    this.load();
  }

  /** Switches tab and loads its tasks. */
  setScope(next: BonusScope): void {
    if (next === this.scope()) return;
    this.scope.set(next);
    this.clearMessages();
    this.sendBackFor.set(null);
    this.editing.set(null);
    this.state.set('loading');
    this.load();
  }

  /**
   * Loads the tasks of the current tab. Cards stay on screen while a refresh runs.
   * A response for a tab the user has already left is dropped.
   */
  load(): void {
    const requested = this.scope();
    if (this.state() !== 'ready') this.state.set('loading');
    this.api
      .list(requested)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => {
          if (requested !== this.scope()) return;
          this.tasks.set(res.tasks);
          this.state.set('ready');
        },
        error: () => {
          if (requested !== this.scope()) return;
          this.state.set('error');
        },
      });
  }

  approve(row: BonusKidRow, title: string): void {
    const claim = row.claim;
    if (!claim || this.busy() !== null) return;
    this.clearMessages();
    this.busy.set(`claim:${claim.id}`);
    this.api
      .approve(claim.id, claim.version)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => {
          this.busy.set(null);
          this.notice.set({ key: 'bonus.notice.approved', params: { title, name: row.name } });
          this.load();
        },
        error: (err) => {
          this.busy.set(null);
          this.error.set(this.errorKey(err));
          if (this.isStale(err)) this.load();
        },
      });
  }

  openSendBack(row: BonusKidRow): void {
    if (!row.claim) return;
    this.sendBackFor.set(row.claim.id);
    this.sendBackNote.set('');
    this.sendBackError.set(null);
  }

  closeSendBack(): void {
    if (this.busy() !== null) return;
    this.sendBackFor.set(null);
    this.sendBackError.set(null);
  }

  /** Sends a submitted claim back. The child can try again. The note is optional. */
  submitSendBack(row: BonusKidRow, title: string): void {
    const claim = row.claim;
    if (!claim || this.busy() !== null) return;
    const note = this.sendBackNote().trim();
    if (note.length > NOTE_MAX) {
      this.sendBackError.set('bonus.error.noteTooLong');
      return;
    }
    this.clearMessages();
    this.busy.set(`claim:${claim.id}`);
    this.sendBackError.set(null);
    this.api
      .reject(claim.id, claim.version, note)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => {
          this.busy.set(null);
          this.sendBackFor.set(null);
          this.notice.set({ key: 'bonus.notice.sentBack', params: { title, name: row.name } });
          this.load();
        },
        error: (err) => {
          this.busy.set(null);
          if (this.isStale(err)) {
            this.sendBackFor.set(null);
            this.error.set(this.errorKey(err));
            this.load();
          } else {
            this.sendBackError.set(this.errorKey(err));
          }
        },
      });
  }

  /** Takes a claim back so the task reopens for the other eligible children. No points are posted. */
  unassign(row: BonusKidRow, title: string): void {
    const claim = row.claim;
    if (!claim || this.busy() !== null) return;
    this.clearMessages();
    this.busy.set(`claim:${claim.id}`);
    this.api
      .unassign(claim.id, claim.version)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => {
          this.busy.set(null);
          this.sendBackFor.set(null);
          this.notice.set({ key: 'bonus.notice.unassigned', params: { title, name: row.name } });
          this.load();
        },
        error: (err) => {
          this.busy.set(null);
          this.error.set(this.errorKey(err));
          if (this.isStale(err)) this.load();
        },
      });
  }

  /** Opens the edit panel. Only tasks with no claims can be edited. */
  openEdit(task: BonusTask, mode: 'edit' | 'cancel' = 'edit'): void {
    if (!task.editable || this.busy() !== null) return;
    this.clearMessages();
    this.sendBackFor.set(null);
    this.editing.set(task);
    this.editMode.set(mode);
    this.editTitle.set(task.title);
    this.editDescription.set(task.description ?? '');
    this.editPoints.set(task.points);
    this.editDay.set(task.day);
    this.editError.set(null);
  }

  closeEdit(): void {
    if (this.busy() !== null) return;
    this.editing.set(null);
    this.editError.set(null);
  }

  /** Saves the fields that changed. Nothing is sent when nothing changed. */
  saveEdit(): void {
    const task = this.editing();
    if (!task || this.busy() !== null) return;
    const title = this.editTitle().trim();
    const description = this.editDescription().trim();
    const points = this.editPoints();
    const day = this.editDay();
    if (!title) return this.editError.set('bonus.new.error.titleRequired');
    if (title.length > TITLE_MAX) return this.editError.set('bonus.new.error.titleTooLong');
    if (description.length > DESCRIPTION_MAX) return this.editError.set('bonus.new.error.descriptionTooLong');
    if (points === null || !Number.isInteger(points)) return this.editError.set('bonus.new.error.pointsInvalid');
    if (points < POINTS_MIN || points > POINTS_MAX) return this.editError.set('bonus.new.error.pointsRange');
    if (!DAY_PATTERN.test(day)) return this.editError.set('bonus.new.error.dayInvalid');

    const body: BonusTaskEditRequest = {};
    if (title !== task.title) body.title = title;
    if (description !== (task.description ?? '')) body.description = description || null;
    if (points !== task.points) body.points = points;
    if (day !== task.day) body.day = day;
    if (Object.keys(body).length === 0) {
      this.editing.set(null);
      return;
    }

    this.clearMessages();
    this.editError.set(null);
    this.busy.set(`task:${task.id}`);
    this.api
      .edit(task.id, task.version, body)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => {
          this.busy.set(null);
          this.editing.set(null);
          this.notice.set({ key: 'bonus.notice.edited', params: { title } });
          this.load();
        },
        error: (err) => {
          this.busy.set(null);
          if (this.isStale(err)) {
            this.editing.set(null);
            this.error.set(this.errorKey(err));
            this.load();
          } else {
            this.editError.set(this.errorKey(err));
          }
        },
      });
  }

  /** Cancels a task that no child has claimed. The task stays in the history as cancelled. */
  cancelTask(task: BonusTask): void {
    if (!task.editable || this.busy() !== null) return;
    this.clearMessages();
    this.busy.set(`task:${task.id}`);
    this.api
      .cancel(task.id, task.version)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => {
          this.busy.set(null);
          this.editing.set(null);
          this.notice.set({ key: 'bonus.notice.cancelled', params: { title: task.title } });
          this.load();
        },
        error: (err) => {
          this.busy.set(null);
          this.editing.set(null);
          this.error.set(this.errorKey(err));
          if (this.isStale(err)) this.load();
        },
      });
  }

  /** Points in the browser's number format, such as "1,000". */
  formatPoints(n: number): string {
    return new Intl.NumberFormat(undefined).format(n);
  }

  /** Time such as "4:02 pm" in the browser's locale. */
  time(iso: string): string {
    return new Intl.DateTimeFormat(undefined, this.timeFormat.intl({ hour: 'numeric', minute: '2-digit' })).format(new Date(iso));
  }

  /** Day such as "Sun 11 Oct". The day is a family-local YYYY-MM-DD, so it is formatted in UTC. */
  dayLabel(day: string): string {
    return new Intl.DateTimeFormat(undefined, { weekday: 'short', day: 'numeric', month: 'short', timeZone: 'UTC' }).format(
      new Date(`${day}T00:00:00Z`),
    );
  }

  /** End of the window. A task that ends at midnight runs to the end of its day. */
  untilLabel(task: BonusTask): { key: string; params: Record<string, string> } {
    const end = new Date(task.ends_at);
    if (end.getHours() === 0 && end.getMinutes() === 0) return { key: 'kidBonus.untilEndOfDay', params: {} };
    return { key: 'bonus.until', params: { time: this.time(task.ends_at) } };
  }

  private toListRow(task: BonusTask): BonusListRow {
    const window = task.show_from
      ? { key: 'bonus.window.range', params: { from: this.time(task.show_from), to: this.time(task.ends_at) } }
      : this.untilLabel(task);
    const result = this.resultOf(task);
    return {
      task,
      dayLabel: this.dayLabel(task.day),
      windowKey: window.key,
      windowParams: window.params,
      resultKey: result.key,
      resultParams: result.params,
      doneMembers: (task.claims ?? [])
        .filter((c) => c.status === 'approved')
        .map((c) => this.memberById().get(c.family_member_id))
        .filter((m): m is MemberDetail => m !== undefined),
    };
  }

  /** What happened to a task. Names come from the approved claims. */
  private resultOf(task: BonusTask): { key: string; params: Record<string, string | undefined> } {
    if (task.state === 'cancelled') return { key: 'bonus.result.cancelled', params: {} };
    const claims = task.claims ?? [];
    const approved = claims.filter((c) => c.status === 'approved');
    if (approved.length > 0) {
      const names = approved
        .map((c) => this.memberById().get(c.family_member_id)?.display_name ?? '')
        .filter((name) => name !== '');
      return { key: 'bonus.result.done', params: { names: names.join(', ') } };
    }
    if (claims.length === 0) return { key: 'bonus.result.nobody', params: {} };
    return { key: 'bonus.result.notDone', params: {} };
  }

  /** Builds the rows of one card. first_come shows the live claim holder. everyone_once shows each eligible kid. */
  private toCard(task: BonusTask): BonusCardView {
    const claims = task.claims ?? [];
    if (task.claim_mode === 'first_come') {
      const holder = this.latest(claims.filter((c) => LIVE_STATUSES.includes(c.status)));
      return { task, rows: holder ? [this.toRow(holder.family_member_id, holder)] : [] };
    }
    const rows = task.eligible_member_ids.map((memberId) =>
      this.toRow(memberId, this.latest(claims.filter((c) => c.family_member_id === memberId))),
    );
    return { task, rows };
  }

  private toRow(memberId: string, claim: BonusClaim | null): BonusKidRow {
    const member = this.memberById().get(memberId);
    const status = claim?.status ?? 'none';
    let detailKey = 'bonus.row.notYet';
    let detailParams: Record<string, string> = {};
    if (claim) {
      switch (claim.status) {
        case 'claimed':
          detailKey = 'bonus.row.grabbedAt';
          detailParams = { when: this.time(claim.claimed_at) };
          break;
        case 'submitted':
          detailKey = 'bonus.row.doneAt';
          detailParams = { when: this.time(claim.completed_at ?? claim.claimed_at) };
          break;
        case 'approved':
          detailKey = 'bonus.row.approvedAt';
          detailParams = { when: this.time(claim.decided_at ?? claim.claimed_at) };
          break;
        case 'rejected':
          detailKey = 'bonus.row.sentBack';
          break;
        case 'released':
          detailKey = 'bonus.row.givenBack';
          break;
      }
    }
    return {
      memberId,
      member,
      name: member?.display_name ?? '',
      claim,
      statusKey: `bonus.status.${status}`,
      detailKey,
      detailParams,
    };
  }

  private latest(claims: BonusClaim[]): BonusClaim | null {
    let best: BonusClaim | null = null;
    for (const c of claims) {
      if (!best || c.claimed_at > best.claimed_at) best = c;
    }
    return best;
  }

  private clearMessages(): void {
    this.notice.set(null);
    this.error.set(null);
  }

  private isStale(err: unknown): boolean {
    const status = (err as Partial<ApiError> | null)?.status;
    return status === 409 || status === 412;
  }

  /** Translation key for a failed decision or edit. Stale versions mean someone else changed the record. */
  private errorKey(err: unknown): string {
    const apiErr = err as Partial<ApiError> | null;
    switch (apiErr?.status) {
      case 0:
        return 'bonus.error.network';
      case 400:
        return apiErr?.params?.['field'] === 'day' && apiErr?.params?.['reason'] === 'in_past'
          ? 'bonus.new.error.dayPast'
          : 'bonus.error.generic';
      case 403:
        return 'bonus.error.forbidden';
      case 409:
        if (apiErr?.code === 'has_claims') return 'bonus.error.hasClaims';
        if (apiErr?.code === 'wrong_state') return 'bonus.error.wrongState';
        if (apiErr?.code === 'expired') return 'bonus.error.expired';
        return 'bonus.error.changed';
      case 412:
        return 'bonus.error.changed';
      default:
        return 'bonus.error.generic';
    }
  }
}
