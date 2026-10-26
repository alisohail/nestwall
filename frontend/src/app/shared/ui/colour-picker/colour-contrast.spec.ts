import { contrastRatio, isLowContrast } from './colour-contrast';
import { COLOUR_PALETTE } from './colour-picker.component';

describe('colour contrast', () => {
  it('keeps every palette colour readable on the light and dark card', () => {
    COLOUR_PALETTE.forEach((colour) => expect(isLowContrast(colour)).withContext(colour).toBeFalse());
  });

  it('measures black on white as 21:1', () => {
    expect(contrastRatio('#000000', '#ffffff')).toBeCloseTo(21, 1);
  });

  it('flags colours that are too faint on the light or dark card', () => {
    expect(isLowContrast('#ffffff')).toBeTrue();
    expect(isLowContrast('#f59e0b')).toBeTrue();
    expect(isLowContrast('#221d52')).toBeTrue();
  });

  it('accepts a readable colour', () => {
    expect(isLowContrast('#6366f1')).toBeFalse();
  });

  it('does not judge values that are not six-digit hex colours', () => {
    expect(isLowContrast('')).toBeFalse();
    expect(isLowContrast('red')).toBeFalse();
    expect(isLowContrast('#fff')).toBeFalse();
  });
});
