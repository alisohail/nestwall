import type { Mock } from "vitest";
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Signal, WritableSignal, computed, signal } from '@angular/core';
import { provideRouter } from '@angular/router';
import { of } from 'rxjs';
import { getTranslocoTestingModule } from '../../core/transloco/transloco-testing.module';
import { AuthService, MeResponse } from '../../core/auth/auth.service';
import { AvatarService } from '../../shared/avatar/avatar.service';
import { SessionsService } from '../sessions/sessions.service';
import { ProfileComponent } from './profile.component';

const parentMe: MeResponse = {
    user: { id: 'u1', user_id: 'sam', email: 'sam@example.com', display_name: 'Sam', is_child: false, must_change_password: false },
    family: {
        member_id: 'm1',
        family_id: 'f1',
        family_name: 'Rivera',
        role_id: 'r-parent',
        role_name: 'Parent',
        status: 'active',
        colour: '#2563eb',
        avatar_id: null,
        is_parent: true,
    },
};

describe('ProfileComponent', () => {
    let fixture: ComponentFixture<ProfileComponent>;
    let me: WritableSignal<MeResponse | null | undefined>;
    let authSpy: {
        currentUser: WritableSignal<MeResponse | null | undefined>;
        isChild: Signal<boolean>;
        checkAuth: Mock;
    };

    beforeEach(async () => {
        me = signal<MeResponse | null | undefined>(parentMe);
        authSpy = {
            currentUser: me,
            isChild: computed(() => me()?.user.is_child === true),
            checkAuth: vi.fn().mockName('checkAuth').mockReturnValue(of(true)),
        };

        const sessionsSpy = {
            listOwn: vi.fn().mockName("SessionsService.listOwn"),
            listForMember: vi.fn().mockName("SessionsService.listForMember"),
            revokeOwn: vi.fn().mockName("SessionsService.revokeOwn"),
            revokeOthers: vi.fn().mockName("SessionsService.revokeOthers"),
            revokeAllForMember: vi.fn().mockName("SessionsService.revokeAllForMember")
        };
        sessionsSpy.listOwn.mockReturnValue(of({ sessions: [] }));

        const avatarSpy = {
            url: vi.fn().mockName("AvatarService.url")
        };
        avatarSpy.url.mockImplementation((id: string, size: 96 | 256 = 96) => `/api/v1/avatars/${id}?size=${size}`);

        await TestBed.configureTestingModule({
            imports: [ProfileComponent, getTranslocoTestingModule()],
            providers: [
                provideRouter([]),
                { provide: AuthService, useValue: authSpy },
                { provide: SessionsService, useValue: sessionsSpy },
                { provide: AvatarService, useValue: avatarSpy },
            ],
        }).compileComponents();

        fixture = TestBed.createComponent(ProfileComponent);
        fixture.detectChanges();
    });

    const el = (selector: string): HTMLElement | null => fixture.nativeElement.querySelector(selector);

    it('should title the page Profile', () => {
        expect(el('h1')?.textContent?.trim()).toBe('Profile');
    });

    it('should show the hero with the name and role with family name', () => {
        expect(el('[data-testid="profile-hero"] h2')?.textContent?.trim()).toBe('Sam');
        expect(el('[data-testid="profile-hero"] p')?.textContent?.trim()).toBe('Parent · Rivera');
    });

    it('should open the photo editor from the avatar edit button', () => {
        (el('[data-testid="me-edit-picture"]') as HTMLButtonElement).click();
        fixture.detectChanges();
        expect(el('[data-testid="photo-editor"]')).toBeTruthy();
        expect(el('#photo-dialog-title')?.textContent?.trim()).toBe('My picture');
    });

    it('should show the my details form', () => {
        expect(el('[data-testid="me-details"] [data-testid="my-details-form"]')).toBeTruthy();
    });

    it('should show the change password card', () => {
        expect(el('[data-testid="me-password"] [data-testid="own-password"]')).toBeTruthy();
    });

    it('should show the signed-in devices section', () => {
        expect(el('app-sessions')).toBeTruthy();
    });

    it('should not show the kid-only points, badges or share blocks', () => {
        expect(el('app-my-points')).toBeNull();
        expect(el('app-my-badges-card')).toBeNull();
        expect(el('[data-testid="me-share"]')).toBeNull();
        expect(el('[data-testid="me-picture"]')).toBeNull();
    });

    it('should hide the hero and details when the user has no family membership', () => {
        me.set({ user: parentMe.user, family: null });
        fixture.detectChanges();
        expect(el('[data-testid="profile-hero"]')).toBeNull();
        expect(el('[data-testid="me-details"]')).toBeNull();
        expect(el('[data-testid="me-password"]')).toBeTruthy();
    });

    it('should refresh the session after the picture changes', () => {
        fixture.componentInstance.onPictureChanged();
        expect(authSpy.checkAuth).toHaveBeenCalled();
    });
});
