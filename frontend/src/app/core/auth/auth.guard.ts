import { inject } from '@angular/core';
import { CanActivateFn, Router, RouterStateSnapshot } from '@angular/router';
import { AuthService, MeResponse } from './auth.service';
import { map, catchError, of } from 'rxjs';

export const CHANGE_PASSWORD_PATH = '/change-password';

export const authGuard: CanActivateFn = (_route, state) => {
  const authService = inject(AuthService);
  const router = inject(Router);

  const currentUser = authService.currentUser();

  // If already loaded/checked
  if (currentUser !== undefined) {
    return resolveAccess(currentUser, state, router);
  }

  // Otherwise check auth with server
  return authService.checkAuth().pipe(
    map((isAuth) => resolveAccess(isAuth ? authService.currentUser() : null, state, router)),
    catchError(() => {
      router.navigate(['/login']);
      return of(false);
    })
  );
};

/**
 * Sends a signed-in user whose password was set by someone else to the
 * change-password screen. The check is skipped on that screen to avoid a loop.
 */
function resolveAccess(
  me: MeResponse | null | undefined,
  state: RouterStateSnapshot,
  router: Router
): boolean {
  if (!me) {
    router.navigate(['/login']);
    return false;
  }
  if (me.user.must_change_password && !state.url.startsWith(CHANGE_PASSWORD_PATH)) {
    router.navigate([CHANGE_PASSWORD_PATH]);
    return false;
  }
  return true;
}
