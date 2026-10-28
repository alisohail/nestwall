import { Component, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { SwUpdate } from '@angular/service-worker';
import { TranslocoModule } from '@ngneat/transloco';
import { filter, fromEvent } from 'rxjs';

const INSTALL_DISMISSED_KEY = 'nw-pwa-install-dismissed';

// Not in lib.dom yet; Chrome and Edge fire it when the app can be installed.
interface BeforeInstallPromptEvent extends Event {
  prompt(): Promise<void>;
}

/** Offers to install the app and to reload when a new version is ready. */
@Component({
    selector: 'app-pwa-prompts',
    imports: [TranslocoModule],
    templateUrl: './pwa-prompts.component.html',
    styleUrl: './pwa-prompts.component.scss'
})
export class PwaPromptsComponent {
  private readonly swUpdate = inject(SwUpdate);
  private installEvent: BeforeInstallPromptEvent | null = null;

  readonly installAvailable = signal(false);
  readonly updateReady = signal(false);

  constructor() {
    fromEvent<BeforeInstallPromptEvent>(window, 'beforeinstallprompt')
      .pipe(takeUntilDestroyed())
      .subscribe((event) => {
        event.preventDefault();
        if (localStorage.getItem(INSTALL_DISMISSED_KEY)) {
          return;
        }
        this.installEvent = event;
        this.installAvailable.set(true);
      });

    fromEvent(window, 'appinstalled')
      .pipe(takeUntilDestroyed())
      .subscribe(() => this.hideInstall());

    if (this.swUpdate.isEnabled) {
      this.swUpdate.versionUpdates
        .pipe(
          filter((event) => event.type === 'VERSION_READY'),
          takeUntilDestroyed(),
        )
        .subscribe(() => this.updateReady.set(true));
    }
  }

  async install(): Promise<void> {
    const event = this.installEvent;
    this.hideInstall();
    await event?.prompt();
  }

  dismissInstall(): void {
    localStorage.setItem(INSTALL_DISMISSED_KEY, '1');
    this.hideInstall();
  }

  reload(): void {
    document.location.reload();
  }

  dismissUpdate(): void {
    this.updateReady.set(false);
  }

  private hideInstall(): void {
    this.installEvent = null;
    this.installAvailable.set(false);
  }
}
