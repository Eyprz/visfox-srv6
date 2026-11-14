import { Radio } from 'lucide-react'
import { useState } from 'react'
import { useParamEditor } from '../../hooks/useParamEditor'
import { useT } from '../../i18n'
import type { FunctionCatalogItem, FunctionInstance } from '../../lib/types'
import { useDesignerStore } from '../../stores/designer'
import { useMatrixStore } from '../../stores/matrix'
import { cropBounds } from '../../lib/crop'
import { Button } from '../common/Button'
import { ConfirmDialog } from '../common/ConfirmDialog'
import { CropController } from './CropController'

type NumberSchema = { type?: string; minimum?: number; maximum?: number; description?: string }

/** Numeric properties of a param_schema that have a finite range (slider-capable). */
export function numericParams(schema: unknown): [string, NumberSchema][] {
  const props = (schema as { properties?: Record<string, NumberSchema> })?.properties ?? {}
  return Object.entries(props).filter(
    ([, p]) =>
      (p.type === 'integer' || p.type === 'number') &&
      typeof p.minimum === 'number' &&
      typeof p.maximum === 'number',
  )
}

// v1 parameter UIs: color has sliders; crop gets the position controller
// (sliders as a fallback when its schema lacks the ranges); other types run with the IEL default params.
const editable = new Set(['color', 'crop'])

/**
 * Parameters of the selected function. Ranges come from the IEL param_schema. While the preset is
 * ON AIR, editing stays disabled until the operator enables live editing.
 * Render with `key={fn.instance_id}` so each node starts with its own editor state.
 */
export function ParamEditor({ fn, item }: { fn: FunctionInstance; item?: FunctionCatalogItem }) {
  const t = useT()
  const { live, change, commit, errorOf } = useParamEditor(fn)
  const inUse = useDesignerStore((s) => s.inUseCellIds)
  const liveEditEnabled = useDesignerStore((s) => s.liveEditEnabled)
  const enableLiveEdit = useDesignerStore((s) => s.enableLiveEdit)
  // Selector returns a string (stable) rather than a new array, which would re-render forever.
  const onAirKey = useMatrixStore((s) =>
    inUse.filter((id) => s.cells[id]?.execution_state === 'active').join(','),
  )
  const onAir = onAirKey ? onAirKey.split(',') : []
  const [confirming, setConfirming] = useState(false)

  if (!editable.has(fn.type)) {
    return <p className="text-ink-muted text-sm">{t('designer.defaultParams')}</p>
  }

  const gated = live && onAir.length > 0 && !liveEditEnabled
  const fields = numericParams(item?.param_schema)
  // crop uses the position controller when its schema gives the x / y ranges.
  const crop = fn.type === 'crop' ? cropBounds(item?.param_schema) : null

  return (
    <div className="flex flex-col gap-3">
      {live && inUse.length > 0 && (
        <div
          className={
            onAir.length > 0
              ? 'bg-exec-active rounded-md px-3 py-2 text-sm text-white'
              : 'border-line-strong rounded-md border px-3 py-2 text-sm'
          }
        >
          <p className="flex items-center gap-1.5 font-bold">
            {onAir.length > 0 && <Radio className="size-4" aria-hidden />}
            {onAir.length > 0 ? t('designer.onAirBanner') : t('designer.liveBanner')}
          </p>
          <p className="text-xs opacity-90">
            {t('designer.appliedTo', { cells: inUse.join(', ') })}
          </p>
          {gated && (
            <Button
              size="sm"
              variant="secondary"
              className="mt-2 w-full"
              onClick={() => setConfirming(true)}
            >
              {t('designer.enableLiveEdit')}
            </Button>
          )}
        </div>
      )}
      {!live && <p className="text-ink-subtle text-xs">{t('designer.unsavedParams')}</p>}

      {crop && (
        <CropController
          value={{ x: Number(fn.params.x ?? 0), y: Number(fn.params.y ?? 0) }}
          bounds={crop}
          disabled={gated}
          onChange={(p) => change(p)}
          onCommit={commit}
          title={`${t('crop.title')}: ${fn.instance_id}`}
        />
      )}
      {crop && (errorOf('x') || errorOf('y')) && (
        <p className="text-health-critical text-xs">{errorOf('x') ?? errorOf('y')}</p>
      )}
      <fieldset
        hidden={Boolean(crop)}
        disabled={gated}
        className="flex flex-col gap-3 disabled:opacity-50"
      >
        {fields.map(([key, schema]) => {
          const value = Number(fn.params[key] ?? schema.minimum ?? 0)
          const step = schema.type === 'integer' ? 1 : 0.1
          const clamp = (v: number) => Math.min(schema.maximum!, Math.max(schema.minimum!, v))
          const error = errorOf(key)
          return (
            <label key={key} className="block text-sm">
              <span className="mb-1 flex items-center justify-between">
                <span className="font-semibold uppercase">{key}</span>
                <input
                  type="number"
                  aria-label={`${key} value`}
                  min={schema.minimum}
                  max={schema.maximum}
                  step={step}
                  value={value}
                  onChange={(e) => {
                    if (e.target.value !== '') change({ [key]: clamp(Number(e.target.value)) })
                  }}
                  onBlur={commit}
                  onKeyDown={(e) => e.key === 'Enter' && commit()}
                  className="border-line-strong bg-surface w-20 rounded border px-1.5 py-0.5 text-right tabular-nums"
                />
              </span>
              <input
                type="range"
                aria-label={key}
                min={schema.minimum}
                max={schema.maximum}
                step={step}
                value={value}
                onChange={(e) => change({ [key]: Number(e.target.value) })}
                // End of the gesture: always send the final value.
                onPointerUp={commit}
                onKeyUp={commit}
                className="accent-accent w-full"
              />
              <span className="text-ink-subtle flex justify-between text-[0.625rem] tabular-nums">
                <span>{schema.minimum}</span>
                <span>{schema.maximum}</span>
              </span>
              {error && <span className="text-health-critical text-xs">{error}</span>}
            </label>
          )
        })}
      </fieldset>

      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={t('designer.liveEditTitle')}
        description={t('designer.liveEditBody', { cells: onAir.join(', ') })}
        confirmLabel={t('designer.enableLiveEdit')}
        tone="danger"
        onConfirm={enableLiveEdit}
      />
    </div>
  )
}
