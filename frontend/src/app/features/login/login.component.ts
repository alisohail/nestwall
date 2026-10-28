import { Component, inject, signal } from '@angular/core';
import { Router, RouterLink } from '@angular/router';
import { ReactiveFormsModule, FormBuilder, Validators } from '@angular/forms';
import { TranslocoModule } from '@ngneat/transloco';
import { AuthService, MeResponse } from '../../core/auth/auth.service';
import { ApiError } from '../../core/api/api-client.service';
import { SetupService } from '../../core/setup/setup.service';
import { BannerComponent } from '../../shared/ui/banner/banner.component';
import { LoadingComponent } from '../../shared/ui/loading/loading.component';
import { ForgotPasswordResponse, ForgotPasswordService } from './forgot-password.service';

/** Steps of the forgot-password dialog. */
type ForgotStep = 'ask' | 'ask_parent' | 'notified' | 'email' | 'email_sent' | 'email_not_set_up';

@Component({
    selector: 'app-login',
    imports: [ReactiveFormsModule, TranslocoModule, RouterLink, BannerComponent, LoadingComponent],
    templateUrl: './login.component.html',
    styleUrl: './login.component.scss'
})
export class LoginComponent {
  private fb = inject(FormBuilder);
  private authService = inject(AuthService);
  private setupService = inject(SetupService);
  private router = inject(Router);
  private forgotService = inject(ForgotPasswordService);

  loading = signal(false);
  error = signal<string | null>(null);
  showForgot = signal(false);
  forgotStep = signal<ForgotStep>('ask');
  forgotLoading = signal(false);
  forgotError = signal<string | null>(null);
  forgotUserId = signal('');

  forgotForm = this.fb.group({
    user_id: ['', [Validators.required, Validators.pattern(/\S/)]],
  });

  forgotEmailForm = this.fb.group({
    email: ['', [Validators.required, Validators.email]],
  });

  form = this.fb.group({
    user_id: ['', Validators.required],
    password: ['', Validators.required],
  });

  constructor() {
    this.setupService.getStatus().subscribe({
      next: (status) => {
        if (status.setup_required) this.router.navigate(['/setup']);
      },
      // Keep the login form visible if the status check fails.
      error: () => undefined,
    });
  }

  submit(): void {
    this.form.markAllAsTouched();
    if (this.form.invalid) return;

    this.loading.set(true);
    this.error.set(null);

    this.authService
      .login({
        user_id: this.form.value.user_id!,
        password: this.form.value.password!,
      })
      .subscribe({
        next: (me: MeResponse) => {
          this.loading.set(false);
          // A temporary password must be replaced before anything else.
          if (me.user.must_change_password) {
            this.router.navigate(['/change-password']);
            return;
          }
          // Children land on /today; parents and admins on /dashboard
          const dest = me.user.is_child ? '/today' : '/dashboard';
          this.router.navigate([dest]);
        },
        error: (err: ApiError) => {
          this.loading.set(false);
          this.error.set(this.errorKey(err));
        },
      });
  }

  private errorKey(err: ApiError): string {
    if (err.status === 0) return 'login.error.network';
    switch (err.code) {
      case 'invalid_credentials':
        return 'login.error.invalid_credentials';
      case 'rate_limited':
        return 'login.error.rate_limited';
      default:
        return 'login.error.generic';
    }
  }

  openForgot(): void {
    this.forgotForm.reset({ user_id: this.form.value.user_id ?? '' });
    this.forgotEmailForm.reset({ email: '' });
    this.forgotStep.set('ask');
    this.forgotError.set(null);
    this.showForgot.set(true);
  }

  /** Parent path: switches to the email step without contacting the server. */
  chooseEmailReset(): void {
    this.forgotEmailForm.reset({ email: '' });
    this.forgotError.set(null);
    this.forgotStep.set('email');
  }

  /** Parent path: sends the address. The reply is the same whether or not it matches an account. */
  submitForgotEmail(): void {
    this.forgotEmailForm.markAllAsTouched();
    if (this.forgotEmailForm.invalid) return;

    const email = this.forgotEmailForm.value.email!.trim();
    this.forgotLoading.set(true);
    this.forgotError.set(null);
    this.forgotService.requestResetByEmail(email).subscribe({
      next: (res) => {
        this.forgotLoading.set(false);
        this.forgotStep.set(this.stepForStatus(res.status));
      },
      error: (err: ApiError) => {
        this.forgotLoading.set(false);
        this.forgotError.set(this.forgotErrorKey(err));
      },
    });
  }


  /** Maps a forgot-password reply to the dialog step it shows. */
  private stepForStatus(status: ForgotPasswordResponse['status']): ForgotStep {
    switch (status) {
      case 'child':
        return 'ask_parent';
      case 'email_not_set_up':
        return 'email_not_set_up';
      default:
        return 'email_sent';
    }
  }

  closeForgot(): void {
    this.showForgot.set(false);
  }

  /** Step 1: checks the user ID. A child is told to ask a parent; anyone else gets the email step. */
  submitForgot(): void {
    this.forgotForm.markAllAsTouched();
    if (this.forgotForm.invalid) return;

    const userId = this.forgotForm.value.user_id!.trim();
    this.forgotLoading.set(true);
    this.forgotError.set(null);
    this.forgotService.requestReset(userId).subscribe({
      next: (res) => {
        this.forgotUserId.set(userId);
        this.forgotLoading.set(false);
        this.forgotStep.set(this.stepForStatus(res.status));
      },
      error: (err: ApiError) => {
        this.forgotLoading.set(false);
        this.forgotError.set(this.forgotErrorKey(err));
      },
    });
  }

  /** Step 2: tells every parent in the family that a reset is needed. */
  tellParents(): void {
    this.forgotLoading.set(true);
    this.forgotError.set(null);
    this.forgotService.notifyParents(this.forgotUserId()).subscribe({
      next: () => {
        this.forgotLoading.set(false);
        this.forgotStep.set('notified');
      },
      error: (err: ApiError) => {
        this.forgotLoading.set(false);
        this.forgotError.set(this.forgotErrorKey(err));
      },
    });
  }

  private forgotErrorKey(err: ApiError): string {
    if (err.status === 0) return 'login.forgot_dialog.error.network';
    if (err.code === 'rate_limited') return 'login.forgot_dialog.error.rate_limited';
    return 'login.forgot_dialog.error.generic';
  }
}

