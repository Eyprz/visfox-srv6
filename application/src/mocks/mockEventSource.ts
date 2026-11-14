import { subscribe } from './events'

/**
 * EventSource replacement used when MSW cannot register a Service Worker (insecure origins such as
 * http://<LAN IP>). MSW then falls back to patching fetch in the page, which does not cover EventSource,
 * so this class connects `/api/v1/events` straight to the in-memory mock event hub.
 */
export class MockEventSource extends EventTarget {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSED = 2
  readonly CONNECTING = 0
  readonly OPEN = 1
  readonly CLOSED = 2

  readonly url: string
  readonly withCredentials = false
  readyState: number = MockEventSource.CONNECTING
  onopen: ((this: EventSource, ev: Event) => unknown) | null = null
  onerror: ((this: EventSource, ev: Event) => unknown) | null = null
  onmessage: ((this: EventSource, ev: MessageEvent) => unknown) | null = null
  private unsubscribe: (() => void) | null = null

  constructor(url: string | URL) {
    super()
    this.url = String(url)
    // Open asynchronously like a real EventSource.
    setTimeout(() => {
      if (this.readyState === MockEventSource.CLOSED) return
      this.readyState = MockEventSource.OPEN
      const open = new Event('open')
      this.dispatchEvent(open)
      this.onopen?.call(this as unknown as EventSource, open)
      this.unsubscribe = subscribe((frame) => {
        const message = new MessageEvent(frame.event, { data: frame.data, lastEventId: frame.id })
        this.dispatchEvent(message)
      })
    }, 0)
  }

  close(): void {
    this.readyState = MockEventSource.CLOSED
    this.unsubscribe?.()
    this.unsubscribe = null
  }
}
