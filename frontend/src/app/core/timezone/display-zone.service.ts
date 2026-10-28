import { Injectable, signal } from '@angular/core';

/**
 * Per-device choice for clock times. "family" shows times in the family timezone;
 * "device" shows them in this browser's timezone. Dates, scheduling and the wall
 * display always use the family timezone.
 */
export type DisplayZonePreference = 'family' | 'device';

const STORAGE_KEY = 'nestwall.displayZone';

@Injectable({ providedIn: 'root' })
export class DisplayZoneService {
  readonly preference = signal<DisplayZonePreference>(this.read());

  /** Browser timezone, or 'UTC' when the browser does not report one. */
  readonly deviceZone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';

  /**
   * Zone to pass to clock formatting. Undefined means the browser's zone,
   * so device mode returns undefined and family mode returns the family zone.
   */
  clockZone(familyZone?: string): string | undefined {
    return this.preference() === 'device' ? undefined : familyZone;
  }

  set(pref: DisplayZonePreference): void {
    this.preference.set(pref);
    try {
      localStorage.setItem(STORAGE_KEY, pref);
    } catch {
      // Storage can be unavailable (private mode); the choice then lasts for this page only.
    }
  }

  private read(): DisplayZonePreference {
    try {
      return localStorage.getItem(STORAGE_KEY) === 'device' ? 'device' : 'family';
    } catch {
      return 'family';
    }
  }
}
