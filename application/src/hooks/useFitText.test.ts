import { describe, expect, it } from 'vitest'
import { fitFontSize } from './useFitText'

describe('fitFontSize', () => {
  const base = { maxPx: 18, minPx: 12 }
  it('keeps the maximum size when the text fits', () => {
    expect(fitFontSize({ ...base, naturalPx: 100, availablePx: 150 })).toBe(18)
  })
  it('shrinks proportionally to fit the width', () => {
    expect(fitFontSize({ ...base, naturalPx: 180, availablePx: 150 })).toBe(15)
  })
  it('never goes below the minimum (the rest is truncated)', () => {
    expect(fitFontSize({ ...base, naturalPx: 400, availablePx: 150 })).toBe(12)
  })
})
