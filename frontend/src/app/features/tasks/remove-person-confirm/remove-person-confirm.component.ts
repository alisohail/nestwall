import { Component, DestroyRef, OnInit, computed, inject, input, output, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { TranslocoModule } from '@ngneat/transloco';
import { AvatarComponent } from '../../../shared/avatar/avatar.component';
import { MemberDetail } from '../../family/family.service';
import { taskErrorKey } from '../tasks.format';
import { RemoveScope, Task } from '../tasks.models';
import { TasksService } from '../tasks.service';

/**
 * Removes one person's copy of a task. Asks which day (or all repeats) when the task repeats,
 * and which person when several are candidates. The last person on the task archives it.
 */
@Component({
    selector: 'app-remove-person-confirm',
    imports: [TranslocoModule, AvatarComponent],
    templateUrl: './remove-person-confirm.component.html',
    styleUrl: './remove-person-confirm.component.scss',
    host: { '(document:keydown.escape)': 'cancelled.emit()' }
})
export class RemovePersonConfirmComponent implements OnInit {
  private tasks = inject(TasksService);
  private destroyRef = inject(DestroyRef);

  /** Occurrence ID of the task, as the Tasks list and the calendar know it. */
  readonly taskId = input.required<string>();
  readonly title = input.required<string>();
  /** People the removal can apply to. The first is chosen; a picker shows when there are several. */
  readonly people = input.required<MemberDetail[]>();

  readonly removed = output<void>();
  readonly cancelled = output<void>();

  readonly task = signal<Task | null>(null);
  readonly loadFailed = signal(false);
  readonly scope = signal<RemoveScope>('occurrence');
  readonly memberId = signal<string | null>(null);
  readonly busy = signal(false);
  readonly error = signal<string | null>(null);

  readonly picked = computed(() => this.memberId() ?? this.people()[0]?.id ?? '');
  readonly pickedName = computed(
    () => this.people().find((p) => p.id === this.picked())?.display_name ?? '',
  );
  /** Only one person is on this day's copy, so removing them archives the task. */
  readonly lastPerson = computed(() => (this.task()?.assignees.length ?? 0) <= 1);

  readonly message = computed(() => {
    if (this.lastPerson()) return { key: 'tasks.remove.message_last', params: {} };
    if (this.task()?.repeats && this.scope() === 'series')
      return { key: 'tasks.remove.message_series', params: { name: this.pickedName() } };
    return { key: 'tasks.remove.message_one', params: { name: this.pickedName() } };
  });

  ngOnInit(): void {
    this.tasks
      .get(this.taskId())
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (task) => this.task.set(task),
        error: () => this.loadFailed.set(true),
      });
  }

  pick(id: string): void {
    this.memberId.set(id);
  }

  confirm(): void {
    const task = this.task();
    if (!task || this.busy()) return;
    this.busy.set(true);
    this.error.set(null);
    this.tasks
      .removePerson(this.taskId(), task.version, this.picked(), this.scope())
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: () => {
          this.busy.set(false);
          this.removed.emit();
        },
        error: (err) => {
          this.busy.set(false);
          this.error.set(taskErrorKey(err));
        },
      });
  }
}
