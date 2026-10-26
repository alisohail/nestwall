import { AbstractControl, ValidationErrors } from '@angular/forms';

export const NAME_MAX = 80;
export const EMAIL_MAX = 254;
export const BIRTH_DATE_MIN = '1900-01-01';

/** Local calendar date as YYYY-MM-DD, so the date input and the future-date check agree. */
export function todayIso(): string {
  return new Date().toLocaleDateString('en-CA');
}

/** Rejects a date that is malformed, in the future, or before 1900. ISO dates compare as strings. */
export function birthDateRange(control: AbstractControl): ValidationErrors | null {
  const value = control.value as string;
  if (!value) return null;
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return { format: true };
  if (value > todayIso() || value < BIRTH_DATE_MIN) return { range: true };
  return null;
}
