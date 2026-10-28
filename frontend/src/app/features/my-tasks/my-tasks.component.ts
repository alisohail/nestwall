import { Component, DestroyRef, WritableSignal, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { ActivatedRoute, Router } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';
import { Observable } from 'rxjs';
import { BannerComponent } from '../../shared/ui/banner/banner.component';
import { LoadingComponent } from '../../shared/ui/loading/loading.component';
import { LoadErrorComponent } from '../../shared/ui/load-error/load-error.component';
import { SkeletonComponent } from '../../shared/ui/skeleton/skeleton.component';
import { AllDoneComponent } from '../../shared/all-done/all-done.component';
import { ApiError } from '../../core/api/api-client.service';
import { AuthService } from '../../core/auth/auth.service';
import { addDaysIso, formatDay, taskErrorKey } from '../tasks/tasks.format';
import { MyTaskItem, MyTasksPage, MyTasksResponse } from './my-tasks.models';
import { KidBonusListComponent } from '../bonus/kid/kid-bonus-list.component';
import { groupByPart, PartGroup } from '../today/today.component';
import { TodayTaskComponent } from '../today/today-task/today-task.component';
import { MyTasksService } from './my-tasks.service';
import { TaskRowComponent } from './task-row/task-row.component';
import { EmptyStateComponent } from '../../shared/ui/empty-state/empty-state.component';

export type MyTasksTab = 'today' | 'past_due' | 'coming' | 'bonus' | 'waiting' | 'expired' | 'done';

const TABS: MyTasksTab[] = ['today', 'past_due', 'coming', 'bonus', 'waiting', 'expired', 'done'];

/** The two lists that page from the server. */
type PagedList = 'coming' | 'done';

/** idle: not requested yet. loading: first page or next page in flight. */
type PageState = 'idle' | 'loading' | 'ready' | 'error';

interface PagedSignals {
  /** The page on screen. */
  items: WritableSignal<MyTaskItem[]>;
  /** Cursor that loaded the page on screen; null for the first page. */
  cursor: WritableSignal<string | null>;
  /** Cursors of the pages before the one on screen, so Previous can go back. */
  history: WritableSignal<(string | null)[]>;
  /** Cursor of the page after the one on screen; null on the last page. */
  next: WritableSignal<string | null>;
  state: WritableSignal<PageState>;
}

/** Copies that share a due day, for the Coming up list. */
export interface DayGroup {
  date: string;
  label: string;
  tomorrow: boolean;
  items: MyTaskItem[];
}

/** The child's own tasks: tabs for today, past due, coming up, bonus, waiting for a parent, and done. */
@Component({
    selector: 'app-my-tasks',
    imports: [
        LoadingComponent,
        LoadErrorComponent,
        SkeletonComponent, TranslocoModule, BannerComponent, AllDoneComponent, TaskRowComponent, TodayTaskComponent, KidBonusListComponent, EmptyStateComponent
    ],
    templateUrl: './my-tasks.component.html',
    styleUrl: './my-tasks.component.scss'
})
export class MyTasksComponent {
  private service = inject(MyTasksService);
  private route = inject(ActivatedRoute);
  private router = inject(Router);
  private destroyRef = inject(DestroyRef);
  private auth = inject(AuthService);

  readonly tabs = TABS;

  /** Tabs that show a count badge, and its colour, as in the mock. */
  readonly badges: Partial<Record<MyTasksTab, 'teal' | 'amber'>> = {
    today: 'teal',
    past_due: 'amber',
    bonus: 'amber',
    waiting: 'amber',
  };

  tab = signal<MyTasksTab>('today');
  data = signal<MyTasksResponse | null>(null);
  loadState = signal<'loading' | 'ready' | 'error'>('loading');
  loadError = signal<ApiError | null>(null);
  /** Bonus cards the child can still act on, reported by the bonus list. */
  bonusOpen = signal(0);
  /** The copy whose undo is in flight, and the translation key of the last failed undo. */
  withdrawingId = signal<string | null>(null);
  withdrawError = signal<string | null>(null);

  /** The signed-in child's first name for the all done card. */
  firstName = computed(() => this.auth.currentUser()?.user.display_name ?? '');

  today = computed<MyTaskItem[]>(() => this.data()?.today ?? []);
  /** Open copies due today, grouped in the order the list shows them. */
  todayGroups = computed<PartGroup[]>(() => groupByPart(this.today()));
  /** Something was due today and nothing is left open, so the all done card replaces the list. */
  todayAllDone = computed(() => (this.data()?.summary.today_total ?? 0) > 0 && this.today().length === 0);
  pastDue = computed<MyTaskItem[]>(() => this.data()?.catch_up ?? []);
  waiting = computed<MyTaskItem[]>(() => this.data()?.waiting ?? []);
  /** Open copies whose occurrence was missed. The child can view them but not act on them. */
  expired = computed<MyTaskItem[]>(() => this.data()?.expired ?? []);

  /** Coming up, one page at a time. */
  comingItems = signal<MyTaskItem[]>([]);
  comingCursor = signal<string | null>(null);
  comingHistory = signal<(string | null)[]>([]);
  comingNext = signal<string | null>(null);
  comingState = signal<PageState>('idle');
  /** Done, one page at a time. */
  doneItems = signal<MyTaskItem[]>([]);
  doneCursor = signal<string | null>(null);
  doneHistory = signal<(string | null)[]>([]);
  doneNext = signal<string | null>(null);
  doneState = signal<PageState>('idle');
  /** Bumped on each fresh load so a response for an older load is dropped. */
  private pageGeneration: Record<PagedList, number> = { coming: 0, done: 0 };

  /** Coming up grouped by due day, earliest first. Tomorrow carries its own label. */
  comingGroups = computed<DayGroup[]>(() => {
    const today = this.data()?.date;
    if (!today) return [];
    const tomorrow = addDaysIso(today, 1);
    const byDate = new Map<string, MyTaskItem[]>();
    for (const item of this.comingItems()) {
      const list = byDate.get(item.due_date) ?? [];
      list.push(item);
      byDate.set(item.due_date, list);
    }
    return [...byDate.keys()]
      .sort()
      .map((date) => ({
        date,
        label: formatDay(date),
        tomorrow: date === tomorrow,
        items: byDate.get(date) ?? [],
      }));
  });

  constructor() {
    // Runs before the tab subscription so a tab from the URL is loaded once, not restarted.
    this.reload();
    this.route.queryParamMap.pipe(takeUntilDestroyed(this.destroyRef)).subscribe((params) => {
      const requested = params.get('tab') as MyTasksTab | null;
      this.tab.set(requested && TABS.includes(requested) ? requested : 'today');
      this.openTab(this.tab());
    });
  }

  /** Loads the child's tasks. Existing content stays on screen while a refresh runs. */
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
        },
        error: (err: ApiError) => {
          this.loadError.set(err);
          this.loadState.set('error');
        },
      });
    // A list already on screen restarts from its first page so it shows the copies as they are now.
    if (this.comingState() !== 'idle') this.loadFirst('coming');
    if (this.doneState() !== 'idle') this.loadFirst('done');
  }

  /** Switches tab and keeps it in the URL so the view can be reopened. */
  select(tab: MyTasksTab): void {
    this.tab.set(tab);
    this.openTab(tab);
    void this.router.navigate([], { relativeTo: this.route, queryParams: { tab }, replaceUrl: true });
  }

  /** Loads a paged list the first time its tab opens. */
  private openTab(tab: MyTasksTab): void {
    if (tab === 'coming' && this.comingState() === 'idle') this.loadFirst('coming');
    if (tab === 'done' && this.doneState() === 'idle') this.loadFirst('done');
  }

  /** Shows the first page of a paged list and forgets the pages before it. */
  loadFirst(list: PagedList): void {
    this.pageGeneration[list]++;
    const signals = this.pagedSignals(list);
    signals.history.set([]);
    signals.cursor.set(null);
    signals.next.set(null);
    this.fetchPage(list);
  }

  /** Shows the page after the one on screen. Does nothing on the last page or while a load is running. */
  nextPage(list: PagedList): void {
    const signals = this.pagedSignals(list);
    const next = signals.next();
    if (!next || signals.state() === 'loading') return;
    signals.history.update((history) => [...history, signals.cursor()]);
    signals.cursor.set(next);
    this.fetchPage(list);
  }

  /** Shows the page before the one on screen. Does nothing on the first page or while a load is running. */
  prevPage(list: PagedList): void {
    const signals = this.pagedSignals(list);
    const history = signals.history();
    if (!history.length || signals.state() === 'loading') return;
    signals.cursor.set(history[history.length - 1]);
    signals.history.set(history.slice(0, -1));
    this.fetchPage(list);
  }

  /** Retries the page that failed to load. The cursor already points at it. */
  retryPage(list: PagedList): void {
    this.fetchPage(list);
  }

  /** Page number shown in the pager, starting at 1. */
  pageNumber(list: PagedList): number {
    return this.pagedSignals(list).history().length + 1;
  }

  private fetchPage(list: PagedList): void {
    const signals = this.pagedSignals(list);
    const generation = this.pageGeneration[list];
    const cursor = signals.cursor();
    signals.state.set('loading');
    const request: Observable<MyTasksPage> = list === 'coming' ? this.service.coming(cursor) : this.service.done(cursor);
    request.pipe(takeUntilDestroyed(this.destroyRef)).subscribe({
      next: (page) => {
        if (generation !== this.pageGeneration[list]) return;
        signals.items.set(page.tasks);
        signals.next.set(page.next_cursor);
        signals.state.set('ready');
      },
      error: () => {
        if (generation === this.pageGeneration[list]) signals.state.set('error');
      },
    });
  }

  private pagedSignals(list: PagedList): PagedSignals {
    return list === 'coming'
      ? { items: this.comingItems, cursor: this.comingCursor, history: this.comingHistory, next: this.comingNext, state: this.comingState }
      : { items: this.doneItems, cursor: this.doneCursor, history: this.doneHistory, next: this.doneNext, state: this.doneState };
  }

  /** Takes a copy the child submitted back to to do, so it can be changed before a parent checks it. */
  withdraw(item: MyTaskItem): void {
    if (this.withdrawingId()) return;
    this.withdrawingId.set(item.task_id);
    this.withdrawError.set(null);
    this.service
      .withdraw(item.task_id, item.version)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => {
          this.withdrawingId.set(null);
          this.reload();
        },
        error: (err) => {
          this.withdrawingId.set(null);
          this.withdrawError.set(taskErrorKey(err));
          this.reload();
        },
      });
  }

  count(tab: MyTasksTab): number {
    switch (tab) {
      case 'today':
        return this.today().length;
      case 'past_due':
        return this.pastDue().length;
      case 'waiting':
        return this.waiting().length;
      case 'expired':
        return this.expired().length;
      case 'bonus':
        return this.bonusOpen();
      default:
        return 0;
    }
  }
}
