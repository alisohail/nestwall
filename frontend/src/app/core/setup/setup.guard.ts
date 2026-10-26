import { inject } from '@angular/core';
import { CanActivateFn, Router } from '@angular/router';
import { SetupService } from './setup.service';
import { map, catchError, of } from 'rxjs';

/** Opens /setup only while no family exists. Otherwise, or if the status is unknown, redirects to /login. */
export const setupGuard: CanActivateFn = () => {
  const setupService = inject(SetupService);
  const router = inject(Router);
  const toLogin = router.createUrlTree(['/login']);

  return setupService.getStatus().pipe(
    map((status) => (status.setup_required ? true : toLogin)),
    catchError(() => of(toLogin))
  );
};
