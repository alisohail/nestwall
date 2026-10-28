import { Component, HostListener, computed, inject, input, output, signal, ChangeDetectionStrategy } from '@angular/core';
import { Router } from '@angular/router';
import { TranslocoModule, TranslocoService } from '@ngneat/transloco';
import { AvatarComponent } from '../../../shared/avatar/avatar.component';
import { IconComponent } from '../../../shared/ui/icon/icon.component';
import { MemberDetail } from '../../family/family.service';
import { RemovePersonConfirmComponent } from '../../tasks/remove-person-confirm/remove-person-confirm.component';
import { TaskConfirmComponent } from '../../tasks/task-confirm/task-confirm.component';
import { formatDay } from '../../tasks/tasks.format';
import { clockIn } from '../calendar.dates';
import type { DeleteScope } from '../calendar.models';
import type { MonthPill } from '../calendar.component';

/** Day of an instant in a zone, as YYYY-MM-DD. */
function dateIn(iso: string, zone: string): string {
  const parts = new Intl.DateTimeFormat('en-CA', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    timeZone: zone,
  }).formatToParts(new Date(iso));
  const get = (type: string) => parts.find((p) => p.type === type)?.value ?? '';
  return `${get('year')}-${get('month')}-${get('day')}`;
}

/** Details of one calendar item. Events can be edited here; task deadlines open the task. */
@Component({
    selector: 'app-event-detail',
    imports: [TranslocoModule, IconComponent, AvatarComponent, TaskConfirmComponent, RemovePersonConfirmComponent],
    templateUrl: './event-detail.component.html',
    changeDetection: ChangeDetectionStrategy.Eager,
    styleUrl: './event-detail.component.scss'
})
export class EventDetailComponent {
  private router = inject(Router);
  private i18n = inject(TranslocoService);

  readonly pill = input.required<MonthPill>();
  readonly people = input<MemberDetail[]>([]);
  /** Hides edit and delete, for a read-only popup such as the dashboard band. */
  readonly viewOnly = input(false);

  readonly edit = output<void>();
  readonly remove = output<DeleteScope>();
  /** A task person's copy was removed from the detail's confirm. */
  readonly removed = output<void>();
  readonly closed = output<void>();

  /** Delete confirmation is showing. */
  readonly confirming = signal(false);
  /** Which occurrences a delete of a repeating event removes. */
  readonly scope = signal<DeleteScope>('occurrence');

  readonly item = computed(() => this.pill().item);
  readonly isTask = computed(() => this.pill().task);
  readonly synced = computed(() => !this.isTask() && !!this.item().imported);

  readonly day = computed(() => formatDay(this.item().occurrence_date));

  /** Time line such as "4:00 pm – 5:00 pm", with the end day when it falls on another day. */
  readonly when = computed(() => {
    const item = this.item();
    if (item.all_day) return this.i18n.translate('calendar.detail.all_day');
    const zone = item.time_zone || undefined;
    const from = clockIn(item.starts_at, zone);
    const to = clockIn(item.ends_at, zone);
    const endDay = zone ? dateIn(item.ends_at, zone) : item.occurrence_date;
    const endsOtherDay = endDay !== item.occurrence_date;
    if (from === to && !endsOtherDay) return from;
    return `${from} – ${to}${endsOtherDay ? ` (${formatDay(endDay)})` : ''}`;
  });

  /** Reminder lines for an event, such as "15 min before". Tasks have none. */
  readonly reminders = computed(() =>
    this.isTask() ? [] : (this.item().reminders ?? []).map((m) => this.reminderLabel(m)),
  );

  private reminderLabel(minutes: number): string {
    if (minutes === 0) return this.i18n.translate('calendar.event.reminder_at_start');
    if (minutes % 1440 === 0) return this.i18n.translate('calendar.event.reminder_days', { count: minutes / 1440 });
    if (minutes % 60 === 0) return this.i18n.translate('calendar.event.reminder_hours', { count: minutes / 60 });
    return this.i18n.translate('calendar.event.reminder_minutes', { count: minutes });
  }

  onBackdrop(e: MouseEvent): void {
    if (e.target === e.currentTarget) this.closed.emit();
  }

  openDelete(): void {
    this.scope.set('occurrence');
    this.confirming.set(true);
  }

  /** Escape closes the delete confirmation only; the detail stays open. */
  @HostListener('document:keydown.escape')
  onEscape(): void {
    if (this.confirming()) this.confirming.set(false);
  }

  openTask(): void {
    const taskId = this.item().task_id;
    if (!taskId) return;
    this.router.navigate(['/tasks', taskId, 'edit']);
  }
}
