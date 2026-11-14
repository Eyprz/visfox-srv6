import { useLayoutEffect, type RefObject } from 'react'

/**
 * Font size (px) that fits text of `naturalPx` width (measured at `maxPx`) into `availablePx`:
 * the largest size up to `maxPx`, never below `minPx` (below that the caller truncates with an ellipsis).
 */
export function fitFontSize({
  naturalPx,
  availablePx,
  maxPx,
  minPx,
}: {
  naturalPx: number
  availablePx: number
  maxPx: number
  minPx: number
}): number {
  if (naturalPx <= availablePx || naturalPx <= 0) return maxPx
  return Math.max(minPx, Math.floor((maxPx * availablePx) / naturalPx))
}

const remPx = () => parseFloat(getComputedStyle(document.documentElement).fontSize) || 16

/**
 * Shrinks a single-line element's font size so its text fits the element's width
 * (matrix headers). The element must be `white-space: nowrap` with
 * `overflow: hidden; text-overflow: ellipsis` so text that would need less than `minRem` is cut with "...".
 * Re-fits on resize (cell size / zoom), on text change, and once web fonts have loaded.
 */
export function useFitText(
  ref: RefObject<HTMLElement | null>,
  text: string,
  { maxRem, minRem }: { maxRem: number; minRem: number },
) {
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const fit = () => {
      const rem = remPx()
      const maxPx = maxRem * rem
      el.style.fontSize = `${maxPx}px`
      el.style.fontSize = `${fitFontSize({
        naturalPx: el.scrollWidth,
        availablePx: el.clientWidth,
        maxPx,
        minPx: minRem * rem,
      })}px`
    }
    fit()
    const observer = new ResizeObserver(fit)
    observer.observe(el)
    let alive = true
    void document.fonts?.ready.then(() => alive && fit())
    return () => {
      alive = false
      observer.disconnect()
    }
  }, [ref, text, maxRem, minRem])
}
