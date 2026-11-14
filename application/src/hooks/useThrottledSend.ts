import { useEffect, useMemo, useRef } from 'react'
import { createThrottledSender, type ThrottledSender } from '../lib/throttledSender'

/**
 * React wrapper around createThrottledSender for sliders and the crop controller.
 *
 *   const sender = useThrottledSend((params) => patchParams(params), { onError })
 *   onChange={(v) => sender.push(v)}  onPointerUp={() => sender.flush()}
 *
 * The latest `send` / `onError` are read through refs, so the sender is created once per component.
 * On unmount the final value is still sent.
 */
export function useThrottledSend<T>(
  send: (value: T) => Promise<unknown>,
  options: { intervalMs?: number; onError?: (error: unknown) => void } = {},
): ThrottledSender<T> {
  const sendRef = useRef(send)
  const onErrorRef = useRef(options.onError)
  useEffect(() => {
    sendRef.current = send
    onErrorRef.current = options.onError
  })

  const intervalMs = options.intervalMs ?? 100
  const sender = useMemo(
    () =>
      createThrottledSender<T>((value) => sendRef.current(value), {
        intervalMs,
        onError: (error) => onErrorRef.current?.(error),
      }),
    [intervalMs],
  )

  useEffect(
    () => () => {
      void sender.flush()
    },
    [sender],
  )
  return sender
}
