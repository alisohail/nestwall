import { Component, DestroyRef, inject, signal } from '@angular/core';
import { DOCUMENT } from '@angular/common';
import { Router } from '@angular/router';
import {
  ReactiveFormsModule,
  FormBuilder,
  Validators,
  AbstractControl,
  ValidationErrors,
} from '@angular/forms';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { TranslocoModule, TranslocoService } from '@ngneat/transloco';
import { catchError, filter, finalize, of, switchMap, take, timer } from 'rxjs';
import { SetupService } from '../../core/setup/setup.service';
import { ApiError } from '../../core/api/api-client.service';
import { FAMILY_TIMEZONES } from '../../core/timezones';
import { BannerComponent } from '../../shared/ui/banner/banner.component';
import { LoadingComponent } from '../../shared/ui/loading/loading.component';

type Step = 1 | 2 | 3;

/** Must match backend validation: user ID 3-32 chars; password 8-256 chars. */
const USER_ID_PATTERN = /^[a-zA-Z0-9._-]{3,32}$/;
const PASSWORD_MIN = 8;
const PASSWORD_MAX = 256;

function passwordsMatch(group: AbstractControl): ValidationErrors | null {
  const pw = group.get('password')?.value;
  const confirm = group.get('confirm')?.value;
  return pw && confirm && pw !== confirm ? { mismatch: true } : null;
}

@Component({
  selector: 'app-setup',
  standalone: true,
  imports: [ReactiveFormsModule, TranslocoModule, BannerComponent, LoadingComponent],
  templateUrl: './setup.component.html',
  styleUrl: './setup.component.scss',
})
export class SetupComponent {
  private fb = inject(FormBuilder);
  private setupService = inject(SetupService);
  private router = inject(Router);
  private transloco = inject(TranslocoService);
  private destroyRef = inject(DestroyRef);
  private document = inject(DOCUMENT);

  step = signal<Step>(1);
  loading = signal(false);
  /** Transloco key for the server error, or null. */
  serverError = signal<string | null>(null);

  /** The restore path replaces the account form: the family comes from the backup, not from this wizard. */
  restoreMode = signal(false);
  restoreFile = signal<File | null>(null);
  restoreFileMissing = signal(false);
  restorePassphrase = signal('');
  restoreBusy = signal(false);
  restarting = signal(false);
  /** Transloco key for the restore error, or null. */
  restoreError = signal<string | null>(null);

  accountForm = this.fb.group(
    {
      display_name: [
        '',
        [Validators.required, Validators.maxLength(80)],
      ],
      email: [
        '',
        [Validators.required, Validators.email, Validators.maxLength(254)],
      ],
      user_id: [
        '',
        [Validators.required, Validators.pattern(USER_ID_PATTERN)],
      ],
      password: [
        '',
        [Validators.required, Validators.minLength(PASSWORD_MIN), Validators.maxLength(PASSWORD_MAX)],
      ],
      confirm: ['', Validators.required],
      family_name: [
        '',
        [Validators.required, Validators.maxLength(80)],
      ],
      timezone: [
        Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC',
        Validators.required,
      ],
    },
    { validators: passwordsMatch }
  );

  passwordStrength(): 0 | 1 | 2 | 3 | 4 {
    const pw: string = this.accountForm.get('password')?.value ?? '';
    if (!pw) return 0;
    let score = 0;
    if (pw.length >= 12) score++;
    if (pw.length >= 18) score++;
    if (/[A-Z]/.test(pw) || /[0-9]/.test(pw)) score++;
    if (/[^A-Za-z0-9]/.test(pw)) score++;
    return score as 0 | 1 | 2 | 3 | 4;
  }

  strengthKey(): string {
    const keys = [
      '',
      'setup.password.weak',
      'setup.password.fair',
      'setup.password.good',
      'setup.password.strong',
    ] as const;
    return keys[this.passwordStrength()];
  }

  readonly timezones = FAMILY_TIMEZONES;

  start(): void {
    this.step.set(2);
  }

  openRestore(): void {
    this.restoreMode.set(true);
    this.restoreError.set(null);
  }

  closeRestore(): void {
    if (this.restoreBusy()) return;
    this.restoreMode.set(false);
    this.restoreError.set(null);
  }

  chooseRestoreFile(event: Event): void {
    const input = event.target as HTMLInputElement;
    const file = input.files?.[0] ?? null;
    this.restoreFile.set(file);
    this.restoreFileMissing.set(false);
    this.restoreError.set(null);
  }

  submitRestore(): void {
    const file = this.restoreFile();
    if (!file) {
      this.restoreFileMissing.set(true);
      return;
    }
    if (this.restoreBusy()) return;
    this.restoreBusy.set(true);
    this.restoreError.set(null);

    this.setupService
      .restoreFirstRun(file, this.restorePassphrase())
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => this.restoreBusy.set(false)),
      )
      .subscribe({
        next: () => {
          this.restorePassphrase.set('');
          this.restarting.set(true);
          this.waitForRestart();
        },
        error: (err: ApiError) => {
          this.restorePassphrase.set('');
          this.restoreError.set(this.restoreErrorKey(err));
        },
      });
  }

  /** Polls the liveness probe until the restarted API answers, then opens sign-in on the restored family. */
  private waitForRestart(): void {
    timer(1500, 2000)
      .pipe(
        switchMap(() => this.setupService.live().pipe(catchError(() => of(null)))),
        filter((res) => res !== null),
        take(1),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe(() => this.document.location.assign('/login'));
  }

  private restoreErrorKey(err: ApiError): string {
    if (err.status === 0) return 'setup.error.network';
    switch (err.code) {
      case 'restore_unreadable':
        return 'setup.restore.error.unreadable';
      case 'passphrase_required':
        return 'setup.restore.error.passphrase_required';
      case 'setup_complete':
        return 'setup.error.setup_complete';
      default:
        return 'setup.restore.error.generic';
    }
  }

  prevStep(): void {
    if (this.step() === 2) this.step.set(1);
  }

  submit(): void {
    this.accountForm.markAllAsTouched();
    if (this.accountForm.invalid) return;

    this.loading.set(true);
    this.serverError.set(null);

    const av = this.accountForm.value;

    this.setupService
      .createParent({
        user_id: av.user_id!,
        password: av.password!,
        display_name: av.display_name!,
        email: av.email!,
        family_name: av.family_name!,
        timezone: av.timezone!,
        locale: this.transloco.getActiveLang(),
      })
      .subscribe({
        next: () => {
          this.loading.set(false);
          // Setup does not sign the parent in; the success step leads to the login page.
          this.step.set(3);
        },
        error: (err: ApiError) => {
          this.loading.set(false);
          this.serverError.set(this.errorKey(err));
        },
      });
  }

  private errorKey(err: ApiError): string {
    if (err.status === 0) return 'setup.error.network';
    switch (err.code) {
      case 'user_id_taken':
        return 'setup.error.user_id_taken';
      case 'email_taken':
        return 'setup.error.email_taken';
      case 'setup_complete':
        return 'setup.error.setup_complete';
      case 'validation_failed':
        return 'setup.error.validation';
      default:
        return 'setup.error.generic';
    }
  }

  goToLogin(): void {
    this.router.navigate(['/login']);
  }
}
