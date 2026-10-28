import { contrastRatio, isLowContrast } from './colour-contrast';
import { COLOUR_PALETTE } from './colour-picker.component';

describe('colour contrast', () => {
    it('keeps every palette colour readable on the light and dark card', () => {
        COLOUR_PALETTE.forEach((colour) => expect(isLowContrast(colour), colour).toBe(false));
    });

    it('measures black on white as 21:1', () => {
        expect(contrastRatio('#000000', '#ffffff')).toBeCloseTo(21, 1);
    });

    it('flags colours that are too faint on the light or dark card', () => {
        expect(isLowContrast('#ffffff')).toBe(true);
        expect(isLowContrast('#f59e0b')).toBe(true);
        expect(isLowContrast('#221d52')).toBe(true);
    });

    it('accepts a readable colour', () => {
        expect(isLowContrast('#6366f1')).toBe(false);
    });

    it('does not judge values that are not six-digit hex colours', () => {
        expect(isLowContrast('')).toBe(false);
        expect(isLowContrast('red')).toBe(false);
        expect(isLowContrast('#fff')).toBe(false);
    });
});
