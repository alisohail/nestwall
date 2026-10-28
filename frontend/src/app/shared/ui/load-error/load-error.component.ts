import { Component, computed, input, output } from '@angular/core';
import { TranslocoModule } from '@ngneat/transloco';
import { ApiError } from '../../../core/api/api-client.service';
import { ErrorPageComponent } from '../error-page/error-page.component';

interface LoadErrorReason {
  icon: string;
  titleKey: string;
  bodyKey: string;
}

/**
 * Full-page screen shown when a page's data cannot load. Explains the reason
 * (network, access, missing, server or other) and offers a retry.
 */
@Component({
    selector: 'app-load-error',
    host: { role: 'alert' },
    imports: [TranslocoModule, ErrorPageComponent],
    template: `
    <app-error-page
      [icon]="reason().icon"
      tone="danger"
      [code]="code()"
      [titleKey]="reason().titleKey"
      [bodyKey]="reason().bodyKey"
      [reference]="reference()"
    >
      <button type="button" class="btn primary" data-testid="load-error-retry" (click)="retry.emit()">
        {{ 'errors.actions.try_again' | transloco }}
      </button>
    </app-error-page>
  `
})
export class LoadErrorComponent {
  readonly error = input<ApiError | null | undefined>(null);
  readonly retry = output<void>();

  readonly reason = computed<LoadErrorReason>(() => {
    const status = this.error()?.status;
    if (!status) return { icon: 'wifi_off', titleKey: 'errors.load.network.title', bodyKey: 'errors.load.network.body' };
    if (status === 401 || status === 403) return { icon: 'lock', titleKey: 'errors.load.forbidden.title', bodyKey: 'errors.load.forbidden.body' };
    if (status === 404) return { icon: 'search_off', titleKey: 'errors.load.not_found.title', bodyKey: 'errors.load.not_found.body' };
    if (status >= 500) return { icon: 'error_outline', titleKey: 'errors.load.server.title', bodyKey: 'errors.load.server.body' };
    return { icon: 'error_outline', titleKey: 'errors.load.generic.title', bodyKey: 'errors.load.generic.body' };
  });

  readonly code = computed(() => String(this.error()?.status ?? 'NETWORK'));

  readonly reference = computed(() => {
    const err = this.error();
    return err ? `${err.status ?? 0} ${err.code}` : 'network';
  });
}
