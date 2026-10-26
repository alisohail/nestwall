/** Light and dark card surfaces (see the theme tokens). A colour must reach MIN_RATIO against both. */
const SURFACES = ['#ffffff', '#221d52'];

/** WCAG 2.x non-text contrast: 3:1 for graphics and UI components. */
const MIN_RATIO = 3;

const HEX = /^#[0-9a-f]{6}$/i;

function luminance(hex: string): number {
  const [r, g, b] = [1, 3, 5].map((i) => {
    const c = parseInt(hex.slice(i, i + 2), 16) / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

export function contrastRatio(a: string, b: string): number {
  const [light, dark] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (light + 0.05) / (dark + 0.05);
}

/** True when a #RRGGBB colour is too faint on either surface. Values that are not hex colours are not judged. */
export function isLowContrast(hex: string): boolean {
  if (!HEX.test(hex)) return false;
  return SURFACES.some((surface) => contrastRatio(hex, surface) < MIN_RATIO);
}
