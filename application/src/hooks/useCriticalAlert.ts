import { useEffect } from 'react'
import { newlyCritical, playAlert } from '../lib/alertSound'
import type { HealthState } from '../lib/types'
import { useMatrixStore } from '../stores/matrix'
import { useUiStore } from '../stores/ui'

/** Plays the alert sound when a cell newly becomes CRITICAL and the sound is enabled. */
export function useCriticalAlert(): void {
  useEffect(() => {
    const healthOf = (cells: ReturnType<typeof useMatrixStore.getState>['cells']) =>
      Object.fromEntries(Object.values(cells).map((c) => [c.cell_id, c.health_state])) as Record<
        string,
        HealthState | undefined
      >
    let previous = healthOf(useMatrixStore.getState().cells)
    // subscribe() runs outside React rendering, so frequent telemetry updates do not re-render anything here.
    return useMatrixStore.subscribe((state) => {
      const next = healthOf(state.cells)
      if (useUiStore.getState().alertSound && newlyCritical(previous, next).length > 0) playAlert()
      previous = next
    })
  }, [])
}
