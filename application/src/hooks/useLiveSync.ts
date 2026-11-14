import { useEffect } from 'react'
import { startLiveSync } from '../lib/sse'

/** Starts the REST snapshot + SSE live sync for the lifetime of the app shell. */
export function useLiveSync(): void {
  // StrictMode mounts effects twice in development; the cleanup closes the first EventSource.
  useEffect(() => startLiveSync(), [])
}
