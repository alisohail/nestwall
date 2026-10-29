import { ChangeDetectorRef, DestroyRef, inject } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { AbstractControl } from '@angular/forms';

/**
 * Re-renders the calling component whenever the form changes. Zoneless apps only
 * refresh on DOM events and signals, so programmatic patches (for example after a
 * load) would otherwise leave the template showing old values.
 * Call from an injection context such as a field initialiser or constructor.
 */
export function refreshOnFormChange(form: AbstractControl): void {
  const cdr = inject(ChangeDetectorRef);
  form.events.pipe(takeUntilDestroyed(inject(DestroyRef))).subscribe(() => cdr.markForCheck());
}
