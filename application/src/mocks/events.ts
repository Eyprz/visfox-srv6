import type { SseEvent } from '../lib/types'

// In-memory SSE hub for the mock IEL (`GET /api/v1/events`).

type Frame = { id: string; event: SseEvent['type']; data: string }
type Listener = (frame: Frame) => void

const BUFFER_SIZE = 500
const buffer: Frame[] = []
const listeners = new Set<Listener>()
let seq = 0
let lastSentAt = Date.now()

export function publish(event: SseEvent): void {
  seq += 1
  const frame: Frame = { id: String(seq), event: event.type, data: JSON.stringify(event) }
  buffer.push(frame)
  if (buffer.length > BUFFER_SIZE) buffer.shift()
  lastSentAt = Date.now()
  listeners.forEach((listener) => listener(frame))
}

/**
 * Subscribe a client. When `lastEventId` is still in the buffer, missed events are replayed;
 * otherwise nothing is replayed and the client is expected to resync with `GET /matrix`.
 */
export function subscribe(listener: Listener, lastEventId?: string | null): () => void {
  if (lastEventId) {
    const index = buffer.findIndex((frame) => frame.id === lastEventId)
    if (index >= 0) buffer.slice(index + 1).forEach(listener)
  }
  listeners.add(listener)
  return () => listeners.delete(listener)
}

/** Heartbeat when no other event has been sent for 15 s. */
export function startHeartbeat(): void {
  setInterval(() => {
    if (Date.now() - lastSentAt >= 15_000) {
      publish({ type: 'heartbeat', timestamp: new Date().toISOString() })
    }
  }, 1_000)
}
