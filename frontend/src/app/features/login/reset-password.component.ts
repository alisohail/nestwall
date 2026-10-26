import { Component, inject, signal } from '@angular/core';
import { ActivatedRoute, RouterLink } from '@angular/router';
import { FormBuilder, ReactiveFormsModule, Validators } from '@angular/forms';
import { TranslocoModule } from '@ngneat/transloco';
import { ApiError } from '../../core/api/api-client.service';
import { BannerComponent } from '../../shared/ui/banner/banner.component';
import { ForgotPasswordService } from './forgot-password.service';

/** `form` while the user chooses a password, then `done` or `invalid`. */
type ResetState = 'form' | 'done' | 'invalid';

/**
 * Landing page for the emailed reset link (M7-04). The token is read from the
 * URL and sent once with the new password. A successful reset signs every
 * session out, so the user goes to sign in again.
 */
@Component({
  selector: 'app-reset-password',
  standalone: true,
  imports: [ReactiveFormsModule, RouterLink, TranslocoModule, BannerComponent],
  templateUrl: './reset-password.component.html',
  styleUrl: './reset-password.component.scss',
})
export class ResetPasswordComponent {
  private fb = inject(FormBuilder);
  private route = inject(ActivatedRoute);
  private resetService = inject(ForgotPasswordService);

  private token = this.route.snapshot.paramMap.get('token') ?? '';

  loading = signal(false);
  error = signal<string | null>(null);
  state = signal<ResetState>('form');

  form = this.fb.group({
    password: ['', [Validators.required, Validators.minLength(8), Validators.maxLength(256)]],
    repeat: ['', Validators.required],
  });

  submit(): void {
    this.form.markAllAsTouched();
    if (this.form.invalid) return;

    const password = this.form.controls.password.value ?? '';
    const repeat = this.form.controls.repeat.value ?? '';
    if (password !== repeat) {
      this.error.set('reset_password.error.mismatch');
      return;
    }

    this.loading.set(true);
    this.error.set(null);
    this.resetService.resetPassword(this.token, password).subscribe({
      next: () => {
        this.loading.set(false);
        this.state.set('done');
      },
      error: (err: ApiError) => {
        this.loading.set(false);
        if (err.code === 'reset_link_invalid') {
          this.state.set('invalid');
          return;
        }
        this.error.set(this.errorKey(err));
      },
    });
  }

  private errorKey(err: ApiError): string {
    if (err.status === 0) return 'reset_password.error.network';
    if (err.code === 'rate_limited') return 'reset_password.error.rate_limited';
    if (err.status === 400) return 'reset_password.error.password';
    return 'reset_password.error.generic';
  }
}
