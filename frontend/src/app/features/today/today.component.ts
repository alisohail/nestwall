import { Component, DestroyRef, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { RouterLink } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';
import { LoadingComponent } from '../../shared/ui/loading/loading.component';
import { SkeletonComponent } from '../../shared/ui/skeleton/skeleton.component';
import { AvatarComponent } from '../../shared/avatar/avatar.component';
import { LoadErrorComponent } from '../../shared/ui/load-error/load-error.component';
import { ApiError } from '../../core/api/api-client.service';
import { AuthService } from '../../core/auth/auth.service';
import { DisplayZoneService } from '../../core/timezone/display-zone.service';
import { BadgesService } from '../badges/badges.service';
import { MyBadges } from '../badges/badges.models';
import { CalendarService } from '../calendar/calendar.service';
import { CalendarItem } from '../calendar/calendar.models';
import { clockIn } from '../calendar/calendar.dates';
import { KidBonusListComponent } from '../bonus/kid/kid-bonus-list.component';
import { MyTaskItem, MyTasksResponse } from '../my-tasks/my-tasks.models';
import { MyTasksService } from '../my-tasks/my-tasks.service';
import { TaskRowComponent } from '../my-tasks/task-row/task-row.component';
import { MyPointsResponse, Reward } from '../rewards/rewards.models';
import { RewardsService } from '../rewards/rewards.service';
import { PartOfDay } from '../tasks/tasks.models';
import { MyBadgesPillComponent } from '../badges/my-badges-pill/my-badges-pill.component';
import { TodayTaskComponent } from './today-task/today-task.component';
import { AllDoneComponent } from '../../shared/all-done/all-done.component';
import { WeatherTileComponent } from '../weather/weather-tile.component';

/** One slice of this week's donut. */
export type WeekSlice = 'done' | 'missed' | 'bonus';

/** Translation keys for each slice's label in the donut centre. */
const SLICE_LABEL_KEYS: Record<WeekSlice, string> = {
  done: 'me.points.stats.done',
  missed: 'me.points.stats.missed',
  bonus: 'me.points.stats.bonus',
};

/** This week's totals, the slice edges in percent, and the CSS background that draws them as a donut. */
export interface WeekPie {
  done: number;
  missed: number;
  bonus: number;
  doneEnd: number;
  missedEnd: number;
  background: string;
}

/** Order of the part-of-day groups on the Today list. */
const PART_ORDER: PartOfDay[] = ['morning', 'after_school', 'anytime', 'evening'];

/** A group of today's open copies that share a part of the day. */
export interface PartGroup {
  part: PartOfDay;
  items: MyTaskItem[];
}

/** Child home. Today's progress, streak, catch-up, today's tasks grouped by part of day, and tomorrow. */
@Component({
    selector: 'app-today',
    imports: [
        LoadingComponent,
        SkeletonComponent,
        RouterLink,
        TranslocoModule,
        AvatarComponent,
        LoadErrorComponent,
        TaskRowComponent,
        TodayTaskComponent,
        MyBadgesPillComponent,
        KidBonusListComponent,
        AllDoneComponent,
        WeatherTileComponent,
    ],
    templateUrl: './today.component.html',
    styleUrl: './today.component.scss'
})
export class TodayComponent {
  private service = inject(MyTasksService);
  private rewardsService = inject(RewardsService);
  private destroyRef = inject(DestroyRef);
  private auth = inject(AuthService);
  private displayZone = inject(DisplayZoneService);
  private badgesService = inject(BadgesService);
  private calendarService = inject(CalendarService);

  /** The signed-in child's first name for the greeting; empty until the session loads. */
  firstName = computed(() => this.auth.currentUser()?.user.display_name ?? '');

  /** The child's picture and colour for the header avatar, the same as the shell uses. */
  avatarId = computed(() => this.auth.currentUser()?.family?.avatar_id ?? null);
  avatarColour = computed(() => this.auth.currentUser()?.family?.colour ?? '#888888');

  data = signal<MyTasksResponse | null>(null);
  loadState = signal<'loading' | 'ready' | 'error'>('loading');
  loadError = signal<ApiError | null>(null);

  /** Balance and today's earnings. Null when the load fails, which hides the points chip and hero line. */
  points = signal<MyPointsResponse | null>(null);

  /** Rewards the child can see. Empty when the load fails, which shows the empty reward card. */
  rewards = signal<Reward[]>([]);

  /** Set once the points and rewards requests finish, so the reward card can show a skeleton until then. */
  pointsLoaded = signal(false);
  rewardListLoaded = signal(false);
  rewardsReady = computed(() => this.pointsLoaded() && this.rewardListLoaded());

  /** Badges the child can see. Null when the load fails, which hides the next badge footer. */
  badges = signal<MyBadges | null>(null);

  /** Set once the badges request finishes, so the hero footer can show a skeleton until then. */
  badgesReady = signal(false);

  /** Today's events the child can see. Tasks are already in the list, so only events show here. */
  events = signal<CalendarItem[]>([]);

  /** Set when today's events fail to load, which shows an error instead of the empty note. */
  eventsFailed = signal(false);

  /** Rewards that are active and not archived. */
  activeRewards = computed(() => this.rewards().filter((reward) => reward.status === 'active' && !reward.archived));

  /** Cheapest active reward the balance has not reached yet, or null when there is none. */
  nextReward = computed<Reward | null>(() => {
    const balance = this.points()?.balance;
    if (balance === undefined) return null;
    const open = this.activeRewards().filter((reward) => reward.cost > balance);
    return open.sort((a, b) => a.cost - b.cost)[0] ?? null;
  });

  /** The two most expensive active rewards the balance already covers. */
  affordable = computed<Reward[]>(() => {
    const balance = this.points()?.balance;
    if (balance === undefined) return [];
    return this.activeRewards()
      .filter((reward) => reward.cost <= balance)
      .sort((a, b) => a.cost - b.cost)
      .slice(-2);
  });

  /** Every active reward is already within reach, so there is no next reward to save for. */
  allAffordable = computed(() => this.points() !== null && !this.nextReward() && this.activeRewards().length > 0);

  pointsToGo = computed(() => {
    const reward = this.nextReward();
    return reward ? reward.cost - (this.points()?.balance ?? 0) : 0;
  });

  rewardPct = computed(() => {
    const reward = this.nextReward();
    return reward ? percent(this.points()?.balance ?? 0, reward.cost) : 0;
  });

  /** Points earned today. The API gives this week's totals by day and today's index in that week. */
  pointsToday = computed(() => {
    const points = this.points();
    return points?.week?.[points.today] ?? 0;
  });

  /** This week's done, missed and bonus counts as donut slices. Null until the points load or when they fail. */
  weekPie = computed<WeekPie | null>(() => {
    const points = this.points();
    if (!points) return null;
    const sum = (values: number[] | undefined) => (values ?? []).reduce((total, v) => total + v, 0);
    const done = sum(points.tasks_done);
    const missed = sum(points.tasks_missed);
    const bonus = sum(points.bonus);
    const total = done + missed + bonus;
    const doneEnd = total ? (done / total) * 100 : 0;
    const missedEnd = total ? ((done + missed) / total) * 100 : 0;
    return {
      done,
      missed,
      bonus,
      doneEnd,
      missedEnd,
      background: total
        ? `conic-gradient(var(--nw-color-success) 0% ${doneEnd}%, var(--nw-color-error) ${doneEnd}% ${missedEnd}%, var(--nw-color-points) ${missedEnd}% 100%)`
        : 'var(--nw-color-surface-variant)',
    };
  });

  /** The slice under the pointer or the legend item, or null for the default (done). */
  hovered = signal<WeekSlice | null>(null);

  /** The value and label shown in the donut's centre: the hovered slice, or done by default. */
  centre = computed<{ value: number; labelKey: string } | null>(() => {
    const pie = this.weekPie();
    if (!pie) return null;
    const slice = this.hovered() ?? 'done';
    return { value: pie[slice], labelKey: SLICE_LABEL_KEYS[slice] };
  });

  /** Picks the slice under the pointer on the ring. The hole in the middle keeps the last slice. */
  onRingMove(event: MouseEvent, ring: HTMLElement): void {
    const pie = this.weekPie();
    if (!pie || pie.done + pie.missed + pie.bonus === 0) return;
    const rect = ring.getBoundingClientRect();
    const dx = event.clientX - (rect.left + rect.width / 2);
    const dy = event.clientY - (rect.top + rect.height / 2);
    if (Math.hypot(dx, dy) < rect.width * 0.33) return;
    const angle = (Math.atan2(dx, -dy) * 180) / Math.PI;
    const share = ((((angle % 360) + 360) % 360) / 360) * 100;
    this.hovered.set(share < pie.doneEnd ? 'done' : share < pie.missedEnd ? 'missed' : 'bonus');
  }

  /** The next streak badge above the current streak, with the days left and progress. Null when badges are off or none is left. */
  nextBadge = computed<{ days: number; more: number; pct: number } | null>(() => {
    const badges = this.badges();
    const streak = this.data()?.streak_days;
    if (!badges?.enabled || !badges.show_on_kid_dashboard || streak === undefined) return null;
    const targets = (badges.groups.find((group) => group.group === 'streaks')?.badges ?? [])
      .map((badge) => badge.target)
      .filter((target) => target > streak)
      .sort((a, b) => a - b);
    const days = targets[0];
    return days === undefined ? null : { days, more: days - streak, pct: percent(streak, days) };
  });

  donePct = computed(() => {
    const summary = this.data()?.summary;
    return summary ? percent(summary.today_done, summary.today_total) : 0;
  });

  /** Open copies due today, grouped in the order the list shows them. */
  groups = computed<PartGroup[]>(() => groupByPart(this.data()?.today ?? []));

  /** Open copies still to do today. Used for the hero line. */
  openToday = computed(() => this.data()?.today.length ?? 0);

  /** Something was due today and nothing is left open, so the all-done card replaces the list. */
  allDone = computed(() => {
    const data = this.data();
    return !!data && data.summary.today_total > 0 && data.today.length === 0 && data.catch_up.length === 0;
  });

  /** Time-of-day part of the greeting, from the browser's local hour. */
  greetingPart(): 'morning' | 'afternoon' | 'evening' {
    const hour = new Date().getHours();
    if (hour < 12) return 'morning';
    return hour < 18 ? 'afternoon' : 'evening';
  }

  constructor() {
    this.reload();
  }

  /** Loads the child's day, points, and rewards. Existing content stays on screen while a refresh runs. */
  reload(): void {
    if (!this.data()) {
      this.loadState.set('loading');
      this.loadError.set(null);
    }
    this.service
      .mine()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (data) => {
          this.data.set(data);
          this.loadState.set('ready');
          this.loadEvents(data.date);
        },
        error: (err: ApiError) => {
          this.loadError.set(err);
          this.loadState.set('error');
        },
      });
    this.badgesService
      .mine()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (badges) => {
          this.badges.set(badges);
          this.badgesReady.set(true);
        },
        error: () => {
          this.badges.set(null);
          this.badgesReady.set(true);
        },
      });
    this.rewardsService
      .myPoints()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (points) => {
          this.points.set(points);
          this.pointsLoaded.set(true);
        },
        error: () => {
          this.points.set(null);
          this.pointsLoaded.set(true);
        },
      });
    this.rewardsService
      .list()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (list) => {
          this.rewards.set(list.rewards);
          this.rewardListLoaded.set(true);
        },
        error: () => {
          this.rewards.set([]);
          this.rewardListLoaded.set(true);
        },
      });
  }

  /** Clock time of an event, such as "16:30", in the family or device timezone per the display setting. */
  clock(iso: string): string {
    return clockIn(iso, this.displayZone.clockZone(this.data()?.timezone));
  }

  private loadEvents(date: string): void {
    this.calendarService
      .range(date, date, false)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (range) => {
          this.events.set(range.items);
          this.eventsFailed.set(false);
        },
        error: () => {
          this.events.set([]);
          this.eventsFailed.set(true);
        },
      });
  }

  /** Long date such as "Friday 9 October", in the viewer's locale. The family date is a plain calendar day. */
  longDay(iso: string): string {
    return new Intl.DateTimeFormat(undefined, { weekday: 'long', day: 'numeric', month: 'long', timeZone: 'UTC' }).format(
      new Date(`${iso}T00:00:00Z`),
    );
  }
}

function percent(done: number, total: number): number {
  return total > 0 ? Math.round((done * 100) / total) : 0;
}

/** Groups copies by part of day in the list order. Anything without a part of day counts as anytime. */
export function groupByPart(items: MyTaskItem[]): PartGroup[] {
  return PART_ORDER.map((part) => ({
    part,
    items: items.filter((item) => (item.part_of_day ?? 'anytime') === part),
  })).filter((group) => group.items.length > 0);
}
