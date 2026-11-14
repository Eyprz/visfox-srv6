import { useState, type ReactNode } from 'react'
import { ConfirmDialog } from '../components/common/ConfirmDialog'
import { useT } from '../i18n'
import { api, request } from '../lib/api'
import { notifyError } from '../lib/errors'
import { issueText } from '../lib/issues'
import type { MatrixCell, ValidationIssue } from '../lib/types'
import { useMatrixStore } from '../stores/matrix'
import { usePresetsStore } from '../stores/presets'
import { useToastStore } from '../stores/toasts'

/**
 * Preset assignment flow shared by the cell dropdown and the inspector:
 * validate (POST /matrix/validate) -> confirm when the cell is ON AIR, or when the IEL names STANDBY
 * cells whose release would make room (`releasable_cells`) -> apply (PUT .../preset with
 * `release_cells`). Returns `apply(presetId)` and the confirmation dialog element to render.
 */
export function useApplyPreset(cell: MatrixCell | undefined): {
  apply: (presetId: string) => Promise<void>
  pending: boolean
  dialog: ReactNode
} {
  const t = useT()
  const [pending, setPending] = useState(false)
  const [confirm, setConfirm] = useState<{
    presetId: string
    name: string
    release: string[]
    reason: string
  } | null>(null)

  const put = async (presetId: string, release: string[]) => {
    if (!cell) return
    const updated = await request(
      api.PUT('/matrix/cells/{cellId}/preset', {
        params: { path: { cellId: cell.cell_id } },
        body:
          release.length > 0
            ? { preset_id: presetId, release_cells: release }
            : { preset_id: presetId },
      }),
    )
    useMatrixStore.getState().upsertCell(updated)
    useToastStore.getState().push({
      tone: 'success',
      title: t('apply.done', { cell: cell.cell_id, preset: updated.preset_name ?? presetId }),
    })
  }

  const apply = async (presetId: string) => {
    if (!cell || pending || presetId === cell.preset_id) return
    setPending(true)
    try {
      const result = await request(
        api.POST('/matrix/validate', { body: { cell_id: cell.cell_id, preset_id: presetId } }),
      )
      const name = usePresetsStore.getState().presets[presetId]?.name ?? presetId
      if (!result.feasible) {
        // Localized reason plus the IEL detail or the bottleneck VVF nodes.
        const reasons = (result.errors ?? []).map((e) => issueText(t, e)).join(' ')
        const release = releasableCells(result.errors ?? [])
        if (release.length > 0) {
          setConfirm({ presetId, name, release, reason: reasons })
          return
        }
        useToastStore.getState().push({
          tone: 'error',
          title: t('apply.infeasible', { cell: cell.cell_id }),
          description: reasons || result.reason,
        })
        return
      }
      if (cell.execution_state === 'active') {
        setConfirm({ presetId, name, release: [], reason: '' })
        return
      }
      await put(presetId, [])
    } catch (error) {
      notifyError(t, error)
    } finally {
      setPending(false)
    }
  }

  const active = cell?.execution_state === 'active'
  const activeBody =
    cell && confirm
      ? t('apply.activeBody', { from: cell.preset_name ?? '-', to: confirm.name })
      : ''
  const dialog = cell && confirm && (
    <ConfirmDialog
      open
      onOpenChange={(open) => !open && setConfirm(null)}
      title={
        confirm.release.length > 0
          ? t('apply.releaseTitle', { cell: cell.cell_id })
          : t('apply.activeTitle', { cell: cell.cell_id })
      }
      description={
        confirm.release.length > 0
          ? [
              t('apply.releaseBody', { reason: confirm.reason, cells: confirm.release.join(', ') }),
              active ? activeBody : '',
            ]
              .filter(Boolean)
              .join(' ')
          : activeBody
      }
      confirmLabel={
        confirm.release.length > 0 ? t('apply.releaseConfirm') : t('apply.activeConfirm')
      }
      tone="danger"
      onConfirm={async () => {
        try {
          await put(confirm.presetId, confirm.release)
        } catch (error) {
          notifyError(t, error)
        }
      }}
    />
  )

  return { apply, pending, dialog }
}

/**
 * The STANDBY cells the IEL offers to release so that the assignment fits: only when every issue is
 * a shortage that a release solves (a format mismatch, for one, cannot be released away).
 */
export function releasableCells(issues: ValidationIssue[]): string[] {
  if (issues.length === 0 || !issues.every((i) => (i.releasable_cells ?? []).length > 0)) return []
  return [...new Set(issues.flatMap((i) => i.releasable_cells ?? []))]
}
