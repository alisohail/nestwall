import { Component, computed, input, output } from '@angular/core';
import { TranslocoModule } from '@ngneat/transloco';
import { IconComponent } from '../../../shared/ui/icon/icon.component';
import { clockIn } from '../calendar.dates';
import type { MonthPill } from '../calendar.component';

/** Every item on one day, opened from "+N more". Picking an item hands it back to the month page. */
@Component({
  selector: 'app-day-dialog',
  standalone: true,
  imports: [TranslocoModule, IconComponent],
  templateUrl: './day-dialog.component.html',
  styleUrl: './day-dialog.component.scss',
})
export class DayDialogComponent {
  readonly iso = input.required<string>();
  readonly pills = input.required<MonthPill[]>();
  readonly zone = input<string | undefined>(undefined);

  readonly picked = output<MonthPill>();
  readonly closed = output<void>();

  readonly weekday = computed(() =>
    new Intl.DateTimeFormat(undefined, { weekday: 'short', timeZone: 'UTC' })
      .format(new Date(`${this.iso()}T00:00:00Z`))
      .toUpperCase(),
  );

  readonly dayNumber = computed(() => Number(this.iso().slice(8)));

  timeOf(pill: MonthPill): string {
    return clockIn(pill.item.starts_at, pill.item.time_zone || this.zone());
  }

  onBackdrop(e: MouseEvent): void {
    if (e.target === e.currentTarget) this.closed.emit();
  }
}
