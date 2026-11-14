import { ArrowRight, Grid3x3 } from 'lucide-react'
import { Fragment, type ReactNode } from 'react'
import { useNavigate } from 'react-router'
import { useCellPath } from '../../hooks/useCellPath'
import { useT } from '../../i18n'
import { cx } from '../../lib/cx'
import { formatLossRate, formatMs } from '../../lib/format'
import type { CellPath } from '../../lib/types'
import { functionLabel } from '../../lib/vvf'
import { useCell } from '../../stores/matrix'
import { usePresetsStore } from '../../stores/presets'
import { useResourcesStore } from '../../stores/resources'
import { useSelectionStore } from '../../stores/selection'
import { Button } from '../common/Button'
import { HealthLamp } from '../common/HealthLamp'
import { StateBadge } from '../common/StateBadge'

type PathNode = CellPath['nodes'][number]
type PathHop = CellPath['hops'][number]

function Section({ title, note, children }: { title: string; note?: string; children: ReactNode }) {
  return (
    <section className="border-line border-b px-5 py-4">
      <h2 className="text-ink-subtle mb-3 text-[0.6875rem] font-semibold tracking-wider uppercase">
        {title}
        {note && <span className="ml-2 font-normal tracking-normal normal-case">{note}</span>}
      </h2>
      {children}
    </section>
  )
}

/**
 * Real path of one cell: the L7 chain as VVF blocks with per-hop metrics, the L3
 * SRv6 mapping (segment list, routers, SIDs; read-only diagnostics), and the bottleneck the IEL
 * reports highlighted in red. Latencies are measured values only (v1 has no SLA).
 */
export function PathView({ cellId }: { cellId: string }) {
  const t = useT()
  const navigate = useNavigate()
  const cell = useCell(cellId)
  const { path, error, loading } = useCellPath(cellId)
  const sources = useResourcesStore((s) => s.sources)
  const destinations = useResourcesStore((s) => s.destinations)
  const catalog = usePresetsStore((s) => s.catalog)
  const constraints = usePresetsStore((s) => s.constraints)
  const nameOf = (id: string) => [...sources, ...destinations].find((r) => r.id === id)?.name ?? id
  // Function names for instance ids, from the cell's live chain and the IEL catalog.
  const fnName = (instanceId: string) => {
    const fn = cell?.live_chain?.find((f) => f.instance_id === instanceId)
    return fn ? functionLabel(fn, catalog, constraints) : instanceId
  }

  const openInMatrix = () => {
    useSelectionStore.getState().select(cellId)
    navigate('/')
  }

  const header = (
    <header className="border-line flex flex-wrap items-center gap-3 border-b px-5 py-4">
      <div className="min-w-0">
        <h1 className="truncate text-xl font-bold">
          {cell ? `${nameOf(cell.source_id)} → ${nameOf(cell.destination_id)}` : cellId}
        </h1>
        <p className="text-ink-subtle text-xs">
          {cellId}
          {cell?.preset_name && ` · ${cell.preset_name}`}
        </p>
      </div>
      {cell && <StateBadge state={cell.execution_state} />}
      {cell?.health_state && <HealthLamp state={cell.health_state} />}
      {path?.e2e_latency_ms !== undefined && (
        // Measured E2E latency only: v1 has no SLA to compare it with.
        <p className="text-sm tabular-nums">
          {t('topology.e2e')} {formatMs(path.e2e_latency_ms)}
        </p>
      )}
      <Button size="sm" className="ml-auto" onClick={openInMatrix}>
        <Grid3x3 className="size-4" aria-hidden />
        {t('topology.openInMatrix')}
      </Button>
    </header>
  )

  if (loading) return <Shell header={header}>{t('topology.loading')}</Shell>
  if (error || !path) {
    const key =
      error === 'inactive'
        ? 'topology.inactive'
        : error === 'notFound'
          ? 'topology.notFound'
          : 'topology.failed'
    return <Shell header={header}>{t(key)}</Shell>
  }

  // Switchers keep role vvf; the switcher field marks them.
  const roleLabel = (n: PathNode) =>
    n.switcher ? t('topology.role.switcher') : t(`topology.role.${n.role}`)
  const bottleneck = path.bottleneck
  // Endpoints by TED node name, VVF nodes by their label.
  const labelOf = (id: string) => {
    const n = path.nodes.find((x) => x.node_id === id)
    if (!n) return id
    return n.role === 'headend' || n.role === 'egress' ? nameOf(n.node_id) : n.label
  }
  const isHotHop = (h: PathHop) =>
    bottleneck?.kind === 'hop' && bottleneck.ref === `${h.from}->${h.to}`
  const isHotNode = (n: PathNode) => bottleneck?.kind === 'vvf' && bottleneck.ref === n.node_id

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-auto">
      {header}
      {bottleneck && (
        <p
          role="alert"
          className="bg-health-critical/10 text-health-critical border-line border-b px-5 py-2 text-sm font-semibold"
        >
          {t('topology.bottleneck', {
            ref: (bottleneck.kind === 'hop' ? bottleneck.ref.split('->') : [bottleneck.ref])
              .map(labelOf)
              .join(' → '),
          })}
        </p>
      )}

      <Section title={t('topology.l7')}>
        <ol className="flex items-stretch overflow-x-auto pb-2" aria-label={t('topology.l7')}>
          {path.nodes.map((n, i) => {
            const hop = path.hops.find((h) => h.from === n.node_id)
            return (
              <Fragment key={n.node_id}>
                <li
                  className={cx(
                    'bg-surface flex w-56 shrink-0 flex-col rounded-md border-2 px-3 py-2',
                    isHotNode(n) || n.unassigned
                      ? 'border-health-critical'
                      : n.switcher
                        ? 'border-exec-active/60 border-dashed'
                        : n.role === 'vvf'
                          ? 'border-accent/60'
                          : 'border-line-strong',
                  )}
                >
                  <span className="text-ink-subtle text-[0.625rem] font-semibold tracking-wider uppercase">
                    {roleLabel(n)}
                  </span>
                  <span className="truncate font-bold">
                    {n.role === 'headend' || n.role === 'egress' ? nameOf(n.node_id) : n.label}
                  </span>
                  {n.unassigned && (
                    <span className="text-health-critical text-xs font-semibold">
                      {t('topology.unassigned')}
                    </span>
                  )}
                  {n.switcher && (
                    <p className="mt-1 text-xs">
                      {t('topology.inputPort', { port: n.switcher.input_port })}
                      {/* Fed by the previous node: the source, or an upstream switcher (cascade). */}
                      <span className="text-ink-subtle">
                        {' '}
                        ← {i > 0 ? labelOf(path.nodes[i - 1].node_id) : ''}
                      </span>
                    </p>
                  )}
                  {n.role === 'vvf' && !n.switcher && (
                    <ol className="mt-1 flex flex-col gap-0.5 text-xs">
                      {(n.instance_ids ?? []).map((id, k) => (
                        <li key={id} className="flex gap-1.5">
                          <span className="text-ink-subtle tabular-nums">{k + 1}.</span>
                          {fnName(id)}
                        </li>
                      ))}
                    </ol>
                  )}
                  {typeof n.processing_latency_ms === 'number' && (
                    <span
                      className={cx(
                        'mt-auto pt-1 text-xs tabular-nums',
                        isHotNode(n) ? 'text-health-critical font-bold' : 'text-ink-muted',
                      )}
                    >
                      {t('topology.processing')} {formatMs(n.processing_latency_ms)}
                    </span>
                  )}
                </li>
                {i < path.nodes.length - 1 && (
                  <HopView hop={hop} hot={hop ? isHotHop(hop) : false} />
                )}
              </Fragment>
            )
          })}
        </ol>
      </Section>

      <Section title={t('topology.l3')} note={t('topology.l3Note')}>
        <div className="mb-3 flex flex-wrap items-center gap-1.5 text-sm">
          <span className="text-ink-subtle mr-1 text-xs">{t('topology.segmentList')}</span>
          {(path.segment_list ?? []).map((sid, i) => (
            <Fragment key={`${sid}-${i}`}>
              {i > 0 && <ArrowRight className="text-ink-subtle size-3.5" aria-hidden />}
              <code className="border-line-strong rounded border px-1.5 py-0.5 font-mono text-xs">
                {sid}
              </code>
            </Fragment>
          ))}
        </div>
        <table className="w-full max-w-3xl border-collapse text-sm">
          <thead>
            <tr className="border-line text-ink-subtle border-b text-left text-[0.6875rem] font-semibold tracking-wider uppercase">
              <th scope="col" className="py-1.5 pr-4">
                {t('topology.node')}
              </th>
              <th scope="col" className="py-1.5 pr-4">
                {t('topology.roleCol')}
              </th>
              <th scope="col" className="py-1.5 pr-4">
                {t('topology.router')}
              </th>
              <th scope="col" className="py-1.5">
                SID
              </th>
            </tr>
          </thead>
          <tbody>
            {path.nodes.map((n) => (
              <tr key={n.node_id} className="border-line border-b last:border-b-0">
                <td className="py-1.5 pr-4">
                  {n.role === 'headend' || n.role === 'egress' ? nameOf(n.node_id) : n.label}
                  <span className="text-ink-subtle ml-1.5 text-xs">{n.node_id}</span>
                </td>
                <td className="py-1.5 pr-4">{roleLabel(n)}</td>
                <td className="py-1.5 pr-4 font-mono text-xs">{n.router_id ?? '-'}</td>
                <td className="py-1.5 font-mono text-xs">{n.sid ?? '-'}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </Section>
    </div>
  )
}

function Shell({ header, children }: { header: ReactNode; children: ReactNode }) {
  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-auto">
      {header}
      <p className="text-ink-muted px-5 py-4 text-sm">{children}</p>
    </div>
  )
}

/** Connector between two path nodes: arrow in the video direction with the hop metrics. */
function HopView({ hop, hot }: { hop?: PathHop; hot: boolean }) {
  const t = useT()
  return (
    <li
      aria-label={t('topology.hop')}
      className={cx(
        'flex w-32 shrink-0 flex-col items-center justify-center px-1 text-center text-[0.6875rem] tabular-nums',
        hot ? 'text-health-critical font-bold' : 'text-ink-muted',
      )}
    >
      {hop?.latency_ms !== undefined && <span className="text-sm">{formatMs(hop.latency_ms)}</span>}
      <span
        aria-hidden
        className={cx(
          'my-1 flex h-0.5 w-full items-center justify-end',
          hot ? 'bg-health-critical' : 'bg-accent',
        )}
      >
        <span
          className={cx(
            'size-0 border-y-[5px] border-l-[8px] border-y-transparent',
            hot ? 'border-l-health-critical' : 'border-l-accent',
          )}
        />
      </span>
      {hop?.packet_loss_rate !== undefined && (
        <span>
          {t('topology.loss')} {formatLossRate(hop.packet_loss_rate)}
        </span>
      )}
      {hop?.bandwidth_utilization !== undefined && (
        <span>
          {t('topology.bandwidth')} {Math.round(hop.bandwidth_utilization * 100)} %
        </span>
      )}
    </li>
  )
}
