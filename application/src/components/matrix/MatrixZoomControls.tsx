import { Minus, Plus, RotateCcw } from 'lucide-react'
import { useT } from '../../i18n'
import { cx } from '../../lib/cx'
import { MATRIX_ZOOM, useUiStore } from '../../stores/ui'

const button =
  'flex size-9 items-center justify-center text-ink-muted hover:bg-surface-muted hover:text-ink disabled:cursor-not-allowed disabled:opacity-40'

/**
 * Matrix-only zoom controls (bottom-right of the matrix area): zoom out / percentage / zoom in / reset.
 * The factor multiplies the fitted cell size and is persisted with the UI preferences.
 * Clicking the percentage also resets to 100%.
 */
export function MatrixZoomControls({
  className,
  ...rest
}: {
  className?: string
  'data-keep-selection'?: boolean
}) {
  const t = useT()
  const zoom = useUiStore((s) => s.matrixZoom)
  const setZoom = useUiStore((s) => s.setMatrixZoom)
  const percent = Math.round(zoom * 100)

  return (
    <div
      {...rest}
      role="group"
      aria-label={t('zoom.controls')}
      className={cx(
        'divide-line border-line-strong bg-surface flex items-center divide-x overflow-hidden rounded-md border shadow-md',
        className,
      )}
    >
      <button
        type="button"
        className={button}
        aria-label={t('zoom.out')}
        title={t('zoom.out')}
        disabled={zoom <= MATRIX_ZOOM.min}
        onClick={() => setZoom(zoom - MATRIX_ZOOM.step)}
      >
        <Minus className="size-4" aria-hidden />
      </button>
      <button
        type="button"
        className="text-ink hover:bg-surface-muted h-9 w-16 text-sm font-semibold tabular-nums"
        aria-label={`${percent}% - ${t('zoom.reset')}`}
        title={t('zoom.reset')}
        onClick={() => setZoom(1)}
      >
        {percent}%
      </button>
      <button
        type="button"
        className={button}
        aria-label={t('zoom.in')}
        title={t('zoom.in')}
        disabled={zoom >= MATRIX_ZOOM.max}
        onClick={() => setZoom(zoom + MATRIX_ZOOM.step)}
      >
        <Plus className="size-4" aria-hidden />
      </button>
      <button
        type="button"
        className={button}
        aria-label={t('zoom.reset')}
        title={t('zoom.reset')}
        disabled={zoom === 1}
        onClick={() => setZoom(1)}
      >
        <RotateCcw className="size-4" aria-hidden />
      </button>
    </div>
  )
}
