import { Component, DestroyRef, computed, effect, inject, input, output, signal, untracked } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { RouterLink } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';
import { AuthService } from '../../../core/auth/auth.service';
import { TimeFormatService } from '../../../core/time-format/time-format.service';
import { TaskConfirmComponent } from '../../tasks/task-confirm/task-confirm.component';
import { MyTaskItem } from '../../my-tasks/my-tasks.models';
import { MyTasksService } from '../../my-tasks/my-tasks.service';
import { taskErrorKey } from '../../tasks/tasks.format';
import { ChecklistItem } from '../../tasks/tasks.models';
import { TasksService } from '../../tasks/tasks.service';

/**
 * One open task on the Today list. Ticking a step updates the count but never completes the task.
 * "I'm done!" completes it; a task without steps completes from its check circle.
 */
@Component({
    selector: 'app-today-task',
    imports: [RouterLink, TranslocoModule, TaskConfirmComponent],
    templateUrl: './today-task.component.html',
    styleUrl: './today-task.component.scss'
})
export class TodayTaskComponent {
  item = input.required<MyTaskItem>();

  /** The tab the card is shown on. The steps page returns to it. */
  origin = input<string>('today');

  /** Emitted after a change so the Today list reloads and the hero counts stay right. */
  changed = output<void>();

  private destroyRef = inject(DestroyRef);
  private myTasks = inject(MyTasksService);
  private tasks = inject(TasksService);
  private auth = inject(AuthService);
  private timeFormat = inject(TimeFormatService);

  /** Version and steps from the last tick response. Cleared when the list reloads. */
  private local = signal<{ version: number; checklist: ChecklistItem[] } | null>(null);
  /** True while the confirm step is open for a task with no steps. */
  confirming = signal(false);
  busy = signal(false);
  /** Translation key of the last failed action, or null. */
  error = signal<string | null>(null);

  version = computed(() => this.local()?.version ?? this.item().version);
  steps = computed<ChecklistItem[]>(() => this.local()?.checklist ?? this.item().checklist ?? []);
  doneCount = computed(() => this.steps().filter((s) => s.done).length);
  allStepsDone = computed(() => this.steps().length > 0 && this.doneCount() === this.steps().length);
  /** True when the task has steps and at least one is still open. The confirm step warns but still allows the submit. */
  stepsLeft = computed(() => this.steps().length > 0 && !this.allStepsDone());
  colour = computed(() => this.item().category_colour ?? 'var(--nw-color-primary)');
  part = computed(() => this.item().part_of_day ?? 'anytime');

  /** Clock time such as "5:00 pm" from the instant the copy is due. */
  clock = computed(() => new Intl.DateTimeFormat(undefined, this.timeFormat.intl({ hour: 'numeric', minute: '2-digit' })).format(new Date(this.item().due_at)));

  constructor() {
    effect(() => {
      this.item();
      untracked(() => this.local.set(null));
    });
  }

  /** Ticks or unticks one step. The response carries the new version and steps. */
  toggle(step: ChecklistItem): void {
    if (this.busy()) return;
    this.busy.set(true);
    this.error.set(null);
    this.myTasks
      .tickChecklist(this.item().task_id, step.id, !step.done, this.version())
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (task) => {
          this.local.set({ version: task.version, checklist: task.checklist ?? this.steps() });
          this.busy.set(false);
          this.changed.emit();
        },
        error: (err) => {
          this.busy.set(false);
          this.error.set(taskErrorKey(err));
          this.changed.emit();
        },
      });
  }

  /** Marks the copy done. The busy state stays on until the list reloads and the card leaves. */
  finish(): void {
    const memberId = this.auth.currentUser()?.family?.member_id;
    if (this.busy() || !memberId) return;
    this.busy.set(true);
    this.error.set(null);
    this.tasks
      .complete(this.item().task_id, memberId)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => this.changed.emit(),
        error: (err) => {
          this.busy.set(false);
          this.error.set(taskErrorKey(err));
          this.changed.emit();
        },
      });
  }
}
