import { Injectable, computed, effect, inject } from '@angular/core';
import { AuthService } from '../auth/auth.service';
import { setTwelveHour } from './clock-style';

/**
 * The family's clock style. Display code uses this so every page shows the same format.
 * Parsing code that reads values back (for example a stored HH:mm) stays 24-hour on purpose.
 */
@Injectable({ providedIn: 'root' })
export class TimeFormatService {
  private auth = inject(AuthService);

  /** True for 12-hour (AM/PM). Families default to 12h when the setting is absent. */
  readonly twelveHour = computed(() => this.auth.currentUser?.()?.family?.time_format !== '24h');

  constructor() {
    effect(() => setTwelveHour(this.twelveHour()));
  }

  /** Intl options with hour12 set to match the family setting. */
  intl(options: Intl.DateTimeFormatOptions): Intl.DateTimeFormatOptions {
    return { ...options, hour12: this.twelveHour() };
  }

  /** Angular date pipe format for a time only, for example 3:05 PM or 15:05. */
  pipeTime(): string {
    return this.twelveHour() ? 'h:mm a' : 'HH:mm';
  }

  /** Angular date pipe format for a weekday, date and time, for example Mon 4 Nov, 3:05 PM. */
  pipeDayTime(): string {
    return this.twelveHour() ? 'EEE d MMM, h:mm a' : 'EEE d MMM, HH:mm';
  }

  /** Angular date pipe format for a date and time, for example Nov 4, 2026, 3:05 PM. */
  pipeDateTime(): string {
    return this.twelveHour() ? 'MMM d, y, h:mm a' : 'MMM d, y, HH:mm';
  }
}
