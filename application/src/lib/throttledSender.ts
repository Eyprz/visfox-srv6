// Live parameter sending rule:
// - while the operator drags / holds, send the latest value at most every `intervalMs` (100 ms)
// - only the latest value is sent; intermediate values are dropped (no queue)
// - at most one request in flight; the next one waits for the response
// - flush() at the end of the gesture always sends the final value
// - after a failed send, pending values are dropped so they do not fight the IEL rollback

export type ThrottledSender<T> = {
  push: (value: T) => void
  /** Sends the pending value now (after any in-flight request) and resolves when everything is sent. */
  flush: () => Promise<void>
  /** Drops the pending value and stops timers (in-flight requests still complete). */
  cancel: () => void
}

export type ThrottledSenderOptions = {
  intervalMs?: number
  onError?: (error: unknown) => void
  now?: () => number
}

export function createThrottledSender<T>(
  send: (value: T) => Promise<unknown>,
  { intervalMs = 100, onError, now = Date.now }: ThrottledSenderOptions = {},
): ThrottledSender<T> {
  let pending: { value: T } | null = null
  let inFlight: Promise<void> | null = null
  let timer: ReturnType<typeof setTimeout> | null = null
  let lastSentAt = -Infinity

  const fire = () => {
    timer = null
    if (!pending || inFlight) return
    const { value } = pending
    pending = null
    lastSentAt = now()
    inFlight = send(value)
      .then(
        () => undefined,
        (error: unknown) => {
          pending = null
          onError?.(error)
        },
      )
      .finally(() => {
        inFlight = null
        if (pending) schedule()
      })
  }

  const schedule = () => {
    if (timer || inFlight || !pending) return
    const wait = Math.max(0, lastSentAt + intervalMs - now())
    if (wait === 0) fire()
    else timer = setTimeout(fire, wait)
  }

  return {
    push(value) {
      pending = { value }
      schedule()
    },
    async flush() {
      while (inFlight || pending) {
        if (inFlight) await inFlight
        if (pending) {
          if (timer) clearTimeout(timer)
          timer = null
          fire()
        }
      }
    },
    cancel() {
      pending = null
      if (timer) clearTimeout(timer)
      timer = null
    },
  }
}
