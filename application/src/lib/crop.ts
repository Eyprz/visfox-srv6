// Geometry of the crop position controller. The crop function cuts an FHD
// region out of the 8K main stream; x / y are the region's top-left corner in 8K coordinates and their
// ranges come from the IEL param_schema (0..maximum). The region size is the FHD output of `crop`.

export const CROP_REGION = { w: 1920, h: 1080 } as const

export type CropPoint = { x: number; y: number }
export type CropBounds = {
  maxX: number
  maxY: number
  /** Full frame size: the region moves inside it. */
  frameW: number
  frameH: number
}

/** Bounds from the param_schema maxima (x: 0..maxX, y: 0..maxY); the frame is max + region size. */
export function cropBounds(schema: unknown): CropBounds | null {
  const props = (schema as { properties?: Record<string, { maximum?: number }> })?.properties
  const maxX = props?.x?.maximum
  const maxY = props?.y?.maximum
  if (typeof maxX !== 'number' || typeof maxY !== 'number') return null
  return { maxX, maxY, frameW: maxX + CROP_REGION.w, frameH: maxY + CROP_REGION.h }
}

export function clampCrop(p: CropPoint, b: CropBounds): CropPoint {
  return {
    x: Math.round(Math.min(b.maxX, Math.max(0, p.x))),
    y: Math.round(Math.min(b.maxY, Math.max(0, p.y))),
  }
}

/** Top-left that centers the region on a frame point (click on the frame). */
export function centerOn(p: CropPoint, b: CropBounds): CropPoint {
  return clampCrop({ x: p.x - CROP_REGION.w / 2, y: p.y - CROP_REGION.h / 2 }, b)
}

export type Direction = 'up' | 'down' | 'left' | 'right'
const unit: Record<Direction, CropPoint> = {
  up: { x: 0, y: -1 },
  down: { x: 0, y: 1 },
  left: { x: -1, y: 0 },
  right: { x: 1, y: 0 },
}

export function moveCrop(p: CropPoint, dir: Direction, distance: number, b: CropBounds): CropPoint {
  return clampCrop({ x: p.x + unit[dir].x * distance, y: p.y + unit[dir].y * distance }, b)
}
