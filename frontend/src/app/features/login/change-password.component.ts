import { Component, inject, signal } from '@angular/core';
import { Router } from '@angular/router';
import { ReactiveFormsModule, FormBuilder, Validators, AbstractControl, ValidationErrors } from '@angular/forms';
import { TranslocoModule } from '@ngneat/transloco';
import { AuthService } from '../../core/auth/auth.service';
import { ApiError } from '../../core/api/api-client.service';
import { BannerComponent } from '../../shared/ui/banner/banner.component';
import { LoadingComponent } from '../../shared/ui/loading/loading.component';

/** Matches the server rule in ValidatePassword. */
const MIN_PASSWORD_LENGTH = 8;

/** Confirms the two new password fields match. */
function matchingPasswords(group: AbstractControl): ValidationErrors | null {
  const next = group.get('new_password')?.value;
  const confirm = group.get('confirm_password')?.value;
  return next && confirm && next !== confirm ? { mismatch: true } : null;
}

/**
 * Shown after sign-in when the account has a temporary password. The user
 * must replace it before the rest of the app is available.
 */
@Component({
    selector: 'app-change-password',
    imports: [ReactiveFormsModule, TranslocoModule, BannerComponent, LoadingComponent],
    templateUrl: './change-password.component.html',
    styleUrl: './change-password.component.scss'
})
export class ChangePasswordComponent {
  private fb = inject(FormBuilder);
  private authService = inject(AuthService);
  private router = inject(Router);

  readonly minLength = MIN_PASSWORD_LENGTH;
  loading = signal(false);
  error = signal<string | null>(null);

  form = this.fb.nonNullable.group(
    {
      current_password: ['', Validators.required],
      new_password: ['', [Validators.required, Validators.minLength(MIN_PASSWORD_LENGTH)]],
      confirm_password: ['', Validators.required],
    },
    { validators: matchingPasswords }
  );

  submit(): void {
    this.form.markAllAsTouched();
    this.error.set(null);
    if (this.form.invalid) return;

    const { current_password, new_password } = this.form.getRawValue();
    this.loading.set(true);
    this.authService.changePassword({ current_password, new_password }).subscribe({
      next: () => {
        this.loading.set(false);
        const isChild = this.authService.currentUser()?.user.is_child ?? false;
        this.router.navigate([isChild ? '/today' : '/dashboard']);
      },
      error: (err: ApiError) => {
        this.loading.set(false);
        if (err.code === 'unauthenticated' || err.code === 'session_invalid') {
          this.router.navigate(['/login']);
          return;
        }
        this.error.set(this.errorKey(err));
      },
    });
  }

  private errorKey(err: ApiError): string {
    if (err.status === 0) return 'login.change_password.error.network';
    switch (err.code) {
      case 'invalid_credentials':
        return 'login.change_password.error.invalid_credentials';
      case 'same_password':
        return 'login.change_password.error.same_password';
      case 'rate_limited':
        return 'login.change_password.error.rate_limited';
      default:
        return 'login.change_password.error.generic';
    }
  }
}
