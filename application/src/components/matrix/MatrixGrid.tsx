import { useRef, type CSSProperties } from 'react'
import { useFitText } from '../../hooks/useFitText'
import { useMatrixCellSize } from '../../hooks/useMatrixCellSize'
import { useT } from '../../i18n'
import { axisIndexLabel, cellIdOf } from '../../lib/cellId'
import { useResourcesStore } from '../../stores/resources'
import { useSelectionStore } from '../../stores/selection'
import { useUiStore } from '../../stores/ui'
import { MatrixCellView } from './MatrixCellView'
import { MatrixZoomControls } from './MatrixZoomControls'

/**
 * Render limit per axis: no operational limit, but never render more than this
 * so a bug that floods resources cannot freeze the browser.
 */
export const MAX_AXIS = 256

/**
 * Crosspoint matrix as a grid of square button cells.
 * Headers are not sticky: they scroll with the grid on every device (fixed headers made scrolling
 * awkward and overlapped cells on mobile).
 * The grid itself does not subscribe to cells; each MatrixCellView subscribes to its own cell.
 */
// Layout metrics (rem) for the cell size: section padding (p-5), framed area padding (p-3) + border,
// the OUT header row (h-14) and one gap (gap-4: room for the 7px health rings of neighboring cells). The cap is the FHD baseline from index.css.
const CHROME_REM = 2.5 + 1.5 + 2 / 16 + 3.5 + 1
const GAP_REM = 1
const MAX_CELL_REM = 12.5

// Arrow keys move the selection between cells: [row delta, column delta].
const ARROW_STEPS: Record<string, [number, number]> = {
  ArrowUp: [-1, 0],
  ArrowDown: [1, 0],
  ArrowLeft: [0, -1],
  ArrowRight: [0, 1],
}
const clamp = (i: number, length: number) => Math.min(Math.max(i, 0), length - 1)

export function MatrixGrid() {
  const t = useT()
  const regionRef = useRef<HTMLElement>(null)
  const cellPx = useMatrixCellSize(regionRef, {
    chromeRem: CHROME_REM,
    gapRem: GAP_REM,
    maxRem: MAX_CELL_REM,
  })
  const zoom = useUiStore((s) => s.matrixZoom)
  // Select the stable action (a selector returning a new function would re-render forever).
  const select = useSelectionStore((s) => s.select)
  const clearSelection = () => select(null)
  // Zoom multiplies the fitted size (or the CSS default before the first measurement).
  const cellSize = cellPx ? `${Math.round(cellPx * zoom)}px` : `calc(${MAX_CELL_REM}rem * ${zoom})`
  const allSources = useResourcesStore((s) => s.sources)
  const allDestinations = useResourcesStore((s) => s.destinations)
  const sources = allSources.slice(0, MAX_AXIS)
  const destinations = allDestinations.slice(0, MAX_AXIS)

  const truncated = [
    allSources.length > MAX_AXIS && { axis: 'IN', total: allSources.length },
    allDestinations.length > MAX_AXIS && { axis: 'OUT', total: allDestinations.length },
  ].filter((x): x is { axis: string; total: number } => Boolean(x))

  return (
    <section
      ref={regionRef}
      aria-label={t('matrix.region')}
      // Clicking anywhere in the matrix area other than a cell (or a control marked data-keep-selection)
      // clears the selection; Esc does the same. Portaled menus stop propagation themselves.
      onClick={(e) => {
        const target = e.target as HTMLElement
        // Clicks in portals (menus, dialogs) bubble here through the React tree; they are not outside clicks.
        if (!e.currentTarget.contains(target)) return
        if (!target.closest('[data-cell-id], [data-keep-selection]')) clearSelection()
      }}
      onKeyDown={(e) => {
        if (!e.currentTarget.contains(e.target as Node)) return
        if (e.key === 'Escape') {
          clearSelection()
          return
        }
        const step = ARROW_STEPS[e.key]
        const from = (e.target as HTMLElement).closest<HTMLElement>('[data-cell-id]')?.dataset
          .cellId
        if (!step || !from) return
        const row = sources.findIndex((s) =>
          destinations.some((d) => cellIdOf(s.id, d.id) === from),
        )
        const col = destinations.findIndex(
          (d) => row >= 0 && cellIdOf(sources[row].id, d.id) === from,
        )
        if (row < 0 || col < 0) return
        e.preventDefault()
        // Clamp at the edges (no wrap-around), then select and focus the neighbor.
        const next = cellIdOf(
          sources[clamp(row + step[0], sources.length)].id,
          destinations[clamp(col + step[1], destinations.length)].id,
        )
        select(next)
        regionRef.current?.querySelector<HTMLElement>(`[data-cell-id="${next}"]`)?.focus()
      }}
      className="relative flex min-h-0 min-w-0 flex-1 flex-col gap-3 p-5"
    >
      {truncated.map((x) => (
        <p
          key={x.axis}
          role="alert"
          className="border-health-warning/60 bg-health-warning/10 shrink-0 rounded-md border px-4 py-2 text-sm"
        >
          {t('matrix.truncated', { axis: x.axis, total: x.total, shown: MAX_AXIS, max: MAX_AXIS })}
        </p>
      ))}
      {sources.length === 0 || destinations.length === 0 ? (
        <p className="text-ink-muted text-sm">{t('matrix.empty')}</p>
      ) : (
        // Cells have a fixed size (--matrix-cell); the framed area scrolls when the grid does not fit.
        <div className="border-line bg-surface min-h-0 max-w-full self-start overflow-auto rounded-lg border p-3 shadow-sm">
          <div
            role="grid"
            aria-label={t('matrix.region')}
            aria-rowcount={sources.length + 1}
            aria-colcount={destinations.length + 1}
            className="grid w-max gap-4"
            style={{
              ...({ '--matrix-cell': cellSize } as CSSProperties),
              gridTemplateColumns: `var(--matrix-cell) repeat(${destinations.length}, var(--matrix-cell))`,
            }}
          >
            {/* Header row: corner + OUT headers. */}
            <div role="row" className="contents">
              <div
                role="columnheader"
                className="bg-surface text-ink-subtle flex h-14 items-center justify-center rounded-md text-[0.6875rem] font-semibold tracking-wider uppercase"
              >
                {t('matrix.corner')}
              </div>
              {destinations.map((d) => (
                <div
                  key={d.id}
                  role="columnheader"
                  className="border-slot bg-surface-muted relative flex h-14 flex-col items-center justify-center rounded-md border-2 px-2"
                >
                  <AxisLabel index={axisIndexLabel(d.id)} name={d.name} />
                </div>
              ))}
            </div>
            {sources.map((s) => (
              <div key={s.id} role="row" className="contents">
                <div
                  role="rowheader"
                  className="border-slot bg-surface-muted relative flex aspect-square flex-col items-center justify-center rounded-md border-2 px-2"
                >
                  <AxisLabel index={axisIndexLabel(s.id)} name={s.name} />
                </div>
                {destinations.map((d) => (
                  <MatrixCellView key={d.id} cellId={cellIdOf(s.id, d.id)} />
                ))}
              </div>
            ))}
          </div>
        </div>
      )}
      {sources.length > 0 && destinations.length > 0 && (
        <MatrixZoomControls data-keep-selection className="absolute right-8 bottom-8 z-30" />
      )}
    </section>
  )
}

/**
 * IN / OUT header: the node name (from the TED) large in the center on one line, the IN n / OUT n index
 * small in the top-left corner. The name shrinks to fit the header width (1.125rem down to 0.75rem);
 * names that would need less are cut with "..." (full name in the tooltip). The parent must be `relative`.
 */
function AxisLabel({ index, name }: { index: string; name: string }) {
  const nameRef = useRef<HTMLSpanElement>(null)
  useFitText(nameRef, name, { maxRem: 1.125, minRem: 0.75 })
  return (
    <>
      <span className="text-ink-subtle absolute top-1 left-1.5 text-[0.625rem] font-semibold tracking-wide">
        {index}
      </span>
      <span
        ref={nameRef}
        title={name}
        className="text-ink block w-full truncate text-center text-lg leading-tight font-bold"
      >
        {name}
      </span>
    </>
  )
}
