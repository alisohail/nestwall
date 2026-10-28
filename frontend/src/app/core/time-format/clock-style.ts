/**
 * The family's clock style, kept at module level so pure formatters (for example clockIn) match the
 * setting without a parameter on every call. TimeFormatService keeps it in sync with the signed-in family.
 */
let twelveHour = true;

export function setTwelveHour(value: boolean): void {
  twelveHour = value;
}

export function isTwelveHour(): boolean {
  return twelveHour;
}

/** Intl options with hour12 set to the family's clock style. */
export function clockOptions(options: Intl.DateTimeFormatOptions): Intl.DateTimeFormatOptions {
  return { ...options, hour12: twelveHour };
}
