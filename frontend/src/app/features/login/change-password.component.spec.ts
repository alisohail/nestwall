import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ReactiveFormsModule } from '@angular/forms';
import { Router } from '@angular/router';
import { signal, WritableSignal } from '@angular/core';
import { of, throwError } from 'rxjs';
import { getTranslocoTestingModule } from '../../core/transloco/transloco-testing.module';
import { AuthService, MeResponse } from '../../core/auth/auth.service';
import { ChangePasswordComponent } from './change-password.component';

const parentMe: MeResponse = {
  user: { id: 'u1', user_id: 'parent1', email: null, display_name: 'Alice', is_child: false, must_change_password: true },
  family: null,
};

describe('ChangePasswordComponent', () => {
  let fixture: ComponentFixture<ChangePasswordComponent>;
  let component: ChangePasswordComponent;
  let authSpy: jasmine.SpyObj<AuthService>;
  let currentUser: WritableSignal<MeResponse | null | undefined>;
  let routerSpy: jasmine.SpyObj<Router>;

  beforeEach(async () => {
    currentUser = signal<MeResponse | null | undefined>(parentMe);
    authSpy = jasmine.createSpyObj('AuthService', ['changePassword']);
    (authSpy as unknown as { currentUser: WritableSignal<MeResponse | null | undefined> }).currentUser = currentUser;
    routerSpy = jasmine.createSpyObj('Router', ['navigate']);

    await TestBed.configureTestingModule({
      imports: [ChangePasswordComponent, ReactiveFormsModule, getTranslocoTestingModule()],
      providers: [
        { provide: AuthService, useValue: authSpy },
        { provide: Router, useValue: routerSpy },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(ChangePasswordComponent);
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
    fill('temp-pass', 'short', 'short');
    component.submit();
    expect(authSpy.changePassword).not.toHaveBeenCalled();
    expect(component.form.get('new_password')?.hasError('minlength')).toBeTrue();
  });

  it('should reject mismatched new passwords', () => {
    fill('temp-pass', 'new-secret-1', 'new-secret-2');
    component.submit();
    expect(authSpy.changePassword).not.toHaveBeenCalled();
    expect(component.form.hasError('mismatch')).toBeTrue();
  });

  it('should save and navigate to /dashboard for a parent', () => {
    authSpy.changePassword.and.returnValue(of(undefined));
    fill('temp-pass', 'new-secret-1', 'new-secret-1');
    component.submit();
    expect(authSpy.changePassword).toHaveBeenCalledWith({
      current_password: 'temp-pass',
      new_password: 'new-secret-1',
    });
    expect(routerSpy.navigate).toHaveBeenCalledWith(['/dashboard']);
  });

  it('should navigate to /today for a child', () => {
    currentUser.set({ ...parentMe, user: { ...parentMe.user, is_child: true } });
    authSpy.changePassword.and.returnValue(of(undefined));
    fill('temp-pass', 'new-secret-1', 'new-secret-1');
    component.submit();
    expect(routerSpy.navigate).toHaveBeenCalledWith(['/today']);
  });

  it('should show the wrong-temporary-password message on 401', () => {
    authSpy.changePassword.and.returnValue(throwError(() => ({ code: 'invalid_credentials', status: 401 })));
    fill('wrong-pass', 'new-secret-1', 'new-secret-1');
    component.submit();
    fixture.detectChanges();
    expect(component.error()).toBe('login.change_password.error.invalid_credentials');
    expect(fixture.nativeElement.querySelector('[data-testid="change-password-error"]')).toBeTruthy();
  });

  it('should send the user to sign in when the session has ended', () => {
    authSpy.changePassword.and.returnValue(throwError(() => ({ code: 'session_invalid', status: 401 })));
    fill('temp-pass', 'new-secret-1', 'new-secret-1');
    component.submit();
    expect(routerSpy.navigate).toHaveBeenCalledWith(['/login']);
  });
});
