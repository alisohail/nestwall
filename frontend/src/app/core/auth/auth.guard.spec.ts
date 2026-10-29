import type { MockedObject } from "vitest";
import { TestBed } from '@angular/core/testing';
import { ActivatedRouteSnapshot, Router, RouterStateSnapshot } from '@angular/router';
import { signal, WritableSignal } from '@angular/core';
import { authGuard } from './auth.guard';
import { AuthService, MeResponse } from './auth.service';
import { mockOf } from '../../testing/mock-of';

const user = (mustChange: boolean): MeResponse => ({
    user: { id: 'u1', user_id: 'ava', email: null, display_name: 'Ava', is_child: true, must_change_password: mustChange },
    family: null,
});

describe('authGuard', () => {
    let currentUser: WritableSignal<MeResponse | null | undefined>;
    let routerSpy: MockedObject<Router>;
    const route = {} as ActivatedRouteSnapshot;

    function run(url: string): unknown {
        return TestBed.runInInjectionContext(() => authGuard(route, { url } as RouterStateSnapshot));
    }

    beforeEach(() => {
        currentUser = signal<MeResponse | null | undefined>(undefined);
        routerSpy = mockOf<Router>({
            navigate: vi.fn().mockName("Router.navigate")
        });
        TestBed.configureTestingModule({
            providers: [
                { provide: AuthService, useValue: { currentUser, checkAuth: () => undefined } },
                { provide: Router, useValue: routerSpy },
            ],
        });
    });

    it('should allow a signed-in user with no pending password change', () => {
        currentUser.set(user(false));
        expect(run('/today')).toBe(true);
        expect(routerSpy.navigate).not.toHaveBeenCalled();
    });

    it('should send a user with a temporary password to /change-password', () => {
        currentUser.set(user(true));
        expect(run('/today')).toBe(false);
        expect(routerSpy.navigate).toHaveBeenCalledWith(['/change-password']);
    });

    it('should let the change-password screen load for a user who must change', () => {
        currentUser.set(user(true));
        expect(run('/change-password')).toBe(true);
        expect(routerSpy.navigate).not.toHaveBeenCalled();
    });

    it('should send an unauthenticated user to /login', () => {
        currentUser.set(null);
        expect(run('/dashboard')).toBe(false);
        expect(routerSpy.navigate).toHaveBeenCalledWith(['/login']);
    });
});
