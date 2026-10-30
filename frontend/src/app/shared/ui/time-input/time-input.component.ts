import { ChangeDetectionStrategy, Component, computed, forwardRef, inject, input, signal } from '@angular/core';
import { ControlValueAccessor, NG_VALUE_ACCESSOR } from '@angular/forms';
import { TranslocoModule } from '@ngneat/transloco';

import { TimeFormatService } from '../../../core/time-format/time-format.service';

const CLOCK = /^([01]\d|2[0-3]):([0-5]\d)$/;
const pad = (n: number): string => String(n).padStart(2, '0');

/**
 * Time of day chosen from hour, minute and AM/PM menus. Native time inputs follow the phone's clock,
 * so this control follows the family's 12h or 24h setting instead. The value is always 24-hour HH:mm,
 * or an empty string until both hour and minute are chosen.
 */
@Component({
    selector: 'app-time-input',
    imports: [TranslocoModule],
    templateUrl: './time-input.component.html',
    styleUrl: './time-input.component.scss',
    changeDetection: ChangeDetectionStrategy.Eager,
    providers: [{ provide: NG_VALUE_ACCESSOR, useExisting: forwardRef(() => TimeInputComponent), multi: true }]
})
export class TimeInputComponent implements ControlValueAccessor {
  private readonly clock = inject(TimeFormatService);
  readonly twelveHour = this.clock.twelveHour;

  /** Accessible name for the group, for example the field label. */
  readonly label = input.required<string>();
  /** Id applied to the hour menu so a visible label can point at it. */
  readonly inputId = input<string | null>(null);
  /** Id of the hint or error text that describes the hour menu. */
  readonly describedBy = input<string | null>(null);
  readonly invalid = input(false);

  readonly minuteOptions: readonly string[] = Array.from({ length: 60 }, (_, i) => pad(i));
  readonly hourOptions = computed(() => {
    const twelve = this.twelveHour();
    return Array.from({ length: twelve ? 12 : 24 }, (_, i) => pad(twelve ? i + 1 : i));
  });

  /** 24-hour hour and minute. Empty string means not chosen yet. */
  private readonly hour = signal('');
  readonly minute = signal('');
  /** In 12-hour mode, true for PM. Kept apart from the hour so choosing AM/PM first still sticks. */
  readonly pm = signal(false);
  readonly disabled = signal(false);

  /** The hour menu value in the family's clock style. */
  readonly hourSelect = computed(() => {
    const hour = this.hour();
    if (!hour) return '';
    return this.twelveHour() ? pad(Number(hour) % 12 || 12) : hour;
  });

  private onChange: (value: string) => void = () => undefined;
  private onTouched: () => void = () => undefined;

  writeValue(value: string | null): void {
    const match = CLOCK.exec(value ?? '');
    this.hour.set(match?.[1] ?? '');
    this.minute.set(match?.[2] ?? '');
    this.pm.set(match ? Number(match[1]) >= 12 : false);
  }

  registerOnChange(fn: (value: string) => void): void {
    this.onChange = fn;
  }

  registerOnTouched(fn: () => void): void {
    this.onTouched = fn;
  }

  setDisabledState(isDisabled: boolean): void {
    this.disabled.set(isDisabled);
  }

  setHour(value: string): void {
    this.hour.set(value ? this.toTwentyFourHour(value) : '');
    if (value && !this.minute()) this.minute.set('00');
    this.commit();
  }

  setMinute(value: string): void {
    this.minute.set(value);
    this.commit();
  }

  setPeriod(isPm: boolean): void {
    this.pm.set(isPm);
    if (this.hour()) this.hour.set(this.toTwentyFourHour(this.hourSelect()));
    this.commit();
  }

  /** Converts a menu hour to 24-hour, using the current AM/PM choice in 12-hour mode. */
  private toTwentyFourHour(menuHour: string): string {
    if (!this.twelveHour()) return menuHour;
    return pad((Number(menuHour) % 12) + (this.pm() ? 12 : 0));
  }

  private commit(): void {
    this.onChange(this.hour() && this.minute() ? `${this.hour()}:${this.minute()}` : '');
    this.onTouched();
  }
}
