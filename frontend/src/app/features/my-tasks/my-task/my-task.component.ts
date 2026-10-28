import { Component, DestroyRef, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed, toSignal } from '@angular/core/rxjs-interop';
import { ActivatedRoute, RouterLink } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';
import { map } from 'rxjs';
import { BannerComponent } from '../../../shared/ui/banner/banner.component';
import { LoadingComponent } from '../../../shared/ui/loading/loading.component';
import { LoadErrorComponent } from '../../../shared/ui/load-error/load-error.component';
import { ApiError } from '../../../core/api/api-client.service';
import { AuthService } from '../../../core/auth/auth.service';
import { TimeFormatService } from '../../../core/time-format/time-format.service';
import { ChecklistItem, Task, TaskStatus } from '../../tasks/tasks.models';
import { TaskConfirmComponent } from '../../tasks/task-confirm/task-confirm.component';
import { TasksService } from '../../tasks/tasks.service';
import { formatDay, taskErrorKey, todayIso } from '../../tasks/tasks.format';
import { MyTasksService } from '../my-tasks.service';

/** One task copy for the child: its steps, a tick for each step, and the "I'm done!" button. */
@Component({
    selector: 'app-my-task',
    imports: [RouterLink, TranslocoModule, BannerComponent, LoadingComponent, LoadErrorComponent, TaskConfirmComponent],
    templateUrl: './my-task.component.html',
    styleUrl: './my-task.component.scss'
})
export class MyTaskComponent {
  private route = inject(ActivatedRoute);
  private destroyRef = inject(DestroyRef);
  private tasks = inject(TasksService);
  private myTasks = inject(MyTasksService);
  private auth = inject(AuthService);
  private timeFormat = inject(TimeFormatService);

  task = signal<Task | null>(null);
  loadState = signal<'loading' | 'ready' | 'error' | 'missing'>('loading');
  loadError = signal<ApiError | null>(null);
  /** Translation key of the last failed action, or null. */
  actionError = signal<string | null>(null);
  /** ID of the step being ticked, or null. */
  pendingStep = signal<string | null>(null);
  finishing = signal(false);
  /** True while the "I'm done!" confirm step is open. */
  confirming = signal(false);
  /** True while an Undo on this copy is in flight. */
  undoing = signal(false);

  /** The origin the task was opened from. Unknown values fall back to Today. */
  backTab = toSignal(
    this.route.queryParamMap.pipe(map((params) => params.get('from') ?? 'today')),
    { initialValue: 'today' },
  );

  /** The Today page returns to /today; every other origin returns to its My tasks tab. */
  backRoute = computed(() => (this.backTab() === 'home' ? ['/today'] : ['/my-tasks']));
  backQuery = computed(() => (this.backTab() === 'home' ? null : { tab: this.backTab() }));

  /** The child's own member ID from the session. */
  memberId = computed(() => this.auth.currentUser()?.family?.member_id ?? null);

  /** The child's own status on this copy. */
  ownStatus = computed<TaskStatus | null>(() => {
    const id = this.memberId();
    const task = this.task();
    if (!id || !task) return null;
    return task.assignees.find((a) => a.member_id === id)?.status ?? null;
  });

  steps = computed<ChecklistItem[]>(() => this.task()?.checklist ?? []);
  doneCount = computed(() => this.steps().filter((s) => s.done).length);
  /** True when the task has steps and at least one is still open. The confirm step warns but still allows the submit. */
  stepsLeft = computed(() => this.steps().length > 0 && this.doneCount() < this.steps().length);
  pct = computed(() => {
    const total = this.steps().length;
    return total > 0 ? Math.round((this.doneCount() * 100) / total) : 0;
  });

  /** Steps can be ticked while the copy is open. A rejected copy can be redone. */
  canEdit = computed(() => {
    const status = this.ownStatus();
    return status === 'to_do' || status === 'rejected';
  });

  /** A parent with tasks.manage can act on any copy, including one due later. */
  canManage = computed(() => this.auth.can('tasks.manage'));

  /** The copy can be acted on: it is due, or the viewer is a parent. */
  open = computed(() => !this.notYetDue() || this.canManage());

  /** "I'm done!" is enabled while the copy is open, with or without steps ticked. */
  canFinish = computed(() => this.canEdit() && this.open() && !this.finishing());

  /** A copy opens on its due day. Until then a child can neither tick steps nor finish it. */
  notYetDue = computed(() => {
    const due = this.task()?.due_date;
    return !!due && due > todayIso(new Date(), this.auth.currentUser()?.family?.timezone);
  });

  /** Weekday and date of the due day, such as "Sat, Oct 12". */
  dueDay = computed(() => {
    const due = this.task()?.due_date;
    return due ? formatDay(due) : '';
  });

  constructor() {
    this.auth.loadPermissions().pipe(takeUntilDestroyed(this.destroyRef)).subscribe();
    this.route.paramMap.pipe(takeUntilDestroyed(this.destroyRef)).subscribe((params) => {
      const id = params.get('id');
      if (!id) {
        this.loadState.set('missing');
        return;
      }
      this.load(id);
    });
  }

  /** Loads the copy by its occurrence ID. A 404 shows the not-found state. */
  load(id: string): void {
    this.loadState.set('loading');
    this.loadError.set(null);
    this.tasks
      .get(id)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (task) => {
          this.task.set(task);
          this.loadState.set('ready');
        },
        error: (err: ApiError) => {
          if (err?.status === 404) {
            this.loadState.set('missing');
            return;
          }
          this.loadError.set(err);
          this.loadState.set('error');
        },
      });
  }

  /** Retries the load for the ID in the URL. */
  retry(): void {
    const id = this.route.snapshot.paramMap.get('id');
    if (id) this.load(id);
  }

  /** Clock time and weekday such as "Sat 5:00 pm", in the viewer's locale. */
  dueLabel(iso: string): string {
    return new Intl.DateTimeFormat(undefined, this.timeFormat.intl({ weekday: 'short', hour: 'numeric', minute: '2-digit' })).format(new Date(iso));
  }

  /** Ticks or unticks one step. The response is the new task with the new version. */
  toggle(step: ChecklistItem): void {
    const task = this.task();
    if (!task || !this.canEdit() || !this.open() || this.pendingStep()) return;
    this.actionError.set(null);
    this.pendingStep.set(step.id);
    this.myTasks
      .tickChecklist(task.id, step.id, !step.done, task.version)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (updated) => {
          this.task.set(updated);
          this.pendingStep.set(null);
        },
        error: (err) => {
          this.pendingStep.set(null);
          this.actionError.set(taskErrorKey(err));
          this.refresh(task.id);
        },
      });
  }

  /** Marks the copy done. A parent checks it next, unless it needs no approval. */
  finish(): void {
    const task = this.task();
    const memberId = this.memberId();
    if (!task || !memberId || !this.canFinish()) return;
    this.actionError.set(null);
    this.finishing.set(true);
    this.tasks
      .complete(task.id, memberId)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (updated) => {
          this.task.set(updated);
          this.finishing.set(false);
        },
        error: (err) => {
          this.finishing.set(false);
          this.actionError.set(taskErrorKey(err));
          this.refresh(task.id);
        },
      });
  }

  /** Takes a submitted copy back to to do, so the child can change it before a parent checks it. */
  undo(): void {
    const task = this.task();
    if (!task || this.undoing()) return;
    this.actionError.set(null);
    this.undoing.set(true);
    this.myTasks
      .withdraw(task.id, task.version)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (updated) => {
          this.task.set(updated);
          this.undoing.set(false);
        },
        error: (err) => {
          this.undoing.set(false);
          this.actionError.set(taskErrorKey(err));
          this.refresh(task.id);
        },
      });
  }

  /** Reloads the copy after a failed action so the screen shows the server's current version. */
  private refresh(id: string): void {
    this.tasks
      .get(id)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({ next: (task) => this.task.set(task), error: () => undefined });
  }
}
