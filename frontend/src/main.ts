import { bootstrapApplication } from '@angular/platform-browser';
import { AppComponent } from './app/app.component';
import { provideRouter } from '@angular/router';
import { provideHttpClient, withInterceptors } from '@angular/common/http';
import { provideAnimations } from '@angular/platform-browser/animations';
import { APP_INITIALIZER, ErrorHandler, isDevMode, provideZoneChangeDetection } from '@angular/core';
import { provideServiceWorker } from '@angular/service-worker';
import { provideTransloco } from '@ngneat/transloco';
import { catchError, firstValueFrom, of } from 'rxjs';

import { routes } from './app/app.routes';
import { authInterceptor } from './app/core/api/auth.interceptor';
import { ApiClient } from './app/core/api/api-client.service';
import { TranslocoHttpLoader } from './app/core/i18n/transloco-loader';
import { GlobalErrorHandler } from './app/core/errors/global-error-handler';

bootstrapApplication(AppComponent, {
  providers: [
    provideZoneChangeDetection(),provideRouter(routes),
    { provide: ErrorHandler, useClass: GlobalErrorHandler },
    provideHttpClient(withInterceptors([authInterceptor])),
    // Issue a GET first so the CSRF token header is captured before any POST (e.g. direct /login visit).
    {
      provide: APP_INITIALIZER,
      multi: true,
      deps: [ApiClient],
      useFactory: (api: ApiClient) => () =>
        firstValueFrom(api.get('/api/v1/health/live').pipe(catchError(() => of(null)))),
    },
    provideAnimations(),
    provideServiceWorker('ngsw-worker.js', {
      enabled: !isDevMode(),
      registrationStrategy: 'registerWhenStable:30000',
    }),
    provideTransloco({
      config: {
        availableLangs: ['en', 'en-XA'],
        defaultLang: 'en',
        reRenderOnLangChange: true,
        prodMode: !isDevMode(),
      },
      loader: TranslocoHttpLoader,
    }),
  ],
}).catch((err) => console.error(err));

