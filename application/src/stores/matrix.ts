import { create } from 'zustand'
import type { MatrixCell, TelemetryUpdate } from '../lib/types'

// Crosspoint matrix state (SVoT). Updated only from API responses and SSE events (no optimistic updates).

type MatrixState = {
  /** Cells keyed by cell_id (e.g. "IN1-OUT1"). */
  cells: Record<string, MatrixCell>
  loaded: boolean
  /** Replace everything (initial load / resync after reconnect). */
  setCells: (cells: MatrixCell[]) => void
  /** Apply a full cell from `matrix_update` or an API response. */
  upsertCell: (cell: MatrixCell) => void
  /** Apply `telemetry_update`: only health and telemetry change, and only for known cells. */
  applyTelemetry: (updates: TelemetryUpdate[]) => void
}

export const useMatrixStore = create<MatrixState>((set) => ({
  cells: {},
  loaded: false,
  setCells: (cells) =>
    set({ cells: Object.fromEntries(cells.map((c) => [c.cell_id, c])), loaded: true }),
  upsertCell: (cell) => set((s) => ({ cells: { ...s.cells, [cell.cell_id]: cell } })),
  applyTelemetry: (updates) =>
    set((s) => {
      let changed = false
      const cells = { ...s.cells }
      for (const u of updates) {
        const cell = cells[u.cell_id]
        if (!cell) continue
        // New object per changed cell so only subscribers of that cell re-render.
        cells[u.cell_id] = { ...cell, health_state: u.health_state, telemetry: u.telemetry }
        changed = true
      }
      return changed ? { cells } : s
    }),
}))

/** Subscribe to a single cell; the component re-renders only when that cell changes. */
export const useCell = (cellId: string) => useMatrixStore((s) => s.cells[cellId])
