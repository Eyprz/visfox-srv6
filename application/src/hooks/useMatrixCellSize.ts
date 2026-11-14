import { useLayoutEffect, useState, type RefObject } from 'react'

/** Rows that fit the available height at the baseline. */
export const FIT_ROWS = 4
/** Never shrink cells below this (px) so labels stay readable. */
export const MIN_CELL_PX = 80

export type CellSizeInput = {
  /** Available height of the matrix region, px. */
  heightPx: number
  /** Fixed chrome inside that height (paddings, borders, header row + gap), px. */
  chromePx: number
  /** Gap between grid tracks, px. */
  gapPx: number
  /** Upper bound (the FHD baseline size), px. */
  maxPx: number
}

/**
 * Square cell size: four rows fit the available height, capped at the FHD baseline so larger screens
 * show more rows / columns instead of bigger cells, and never below MIN_CELL_PX.
 */
export function cellSizeFor({ heightPx, chromePx, gapPx, maxPx }: CellSizeInput): number {
  const fit = Math.floor((heightPx - chromePx - gapPx * FIT_ROWS) / FIT_ROWS)
  return Math.max(MIN_CELL_PX, Math.min(maxPx, fit))
}

export type CellSizeMetricsRem = { chromeRem: number; gapRem: number; maxRem: number }

/**
 * Measures the matrix region (the element spanning the available height) and returns the cell size in
 * px, recomputed on resize / zoom. Metrics are in rem because the root font size scales on 4K.
 * Returns null until measured (e.g. in tests without layout); the CSS default is used meanwhile.
 */
export function useMatrixCellSize(
  ref: RefObject<HTMLElement | null>,
  { chromeRem, gapRem, maxRem }: CellSizeMetricsRem,
): number | null {
  const [size, setSize] = useState<number | null>(null)
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const update = () => {
      const rem = parseFloat(getComputedStyle(document.documentElement).fontSize) || 16
      const heightPx = el.clientHeight
      setSize(
        heightPx > 0
          ? cellSizeFor({
              heightPx,
              chromePx: chromeRem * rem,
              gapPx: gapRem * rem,
              maxPx: maxRem * rem,
            })
          : null,
      )
    }
    update()
    const observer = new ResizeObserver(update)
    observer.observe(el)
    return () => observer.disconnect()
  }, [ref, chromeRem, gapRem, maxRem])
  return size
}
