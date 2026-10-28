import { Component, DestroyRef, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { RouterLink } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';
import { AuthService } from '../../core/auth/auth.service';
import { TimeFormatService } from '../../core/time-format/time-format.service';
import { ApiError } from '../../core/api/api-client.service';
import { AvatarComponent } from '../../shared/avatar/avatar.component';
import { BannerComponent } from '../../shared/ui/banner/banner.component';
import { IconComponent } from '../../shared/ui/icon/icon.component';
import { LoadErrorComponent } from '../../shared/ui/load-error/load-error.component';
import { LoadingComponent } from '../../shared/ui/loading/loading.component';
import { AgendaBandComponent } from '../calendar/agenda-band/agenda-band.component';
import { WeatherTileComponent } from '../weather/weather-tile.component';
import { BonusService } from '../bonus/bonus.service';
import { BonusClaim, BonusTask } from '../bonus/bonus.models';
import { taskErrorKey } from '../tasks/tasks.format';
import { TasksService } from '../tasks/tasks.service';
import { DashboardActivity, DashboardApproval, DashboardItem, DashboardPerson, ParentDashboard } from './dashboard.models';
import { DashboardService } from './dashboard.service';

/** One line of the Bonus tasks today card. */
export interface DashboardBonusRow {
  id: string;
  title: string;
  points: number;
  badgeKey: string;
  badgeParams: Record<string, string>;
  badgeClass: string;
  detailKey: string;
  detailParams: Record<string, string>;
}

/** One status chip in the Today by person card. */
export interface DashboardStatusChip {
  key: string;
  params: Record<string, string>;
  cls: string;
}

/** Which counts the Today by person card shows. */
export type PersonScope = 'today' | 'week';

const NO_ITEMS: DashboardItem[] = [];

/** Parent home. Shows the day's band, counts, people progress, approvals, bonus tasks and recent activity. */
@Component({
  selector: 'app-dashboard',
  standalone: true,
  imports: [
    RouterLink,
    TranslocoModule,
    AvatarComponent,
    BannerComponent,
    IconComponent,
    LoadingComponent,
    LoadErrorComponent,
    AgendaBandComponent,
    WeatherTileComponent,
  ],
  templateUrl: './dashboard.component.html',
  styleUrl: './dashboard.component.scss',
})
export class DashboardComponent {
  private service = inject(DashboardService);
  private tasks = inject(TasksService);
  private destroyRef = inject(DestroyRef);
  private auth = inject(AuthService);
  private bonusApi = inject(BonusService);
  private timeFormat = inject(TimeFormatService);

  /** Only parents with settings access can set up the weather location. Hint only; the server enforces it. */
  canSettings = computed(() => this.auth.can('settings.view'));

  /** Signed-in parent's first name for the greeting; empty until the session loads. */
  displayName = computed(() => this.auth.currentUser()?.user.display_name ?? '');

  data = signal<ParentDashboard | null>(null);
  loadState = signal<'loading' | 'ready' | 'error'>('loading');
  loadError = signal<ApiError | null>(null);
  /** Today's bonus tasks without cancelled ones. Null until the first load finishes. */
  bonusTasks = signal<BonusTask[] | null>(null);
  bonusError = signal(false);
  bonusRows = computed<DashboardBonusRow[]>(() => (this.bonusTasks() ?? []).map((task) => this.bonusRow(task)));
  /** Key of the approval in progress, so a second click cannot send the same request twice. */
  approving = signal<string | null>(null);
  actionError = signal<string | null>(null);

  donePct = computed(() => {
    const summary = this.data()?.summary;
    return summary ? percent(summary.today_done, summary.today_total) : 0;
  });

  /** Tasks and reward requests that wait for a parent. */
  waitingTotal = computed(() => {
    const summary = this.data()?.summary;
    return summary ? summary.waiting_approval + summary.waiting_reward_requests : 0;
  });

  /** People with points this week, in the order the API returns them. */
  pointPeople = computed(() => (this.data()?.people ?? []).filter((p) => p.points_week > 0));

  /** Today's copies grouped by person, in list order. Feeds the task chips under each person. */
  todayByMember = computed(() => {
    const byMember = new Map<string, DashboardItem[]>();
    for (const item of this.data()?.today ?? []) {
      byMember.set(item.member_id, [...(byMember.get(item.member_id) ?? []), item]);
    }
    return byMember;
  });

  /** Each person's overdue copies, for the Overdue card: "Noah (2 due)". Totals match summary.overdue. */
  overdueGroups = computed(() =>
    (this.data()?.people ?? [])
      .filter((p) => p.overdue > 0)
      .map((p) => ({ member_id: p.member_id, name: p.member_name, colour: p.member_colour, avatar_id: p.avatar_id, count: p.overdue })),
  );

  /** Counts shown in Today by person: today's copies, or this week's when the scope is week. */
  personScope = signal<PersonScope>('today');

  constructor() {
    this.reload();
  }

  /** Loads the dashboard. Existing content stays on screen while a refresh runs. */
  reload(): void {
    if (!this.data()) {
      this.loadState.set('loading');
      this.loadError.set(null);
    }
    this.service
      .parent()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (dashboard) => {
          this.data.set(dashboard);
          this.loadState.set('ready');
        },
        error: (err: ApiError) => {
          this.loadError.set(err);
          this.loadState.set('error');
        },
      });
    this.loadBonus();
  }

  /** Loads today's bonus tasks. A failure hides the list and shows a short message; the rest of the page still works. */
  private loadBonus(): void {
    this.bonusApi
      .list('today')
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => {
          this.bonusError.set(false);
          this.bonusTasks.set(res.tasks.filter((task) => task.state !== 'cancelled'));
        },
        error: () => {
          this.bonusError.set(true);
          this.bonusTasks.set([]);
        },
      });
  }

  /** Name for a family member id, taken from today's people list. */
  private nameOf(memberId: string): string {
    return this.data()?.people.find((p) => p.member_id === memberId)?.member_name ?? '';
  }

  private namesOf(claims: BonusClaim[]): string {
    return claims
      .map((c) => this.nameOf(c.family_member_id))
      .filter((name) => name !== '')
      .join(', ');
  }

  /** Status line for one bonus task. The first matching rule wins. */
  private bonusRow(task: BonusTask): DashboardBonusRow {
    const claims = task.claims ?? [];
    const submitted = claims.filter((c) => c.status === 'submitted');
    const claimed = claims.filter((c) => c.status === 'claimed');
    const approved = claims.filter((c) => c.status === 'approved');
    const base = { id: task.id, title: task.title, points: task.points };

    if (submitted.length > 0) {
      return {
        ...base,
        badgeKey: 'dashboard.bonus.toApprove',
        badgeParams: { count: String(submitted.length) },
        badgeClass: 'teal',
        detailKey: 'dashboard.bonus.doneBy',
        detailParams: { names: this.namesOf(submitted) },
      };
    }
    if (claimed.length > 0) {
      const latest = claimed.reduce((a, b) => (b.claimed_at > a.claimed_at ? b : a));
      return {
        ...base,
        badgeKey: 'bonus.status.claimed',
        badgeParams: {},
        badgeClass: 'amber',
        detailKey: 'bonus.row.grabbedAt',
        detailParams: { when: this.clock(latest.claimed_at) },
      };
    }
    if (task.state === 'scheduled') {
      return {
        ...base,
        badgeKey: 'bonus.state.scheduled',
        badgeParams: {},
        badgeClass: '',
        detailKey: 'dashboard.bonus.appearsAt',
        detailParams: { time: this.clock(task.show_from ?? task.ends_at) },
      };
    }
    if (approved.length > 0) {
      return {
        ...base,
        badgeKey: 'bonus.status.approved',
        badgeParams: {},
        badgeClass: 'ok',
        detailKey: 'bonus.result.done',
        detailParams: { names: this.namesOf(approved) },
      };
    }
    if (task.state === 'expired') {
      return {
        ...base,
        badgeKey: 'bonus.state.expired',
        badgeParams: {},
        badgeClass: '',
        detailKey: claims.length === 0 ? 'bonus.result.nobody' : 'bonus.result.notDone',
        detailParams: {},
      };
    }
    return {
      ...base,
      badgeKey: 'bonus.state.open',
      badgeParams: {},
      badgeClass: '',
      detailKey: 'bonus.nobodyYet',
      detailParams: {},
    };
  }

  approve(item: DashboardApproval): void {
    if (this.approving() !== null) return;
    this.approving.set(approvalKey(item));
    this.actionError.set(null);
    this.tasks
      .approve(item.task_id, item.member_id)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => {
          this.approving.set(null);
          this.reload();
        },
        error: (err) => {
          this.approving.set(null);
          this.actionError.set(taskErrorKey(err));
        },
      });
  }

  isApproving(item: DashboardApproval): boolean {
    return this.approving() === approvalKey(item);
  }

  /** Greeting key for the browser's local hour. */
  greetingKey(): string {
    const hour = new Date().getHours();
    if (hour < 12) return 'dashboard.greeting.morning';
    return hour < 18 ? 'dashboard.greeting.afternoon' : 'dashboard.greeting.evening';
  }

  /** Long date such as "Friday 9 October", in the viewer's locale. The family date is a plain calendar day. */
  longDay(iso: string): string {
    return new Intl.DateTimeFormat(undefined, { weekday: 'long', day: 'numeric', month: 'long', timeZone: 'UTC' }).format(
      new Date(`${iso}T00:00:00Z`),
    );
  }

  /** Clock time such as "5:00 pm" from an instant. */
  clock(iso: string): string {
    return new Intl.DateTimeFormat(undefined, this.timeFormat.intl({ hour: 'numeric', minute: '2-digit' })).format(new Date(iso));
  }

  /** Day and time such as "Fri 9 Oct, 5:00 pm" from an instant. */
  when(iso: string): string {
    return new Intl.DateTimeFormat(undefined, this.timeFormat.intl({
      weekday: 'short',
      day: 'numeric',
      month: 'short',
      hour: 'numeric',
      minute: '2-digit',
    })).format(new Date(iso));
  }

  /** An open copy whose due time has passed. Matches the task list rule. */
  isOverdue(item: DashboardItem): boolean {
    return item.status === 'to_do' && Date.parse(item.due_at) < Date.now();
  }

  percentOf(person: DashboardPerson): number {
    const { done, total } = this.countsFor(person);
    return percent(done, total);
  }

  /** Done and total copies for one person in the selected scope. */
  countsFor(person: DashboardPerson): { done: number; total: number } {
    return this.personScope() === 'week'
      ? { done: person.week_done, total: person.week_total }
      : { done: person.done, total: person.total };
  }

  /** Copies due today that are overdue for one person. */
  overdueFor(memberId: string): number {
    return (this.data()?.today ?? []).filter((item) => item.member_id === memberId && this.isOverdue(item)).length;
  }

  /** Status chip for one person, for today only. Null when nothing is due for them today. */
  personStatus(person: DashboardPerson): DashboardStatusChip | null {
    if (this.personScope() === 'week') return null;
    const overdue = this.overdueFor(person.member_id);
    if (overdue > 0) return { key: 'dashboard.status.overdue_count', params: { count: String(overdue) }, cls: 'err' };
    if (person.total === 0) return null;
    if (person.done >= person.total) return { key: 'dashboard.status.done_today', params: {}, cls: 'teal' };
    return { key: 'dashboard.status.on_track', params: {}, cls: 'ok' };
  }

  /** Translation key for an activity row, from the audit action name. */
  activityKey(event: DashboardActivity): string {
    return event.action.replace('task.', '');
  }

  /** Status pill for an activity row. */
  pillFor(event: DashboardActivity): DashboardStatusChip {
    switch (event.action) {
      case 'task.approved':
        return { key: 'dashboard.activity.pill.approved', params: {}, cls: 'ok' };
      case 'task.skipped':
        return { key: 'dashboard.activity.pill.missed', params: {}, cls: 'err' };
      case 'task.rejected':
        return { key: 'dashboard.activity.pill.rejected', params: {}, cls: 'err' };
      default:
        return { key: 'dashboard.activity.pill.waiting', params: {}, cls: 'amber' };
    }
  }

  /** Today's copies for one person, for the task chips. */
  itemsFor(memberId: string): DashboardItem[] {
    return this.todayByMember().get(memberId) ?? NO_ITEMS;
  }

  /** A copy the child has finished, approved or still waiting for a parent. */
  isDone(item: DashboardItem): boolean {
    return item.status === 'approved' || item.status === 'submitted';
  }
}

function percent(done: number, total: number): number {
  return total > 0 ? Math.round((done * 100) / total) : 0;
}

function approvalKey(item: DashboardApproval): string {
  return `${item.task_id}:${item.member_id}`;
}

