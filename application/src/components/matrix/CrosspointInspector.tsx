import { Ellipsis, Route, SquarePen } from 'lucide-react'
import { useEffect, useState, type ReactNode } from 'react'
import { useNavigate } from 'react-router'
import { useT, type MessageKey } from '../../i18n'
import { issueText } from '../../lib/issues'
import { useApplyPreset } from '../../hooks/useApplyPreset'
import { useCellActions } from '../../hooks/useCellActions'
import { useTake } from '../../hooks/useTake'
import { api, request } from '../../lib/api'
import { axisIndexLabel } from '../../lib/cellId'
import { cx } from '../../lib/cx'
import { formatLossRate, formatMs } from '../../lib/format'
import type { ExecutionState, HealthState } from '../../lib/types'
import { useCell, useMatrixStore } from '../../stores/matrix'
import { usePresetsStore } from '../../stores/presets'
import { useResourcesStore } from '../../stores/resources'
import { useSelectionStore } from '../../stores/selection'
import { Button } from '../common/Button'
import { HealthLamp } from '../common/HealthLamp'
import { StateBadge } from '../common/StateBadge'
import { CellMenu } from './CellMenu'
import { PresetDropdown } from './PresetDropdown'

const healthLabels: Record<HealthState, MessageKey> = {
  healthy: 'health.healthy',
  warning: 'health.warning',
  remediating: 'health.remediating',
  critical: 'health.critical',
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="border-line border-b px-4 py-3">
      <h3 className="text-ink-subtle mb-2 text-[0.6875rem] font-semibold tracking-wider uppercase">
        {title}
      </h3>
      {children}
    </section>
  )
}

/**
 * Crosspoint inspector content: details and actions of the selected cell.
 * Actions depend on the execution state: ACTIVE -> SET STANDBY / OFF (confirmed), STANDBY -> TAKE / OFF,
 * OFF -> preset selection only, DISCONNECTED -> read only.
 */
export function CrosspointInspector() {
  const t = useT()
  const navigate = useNavigate()
  const cellId = useSelectionStore((s) => s.selectedCellId)
  const cell = useCell(cellId ?? '')
  const presets = Object.values(usePresetsStore((s) => s.presets))
  const source = useResourcesStore((s) => s.sources.find((x) => x.id === cell?.source_id))
  const destination = useResourcesStore((s) =>
    s.destinations.find((x) => x.id === cell?.destination_id),
  )
  const { apply, pending: applying, dialog: applyDialog } = useApplyPreset(cell)
  const { take, canTake, blockers, pending: taking } = useTake(cell)
  const { setStandby, turnOff, pending: acting, dialog: actionDialog } = useCellActions(cell)
  const [menuOpen, setMenuOpen] = useState(false)

  // Re-fetch the selected cell (latest state and last_rollback) so the inspector is
  // right even if SSE missed an update. Failures keep the store copy; the connection indicator covers outages.
  useEffect(() => {
    if (!cellId) return
    let alive = true
    request(api.GET('/matrix/cells/{cellId}', { params: { path: { cellId } } }))
      .then((fresh) => alive && useMatrixStore.getState().upsertCell(fresh))
      .catch(() => undefined)
    return () => {
      alive = false
    }
  }, [cellId])

  if (!cellId || !cell) {
    return <p className="text-ink-muted px-4 py-6 text-sm">{t('inspector.empty')}</p>
  }

  const state: ExecutionState = cell.execution_state
  const tel = cell.telemetry
  const rollback = cell.last_rollback

  return (
    <div className="flex flex-col">
      <header className="border-line border-b px-4 py-3">
        <p className="text-lg leading-tight font-bold">
          {source?.name ?? cell.source_id} <span className="text-ink-subtle">→</span>{' '}
          {destination?.name ?? cell.destination_id}
        </p>
        <p className="text-ink-subtle text-xs">
          {axisIndexLabel(cell.source_id)} → {axisIndexLabel(cell.destination_id)} · {cell.cell_id}
        </p>
      </header>

      {/* Health is judged only for cells with a preset and a path (active / standby). */}
      {cell.health_state && (state === 'active' || state === 'standby') && (
        <Section title={t('inspector.health')}>
          <div className="flex items-center gap-2 text-sm font-semibold">
            <HealthLamp state={cell.health_state} />
            {t(healthLabels[cell.health_state])}
          </div>
          {tel && (
            <dl className="mt-2 grid grid-cols-3 gap-2 text-center tabular-nums">
              {[
                [t('telemetry.latency'), formatMs(tel.e2e_latency_ms)],
                [t('telemetry.jitter'), formatMs(tel.jitter_ms, 2)],
                [t('telemetry.loss'), formatLossRate(tel.packet_loss_rate)],
              ].map(([k, v]) => (
                <div key={k} className="bg-surface-muted rounded px-1 py-1.5">
                  <dt className="text-ink-subtle text-[0.625rem] uppercase">{k}</dt>
                  <dd className="text-sm font-semibold">{v}</dd>
                </div>
              ))}
            </dl>
          )}
          {rollback && (
            <div className="border-line mt-2 rounded border px-2 py-1.5 text-xs">
              <p className="font-semibold">
                {t('inspector.lastRollback')}: {t(`rollback.${rollback.result}` as MessageKey)}
              </p>
              <p className="text-ink-muted">{rollback.reason}</p>
              <p className="text-ink-muted">
                {rollback.from.preset_name ?? '-'} → {rollback.to?.preset_name ?? '-'}
              </p>
            </div>
          )}
        </Section>
      )}

      <Section title={t('inspector.routing')}>
        <StateBadge state={state} className="px-3 py-1.5 text-sm" />
      </Section>

      <Section title={t('inspector.preset')}>
        <div className="@container">
          <PresetDropdown
            presetId={cell.preset_id}
            presetName={cell.preset_name}
            disabled={state === 'disconnected'}
            pending={applying}
            onSelect={(id) => void apply(id)}
          />
        </div>
        <div className="mt-2 flex gap-2">
          <Button
            size="sm"
            disabled={!cell.preset_id}
            onClick={() => cell.preset_id && navigate(`/presets/${cell.preset_id}`)}
          >
            <SquarePen className="size-4" aria-hidden />
            {t('inspector.edit')}
          </Button>
          <Button
            size="sm"
            disabled={state !== 'active' && state !== 'standby'}
            onClick={() => navigate(`/topology/${cell.cell_id}`)}
          >
            <Route className="size-4" aria-hidden />
            {t('inspector.showPath')}
          </Button>
          <CellMenu
            cell={cell}
            open={menuOpen}
            onOpenChange={setMenuOpen}
            actions={{
              take: () => void take(),
              canTake,
              apply: (id) => void apply(id),
              setStandby,
              turnOff,
            }}
            trigger={
              <Button size="sm" aria-label={t('menu.more')} title={t('menu.more')}>
                <Ellipsis className="size-4" aria-hidden />
              </Button>
            }
          />
        </div>
      </Section>

      <Section title={t('inspector.actions')}>
        {state === 'standby' && (
          <div className="flex flex-col gap-2">
            <button
              type="button"
              disabled={!canTake}
              aria-busy={taking || undefined}
              onClick={() => void take()}
              className={cx(
                'bg-exec-active h-14 rounded-md text-lg font-black tracking-wider text-white shadow-sm hover:brightness-110 disabled:opacity-60',
                taking && 'animate-pulse',
              )}
            >
              {t('action.take')}
            </button>
            {blockers.length > 0 && (
              <div role="note" className="text-ink-muted text-xs">
                <p className="text-ink font-semibold">{t('take.blocked')}</p>
                <ul className="mt-0.5 flex list-disc flex-col gap-0.5 pl-4">
                  {blockers.map((issue, i) => (
                    <li key={i}>{issueText(t, issue)}</li>
                  ))}
                </ul>
              </div>
            )}
            <Button variant="secondary" pending={acting === 'off'} onClick={turnOff}>
              {t('action.off')}
            </Button>
          </div>
        )}
        {state === 'active' && (
          <div className="grid grid-cols-2 gap-2">
            <Button variant="secondary" pending={acting === 'standby'} onClick={setStandby}>
              {t('action.setStandby')}
            </Button>
            <Button variant="danger" pending={acting === 'off'} onClick={turnOff}>
              {t('action.off')}
            </Button>
          </div>
        )}
        {state === 'off' && <p className="text-ink-muted text-sm">{t('inspector.noActions')}</p>}
        {state === 'disconnected' && (
          <p className="text-ink-muted text-sm">{t('inspector.readOnly')}</p>
        )}
      </Section>

      {state !== 'disconnected' && presets.length > 0 && (
        <Section title={t('inspector.quickPresets')}>
          <ul className="flex flex-col gap-1">
            {presets.map((p) => (
              <li key={p.id}>
                <button
                  type="button"
                  disabled={applying || p.id === cell.preset_id}
                  onClick={() => void apply(p.id)}
                  className="hover:bg-surface-muted disabled:text-ink-subtle w-full rounded px-2 py-1.5 text-left text-sm"
                >
                  {p.name}
                </button>
              </li>
            ))}
          </ul>
        </Section>
      )}

      {applyDialog}
      {actionDialog}
    </div>
  )
}
