import { NavLink } from 'react-router'
import { useT } from '../../i18n'
import { axisIndexLabel } from '../../lib/cellId'
import { cx } from '../../lib/cx'
import type { MatrixCell } from '../../lib/types'
import { useMatrixStore } from '../../stores/matrix'
import { useResourcesStore } from '../../stores/resources'
import { HealthLamp } from '../common/HealthLamp'
import { StateBadge } from '../common/StateBadge'

/** Sort key IN{n}-OUT{m} -> [n, m] so IN10 comes after IN9. */
const order = (c: MatrixCell) => {
  const m = /^IN(\d+)-OUT(\d+)$/.exec(c.cell_id)
  return m ? [Number(m[1]), Number(m[2])] : [Infinity, Infinity]
}

/**
 * Cells that have a real path (ACTIVE / STANDBY) ON AIR first, then by IN / OUT.
 * Selecting one opens /topology/:cellId.
 */
export function CellList() {
  const t = useT()
  const cells = useMatrixStore((s) => s.cells)
  const sources = useResourcesStore((s) => s.sources)
  const destinations = useResourcesStore((s) => s.destinations)
  const nameOf = (id: string) =>
    [...sources, ...destinations].find((r) => r.id === id)?.name ?? axisIndexLabel(id)

  const list = Object.values(cells)
    .filter((c) => c.execution_state === 'active' || c.execution_state === 'standby')
    .sort((a, b) => {
      if (a.execution_state !== b.execution_state) return a.execution_state === 'active' ? -1 : 1
      const [ai, ao] = order(a)
      const [bi, bo] = order(b)
      return ai - bi || ao - bo
    })

  return (
    <nav
      aria-label={t('topology.cells')}
      className="border-line bg-surface flex w-72 shrink-0 flex-col overflow-auto border-r"
    >
      <NavLink
        to="/topology"
        end
        className={({ isActive }) =>
          cx(
            'border-line border-b px-4 py-3 text-sm font-semibold',
            isActive ? 'bg-selected text-on-selected' : 'hover:bg-surface-muted',
          )
        }
      >
        {t('topology.overview')}
      </NavLink>
      <h2 className="text-ink-subtle px-4 pt-3 pb-1 text-[0.6875rem] font-semibold tracking-wider uppercase">
        {t('topology.cells')}
      </h2>
      {list.length === 0 && (
        <p className="text-ink-muted px-4 py-2 text-sm">{t('topology.noCells')}</p>
      )}
      <ul>
        {list.map((c) => (
          <li key={c.cell_id}>
            <NavLink
              to={`/topology/${c.cell_id}`}
              className={({ isActive }) =>
                cx(
                  'border-line flex items-center gap-2 border-b px-4 py-2',
                  isActive ? 'bg-selected text-on-selected' : 'hover:bg-surface-muted',
                )
              }
            >
              <span className="min-w-0 flex-1">
                <span className="block truncate text-sm font-semibold">
                  {nameOf(c.source_id)} → {nameOf(c.destination_id)}
                </span>
                <span className="block text-xs opacity-70">{c.cell_id}</span>
              </span>
              {c.health_state && <HealthLamp state={c.health_state} className="size-3" />}
              <StateBadge state={c.execution_state} className="px-1.5 py-0.5 text-[0.625rem]" />
            </NavLink>
          </li>
        ))}
      </ul>
    </nav>
  )
}
