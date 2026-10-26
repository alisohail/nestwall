import { ErrorHandler, Injectable, Injector, inject } from '@angular/core';
import { Router } from '@angular/router';
import { errorStatus } from './error-codes';

/**
 * App-wide fallback for uncaught errors. An HTTP 403 opens the forbidden page and an HTTP 5xx
 * opens the server-error page. Other errors are logged only, so a harmless UI error does not
 * replace the screen. Screens that handle their own errors (inline banners) never reach this handler.
 */
@Injectable()
export class GlobalErrorHandler implements ErrorHandler {
  private readonly injector = inject(Injector);

  handleError(error: unknown): void {
    console.error(error);

    const target = this.targetFor(errorStatus(error));
    if (target) {
      void this.injector.get(Router).navigateByUrl(target);
    }
  }

  private targetFor(status: number | undefined): string | null {
    if (status === 403) {
      return '/forbidden';
    }
    if (status !== undefined && status >= 500) {
      return '/server-error';
    }
    return null;
  }
}
