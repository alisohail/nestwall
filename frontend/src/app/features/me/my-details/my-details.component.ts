import { Component, DestroyRef, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormBuilder, ReactiveFormsModule, Validators } from '@angular/forms';
import { TranslocoModule } from '@ngneat/transloco';
import { ApiError } from '../../../core/api/api-client.service';
import { AuthService, OwnProfileRequest } from '../../../core/auth/auth.service';
import { BIRTH_DATE_MIN, EMAIL_MAX, NAME_MAX, birthDateRange, todayIso } from '../../../shared/profile/profile-fields';
import { BannerComponent } from '../../../shared/ui/banner/banner.component';
import { LoadingComponent } from '../../../shared/ui/loading/loading.component';
import { UnsavedChangesAware } from '../../../core/unsaved/unsaved-changes';

@Component({
    selector: 'app-my-details',
    imports: [ReactiveFormsModule, TranslocoModule, BannerComponent, LoadingComponent],
    templateUrl: './my-details.component.html',
    styleUrl: './my-details.component.scss'
})
export class MyDetailsComponent implements UnsavedChangesAware {
  private fb = inject(FormBuilder);
  private auth = inject(AuthService);
  private destroyRef = inject(DestroyRef);
  /** The values as last loaded or saved. Anything else counts as unsaved. */
  private baseline = '';

  readonly today = todayIso();
  readonly birthDateMin = BIRTH_DATE_MIN;
  readonly genders = ['female', 'male', 'prefer_not_to_say'];
  /** Parents must keep an email address. The server enforces this too. */
  readonly isParent = this.auth.currentUser()?.family?.is_parent === true;

  loading = signal(false);
  saved = signal(false);
  error = signal<string | null>(null);

  readonly form = this.fb.nonNullable.group({
    display_name: [
      this.auth.currentUser()?.user.display_name ?? '',
      [Validators.required, Validators.pattern(/\S/), Validators.maxLength(NAME_MAX)],
    ],
    email: [
      this.auth.currentUser()?.user.email ?? '',
      this.isParent
        ? [Validators.required, Validators.email, Validators.maxLength(EMAIL_MAX)]
        : [Validators.email, Validators.maxLength(EMAIL_MAX)],
    ],
    date_of_birth: [this.auth.currentUser()?.user.date_of_birth ?? '', [birthDateRange]],
    gender: [this.auth.currentUser()?.user.gender ?? ''],
  });

  constructor() {
    this.baseline = this.snapshot();
  }

  save(): void {
    void this.saveChanges();
  }

  hasUnsavedChanges(): boolean {
    return this.snapshot() !== this.baseline;
  }

  saveChanges(): Promise<boolean> {
    this.saved.set(false);
    this.error.set(null);
    if (this.form.invalid) {
      this.form.markAllAsTouched();
      return Promise.resolve(false);
    }
    const v = this.form.getRawValue();
    const req: OwnProfileRequest = {
      display_name: v.display_name.trim(),
      email: v.email.trim().toLowerCase(),
      date_of_birth: v.date_of_birth,
      gender: v.gender,
    };
    this.loading.set(true);
    return new Promise<boolean>((resolve) => {
      this.auth
        .updateProfile(req)
        .pipe(takeUntilDestroyed(this.destroyRef))
        .subscribe({
          next: () => {
            this.loading.set(false);
            this.saved.set(true);
            this.baseline = this.snapshot();
            resolve(true);
          },
          error: (err: ApiError) => {
            this.loading.set(false);
            this.error.set(this.errorKey(err));
            resolve(false);
          },
        });
    });
  }

  private snapshot(): string {
    return JSON.stringify(this.form.getRawValue());
  }

  private errorKey(err: ApiError): string {
    if (err.status === 0) return 'family.error.network';
    if (err.code === 'email_taken') return 'family.edit.error.email_taken';
    if (err.status === 400) return 'family.edit.error.invalid';
    if (err.status === 403) return 'family.error.forbidden';
    return 'family.error.generic';
  }
}
