import { useEffect, useState } from 'react'
import { api, ApiError, request } from '../lib/api'
import type { CellPath } from '../lib/types'
import { useMatrixStore } from '../stores/matrix'

/** Refresh interval of the real path (hop metrics change with telemetry). */
export const PATH_REFRESH_MS = 2000

export type CellPathState = {
  path: CellPath | null
  /** inactive: OFF / DISCONNECTED (409); notFound: unknown cell (404); failed: other errors. */
  error: 'inactive' | 'notFound' | 'failed' | null
  loading: boolean
}

/**
 * Real path of a cell (GET /matrix/cells/{cellId}/path): loaded on selection,
 * refreshed every PATH_REFRESH_MS and whenever the cell's state or preset changes. L3 data in the
 * result is for display only.
 */
export function useCellPath(cellId: string | undefined): CellPathState {
  const [state, setState] = useState<CellPathState>({ path: null, error: null, loading: true })
  // Re-fetch at once when the cell changes state or preset (SSE matrix_update).
  const version = useMatrixStore((s) => {
    const c = cellId ? s.cells[cellId] : undefined
    return c ? `${c.execution_state}:${c.preset_id ?? ''}:${c.health_state ?? ''}` : ''
  })

  useEffect(() => {
    if (!cellId) return
    let alive = true
    setState((s) => ({ ...s, loading: s.path?.cell_id !== cellId }))
    const load = () =>
      request(api.GET('/matrix/cells/{cellId}/path', { params: { path: { cellId } } }))
        .then((path) => alive && setState({ path, error: null, loading: false }))
        .catch((e) => {
          if (!alive) return
          const error =
            e instanceof ApiError && e.status === 409
              ? 'inactive'
              : e instanceof ApiError && e.status === 404
                ? 'notFound'
                : 'failed'
          setState({ path: null, error, loading: false })
        })
    void load()
    const timer = setInterval(load, PATH_REFRESH_MS)
    return () => {
      alive = false
      clearInterval(timer)
    }
  }, [cellId, version])

  return cellId ? state : { path: null, error: null, loading: false }
}
