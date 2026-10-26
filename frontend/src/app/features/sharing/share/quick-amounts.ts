/**
 * Quick amounts for the share screen, built from the parent's range. The least and the most are
 * always included. The two values between them are spread evenly and rounded to a friendly step
 * (1, 5 or 10, by the width of the range). Duplicates are dropped, so a tiny range gives fewer chips.
 */
export function quickAmounts(min: number, max: number): number[] {
  if (!Number.isFinite(min) || !Number.isFinite(max) || min < 1 || max < min) return [];
  const span = max - min;
  const step = span >= 100 ? 10 : span >= 20 ? 5 : 1;
  const values = [min];
  for (let i = 1; i <= 2; i++) {
    const raw = min + (span * i) / 3;
    const rounded = Math.round(raw / step) * step;
    values.push(Math.min(max, Math.max(min, rounded)));
  }
  values.push(max);
  return [...new Set(values)].sort((a, b) => a - b);
}
