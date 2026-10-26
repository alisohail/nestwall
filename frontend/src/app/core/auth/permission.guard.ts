import { inject } from '@angular/core';
import { CanActivateFn, Router, UrlTree } from '@angular/router';
import { map, of, switchMap } from 'rxjs';
import { AuthService } from './auth.service';

/**
 * Lets a route open only when the signed-in member holds at least one of the codes.
 * Otherwise sends them to their home screen. Use it after authGuard.
 */
export function anyPermissionGuard(...codes: string[]): CanActivateFn {
  return () => {
    const auth = inject(AuthService);
    const router = inject(Router);
    const user$ = auth.currentUser() === undefined ? auth.checkAuth() : of(true);

    return user$.pipe(
      switchMap(() => auth.loadPermissions()),
      map((): boolean | UrlTree => {
        if (!auth.isAuthenticated()) return false;
        if (codes.some((code) => auth.can(code))) return true;
        return router.createUrlTree([auth.isChild() ? '/today' : '/dashboard']);
      })
    );
  };
}
