import { Component, HostListener, computed, inject, input, output, signal, ChangeDetectionStrategy } from '@angular/core';
import { takeUntilDestroyed, toObservable } from '@angular/core/rxjs-interop';
import { TranslocoModule } from '@ngneat/transloco';
import { catchError, map, of, switchMap, tap } from 'rxjs';
import { AvatarComponent } from '../../../shared/avatar/avatar.component';
import { LoadErrorComponent } from '../../../shared/ui/load-error/load-error.component';
import { LoadingComponent } from '../../../shared/ui/loading/loading.component';
import { ApiError } from '../../../core/api/api-client.service';
import { AuthService } from '../../../core/auth/auth.service';
import { MemberDetail } from '../../family/family.service';
import { dayWord, formatDay, formatTime, penaltyPoints, remaining, todayIso } from '../tasks.format';
import { Task, TaskCategory, TaskStatus } from '../tasks.models';
import { TasksService } from '../tasks.service';

/** Popup for one person's copy of a task. Shows the task and its checklist. */
@Component({
    selector: 'app-task-detail',
    imports: [TranslocoModule, AvatarComponent, LoadErrorComponent, LoadingComponent],
    templateUrl: './task-detail.component.html',
    changeDetection: ChangeDetectionStrategy.Eager,
    styleUrl: './task-detail.component.scss'
})
export class TaskDetailComponent {
  private service = inject(TasksService);
  auth = inject(AuthService);

  taskId = input.required<string>();
  memberId = input.required<string>();
  members = input<MemberDetail[]>([]);
  categories = input<TaskCategory[]>([]);

  closed = output<void>();

  task = signal<Task | null>(null);
  loadState = signal<'loading' | 'ready' | 'error'>('loading');
  loadError = signal<ApiError | null>(null);

  private refresh = signal(0);

  member = computed(() => this.members().find((m) => m.id === this.memberId()) ?? null);
  category = computed(() => {
    const id = this.task()?.category_id;
    return id ? (this.categories().find((c) => c.id === id) ?? null) : null;
  });
  copyStatus = computed<TaskStatus | null>(() => {
    const task = this.task();
    if (!task) return null;
    return task.assignees.find((a) => a.member_id === this.memberId())?.status ?? null;
  });
  canSkip = computed(() => this.copyStatus() === 'to_do' && !this.task()?.archived);
  today = todayIso(new Date(), this.auth.currentUser()?.family?.timezone);

  constructor() {
    toObservable(computed(() => ({ id: this.taskId(), member: this.memberId(), n: this.refresh() })))
      .pipe(
        tap(() => {
          this.loadState.set('loading');
          this.loadError.set(null);
          this.task.set(null);
        }),
        switchMap(({ id, member }) =>
          this.service.get(id, member).pipe(
            map((task) => ({ ok: true as const, task })),
            catchError((err: ApiError) => of({ ok: false as const, err })),
          ),
        ),
        takeUntilDestroyed(),
      )
      .subscribe((res) => {
        if (res.ok) {
          this.task.set(res.task);
          this.loadState.set('ready');
        } else {
          this.loadError.set(res.err);
          this.loadState.set('error');
        }
      });
  }

  @HostListener('document:keydown.escape')
  onEscape(): void {
    this.closed.emit();
  }

  reload(): void {
    this.refresh.update((n) => n + 1);
  }

  dayWord(task: Task): string | null {
    return dayWord(task.due_date, this.today);
  }

  formatDay(iso: string): string {
    return formatDay(iso);
  }

  formatTime(hhmm: string): string {
    return formatTime(hhmm);
  }

  expiresIn(task: Task) {
    return task.expires_at ? remaining(task.expires_at) : null;
  }

  penalty(task: Task): number | null {
    if (task.penalty_value == null || task.penalty_unit == null) return null;
    return penaltyPoints(task.points ?? 0, task.penalty_value, task.penalty_unit);
  }

  checklistDone(task: Task): number {
    return (task.checklist ?? []).filter((c) => c.done).length;
  }
}
