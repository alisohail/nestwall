import { Component, DestroyRef, computed, inject, signal, ChangeDetectionStrategy } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { ActivatedRoute, RouterLink } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';
import { forkJoin, of } from 'rxjs';
import { AuthService } from '../../core/auth/auth.service';
import { AvatarComponent } from '../../shared/avatar/avatar.component';
import { BannerComponent } from '../../shared/ui/banner/banner.component';
import { EmptyStateComponent } from '../../shared/ui/empty-state/empty-state.component';
import { ErrorStateComponent } from '../../shared/ui/error-state/error-state.component';
import { LoadingComponent } from '../../shared/ui/loading/loading.component';
import { FamilyService, MemberDetail } from '../family/family.service';
import { RemovePersonConfirmComponent } from './remove-person-confirm/remove-person-confirm.component';
import { TaskConfirmComponent } from './task-confirm/task-confirm.component';
import { TaskDetailComponent } from './task-detail/task-detail.component';
import {
  TASK_TABS,
  TaskGroup,
  TaskTab,
  dayWord,
  dueRangeForTab,
  formatArchivedDate,
  formatDay,
  formatTime,
  groupTasks,
  penaltyPoints,
  remaining,
  statusForTab,
  taskErrorKey,
  todayIso,
} from './tasks.format';
import { ChecklistItem, TaskCategory, TaskListItem, TaskStatus } from './tasks.models';
import { TasksService } from './tasks.service';

const PAGE_SIZE = 20;

interface ViewTarget {
  taskId: string;
  memberId: string;
}

/** Family task list with filters, a View popup per task and an Edit page per task. */
@Component({
    selector: 'app-tasks',
    imports: [
        RouterLink,
        TranslocoModule,
        AvatarComponent,
        BannerComponent,
        EmptyStateComponent,
        ErrorStateComponent,
        LoadingComponent,
        RemovePersonConfirmComponent,
        TaskConfirmComponent,
        TaskDetailComponent,
    ],
    templateUrl: './tasks.component.html',
    changeDetection: ChangeDetectionStrategy.Eager,
    styleUrl: './tasks.component.scss'
})
export class TasksComponent {
  private service = inject(TasksService);
  private family = inject(FamilyService);
  private route = inject(ActivatedRoute);
  private destroyRef = inject(DestroyRef);
  /** Used by the template to disable actions the member may not use. */
  auth = inject(AuthService);

  members = signal<MemberDetail[]>([]);
  categories = signal<TaskCategory[]>([]);
  lookupError = signal<string | null>(null);

  items = signal<TaskListItem[]>([]);
  nextCursor = signal<string | null>(null);
  pageIndex = signal(0);
  loadState = signal<'loading' | 'ready' | 'error'>('loading');
  listError = signal<string | null>(null);
  today = signal(todayIso(new Date(), this.auth.currentUser()?.family?.timezone));

  readonly tabs = TASK_TABS;
  memberFilter = signal('');
  tab = signal<TaskTab>('today');
  categoryFilter = signal('');

  viewing = signal<ViewTarget | null>(null);
  pendingComplete = signal<TaskListItem | null>(null);
  completing = signal(false);
  pendingArchive = signal<TaskListItem | null>(null);
  pendingDelete = signal<TaskListItem | null>(null);
  deleting = signal(false);
  restoringId = signal<string | null>(null);
  pendingStep = signal<string | null>(null);
  actionError = signal<string | null>(null);

  activeMembers = computed(() => this.members().filter((m) => m.status === 'active'));
  visible = computed(() => (this.tab() === 'overdue' ? this.items().filter((item) => this.isOverdue(item)) : this.items()));
  /** Due-date groups for the list. Archived rows are one flat list with no label (see groupTasks). */
  groups = computed<TaskGroup[]>(() => groupTasks(this.visible(), this.tab(), this.today(), Date.now()));

  private requestSeq = 0;
  /** Cursor that starts each page, indexed by page. Page 0 starts from the top of the list. */
  private pageCursors: (string | null)[] = [null];

  constructor() {
    const fromTab = this.route.snapshot.queryParamMap.get('tab');
    if (fromTab && (TASK_TABS as string[]).includes(fromTab)) this.tab.set(fromTab as TaskTab);
    this.auth
      .loadPermissions()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe(() => {
        this.loadLookups();
        this.reload();
      });
  }

  /** Loads members and categories. Lookups the member may not read are skipped, so they do not fail with 403. */
  loadLookups(): void {
    const members$ = this.auth.can('members.view') ? this.family.listMembers() : of({ members: [] as MemberDetail[] });
    const categories$ = this.auth.can('settings.view') ? this.service.categories() : of({ categories: [] as TaskCategory[] });
    forkJoin({ members: members$, categories: categories$ })
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: ({ members, categories }) => {
          this.lookupError.set(null);
          this.members.set(members.members);
          this.categories.set(categories.categories);
        },
        error: (err) => this.lookupError.set(taskErrorKey(err)),
      });
  }

  /** Reloads the current page, for example after a task changes. */
  refresh(): void {
    this.fetch();
  }

  nextPage(): void {
    if (!this.nextCursor()) return;
    this.pageIndex.update((p) => p + 1);
    this.fetch();
  }

  prevPage(): void {
    if (this.pageIndex() === 0) return;
    this.pageIndex.update((p) => p - 1);
    this.fetch();
  }

  /** Starts again from the first page, for example after a filter or tab changes. */
  reload(): void {
    this.pageIndex.set(0);
    this.pageCursors = [null];
    this.fetch();
  }

  setMember(id: string): void {
    this.memberFilter.set(id);
    this.reload();
  }

  setTab(tab: TaskTab): void {
    if (this.tab() === tab) return;
    this.tab.set(tab);
    this.reload();
  }

  setCategory(value: string): void {
    this.categoryFilter.set(value);
    this.reload();
  }

  view(item: TaskListItem): void {
    this.viewing.set({ taskId: item.id, memberId: item.member_id });
  }

  closeView(): void {
    this.viewing.set(null);
  }

  askComplete(item: TaskListItem): void {
    if (item.status === 'to_do' && !item.archived) this.pendingComplete.set(item);
  }

  /** A step can be ticked on the signed-in member's own copy, or on another member's copy with tasks.manage. Archived tasks cannot change. */
  canTickStep(item: TaskListItem): boolean {
    if (item.archived) return false;
    if (!this.auth.can('tasks.complete')) return false;
    if (item.status !== 'to_do' && item.status !== 'rejected') return false;
    const own = item.member_id === this.auth.currentUser()?.family?.member_id;
    return own || this.auth.can('tasks.manage');
  }

  /** Ticks or unticks one step on the copy named by item.member_id. Other copies of the task keep their own state. */
  toggleStep(item: TaskListItem, step: ChecklistItem): void {
    if (!this.canTickStep(item) || this.pendingStep()) return;
    this.actionError.set(null);
    this.pendingStep.set(step.id);
    this.service.tickChecklist(item.id, step.id, !step.done, item.version, item.member_id).subscribe({
      next: (updated) => {
        this.pendingStep.set(null);
        this.items.update((rows) =>
          rows.map((row) =>
            row.id === updated.id && row.member_id === item.member_id
              ? { ...row, checklist: updated.checklist, version: updated.version }
              : row,
          ),
        );
      },
      error: (err) => {
        this.pendingStep.set(null);
        this.actionError.set(taskErrorKey(err));
        this.refresh();
      },
    });
  }

  confirmComplete(): void {
    const item = this.pendingComplete();
    if (!item || this.completing()) return;
    this.completing.set(true);
    this.actionError.set(null);
    this.service.complete(item.id, item.member_id).subscribe({
      next: () => {
        this.completing.set(false);
        this.pendingComplete.set(null);
        this.refresh();
      },
      error: (err) => {
        this.completing.set(false);
        this.pendingComplete.set(null);
        this.actionError.set(taskErrorKey(err));
      },
    });
  }

  askArchive(item: TaskListItem): void {
    this.pendingArchive.set(item);
  }

  askDelete(item: TaskListItem): void {
    this.pendingDelete.set(item);
  }

  /** Permanently deletes an archived task. A stale version refreshes the list so the user sees the latest copy. */
  confirmDelete(): void {
    const item = this.pendingDelete();
    if (!item || this.deleting()) return;
    this.deleting.set(true);
    this.actionError.set(null);
    this.service.deleteTask(item.id, item.version).subscribe({
      next: () => {
        this.deleting.set(false);
        this.pendingDelete.set(null);
        this.refresh();
      },
      error: (err) => {
        this.deleting.set(false);
        this.pendingDelete.set(null);
        this.actionError.set(taskErrorKey(err));
        this.refresh();
      },
    });
  }

  /** Puts an archived task back to its state before archiving. No confirm step, since it can be archived again. */
  restore(item: TaskListItem): void {
    if (this.restoringId()) return;
    this.restoringId.set(item.id);
    this.actionError.set(null);
    this.service.restore(item.id, item.version).subscribe({
      next: () => {
        this.restoringId.set(null);
        this.refresh();
      },
      error: (err) => {
        this.restoringId.set(null);
        this.actionError.set(taskErrorKey(err));
        this.refresh();
      },
    });
  }

  /** Called after the person's copy is removed; the list reloads without it. */
  onRemoved(): void {
    this.pendingArchive.set(null);
    this.refresh();
  }

  memberOf(item: TaskListItem): MemberDetail | null {
    return this.members().find((m) => m.id === item.member_id) ?? null;
  }

  categoryOf(item: TaskListItem): TaskCategory | null {
    return item.category_id ? (this.categories().find((c) => c.id === item.category_id) ?? null) : null;
  }

  dayWord(item: TaskListItem): string | null {
    return dayWord(item.due_date, this.today());
  }

  stepPct(item: TaskListItem): number {
    const steps = item.checklist ?? [];
    return steps.length ? (steps.filter((c) => c.done).length / steps.length) * 100 : 0;
  }

  formatDay(iso: string): string {
    return formatDay(iso);
  }

  formatTime(hhmm: string): string {
    return formatTime(hhmm);
  }

  isOverdue(item: TaskListItem): boolean {
    return !item.archived && item.status === 'to_do' && Date.parse(item.due_at) < Date.now();
  }

  /** Archive date for an archived row, in the family time zone. */
  archivedDate(item: TaskListItem): string | null {
    if (!item.archived_at) return null;
    return formatArchivedDate(item.archived_at, this.auth.currentUser()?.family?.timezone);
  }

  expiresIn(item: TaskListItem) {
    return item.status === 'to_do' && item.expires_at ? remaining(item.expires_at) : null;
  }

  penaltyText(item: TaskListItem): { key: string; params: Record<string, number> } | null {
    if (item.status !== 'to_do' || !item.penalty_value || item.penalty_unit == null) return null;
    if (item.penalty_unit === 'percent') {
      const points = penaltyPoints(item.points ?? 0, item.penalty_value, 'percent');
      return {
        key: 'tasks.list.penalty_percent',
        params: { value: item.penalty_value, points },
      };
    }
    return {
      key: 'tasks.list.penalty_points',
      params: { points: item.penalty_value },
    };
  }

  statusIcon(status: TaskStatus): string {
    switch (status) {
      case 'approved':
        return 'check';
      case 'submitted':
        return 'schedule';
      case 'rejected':
        return 'close';
      case 'skipped':
        return 'skip_next';
      default:
        return '';
    }
  }

  private fetch(): void {
    const seq = ++this.requestSeq;
    const page = this.pageIndex();
    this.today.set(todayIso(new Date(), this.auth.currentUser()?.family?.timezone));
    this.listError.set(null);
    this.loadState.set('loading');
    this.service
      .list({
        member: this.memberFilter() || undefined,
        status: statusForTab(this.tab()),
        ...dueRangeForTab(this.tab(), this.today()),
        category: this.categoryFilter() || undefined,
        archived: this.tab() === 'archived' || undefined,
        expired: this.tab() === 'expired' || undefined,
        cursor: this.pageCursors[page] ?? null,
        limit: PAGE_SIZE,
      })
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => {
          if (seq !== this.requestSeq) return;
          if (res.tasks.length === 0 && page > 0) {
            // The page emptied, for example after its last task was archived, so show the page before it.
            this.pageIndex.set(page - 1);
            this.fetch();
            return;
          }
          this.pageCursors[page + 1] = res.next_cursor;
          this.items.set(res.tasks);
          this.nextCursor.set(res.next_cursor);
          this.loadState.set('ready');
        },
        error: (err) => {
          if (seq !== this.requestSeq) return;
          this.listError.set(taskErrorKey(err));
          this.loadState.set('error');
        },
      });
  }
}
