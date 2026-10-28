import { Component, computed, input, model } from '@angular/core';
import { TranslocoModule } from '@ngneat/transloco';
import { BannerComponent } from '../../../../shared/ui/banner/banner.component';
import { formatDay } from '../../tasks.format';
import { RepeatFreq, RepeatState, previewDates, repeatProblem } from '../recurrence';

const FREQS: RepeatFreq[] = ['none', 'daily', 'weekly', 'monthly', 'yearly'];
/** Monday first, matching the mock. Values are JavaScript weekdays (0 = Sunday). */
const WEEK_ORDER = [1, 2, 3, 4, 5, 6, 0];
const WEEKDAYS = [1, 2, 3, 4, 5];
const WEEKENDS = [6, 0];
const EVERY_DAY = [0, 1, 2, 3, 4, 5, 6];

/** Repeat rule editor with a live list of the next dates the series creates. */
@Component({
    selector: 'app-repeat-builder',
    imports: [TranslocoModule, BannerComponent],
    templateUrl: './repeat-builder.component.html',
    styleUrl: './repeat-builder.component.scss'
})
export class RepeatBuilderComponent {
  state = model.required<RepeatState>();
  startDate = input.required<string>();

  readonly freqs = FREQS;
  readonly weekOrder = WEEK_ORDER;
  readonly nths = [1, 2, 3, 4, 5];

  problem = computed(() => (this.state().freq === 'none' ? null : repeatProblem(this.state(), this.startDate())));
  next = computed(() => (this.state().freq === 'none' || this.problem() ? [] : previewDates(this.state(), this.startDate(), 6)));

  patch(part: Partial<RepeatState>): void {
    this.state.update((s) => ({ ...s, ...part }));
  }

  setFreq(freq: RepeatFreq): void {
    this.patch({ freq });
  }

  toggleDay(day: number): void {
    const days = this.state().weekdays;
    this.patch({ weekdays: days.includes(day) ? days.filter((d) => d !== day) : [...days, day] });
  }

  preset(days: number[]): void {
    this.patch({ weekdays: [...days] });
  }

  dayLabel(day: number): string {
    return new Intl.DateTimeFormat(undefined, { weekday: 'short', timeZone: 'UTC' }).format(new Date(Date.UTC(2024, 0, 7 + day)));
  }

  dateLabel(iso: string): string {
    return formatDay(iso);
  }

  num(event: Event): number {
    return Number((event.target as HTMLInputElement).value);
  }

  text(event: Event): string {
    return (event.target as HTMLInputElement).value;
  }

  isPresetOn(days: number[]): boolean {
    const current = this.state().weekdays;
    return current.length === days.length && days.every((d) => current.includes(d));
  }

  readonly presets = { weekdays: WEEKDAYS, weekends: WEEKENDS, everyDay: EVERY_DAY };
}
