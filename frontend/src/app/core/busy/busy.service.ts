import { Injectable, computed, signal } from '@angular/core';

/** Text for the top error bar. Either the message already shown on the page, or a translation key. */
export interface SaveFailure {
  message?: string;
  key?: string;
}

/**
 * Counts saves in flight. While any save is running, the app shows a blocking
 * "Please wait" overlay. A failed save copies its error into a bar at the top
 * of the screen and scrolls the inline message into view.
 */
@Injectable({
  providedIn: 'root',
})
export class BusyService {
  private readonly pending = signal(0);

  readonly busy = computed(() => this.pending() > 0);

  /** The last failed save, shown in the top error bar until dismissed or the next save starts. */
  readonly saveFailure = signal<SaveFailure | null>(null);

  begin(): void {
    this.saveFailure.set(null);
    this.pending.update((count) => count + 1);
  }

  end(failed: boolean): void {
    this.pending.update((count) => Math.max(0, count - 1));
    if (failed) {
      // The failing component sets its error message after the request errors, so wait for that render.
      setTimeout(() => this.reportFailure());
    }
  }

  dismissFailure(): void {
    this.saveFailure.set(null);
  }

  private reportFailure(): void {
    const alerts = Array.from(document.querySelectorAll<HTMLElement>('[role="alert"]'));
    const inline = alerts.find((el) => !el.closest('.modal-bg'));
    const message = alerts[0]?.textContent?.trim();
    this.saveFailure.set(message ? { message } : { key: 'common.save_failed' });
    inline?.scrollIntoView({ behavior: 'smooth', block: 'center' });
  }
}
