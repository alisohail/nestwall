import type { MockedObject } from "vitest";
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ReactiveFormsModule } from '@angular/forms';
import { Router } from '@angular/router';
import { of, throwError } from 'rxjs';
import { getTranslocoTestingModule } from '../../core/transloco/transloco-testing.module';
import { SetupComponent } from './setup.component';
import { SetupService, SetupParentResponse } from '../../core/setup/setup.service';
import { mockOf } from '../../testing/mock-of';

describe('SetupComponent', () => {
    const parentResponse: SetupParentResponse = {
        family_id: 'f1',
        message: '',
        user: { id: 'u1', user_id: 'u1', display_name: 'Parent', is_child: false },
    };
    let fixture: ComponentFixture<SetupComponent>;
    let component: SetupComponent;
    let setupSpy: MockedObject<SetupService>;
    let routerSpy: MockedObject<Router>;

    beforeEach(async () => {
        setupSpy = mockOf<SetupService>({
            createParent: vi.fn().mockName("SetupService.createParent"),
            restoreFirstRun: vi.fn().mockName("SetupService.restoreFirstRun"),
            live: vi.fn().mockName("SetupService.live")
        });
        routerSpy = mockOf<Router>({
            navigate: vi.fn().mockName("Router.navigate")
        });

        await TestBed.configureTestingModule({
            imports: [SetupComponent, ReactiveFormsModule, getTranslocoTestingModule()],
            providers: [
                { provide: SetupService, useValue: setupSpy },
                { provide: Router, useValue: routerSpy },
            ],
        }).compileComponents();

        fixture = TestBed.createComponent(SetupComponent);
        component = fixture.componentInstance;
        fixture.detectChanges();
    });

    it('should create and start on step 1', () => {
        expect(component).toBeTruthy();
        expect(component.step()).toBe(1);
    });

    function fillValidAccount(): void {
        component.accountForm.setValue({
            display_name: 'Alice Rivera',
            email: 'alice@example.com',
            user_id: 'alice',
            password: 'securepassword1',
            confirm: 'securepassword1',
            family_name: 'Rivera Family',
            timezone: 'UTC',
        });
    }

    it('should require an email for the first parent', () => {
        const email = component.accountForm.get('email')!;
        expect(email.hasError('required')).toBe(true);
        email.setValue('alice@example.com');
        expect(email.valid).toBe(true);
    });

    it('should reject a malformed email', () => {
        const email = component.accountForm.get('email')!;
        email.setValue('alice@');
        expect(email.hasError('email')).toBe(true);
    });

    it('should send the email with the parent account', () => {
        setupSpy.createParent.mockReturnValue(of(parentResponse));

        component.start();
        fillValidAccount();
        component.submit();

        expect(setupSpy.createParent).toHaveBeenCalledWith(expect.objectContaining({ email: 'alice@example.com' }));
    });

    it('should show the taken-email message when the server rejects the email', () => {
        setupSpy.createParent.mockReturnValue(throwError(() => ({ status: 409, code: 'email_taken', message: 'Email taken' })));

        component.start();
        fillValidAccount();
        component.submit();

        expect(component.serverError()).toBe('setup.error.email_taken');
    });

    it('should advance to the account step from the welcome step', () => {
        component.start();
        expect(component.step()).toBe(2);
    });

    it('should go back to the welcome step from the account step', () => {
        component.start();
        component.prevStep();
        expect(component.step()).toBe(1);
    });

    it('should not submit when account form is invalid', () => {
        component.start();
        component.submit();
        expect(setupSpy.createParent).not.toHaveBeenCalled();
    });

    it('should reject passwords shorter than 8 characters', () => {
        const pw = component.accountForm.get('password')!;
        pw.setValue('1234567');
        expect(pw.hasError('minlength')).toBe(true);
        pw.setValue('12345678');
        expect(pw.valid).toBe(true);
    });

    it('should compute password strength correctly', () => {
        component.accountForm.get('password')!.setValue('');
        fixture.detectChanges();
        expect(component.passwordStrength()).toBe(0);

        component.accountForm.get('password')!.setValue('short');
        fixture.detectChanges();
        expect(component.passwordStrength()).toBe(0);

        component.accountForm.get('password')!.setValue('longpassword1');
        fixture.detectChanges();
        expect(component.passwordStrength()).toBeGreaterThanOrEqual(2);

        component.accountForm.get('password')!.setValue('LongPassword1!Extra');
        fixture.detectChanges();
        expect(component.passwordStrength()).toBe(4);
    });

    it('should detect password mismatch', () => {
        component.accountForm.get('password')!.setValue('longpassword1');
        component.accountForm.get('confirm')!.setValue('different123');
        expect(component.accountForm.errors?.['mismatch']).toBe(true);
    });

    it('should call createParent and advance to step 3 on success', () => {
        setupSpy.createParent.mockReturnValue(of(parentResponse));

        component.start();
        fillValidAccount();
        component.submit();

        expect(setupSpy.createParent).toHaveBeenCalled();
        expect(component.step()).toBe(3);
        // Setup does not sign the user in, so there is no navigation after success.
        expect(routerSpy.navigate).not.toHaveBeenCalled();
    });

    it('should show server error on createParent failure', () => {
        setupSpy.createParent.mockReturnValue(throwError(() => ({ code: 'user_id_taken', message: 'User ID already taken' })));

        component.start();
        fillValidAccount();
        component.submit();
        fixture.detectChanges();

        expect(component.serverError()).toBeTruthy();
    });

    it('should navigate to /login from step 3', () => {
        // Jump directly to step 3
        component.step.set(3);
        component.goToLogin();
        expect(routerSpy.navigate).toHaveBeenCalledWith(['/login']);
    });

    describe('restore from a backup', () => {
        it('should open restore mode from the welcome step without the account form', () => {
            component.openRestore();
            fixture.detectChanges();

            expect(component.restoreMode()).toBe(true);
            const el: HTMLElement = fixture.nativeElement;
            expect(el.querySelector('[data-testid="setup-restore-submit"]')).toBeTruthy();
            expect(el.querySelector('#family-name, input[formControlName="family_name"]')).toBeNull();
        });

        it('should ask for a file before submitting', () => {
            component.openRestore();
            component.submitRestore();

            expect(component.restoreFileMissing()).toBe(true);
            expect(setupSpy.restoreFirstRun).not.toHaveBeenCalled();
        });

        it('should upload the archive with the passphrase and wait for restart on success', () => {
            setupSpy.restoreFirstRun.mockReturnValue(of({ restarting: true }));
            setupSpy.live.mockReturnValue(of({ status: 'ok' }));
            const file = new File(['zip'], 'backup.zip', { type: 'application/zip' });

            component.openRestore();
            component.restoreFile.set(file);
            component.restorePassphrase.set('secret');
            component.submitRestore();

            expect(setupSpy.restoreFirstRun).toHaveBeenCalledWith(file, 'secret');
            expect(component.restarting()).toBe(true);
            expect(component.restorePassphrase()).toBe('');
        });

        it('should map a missing passphrase error to its message', () => {
            setupSpy.restoreFirstRun.mockReturnValue(throwError(() => ({ status: 422, code: 'passphrase_required', message: 'x' })));
            component.openRestore();
            component.restoreFile.set(new File(['zip'], 'backup.zip'));
            component.submitRestore();

            expect(component.restoreError()).toBe('setup.restore.error.passphrase_required');
            expect(component.restarting()).toBe(false);
        });
    });
});
