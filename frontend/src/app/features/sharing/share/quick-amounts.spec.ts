import { quickAmounts } from './quick-amounts';

describe('quickAmounts', () => {
  it('spreads four friendly amounts across the parent range', () => {
    expect(quickAmounts(5, 30)).toEqual([5, 15, 20, 30]);
    expect(quickAmounts(100, 500)).toEqual([100, 230, 370, 500]);
  });

  it('always includes the least and the most', () => {
    const values = quickAmounts(7, 90);
    expect(values[0]).toBe(7);
    expect(values[values.length - 1]).toBe(90);
  });

  it('drops duplicates on a tiny range', () => {
    expect(quickAmounts(1, 3)).toEqual([1, 2, 3]);
    expect(quickAmounts(7, 7)).toEqual([7]);
  });

  it('returns nothing for an empty or inverted range', () => {
    expect(quickAmounts(10, 5)).toEqual([]);
    expect(quickAmounts(0, 10)).toEqual([]);
  });
});
