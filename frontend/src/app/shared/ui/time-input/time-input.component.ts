import { ChangeDetectionStrategy, Component, computed, ElementRef, forwardRef, HostListener, inject, input, signal } from '@angular/core';
import { ControlValueAccessor, NG_VALUE_ACCESSOR } from '@angular/forms';
import { TranslocoModule, TranslocoService } from '@ngneat/transloco';

import { TimeFormatService } from '../../../core/time-format/time-format.service';

const CLOCK = /^([01]\d|2[0-3]):([0-5]\d)$/;
/** Typed time such as 9:30, 21:30, 9:30 pm or 9:30a. */
const TYPED = /^\s*(\d{1,2})(?::(\d{2}))?\s*([ap]m?)?\s*$/i;
const MINUTE_STEP = 5;
const pad = (n: number): string => String(n).padStart(2, '0');

/**
 * Time of day typed into a field, or set with steppers in a popup that opens from the field.
 * The family's 12h or 24h setting decides the popup layout. The value is always 24-hour HH:mm,
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
  private readonly host = inject<ElementRef<HTMLElement>>(ElementRef);
  private readonly transloco = inject(TranslocoService);
  readonly twelveHour = this.clock.twelveHour;

  /** Accessible name for the group, for example the field label. */
  readonly label = input.required<string>();
  /** Id applied to the hour menu so a visible label can point at it. */
  readonly inputId = input<string | null>(null);
  /** Id of the hint or error text that describes the hour menu. */
  readonly describedBy = input<string | null>(null);
  readonly invalid = input(false);

  /** 24-hour hour and minute. Empty string means not chosen yet. */
  readonly hour = signal('');
  readonly minute = signal('');
  /** In 12-hour mode, true for PM. Kept apart from the hour so choosing AM/PM first still sticks. */
  readonly pm = signal(false);
  readonly disabled = signal(false);
  readonly open = signal(false);
  /** Text the user is typing. Null when the field shows the chosen time. */
  readonly draft = signal<string | null>(null);

  /** The hour in the family's clock style. */
  readonly hourSelect = computed(() => {
    const hour = this.hour();
    if (!hour) return '';
    return this.twelveHour() ? pad(Number(hour) % 12 || 12) : hour;
  });

  readonly displayText = computed(() => {
    const text = this.draft();
    if (text !== null) return text;
    if (!this.hour()) return '';
    if (!this.twelveHour()) return `${this.hour()}:${this.minute()}`;
    const period = this.transloco.translate(this.pm() ? 'common.time_pm' : 'common.time_am');
    return `${this.hourSelect()}:${this.minute()} ${period}`;
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

  setPeriod(isPm: boolean): void {
    this.pm.set(isPm);
    if (this.hour()) this.hour.set(this.toTwentyFourHour(this.hourSelect()));
    this.commit();
  }

  clear(): void {
    this.hour.set('');
    this.minute.set('');
    this.commit();
    this.close();
  }

  openPanel(): void {
    if (this.disabled()) return;
    this.open.set(true);
  }

  close(): void {
    this.open.set(false);
  }

  onInput(text: string): void {
    this.draft.set(text);
  }

  onEnter(): void {
    this.commitText();
    this.close();
  }

  /** Applies typed text such as 9:30, 21:30 or 9:30 pm. Text that cannot be read is dropped. */
  commitText(): void {
    const text = this.draft();
    if (text === null) return;
    this.draft.set(null);
    if (!text.trim()) {
      this.hour.set('');
      this.minute.set('');
      this.commit();
      return;
    }
    const match = TYPED.exec(text);
    if (!match) return;
    let hour = Number(match[1]);
    const minute = Number(match[2] ?? 0);
    const period = match[3]?.toLowerCase();
    if (minute > 59) return;
    if (period) {
      if (hour < 1 || hour > 12) return;
      hour = (hour % 12) + (period.startsWith('p') ? 12 : 0);
    } else if (this.twelveHour() && hour <= 12) {
      hour = (hour % 12) + (this.pm() ? 12 : 0);
    } else if (hour > 23) {
      return;
    }
    this.pm.set(hour >= 12);
    this.hour.set(pad(hour));
    this.minute.set(pad(minute));
    this.commit();
  }

  /** Up or down one hour. In 12-hour mode the hour wraps inside its AM or PM half. */
  stepHour(delta: number): void {
    if (!this.hour()) {
      this.ensureTime();
      this.commit();
      return;
    }
    this.shiftHour(delta);
    this.commit();
  }

  /** Up or down one 5-minute step. Off-step minutes snap to the next step in that direction, and passing 59 or 00 moves the hour. */
  stepMinute(delta: number): void {
    this.ensureTime();
    const minute = Number(this.minute());
    const next =
      delta > 0
        ? (Math.floor(minute / MINUTE_STEP) * MINUTE_STEP + MINUTE_STEP) % 60
        : (Math.ceil(minute / MINUTE_STEP) * MINUTE_STEP - MINUTE_STEP + 60) % 60;
    const wrapped = delta > 0 ? next <= minute : next >= minute;
    if (wrapped) this.shiftHour(delta);
    this.minute.set(pad(next));
    this.commit();
  }

  @HostListener('document:pointerdown', ['$event.target'])
  onOutsidePointer(target: EventTarget | null): void {
    if (this.open() && !this.host.nativeElement.contains(target as Node)) this.close();
  }

  /** Closes when focus moves to something outside the field, such as the next form field. */
  onFocusOut(event: FocusEvent): void {
    const next = event.relatedTarget as Node | null;
    if (next && !this.host.nativeElement.contains(next)) this.close();
  }

  @HostListener('keydown.escape')
  onEscape(): void {
    if (this.open()) this.close();
  }

  /** Moves the hour without committing. In 12-hour mode it wraps inside its AM or PM half. */
  private shiftHour(delta: number): void {
    if (this.twelveHour()) {
      const next = ((Number(this.hourSelect()) - 1 + delta + 12) % 12) + 1;
      this.hour.set(this.toTwentyFourHour(pad(next)));
    } else {
      this.hour.set(pad((Number(this.hour()) + delta + 24) % 24));
    }
  }

  /** Sets a default time when none is chosen yet, so the first step has something to move from. */
  private ensureTime(): void {
    if (!this.hour()) this.hour.set(this.toTwentyFourHour('09'));
    if (!this.minute()) this.minute.set('00');
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

