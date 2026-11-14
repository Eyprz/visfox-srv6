import { useEffect, useRef, useState } from 'react'
import { useT } from '../i18n'
import { api, request } from '../lib/api'
import { notifyError } from '../lib/errors'
import { issueText } from '../lib/issues'
import { useToastStore } from '../stores/toasts'
import type { MatrixCell } from '../lib/types'
import { useMatrixStore } from '../stores/matrix'
import { useSwitchersStore } from '../stores/switchers'

const FAILED_FLASH_MS = 2000

async function refreshCell(cellId: string) {
  const cell = await request(api.GET('/matrix/cells/{cellId}', { params: { path: { cellId } } }))
  useMatrixStore.getState().upsertCell(cell)
}

/**
 * Take (STANDBY -> ACTIVE) shared by the cell gestures and the inspector button.
 * No confirmation; the request is single-flight. On success the activated and demoted cells are
 * re-fetched (SSE delivers them too, this keeps the view right if SSE is down). On failure the reason is
 * toasted and `failed` is true for a short time so the cell can flash an error.
 * A STANDBY cell the IEL reports as not takeable now (`take_issues`, e.g. IN busy, no free switcher output) is not
 * sent: `canTake` is false for buttons and menus, and gestures (`take`) toast the reason instead.
 */
export function useTake(cell: MatrixCell | undefined) {
  const t = useT()
  const [pending, setPending] = useState(false)
  const [failed, setFailed] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  useEffect(() => () => clearTimeout(timer.current), [])

  // Every reason the IEL reports (IN busy, no switcher output, no VVF node, ...).
  const blockers = cell?.execution_state === 'standby' ? (cell.take_issues ?? []) : []
  const blockedBy = blockers[0] ?? null
  const canTake = cell?.execution_state === 'standby' && !pending && !blockedBy

  const flashFailed = () => {
    setFailed(true)
    clearTimeout(timer.current)
    timer.current = setTimeout(() => setFailed(false), FAILED_FLASH_MS)
  }

  const take = async () => {
    if (!cell || cell.execution_state !== 'standby' || pending) return
    if (blockedBy) {
      useToastStore.getState().push({
        tone: 'error',
        title: t('take.blocked'),
        description: blockers.map((i) => issueText(t, i)).join(' '),
      })
      flashFailed()
      return
    }
    setPending(true)
    try {
      const result = await request(
        api.POST('/matrix/cells/{cellId}/take', { params: { path: { cellId: cell.cell_id } } }),
      )
      // The headend and the switchers switch together; show the new inputs at once (SSE follows).
      useSwitchersStore.getState().applyStates(result.switchers ?? [])
      await Promise.all(
        [result.activated_cell, result.deactivated_cell]
          .filter((id): id is string => Boolean(id))
          .map(refreshCell),
      )
    } catch (error) {
      notifyError(t, error)
      flashFailed()
    } finally {
      setPending(false)
    }
  }

  return { take, canTake, blockedBy, blockers, pending, failed }
}
