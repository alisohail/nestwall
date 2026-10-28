import { Component, computed, inject, input } from '@angular/core';
import { RouterLink } from '@angular/router';
import { TranslocoModule } from '@ngneat/transloco';
import { remaining } from '../../tasks/tasks.format';
import { MyTaskItem } from '../my-tasks.models';
import { TimeFormatService } from '../../../core/time-format/time-format.service';

/**
 * One of the child's own copies as a tappable row. Opens the steps page.
 * Projected content (for example an action button) renders inside the card on the right.
 */
@Component({
    selector: 'app-task-row',
    imports: [RouterLink, TranslocoModule],
    templateUrl: './task-row.component.html',
    styleUrl: './task-row.component.scss'
})
export class TaskRowComponent {
  item = input.required<MyTaskItem>();

  private timeFormat = inject(TimeFormatService);

  /** The tab the row is shown on. The steps page returns to it. */
  origin = input<string>('today');

  /** When a submitted copy was finished. The Waiting tab shows it in place of the due time. */
  doneAt = input<string | null>(null);

  /** The Expired tab marks the row with a red tag. */
  expired = input(false);

  /** True when the finish happened on the current local day. */
  doneToday = computed(() => {
    const at = this.doneAt();
    return !!at && new Date(at).toDateString() === new Date().toDateString();
  });

  doneTime = computed(() => {
    const at = this.doneAt();
    return at ? new Intl.DateTimeFormat(undefined, this.timeFormat.intl({ hour: 'numeric', minute: '2-digit' })).format(new Date(at)) : '';
  });

  doneDay = computed(() => {
    const at = this.doneAt();
    return at ? new Intl.DateTimeFormat(undefined, { weekday: 'short', month: 'short', day: 'numeric' }).format(new Date(at)) : '';
  });

  /** Submitted and approved copies show a tick. Open copies show their due time. */
  isDone = computed(() => {
    const status = this.item().status;
    return status === 'submitted' || status === 'approved';
  });

  /** Time left before an open copy expires, as the parent Tasks list shows it. Null when there is none. */
  expiresIn = computed(() => {
    const at = this.item().expires_at;
    return this.item().status === 'to_do' && at ? remaining(at) : null;
  });

  /** When a parent approved an approved copy, as "Sep 3, 2:15 PM". Null for any other copy. */
  approvedWhen = computed(() => {
    const at = this.item().decided_at;
    if (this.item().status !== 'approved' || !at) return null;
    return new Intl.DateTimeFormat(undefined, this.timeFormat.intl({ weekday: 'short', month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' })).format(new Date(at));
  });

  colour = computed(() => this.item().category_colour ?? 'var(--nw-color-primary)');

  steps = computed(() => this.item().checklist ?? []);

  stepsDone = computed(() => this.steps().filter((s) => s.done).length);

  part = computed(() => this.item().part_of_day ?? 'anytime');

  /** Full date and time such as "25 Oct 2026, 5:00 pm" from the instant the copy is due. */
  clock = computed(() => new Intl.DateTimeFormat(undefined, this.timeFormat.intl({ day: 'numeric', month: 'short', year: 'numeric', hour: 'numeric', minute: '2-digit' })).format(new Date(this.item().due_at)));
}
