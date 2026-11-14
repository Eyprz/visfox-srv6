import { useEffect, useRef, useState, type PointerEvent } from 'react'

export const LONG_PRESS_MS = 800
const MOVE_TOLERANCE_PX = 10

/**
 * Touch / pen long-press. Mouse is ignored (PC takes by double-click).
 * Returns pointer handlers and `pressing` for the progress ring; releasing, moving, or leaving cancels.
 * Vibrates on completion where supported.
 */
export function useLongPress(onComplete: () => void, enabled: boolean) {
  const [pressing, setPressing] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const start = useRef<{ x: number; y: number } | null>(null)
  const cancel = () => {
    clearTimeout(timer.current)
    start.current = null
    setPressing(false)
  }
  useEffect(() => () => clearTimeout(timer.current), [])

  const handlers = {
    onPointerDown: (e: PointerEvent) => {
      if (!enabled || e.pointerType === 'mouse') return
      start.current = { x: e.clientX, y: e.clientY }
      setPressing(true)
      timer.current = setTimeout(() => {
        setPressing(false)
        start.current = null
        navigator.vibrate?.(40)
        onComplete()
      }, LONG_PRESS_MS)
    },
    onPointerMove: (e: PointerEvent) => {
      if (!start.current) return
      if (Math.hypot(e.clientX - start.current.x, e.clientY - start.current.y) > MOVE_TOLERANCE_PX)
        cancel()
    },
    onPointerUp: cancel,
    onPointerLeave: cancel,
    onPointerCancel: cancel,
  }
  return { handlers, pressing }
}
