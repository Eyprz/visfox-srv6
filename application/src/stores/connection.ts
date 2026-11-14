import { create } from 'zustand'

// SSE connection status shown in the header.

export type ConnectionStatus = 'connecting' | 'connected' | 'reconnecting' | 'disconnected'
export type BootstrapStatus = 'loading' | 'ready' | 'failed'

type ConnectionState = {
  status: ConnectionStatus
  /** Initial snapshot load (REST) state. */
  bootstrap: BootstrapStatus
  /** Epoch ms of the last SSE event (any type, including heartbeat). */
  lastEventAt: number | null
  setStatus: (status: ConnectionStatus) => void
  setBootstrap: (bootstrap: BootstrapStatus) => void
  markEvent: () => void
}

export const useConnectionStore = create<ConnectionState>((set) => ({
  status: 'connecting',
  bootstrap: 'loading',
  lastEventAt: null,
  setStatus: (status) => set({ status }),
  setBootstrap: (bootstrap) => set({ bootstrap }),
  markEvent: () => set({ lastEventAt: Date.now() }),
}))
