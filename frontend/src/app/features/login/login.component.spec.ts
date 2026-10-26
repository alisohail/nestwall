import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ReactiveFormsModule } from '@angular/forms';
import { ActivatedRoute, Router, UrlTree } from '@angular/router';
import { of, Subject, throwError } from 'rxjs';
import { signal } from '@angular/core';
import { getTranslocoTestingModule } from '../../core/transloco/transloco-testing.module';
import { LoginComponent } from './login.component';
import { AuthService } from '../../core/auth/auth.service';
import { MeResponse } from '../../core/auth/auth.service';
import { SetupService } from '../../core/setup/setup.service';
import { ForgotPasswordService } from './forgot-password.service';
import { ApiError } from '../../core/api/api-client.service';

const mockMe: MeResponse = {
  user: { id: 'u1', user_id: 'parent1', email: null, display_name: 'Alice', is_child: false, must_change_password: false },
  family: { member_id: 'm1', family_id: 'f1', family_name: 'Rivera', role_id: 'a1b2c3d4-e5f6-7890-abcd-ef1234567890', role_name: 'Parent', status: 'active', colour: '#ff0000', avatar_id: null, is_parent: true },
};

describe('LoginComponent', () => {
  let fixture: ComponentFixture<LoginComponent>;
  let component: LoginComponent;
  let authSpy: jasmine.SpyObj<AuthService>;
  let setupSpy: jasmine.SpyObj<SetupService>;
  let forgotSpy: jasmine.SpyObj<ForgotPasswordService>;
  let routerSpy: jasmine.SpyObj<Router>;

  beforeEach(async () => {
    authSpy = jasmine.createSpyObj('AuthService', ['login']);
    setupSpy = jasmine.createSpyObj('SetupService', ['getStatus']);
    setupSpy.getStatus.and.returnValue(of({ setup_required: false }));
    forgotSpy = jasmine.createSpyObj('ForgotPasswordService', ['requestReset', 'notifyParents', 'requestResetByEmail']);
    routerSpy = Object.assign(jasmine.createSpyObj('Router', ['navigate', 'createUrlTree', 'serializeUrl']), {
      events: new Subject(),
    }) as jasmine.SpyObj<Router>;
    routerSpy.createUrlTree.and.returnValue(new UrlTree());
    routerSpy.serializeUrl.and.returnValue('/pair');

    await TestBed.configureTestingModule({
      imports: [LoginComponent, ReactiveFormsModule, getTranslocoTestingModule()],
      providers: [
        { provide: AuthService, useValue: authSpy },
        { provide: SetupService, useValue: setupSpy },
        { provide: ForgotPasswordService, useValue: forgotSpy },
        { provide: Router, useValue: routerSpy },
        { provide: ActivatedRoute, useValue: {} },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(LoginComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });

  it('should redirect to /setup when no family exists yet', () => {
    setupSpy.getStatus.and.returnValue(of({ setup_required: true }));
    TestBed.createComponent(LoginComponent);
    expect(routerSpy.navigate).toHaveBeenCalledWith(['/setup']);
  });

  it('should stay on the login page when setup is complete', () => {
    expect(routerSpy.navigate).not.toHaveBeenCalledWith(['/setup']);
  });

  it('should link to the wall pairing screen', () => {
    const link = fixture.nativeElement.querySelector('[data-testid="login-pair-link"]') as HTMLElement | null;
    expect(link).not.toBeNull();
    expect(link?.textContent?.trim()).toBe('Set up a wall display');
  });

  it('should show validation errors when submitted empty', () => {
    component.submit();
    fixture.detectChanges();
    const errors = fixture.nativeElement.querySelectorAll('.field-error');
    expect(errors.length).toBeGreaterThan(0);
    expect(authSpy.login).not.toHaveBeenCalled();
  });

  it('should call login and navigate to /dashboard for a parent', () => {
    authSpy.login.and.returnValue(of(mockMe));
    component.form.setValue({ user_id: 'parent1', password: 'secret123' });
    component.submit();
    expect(authSpy.login).toHaveBeenCalledWith({ user_id: 'parent1', password: 'secret123' });
    expect(routerSpy.navigate).toHaveBeenCalledWith(['/dashboard']);
  });

  it('should navigate to /today for a child user', () => {
    const childMe: MeResponse = {
      ...mockMe,
      user: { ...mockMe.user, is_child: true },
    };
    authSpy.login.and.returnValue(of(childMe));
    component.form.setValue({ user_id: 'kid1', password: 'secret123' });
    component.submit();
    expect(routerSpy.navigate).toHaveBeenCalledWith(['/today']);
  });

  it('should show server error on login failure', () => {
    authSpy.login.and.returnValue(throwError(() => ({ code: 'invalid_credentials' })));
    component.form.setValue({ user_id: 'bad', password: 'wrong' });
    component.submit();
    fixture.detectChanges();
    expect(component.error()).toBe('login.error.invalid_credentials');
    const el = fixture.nativeElement.querySelector('[data-testid="login-error"]');
    expect(el).toBeTruthy();
  });

  it('should send a user with a temporary password to /change-password', () => {
    const tempMe: MeResponse = {
      ...mockMe,
      user: { ...mockMe.user, must_change_password: true },
    };
    authSpy.login.and.returnValue(of(tempMe));
    component.form.setValue({ user_id: 'parent1', password: 'temp-pass' });
    component.submit();
    expect(routerSpy.navigate).toHaveBeenCalledWith(['/change-password']);
    expect(routerSpy.navigate).not.toHaveBeenCalledWith(['/dashboard']);
  });

  it('should toggle the forgot-password dialog', () => {
    expect(component.showForgot()).toBeFalse();
    component.openForgot();
    expect(component.showForgot()).toBeTrue();
    component.closeForgot();
    expect(component.showForgot()).toBeFalse();
  });

  it('should render the forgot-password dialog in DOM when open', () => {
    component.openForgot();
    fixture.detectChanges();
    const dialog = fixture.nativeElement.querySelector('[role="dialog"]');
    expect(dialog).toBeTruthy();
    expect(dialog.querySelector('#forgot-user-id')).toBeTruthy();
  });

  it('should prefill the forgot user ID from the sign-in form', () => {
    component.form.setValue({ user_id: 'ava', password: '' });
    component.openForgot();
    expect(component.forgotForm.value.user_id).toBe('ava');
  });

  it('should tell a child that only a parent can reset, after the user ID is checked', () => {
    forgotSpy.requestReset.and.returnValue(of({ status: 'child' }));
    component.openForgot();
    component.forgotForm.setValue({ user_id: 'ava' });
    component.submitForgot();
    expect(forgotSpy.requestReset).toHaveBeenCalledWith('ava');
    expect(component.forgotStep()).toBe('ask_parent');
  });

  it('should show the check-your-email step for a parent or unknown user ID', () => {
    forgotSpy.requestReset.and.returnValue(of({ status: 'accepted' }));
    component.openForgot();
    component.forgotForm.setValue({ user_id: 'sam' });
    component.submitForgot();
    expect(forgotSpy.requestReset).toHaveBeenCalledWith('sam');
    expect(component.forgotStep()).toBe('email_sent');
  });

  it('should show the not-set-up step when email reset is not ready', () => {
    forgotSpy.requestResetByEmail.and.returnValue(of({ status: 'email_not_set_up' }));
    component.openForgot();
    component.forgotEmailForm.setValue({ email: 'sam@example.com' });
    component.submitForgotEmail();
    expect(forgotSpy.requestResetByEmail).toHaveBeenCalledWith('sam@example.com');
    expect(component.forgotStep()).toBe('email_not_set_up');
  });

  it('should show the not-set-up step when a parent user ID has no ready email', () => {
    forgotSpy.requestReset.and.returnValue(of({ status: 'email_not_set_up' }));
    component.openForgot();
    component.forgotForm.setValue({ user_id: 'sam' });
    component.submitForgot();
    expect(component.forgotStep()).toBe('email_not_set_up');
  });

  it('should not call the API when the user ID is blank', () => {
    component.openForgot();
    component.forgotForm.setValue({ user_id: '   ' });
    component.submitForgot();
    expect(forgotSpy.requestReset).not.toHaveBeenCalled();
    expect(component.forgotStep()).toBe('ask');
  });

  it('should show the notified step after the parents are told', () => {
    forgotSpy.requestReset.and.returnValue(of({ status: 'child' }));
    forgotSpy.notifyParents.and.returnValue(of({ status: 'accepted' }));
    component.openForgot();
    component.forgotForm.setValue({ user_id: 'ava' });
    component.submitForgot();
    component.tellParents();
    expect(forgotSpy.notifyParents).toHaveBeenCalledWith('ava');
    expect(component.forgotStep()).toBe('notified');
  });

  it('should show the rate-limit message when the parents cannot be told yet', () => {
    forgotSpy.requestReset.and.returnValue(of({ status: 'child' }));
    const err: ApiError = { code: 'rate_limited', message: '', params: {}, status: 429 };
    forgotSpy.notifyParents.and.returnValue(throwError(() => err));
    component.openForgot();
    component.forgotForm.setValue({ user_id: 'ava' });
    component.submitForgot();
    component.tellParents();
    fixture.detectChanges();
    expect(component.forgotStep()).toBe('ask_parent');
    expect(component.forgotError()).toBe('login.forgot_dialog.error.rate_limited');
  });
});
