import { ReactFlowProvider } from '@xyflow/react'
import { ArrowLeft, Save, Trash2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useBlocker, useNavigate, useParams } from 'react-router'
import { Button } from '../components/common/Button'
import { ConfirmDialog } from '../components/common/ConfirmDialog'
import { DesignerCanvas } from '../components/designer/DesignerCanvas'
import { DesignerSidebar } from '../components/designer/DesignerSidebar'
import { Palette } from '../components/designer/Palette'
import { ResourceEstimate } from '../components/designer/ResourceEstimate'
import { useChainValidation } from '../hooks/useChainValidation'
import { useT } from '../i18n'
import { api, ApiError, request } from '../lib/api'
import { walkChain } from '../lib/designer'
import { isNotImplemented, notifyError } from '../lib/errors'
import { useDesignerStore } from '../stores/designer'
import { usePresetsStore } from '../stores/presets'
import { useToastStore } from '../stores/toasts'

type Load = 'loading' | 'ready' | 'notFound' | 'failed'

/**
 * SFC designer: /presets/new creates, /presets/:presetId edits.
 * Loads the IEL catalog and constraints (and the preset with its SVoT layout when editing), validates
 * the chain with the IEL on every change, and saves the SSoT chain first, then the layout.
 */
export function DesignerPage() {
  const t = useT()
  const navigate = useNavigate()
  const { presetId } = useParams()
  const [load, setLoad] = useState<Load>('loading')
  const [saving, setSaving] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)

  const name = useDesignerStore((s) => s.name)
  const description = useDesignerStore((s) => s.description)
  const functions = useDesignerStore((s) => s.functions)
  const links = useDesignerStore((s) => s.links)
  const positions = useDesignerStore((s) => s.positions)
  const dirty = useDesignerStore((s) => s.dirty)
  const inUse = useDesignerStore((s) => s.inUseCellIds)
  const savedId = useDesignerStore((s) => s.presetId)
  const locked = inUse.length > 0
  // The IEL serves predefined presets only (501 on a write): parameters
  // still apply live, but the chain cannot be saved or deleted.
  const editingUnsupported = usePresetsStore((s) => s.editingUnsupported)

  // Load the catalog / constraints and the preset + layout when editing.
  useEffect(() => {
    let alive = true
    setLoad('loading')
    const run = async () => {
      const [catalog, constraints] = await Promise.all([
        request(api.GET('/functions')),
        request(api.GET('/presets/constraints')),
      ])
      usePresetsStore.getState().setCatalog(catalog, constraints)
      if (!presetId) {
        useDesignerStore.getState().reset()
        return
      }
      const preset = await request(
        api.GET('/presets/{presetId}', { params: { path: { presetId } } }),
      )
      // No saved layout (404) falls back to the automatic column.
      const layout = await request(
        api.GET('/views/presets/{presetId}/layout', { params: { path: { presetId } } }),
      ).catch((e) => {
        if (e instanceof ApiError && e.status === 404) return null
        throw e
      })
      useDesignerStore.getState().reset(preset, layout)
    }
    run()
      .then(() => alive && setLoad('ready'))
      .catch((e) => {
        if (!alive) return
        if (e instanceof ApiError && e.status === 404) setLoad('notFound')
        else {
          setLoad('failed')
          notifyError(t, e)
        }
      })
    return () => {
      alive = false
    }
  }, [presetId, t])

  const walk = walkChain(functions, links)
  const validation = useChainValidation(name, walk.chain)
  const issues = validation.result?.issues ?? []
  const localIssues = walk.complete
    ? []
    : [
        walk.unconnected.length > 0
          ? t('designer.unconnected', { ids: walk.unconnected.join(', ') })
          : t('designer.notToOut'),
      ]
  const canSave =
    load === 'ready' &&
    dirty &&
    !saving &&
    name.trim() !== '' &&
    walk.complete &&
    !validation.pending &&
    validation.result?.valid === true &&
    !editingUnsupported

  // Unsaved changes: confirm before leaving, in the app and on reload / close.
  // Read the store at navigation time: save / delete clear `dirty` right before navigating.
  const blocker = useBlocker(
    ({ currentLocation, nextLocation }) =>
      useDesignerStore.getState().dirty && currentLocation.pathname !== nextLocation.pathname,
  )
  useEffect(() => {
    if (!dirty) return
    const onBeforeUnload = (e: BeforeUnloadEvent) => e.preventDefault()
    window.addEventListener('beforeunload', onBeforeUnload)
    return () => window.removeEventListener('beforeunload', onBeforeUnload)
  }, [dirty])

  const save = async () => {
    setSaving(true)
    try {
      const body = {
        name: name.trim(),
        description: description.trim() || undefined,
        chain: walk.chain,
      }
      // 1. SSoT (the new id comes from this response), 2. SVoT layout.
      const preset = savedId
        ? await request(
            api.PUT('/presets/{presetId}', { params: { path: { presetId: savedId } }, body }),
          )
        : await request(api.POST('/presets', { body }))
      usePresetsStore.getState().upsertPreset(preset)
      useDesignerStore.getState().markSaved(preset)
      try {
        await request(
          api.PUT('/views/presets/{presetId}/layout', {
            params: { path: { presetId: preset.id } },
            body: {
              preset_id: preset.id,
              nodes: Object.entries(positions).map(([node_id, position]) => ({
                node_id,
                position,
              })),
            },
          }),
        )
      } catch (e) {
        // The chain is saved; only the canvas positions were lost.
        notifyError(t, e)
      }
      useToastStore
        .getState()
        .push({ tone: 'success', title: t('designer.saved', { name: preset.name }) })
      navigate('/presets')
    } catch (e) {
      if (isNotImplemented(e)) usePresetsStore.getState().setEditingUnsupported()
      notifyError(t, e)
    } finally {
      setSaving(false)
    }
  }

  const remove = async () => {
    if (!savedId) return
    try {
      await request(api.DELETE('/presets/{presetId}', { params: { path: { presetId: savedId } } }))
      usePresetsStore.getState().removePreset(savedId)
      // Nothing left to lose: leave without the unsaved-changes prompt.
      useDesignerStore.getState().reset()
      useToastStore.getState().push({ tone: 'success', title: t('designer.deleted', { name }) })
      navigate('/presets')
    } catch (e) {
      if (isNotImplemented(e)) usePresetsStore.getState().setEditingUnsupported()
      notifyError(t, e)
    }
  }

  if (load === 'notFound') {
    return <p className="text-ink-muted p-5 text-sm">{t('designer.notFound')}</p>
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="border-line bg-surface flex shrink-0 items-center gap-3 border-b px-4 py-2">
        <Button variant="ghost" size="sm" onClick={() => navigate('/presets')}>
          <ArrowLeft className="size-4" aria-hidden />
          {t('nav.presets')}
        </Button>
        <h1 className="min-w-0 truncate text-lg font-bold">
          {savedId ? name || savedId : t('designer.newTitle')}
          {dirty && <span className="text-ink-subtle ml-2 text-sm font-normal">*</span>}
        </h1>
        <div className="ml-auto flex items-center gap-2">
          {savedId && (
            <Button
              variant="secondary"
              size="sm"
              disabled={load !== 'ready' || editingUnsupported}
              onClick={() => setConfirmDelete(true)}
            >
              <Trash2 className="size-4" aria-hidden />
              {t('action.delete')}
            </Button>
          )}
          <Button variant="primary" size="sm" disabled={!canSave} pending={saving} onClick={save}>
            <Save className="size-4" aria-hidden />
            {t('designer.save')}
          </Button>
        </div>
      </div>
      {editingUnsupported && (
        <p
          role="status"
          className="border-line bg-surface-muted text-ink-muted border-b px-4 py-2 text-xs"
        >
          {t('designer.editingUnsupported')}
        </p>
      )}
      {load === 'ready' ? (
        <ReactFlowProvider>
          <div className="flex min-h-0 flex-1">
            <Palette locked={locked} />
            <div className="flex min-h-0 min-w-0 flex-1 flex-col">
              <DesignerCanvas issues={issues} locked={locked} />
              <ResourceEstimate result={validation.result} pending={validation.pending} />
            </div>
            <DesignerSidebar
              issues={issues}
              localIssues={localIssues}
              validating={validation.pending}
              locked={locked}
            />
          </div>
        </ReactFlowProvider>
      ) : (
        <p className="text-ink-muted p-5 text-sm">
          {load === 'loading' ? t('designer.loading') : t('designer.loadFailed')}
        </p>
      )}

      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title={t('designer.deleteTitle', { name })}
        description={t('designer.deleteBody')}
        confirmLabel={t('action.delete')}
        tone="danger"
        onConfirm={remove}
      />
      <ConfirmDialog
        open={blocker.state === 'blocked'}
        onOpenChange={(open) => !open && blocker.reset?.()}
        title={t('designer.leaveTitle')}
        description={t('designer.leaveBody')}
        confirmLabel={t('designer.leave')}
        tone="danger"
        onConfirm={() => blocker.proceed?.()}
      />
    </div>
  )
}
