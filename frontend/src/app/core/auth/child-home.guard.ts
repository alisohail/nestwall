import { inject } from '@angular/core';
import { CanActivateFn, Router } from '@angular/router';
import { AuthService } from './auth.service';

/** Sends child members from the parent dashboard to their own Today screen. */
export const childHomeGuard: CanActivateFn = () => {
  const auth = inject(AuthService);
  return auth.isChild() ? inject(Router).createUrlTree(['/today']) : true;
};
