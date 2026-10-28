import { Injectable, inject, signal, computed } from '@angular/core';
import { ApiClient } from '../api/api-client.service';
import { ThemePreference, ThemeService } from '../theme/theme.service';
import { Observable, of, tap, map, catchError, switchMap } from 'rxjs';

export interface UserResponse {
  id: string;
  user_id: string;
  email: string | null;
  display_name: string;
  is_child: boolean;
  must_change_password: boolean;
  date_of_birth?: string | null;
  gender?: string | null;
  /** Saved theme choice. Optional so older fixtures still type-check. */
  theme?: ThemePreference;
}

export interface FamilyMembership {
  member_id: string;
  family_id: string;
  family_name: string;
  /** The family's IANA time zone. Optional so older fixtures still type-check. */
  timezone?: string;
  /** Family clock style. Optional so older fixtures still type-check; absent means 12h. */
  time_format?: '12h' | '24h';
  role_id: string;
  role_name: string;
  status: string;
  colour: string;
  avatar_id: string | null;
  is_parent: boolean;
}

export interface MeResponse {
  user: UserResponse;
  family: FamilyMembership | null;
  /** Build version of the server, shown in the navigation. */
  version?: string;
}

export interface LoginRequest {
  user_id: string;
  password: string;
}

export interface ChangePasswordRequest {
  current_password: string;
  new_password: string;
}

/** Body of PUT /api/v1/me/profile. Every field is sent, empty strings clear optional values. */
export interface OwnProfileRequest {
  display_name: string;
  email: string;
  date_of_birth: string;
  gender: string;
}

@Injectable({ providedIn: 'root' })
export class AuthService {
  private api = inject(ApiClient);
  private theme = inject(ThemeService);

  /** undefined = not yet checked; null = unauthenticated */
  currentUser = signal<MeResponse | null | undefined>(undefined);

  isAuthenticated = computed(() => {
    const u = this.currentUser();
    return u !== null && u !== undefined;
  });

  isChild = computed(() => this.currentUser()?.user.is_child ?? false);

  /** True for the Parent role. Parent-only screens use this rather than a permission code. */
  isParent = computed(() => this.currentUser()?.family?.is_parent ?? false);

  /** Permission codes the signed-in member holds. Empty until loadPermissions() succeeds. */
  private grants = signal<ReadonlySet<string>>(new Set());

  /**
   * Set by login() when a parent has no email yet, so the shell can ask once.
   * A page refresh goes through checkAuth() and does not set it again.
   */
  emailPromptPending = signal(false);

  dismissEmailPrompt(): void {
    this.emailPromptPending.set(false);
  }

  /**
   * GET /auth/permissions. Call it on screens that show actions gated by a permission.
   * On failure every action stays disabled (fail closed).
   */
  loadPermissions(): Observable<void> {
    return this.api.get<{ permissions: string[] }>('/api/v1/auth/permissions').pipe(
      tap((res) => this.grants.set(new Set(res.permissions))),
      map(() => undefined),
      catchError(() => {
        this.grants.set(new Set());
        return of(undefined);
      })
    );
  }

  /** True when the signed-in member holds the permission code. Only a UI hint; the server still enforces it. */
  can(code: string): boolean {
    return this.grants().has(code);
  }

  checkAuth(): Observable<boolean> {
    return this.api.get<MeResponse>('/api/v1/auth/me').pipe(
      tap((res) => this.setMe(res)),
      map(() => true),
      catchError(() => {
        this.currentUser.set(null);
        return of(false);
      })
    );
  }

  /**
   * POST /auth/login then GET /auth/me to hydrate the full user + family
   * state in one call chain. The session cookie is set by the server.
   */
  login(req: LoginRequest): Observable<MeResponse> {
    return this.api.post<{ user: UserResponse }>('/api/v1/auth/login', req).pipe(
      switchMap(() => this.api.get<MeResponse>('/api/v1/auth/me')),
      tap((me) => {
        this.setMe(me);
        this.emailPromptPending.set(!!me.family?.is_parent && !me.user.email);
      })
    );
  }

  /** Stores the session and applies the user's saved theme. */
  private setMe(me: MeResponse): void {
    this.currentUser.set(me);
    if (me.user.theme) this.theme.applyPreference(me.user.theme);
  }

  /**
   * PUT /auth/me/theme. Saves the user's light or dark choice on the server.
   * The caller applies the choice on this device first.
   */
  saveTheme(theme: ThemePreference): Observable<void> {
    return this.api.put<void>('/api/v1/auth/me/theme', { theme }, undefined, { silent: true }).pipe(
      tap(() =>
        this.currentUser.update((me) =>
          me ? { ...me, user: { ...me.user, theme } } : me
        )
      )
    );
  }

  /**
   * POST /auth/change-password. Clears the must-change flag on success and
   * signs out the other sessions. The current session stays signed in.
   */
  changePassword(req: ChangePasswordRequest): Observable<void> {
    return this.api.post<void>('/api/v1/auth/change-password', req).pipe(
      tap(() =>
        this.currentUser.update((me) =>
          me ? { ...me, user: { ...me.user, must_change_password: false } } : me
        )
      )
    );
  }

  /** Keeps the family name shown in the app in step with a rename. */
  setFamilyName(familyName: string): void {
    this.currentUser.update((me) =>
      me && me.family ? { ...me, family: { ...me.family, family_name: familyName } } : me
    );
  }

  setTimeFormat(timeFormat: '12h' | '24h'): void {
    this.currentUser.update((me) =>
      me && me.family ? { ...me, family: { ...me.family, time_format: timeFormat } } : me
    );
  }

  /**
   * PUT /api/v1/me/profile. Any signed-in member can edit their own details.
   * The server requires an email for parents. Reloads the session so the shell shows the new name.
   */
  updateProfile(req: OwnProfileRequest): Observable<void> {
    return this.api.put<void>('/api/v1/me/profile', req).pipe(
      switchMap(() => this.checkAuth()),
      map(() => undefined)
    );
  }

  logout(): Observable<void> {
    return this.api.post<void>('/api/v1/auth/logout', {}).pipe(
      tap(() => this.clearSession()),
      catchError(() => {
        this.clearSession();
        return of(undefined);
      })
    );
  }

  private clearSession(): void {
    this.currentUser.set(null);
    this.grants.set(new Set());
    this.emailPromptPending.set(false);
  }
}

