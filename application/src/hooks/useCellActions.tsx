import { useState, type ReactNode } from 'react'
import { ConfirmDialog } from '../components/common/ConfirmDialog'
import { useT } from '../i18n'
import { api, request } from '../lib/api'
import { notifyError } from '../lib/errors'
import type { MatrixCell } from '../lib/types'
import { useMatrixStore } from '../stores/matrix'
import { useResourcesStore } from '../stores/resources'

type Pending = 'standby' | 'off' | null

/**
 * SET STANDBY and OFF for the inspector. Both require confirmation when the
 * cell is ON AIR; OFF on a STANDBY cell runs immediately. Returns the actions and the dialog element.
 */
export function useCellActions(cell: MatrixCell | undefined) {
  const t = useT()
  const [confirm, setConfirm] = useState<Exclude<Pending, null>>()
  const [pending, setPending] = useState<Pending>(null)
  const outName = useResourcesStore(
    (s) =>
      s.destinations.find((d) => d.id === cell?.destination_id)?.name ?? cell?.destination_id ?? '',
  )

  const run = async (kind: Exclude<Pending, null>) => {
    if (!cell) return
    setPending(kind)
    try {
      const path = { params: { path: { cellId: cell.cell_id } } }
      const updated =
        kind === 'standby'
          ? await request(api.POST('/matrix/cells/{cellId}/standby', path))
          : await request(api.DELETE('/matrix/cells/{cellId}/preset', path))
      useMatrixStore.getState().upsertCell(updated)
    } catch (error) {
      notifyError(t, error)
    } finally {
      setPending(null)
    }
  }

  const setStandby = () => setConfirm('standby')
  const turnOff = () => (cell?.execution_state === 'active' ? setConfirm('off') : void run('off'))

  const dialog: ReactNode =
    cell && confirm ? (
      <ConfirmDialog
        open
        onOpenChange={(open) => !open && setConfirm(undefined)}
        title={t(confirm === 'standby' ? 'standby.confirmTitle' : 'off.confirmTitle', {
          cell: cell.cell_id,
        })}
        description={t(confirm === 'standby' ? 'standby.confirmBody' : 'off.confirmBody', {
          out: outName,
        })}
        confirmLabel={t(confirm === 'standby' ? 'standby.confirm' : 'off.confirm')}
        tone="danger"
        onConfirm={() => run(confirm)}
      />
    ) : null

  return { setStandby, turnOff, pending, dialog }
}
