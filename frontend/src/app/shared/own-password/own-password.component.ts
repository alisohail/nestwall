import { Component, inject, signal } from '@angular/core';
import { ReactiveFormsModule, FormBuilder, Validators, AbstractControl, ValidationErrors } from '@angular/forms';
import { TranslocoModule } from '@ngneat/transloco';
import { AuthService } from '../../core/auth/auth.service';
import { ApiError } from '../../core/api/api-client.service';
import { BannerComponent } from '../ui/banner/banner.component';
import { LoadingComponent } from '../ui/loading/loading.component';

/** Matches the server rule in ValidatePassword. */
const MIN_PASSWORD_LENGTH = 8;

/** Confirms the two new password fields match. */
function matchingPasswords(group: AbstractControl): ValidationErrors | null {
  const next = group.get('new_password')?.value;
  const confirm = group.get('confirm_password')?.value;
  return next && confirm && next !== confirm ? { mismatch: true } : null;
}

/**
 * Lets the signed-in user change their own password with the current one.
 * Used on the profile page and in the Family profile dialog for the signed-in parent.
 */
@Component({
    selector: 'app-own-password',
    imports: [ReactiveFormsModule, TranslocoModule, BannerComponent, LoadingComponent],
    templateUrl: './own-password.component.html',
    styleUrl: './own-password.component.scss'
})
export class OwnPasswordComponent {
  private fb = inject(FormBuilder);
  private authService = inject(AuthService);

  readonly minLength = MIN_PASSWORD_LENGTH;
  loading = signal(false);
  error = signal<string | null>(null);
  done = signal(false);

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
    this.done.set(false);
    if (this.form.invalid) return;

    const { current_password, new_password } = this.form.getRawValue();
    this.loading.set(true);
    this.authService.changePassword({ current_password, new_password }).subscribe({
      next: () => {
        this.loading.set(false);
        this.form.reset();
        this.done.set(true);
      },
      error: (err: ApiError) => {
        this.loading.set(false);
        this.error.set(this.errorKey(err));
      },
    });
  }

  private errorKey(err: ApiError): string {
    if (err.status === 0) return 'login.change_password.error.network';
    switch (err.code) {
      case 'invalid_credentials':
        return 'account.password.error.invalid_credentials';
      case 'same_password':
        return 'login.change_password.error.same_password';
      case 'rate_limited':
        return 'login.change_password.error.rate_limited';
      default:
        return 'login.change_password.error.generic';
    }
  }
}
