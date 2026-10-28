import type { MockedObject } from "vitest";
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { of, throwError } from 'rxjs';
import { getTranslocoTestingModule } from '../../core/transloco/transloco-testing.module';
import { AuthService } from '../../core/auth/auth.service';
import { mockOf } from '../../testing/mock-of';
import { OwnPasswordComponent } from './own-password.component';

describe('OwnPasswordComponent', () => {
    let fixture: ComponentFixture<OwnPasswordComponent>;
    let component: OwnPasswordComponent;
    let authSpy: MockedObject<AuthService>;

    beforeEach(async () => {
        authSpy = mockOf<AuthService>({
            changePassword: vi.fn().mockName("AuthService.changePassword")
        });

        await TestBed.configureTestingModule({
            imports: [OwnPasswordComponent, getTranslocoTestingModule()],
            providers: [{ provide: AuthService, useValue: authSpy }],
        }).compileComponents();

        fixture = TestBed.createComponent(OwnPasswordComponent);
        component = fixture.componentInstance;
        fixture.detectChanges();
    });

    function fill(current: string, next: string, confirm: string): void {
        component.form.setValue({
            current_password: current,
            new_password: next,
            confirm_password: confirm,
        });
    }

    it('should create', () => {
        expect(component).toBeTruthy();
    });

    it('should not call the API when the fields are empty', () => {
        component.submit();
        fixture.detectChanges();
        expect(authSpy.changePassword).not.toHaveBeenCalled();
        expect(fixture.nativeElement.querySelectorAll('.field-error').length).toBeGreaterThan(0);
    });

    it('should reject a new password shorter than 8 characters', () => {
        fill('current-pass', 'short', 'short');
        component.submit();
        expect(authSpy.changePassword).not.toHaveBeenCalled();
        expect(component.form.get('new_password')?.hasError('minlength')).toBe(true);
    });

    it('should reject mismatched new passwords and keep the form unsent', () => {
        fill('current-pass', 'new-secret-1', 'new-secret-2');
        component.submit();
        expect(authSpy.changePassword).not.toHaveBeenCalled();
        expect(component.form.hasError('mismatch')).toBe(true);
    });

    it('should save with the current and new password and show the saved message', () => {
        authSpy.changePassword.mockReturnValue(of(undefined));
        fill('current-pass', 'new-secret-1', 'new-secret-1');
        component.submit();
        fixture.detectChanges();

        expect(authSpy.changePassword).toHaveBeenCalledWith({
            current_password: 'current-pass',
            new_password: 'new-secret-1',
        });
        expect(component.done()).toBe(true);
        expect(fixture.nativeElement.querySelector('[data-testid="own-password-saved"]')).toBeTruthy();
        expect(component.form.getRawValue().current_password).toBe('');
    });

    it('should show the wrong current password message on invalid_credentials', () => {
        authSpy.changePassword.mockReturnValue(throwError(() => ({ code: 'invalid_credentials', status: 401 })));
        fill('wrong-pass', 'new-secret-1', 'new-secret-1');
        component.submit();
        fixture.detectChanges();

        expect(component.error()).toBe('account.password.error.invalid_credentials');
        expect(fixture.nativeElement.querySelector('[data-testid="own-password-error"]')).toBeTruthy();
        expect(component.done()).toBe(false);
    });

    it('should map a same-password error to its message', () => {
        authSpy.changePassword.mockReturnValue(throwError(() => ({ code: 'same_password', status: 400 })));
        fill('current-pass', 'current-pass', 'current-pass');
        component.submit();
        expect(component.error()).toBe('login.change_password.error.same_password');
    });

    it('should map a network failure to the connection message', () => {
        authSpy.changePassword.mockReturnValue(throwError(() => ({ code: 'network', status: 0 })));
        fill('current-pass', 'new-secret-1', 'new-secret-1');
        component.submit();
        expect(component.error()).toBe('login.change_password.error.network');
    });

    it('should clear the loading flag after a failure', () => {
        authSpy.changePassword.mockReturnValue(throwError(() => ({ code: 'boom', status: 500 })));
        fill('current-pass', 'new-secret-1', 'new-secret-1');
        component.submit();
        expect(component.loading()).toBe(false);
        expect(component.error()).toBe('login.change_password.error.generic');
    });
});
