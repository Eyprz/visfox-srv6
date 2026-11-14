import { Plus } from 'lucide-react'
import type { DragEvent } from 'react'
import { useT } from '../../i18n'
import { paletteLimits, type PaletteEntry } from '../../lib/designer'
import { cx } from '../../lib/cx'
import type { ChainConstraints } from '../../lib/types'

type VvfDefinition = ChainConstraints['vvfs'][number]
import { useDesignerStore } from '../../stores/designer'
import { usePresetsStore } from '../../stores/presets'
import { PALETTE_MIME, paletteDrag } from './DesignerCanvas'

type Group = {
  key: string
  title: string
  vvf: VvfDefinition | null
  /** Label of the pinned node the elements use (pinned VVFs list one group per node). */
  nodeLabel?: string
  items: PaletteEntry[]
}

/**
 * Function palette: built from GET /functions, grouped by the VVFs in
 * GET /presets/constraints. Click appends to the chain (wired before OUT); drag places an unconnected
 * node. VVFs with require_all offer "add all" to place every member together. A pinned VVF (nodes
 * differ, e.g. the material a caption node burns in) is listed once per node, and its elements carry
 * the node (`vvf_node_id`); a dynamic VVF is listed once and the IEL assigns its node.
 * Types at their IEL limit are disabled; everything is disabled while the preset is in use.
 */
export function Palette({ locked }: { locked: boolean }) {
  const t = useT()
  const catalog = usePresetsStore((s) => s.catalog)
  const constraints = usePresetsStore((s) => s.constraints)
  const functions = useDesignerStore((s) => s.functions)
  const addFunctions = useDesignerStore((s) => s.addFunctions)
  const limits = paletteLimits(catalog, constraints, functions)

  // Groups in the IEL's VVF order; functions without a known VVF go last on their own.
  const groups: Group[] = [
    ...(constraints?.vvfs ?? []).flatMap((v): Group[] => {
      // In the VVF's execution order from the IEL, so "add all" places them in that order.
      const items = catalog
        .filter((c) => c.vvf_id === v.id)
        .sort((a, b) => v.function_types.indexOf(a.type) - v.function_types.indexOf(b.type))
      if (v.allocation !== 'pinned') return [{ key: v.id, title: `VVF: ${v.name}`, vvf: v, items }]
      return (v.nodes ?? []).map((n) => ({
        key: `${v.id}:${n.node_id}`,
        title: `VVF: ${n.label}`,
        vvf: v,
        nodeLabel: n.label,
        items: items.map((i) => ({ ...i, vvf_node_id: n.node_id })),
      }))
    }),
    ...catalog
      .filter((c) => !constraints?.vvfs.some((v) => v.id === c.vvf_id))
      .map((c) => ({ key: c.type, title: c.name, vvf: null, items: [c] })),
  ].filter((g) => g.items.length > 0)

  const drag = (e: DragEvent, items: PaletteEntry[]) => {
    e.dataTransfer.setData(
      PALETTE_MIME,
      JSON.stringify(items.map((i) => ({ type: i.type, vvf_node_id: i.vvf_node_id }))),
    )
    paletteDrag.types = items.map((i) => i.type)
    e.dataTransfer.effectAllowed = 'copy'
  }

  return (
    <aside
      aria-label={t('designer.palette')}
      className="border-line bg-surface flex w-64 shrink-0 flex-col overflow-auto border-r"
    >
      <h2 className="text-ink-subtle border-line border-b px-4 py-3 text-[0.6875rem] font-semibold tracking-wider uppercase">
        {t('designer.palette')}
      </h2>
      {locked && <p className="text-ink-muted px-4 py-2 text-xs">{t('designer.lockedPalette')}</p>}
      {groups.map((g) => {
        const all = g.vvf?.require_all && g.items.length > 1
        const allLimits = paletteLimits(catalog, constraints, functions, g.items.length)
        const allDisabled = locked || g.items.some((i) => allLimits[i.type]?.disabled)
        return (
          <section key={g.key} className="border-line border-b px-3 py-3">
            <div className="mb-2 flex items-center gap-2 px-1">
              <h3 className="min-w-0 flex-1 truncate text-xs font-bold" title={g.title}>
                {g.title}
              </h3>
              {all && (
                <button
                  type="button"
                  disabled={allDisabled}
                  draggable={!allDisabled}
                  onDragStart={(e) => drag(e, g.items)}
                  onClick={() => addFunctions(g.items)}
                  title={t('designer.addAllHint')}
                  className="text-accent hover:bg-accent-soft disabled:text-ink-subtle inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-xs font-semibold disabled:bg-transparent"
                >
                  <Plus className="size-3.5" aria-hidden />
                  {t('designer.addAll')}
                </button>
              )}
            </div>
            <ul className="flex flex-col gap-1">
              {g.items.map((item) => {
                const limit = limits[item.type]
                const disabled = locked || limit?.disabled
                // A single-function pinned VVF is told apart by its node only.
                const label =
                  g.nodeLabel && g.vvf?.function_types.length === 1 ? g.nodeLabel : item.name
                return (
                  <li key={item.type}>
                    <button
                      type="button"
                      disabled={disabled}
                      draggable={!disabled}
                      onDragStart={(e) => drag(e, [item])}
                      onClick={() => addFunctions([item])}
                      title={limit?.reason ? t(`designer.limit.${limit.reason}`) : item.description}
                      className={cx(
                        'border-line-strong w-full rounded-md border px-2.5 py-1.5 text-left text-sm',
                        disabled
                          ? 'text-ink-subtle cursor-not-allowed border-dashed'
                          : 'hover:bg-surface-muted cursor-grab',
                      )}
                    >
                      {label}
                    </button>
                  </li>
                )
              })}
            </ul>
          </section>
        )
      })}
    </aside>
  )
}
