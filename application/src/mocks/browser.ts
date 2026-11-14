import { setupWorker } from 'msw/browser'
import { degradeCell, faults, type FaultMode } from './db'
import { startHeartbeat } from './events'
import { handlers, startTelemetry } from './handlers'
import { MockEventSource } from './mockEventSource'
import { log } from './db'

declare global {
  interface Window {
    /** Mock-only controls for demos (browser console). */
    visfoxMock?: {
      /** Make the next router apply fail: 'error' (502), 'timeout' (504), or 'rollback_fail' (CRITICAL). */
      failNextApply: (mode?: FaultMode) => void
      /** Simulate a large degradation on a cell and roll it back to its LKG (rollback demo). */
      degrade: (cellId: string) => void
    }
  }
}

export async function startMockIel(): Promise<void> {
  const worker = setupWorker(...handlers)
  await worker.start({ onUnhandledFrame: 'bypass', quiet: true })
  if (!('serviceWorker' in navigator)) {
    // Insecure origin: MSW falls back to patching fetch, which does not cover EventSource.
    window.EventSource = MockEventSource as unknown as typeof EventSource
    console.info(
      '[visfox mock] no Service Worker (insecure origin); using in-page fetch and SSE mocks',
    )
  }
  startTelemetry()
  startHeartbeat()
  window.visfoxMock = {
    failNextApply: (mode = 'error') => {
      faults.next = mode
      console.info(`[visfox mock] next router apply will fail: ${mode}`)
    },
    degrade: (cellId) => void degradeCell(cellId),
  }
  log('info', 'Mock IEL (MSW) started')
}
