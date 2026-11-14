import { describe, expect, it } from 'vitest'
import { centerOn, clampCrop, cropBounds, moveCrop } from './crop'

const schema = { properties: { x: { maximum: 5760 }, y: { maximum: 3240 } } }
const b = cropBounds(schema)!

describe('crop geometry', () => {
  it('derives the 8K frame from the schema maxima and the FHD region', () => {
    expect(b).toEqual({ maxX: 5760, maxY: 3240, frameW: 7680, frameH: 4320 })
    expect(cropBounds({ properties: {} })).toBeNull()
  })
  it('keeps the region inside the frame', () => {
    expect(clampCrop({ x: -10, y: 9999 }, b)).toEqual({ x: 0, y: 3240 })
  })
  it('centers the region on a point', () => {
    expect(centerOn({ x: 3840, y: 2160 }, b)).toEqual({ x: 2880, y: 1620 })
    expect(centerOn({ x: 100, y: 100 }, b)).toEqual({ x: 0, y: 0 })
  })
  it('moves in a direction and stops at the edge', () => {
    expect(moveCrop({ x: 100, y: 100 }, 'right', 50, b)).toEqual({ x: 150, y: 100 })
    expect(moveCrop({ x: 10, y: 100 }, 'left', 50, b)).toEqual({ x: 0, y: 100 })
  })
})
