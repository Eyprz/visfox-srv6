import { describe, expect, it } from 'vitest'
import { cellSizeFor, FIT_ROWS, MIN_CELL_PX } from './useMatrixCellSize'

const base = { chromePx: 150, gapPx: 8, maxPx: 200 }

describe('cellSizeFor', () => {
  it('fits four rows into a short viewport (e.g. FHD at 125% OS scaling)', () => {
    const heightPx = 640
    const size = cellSizeFor({ ...base, heightPx })
    expect(size).toBeLessThan(base.maxPx)
    expect(size * FIT_ROWS + base.gapPx * FIT_ROWS + base.chromePx).toBeLessThanOrEqual(heightPx)
  })

  it('caps at the FHD baseline on tall viewports', () => {
    expect(cellSizeFor({ ...base, heightPx: 2000 })).toBe(base.maxPx)
  })

  it('never goes below the minimum', () => {
    expect(cellSizeFor({ ...base, heightPx: 200 })).toBe(MIN_CELL_PX)
  })
})
