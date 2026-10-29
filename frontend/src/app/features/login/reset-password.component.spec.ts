import type { MockedObject } from "vitest";
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ActivatedRoute, convertToParamMap, provideRouter } from '@angular/router';
import { of, throwError } from 'rxjs';
import { getTranslocoTestingModule } from '../../core/transloco/transloco-testing.module';
import { mockOf } from '../../testing/mock-of';
import { ForgotPasswordService } from './forgot-password.service';
import { ResetPasswordComponent } from './reset-password.component';

describe('ResetPasswordComponent', () => {
    let fixture: ComponentFixture<ResetPasswordComponent>;
    let component: ResetPasswordComponent;
    let resetSpy: MockedObject<ForgotPasswordService>;

    async function setup(): Promise<void> {
        resetSpy = mockOf<ForgotPasswordService>({
            resetPassword: vi.fn().mockName("ForgotPasswordService.resetPassword")
        });
        await TestBed.configureTestingModule({
            imports: [ResetPasswordComponent, getTranslocoTestingModule()],
            providers: [
                provideRouter([]),
                { provide: ForgotPasswordService, useValue: resetSpy },
                {
                    provide: ActivatedRoute,
                    useValue: { snapshot: { paramMap: convertToParamMap({ token: 'the-token' }) } },
                },
            ],
        }).compileComponents();

        fixture = TestBed.createComponent(ResetPasswordComponent);
        component = fixture.componentInstance;
        fixture.detectChanges();
    }

    function fill(password: string, repeat: string): void {
        component.form.setValue({ password, repeat });
    }

    beforeEach(async () => {
        await setup();
    });

    it('should create', () => {
        expect(component).toBeTruthy();
    });

    it('should not call the API when the form is invalid', () => {
        component.submit();
        expect(resetSpy.resetPassword).not.toHaveBeenCalled();
    });

    it('should reject a password shorter than 8 characters', () => {
        fill('short', 'short');
        component.submit();
        expect(resetSpy.resetPassword).not.toHaveBeenCalled();
        expect(component.form.controls.password.hasError('minlength')).toBe(true);
    });

    it('should reject mismatched passwords without calling the API', () => {
        fill('a long new password', 'a different password');
        component.submit();
        expect(resetSpy.resetPassword).not.toHaveBeenCalled();
        expect(component.error()).toBe('reset_password.error.mismatch');
    });

    it('should send the token from the URL with the new password', () => {
        resetSpy.resetPassword.mockReturnValue(of(undefined));
        fill('a long new password', 'a long new password');
        component.submit();
        expect(resetSpy.resetPassword).toHaveBeenCalledWith('the-token', 'a long new password');
    });

    it('should show the done state after a successful reset', () => {
        resetSpy.resetPassword.mockReturnValue(of(undefined));
        fill('a long new password', 'a long new password');
        component.submit();
        fixture.detectChanges();
        expect(component.state()).toBe('done');
        expect(fixture.nativeElement.textContent).toContain('Password changed');
    });

    it('should show the invalid state when the link is rejected', () => {
        resetSpy.resetPassword.mockReturnValue(throwError(() => ({ code: 'reset_link_invalid', status: 400 })));
        fill('a long new password', 'a long new password');
        component.submit();
        fixture.detectChanges();
        expect(component.state()).toBe('invalid');
    });

    it('should map a weak password error to the password message', () => {
        resetSpy.resetPassword.mockReturnValue(throwError(() => ({ code: 'weak_password', status: 400 })));
        fill('a long new password', 'a long new password');
        component.submit();
        expect(component.error()).toBe('reset_password.error.password');
        expect(component.state()).toBe('form');
    });

    it('should map a rate limit to the rate-limited message', () => {
        resetSpy.resetPassword.mockReturnValue(throwError(() => ({ code: 'rate_limited', status: 429 })));
        fill('a long new password', 'a long new password');
        component.submit();
        expect(component.error()).toBe('reset_password.error.rate_limited');
    });

    it('should map a network failure to the network message', () => {
        resetSpy.resetPassword.mockReturnValue(throwError(() => ({ code: 'network', status: 0 })));
        fill('a long new password', 'a long new password');
        component.submit();
        expect(component.error()).toBe('reset_password.error.network');
    });
});
