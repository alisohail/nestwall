import { HttpInterceptorFn, HttpResponse } from '@angular/common/http';
import { inject } from '@angular/core';
import { ApiClient } from './api-client.service';
import { tap } from 'rxjs';

export const authInterceptor: HttpInterceptorFn = (req, next) => {
  const apiClient = inject(ApiClient);
  const token = apiClient.getCsrfToken();

  let modifiedReq = req.clone({
    withCredentials: true,
  });

  // Attach CSRF token to mutating requests if we have one
  const mutatingMethods = ['POST', 'PUT', 'DELETE', 'PATCH'];
  if (token && mutatingMethods.includes(req.method)) {
    modifiedReq = modifiedReq.clone({
      headers: modifiedReq.headers.set('X-CSRF-Token', token),
    });
  }

  return next(modifiedReq).pipe(
    tap({
      next: (event) => {
        if (event instanceof HttpResponse) {
          // Look for X-CSRF-Token in response headers
          if (event.headers.has('X-CSRF-Token')) {
            const newToken = event.headers.get('X-CSRF-Token');
            if (newToken) {
              apiClient.setCsrfToken(newToken);
            }
          }
        }
      },
    })
  );
};
