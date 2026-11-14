import {
  Background,
  BaseEdge,
  MarkerType,
  ReactFlow,
  type Edge,
  type EdgeProps,
  type Node,
  type NodeProps,
  Handle,
  Position as Side,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { useEffect, useMemo, useState } from 'react'
import { useT } from '../../i18n'
import { api, request } from '../../lib/api'
import { cx } from '../../lib/cx'
import { notifyError } from '../../lib/errors'
import type { ResourcePool, Topology } from '../../lib/types'
import { useSwitchersStore } from '../../stores/switchers'
import { useUiStore } from '../../stores/ui'
import {
  layoutColumns,
  layoutGraph,
  liveSwitchers,
  streamColors,
  NODE_H,
  NODE_W,
  routedPath,
  type Point,
} from './topologyLayout'

const REFRESH_MS = 5000

type TopoNode = Topology['nodes'][number]
type Data = { node: TopoNode; switcher: boolean; pools: ResourcePool[] }

const statusRing: Record<string, string> = {
  healthy: 'border-line-strong',
  warning: 'border-health-warning',
  remediating: 'border-health-remediating',
  critical: 'border-health-critical',
  offline: 'border-exec-disconnected border-dashed',
}

function TopoNodeView({ data }: NodeProps<Node<Data>>) {
  const { node, switcher, pools } = data
  const t = useT()
  return (
    <div
      className={cx(
        'flex h-full w-full flex-col justify-center rounded-md border-2 px-3',
        switcher
          ? 'bg-surface border-dashed'
          : node.type === 'vvf'
            ? 'bg-accent-soft'
            : 'bg-surface',
        statusRing[node.status ?? 'healthy'],
      )}
    >
      {node.type !== 'source' && (
        <Handle type="target" position={Side.Left} isConnectable={false} />
      )}
      <span className="flex items-baseline gap-2">
        <span className="min-w-0 flex-1 truncate text-sm font-bold">{node.label}</span>
        <PoolUsage pools={pools} />
      </span>
      <span className="text-ink-subtle truncate text-[0.6875rem]">
        {switcher
          ? t('topology.role.switcher')
          : node.type === 'vvf'
            ? (node.function_types ?? []).join(', ')
            : node.id}
      </span>
      {node.type !== 'destination' && (
        <Handle type="source" position={Side.Right} isConnectable={false} />
      )}
    </div>
  )
}

/**
 * Whether a node's resource pools (GET /resources/pools: VVF node, switcher output / inputs, IN
 * unicast, ...) are used by streams, inverted when one has no room left. The same badge works for any
 * pool kind; counts and the streams stay in the tooltip, not on the node.
 */
function PoolUsage({ pools }: { pools: ResourcePool[] }) {
  const t = useT()
  const used = pools.filter((p) => p.used_by.length > 0)
  if (used.length === 0) return null
  const full = pools.some(
    (p) => p.capacity !== null && p.capacity !== undefined && p.used_by.length >= p.capacity,
  )
  const list = used
    .map(
      (p) =>
        `${p.label} ${p.used_by.length}${p.capacity ? `/${p.capacity}` : ''}: ${p.used_by
          .map((u) => `${u.cell_id} (${u.on_air ? 'ON AIR' : 'STANDBY'})`)
          .join(', ')}`,
    )
    .join('\n')
  return (
    <span
      title={t('topology.streamsOf', { cells: list })}
      className={cx(
        'shrink-0 rounded px-1 text-[0.625rem] font-bold tabular-nums',
        full ? 'bg-ink text-surface' : 'bg-surface text-ink',
      )}
    >
      {full ? t('topology.full') : t('topology.inUse')}
    </span>
  )
}

const nodeTypes = { topo: TopoNodeView }

type RoutedData = { points: Point[]; offset: number }

/** A link drawn through its lanes (waypoints) so it passes between nodes, not across them. */
function RoutedEdge({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  data,
  label,
  markerEnd,
  style,
}: EdgeProps<Edge<RoutedData>>) {
  // Streams sharing a link are drawn side by side (offset in px).
  const dy = data?.offset ?? 0
  const points = [
    { x: sourceX, y: sourceY },
    ...(data?.points ?? []),
    { x: targetX, y: targetY },
  ].map((p) => ({ x: p.x, y: p.y + dy }))
  // Label in the middle of the first segment (next to the source).
  const labelX = (points[0].x + points[1].x) / 2
  const labelY = (points[0].y + points[1].y) / 2
  return (
    <BaseEdge
      id={id}
      path={routedPath(points)}
      label={label}
      labelX={labelX}
      labelY={labelY}
      markerEnd={markerEnd}
      style={style}
    />
  )
}

const edgeTypes = { routed: RoutedEdge }

/**
 * L7 abstract topology (GET /resources/topology) shown at /topology: sources,
 * TED VVF nodes, and destinations with the links streams flow on: ON AIR paths with their latency,
 * and STANDBY feeds up to their switcher (hot standby). Read only.
 */
export function TopologyOverview() {
  const t = useT()
  const theme = useUiStore((s) => s.theme)
  const [topology, setTopology] = useState<Topology | null>(null)
  const [pools, setPools] = useState<ResourcePool[]>([])
  const switchers = useSwitchersStore((s) => s.switchers)

  useEffect(() => {
    let alive = true
    let reported = false
    const load = () =>
      Promise.all([request(api.GET('/resources/topology')), request(api.GET('/resources/pools'))])
        .then(([data, poolData]) => {
          if (!alive) return
          setTopology(data)
          setPools(poolData)
        })
        .catch((e) => {
          // Report once; the periodic refresh keeps trying.
          if (alive && !reported) notifyError(t, e)
          reported = true
        })
    void load()
    const timer = setInterval(load, REFRESH_MS)
    return () => {
      alive = false
      clearInterval(timer)
    }
  }, [t])

  const { nodes, edges, legend } = useMemo(() => {
    if (!topology) return { nodes: [] as Node[], edges: [] as Edge[], legend: [] }
    const col = layoutColumns(
      topology,
      new Map(Object.values(switchers).map((sw) => [sw.id, sw.inputs.map((i) => i.from)])),
    )
    // Only what streams flow on is drawn: the links of ON AIR paths, and with hot standby
    // the feeds of STANDBY INs up to their switcher. Wiring nothing flows on has no line.
    const onAir = topology.links.filter((l) => !l.standby)
    // One color per stream when a node carries several streams; then a link used by several streams
    // is drawn once per stream, side by side.
    const colors = streamColors(onAir)
    const linkEdges: Edge<RoutedData>[] = onAir.flatMap((l) => {
      const cells = colors.size > 0 && l.cell_ids?.length ? l.cell_ids : [undefined]
      return cells.map((cell, k) => {
        const color = (cell && colors.get(cell)) ?? 'var(--color-accent)'
        return {
          id: cells.length > 1 ? `${l.source}->${l.target}#${cell}` : `${l.source}->${l.target}`,
          source: l.source,
          target: l.target,
          label: l.latency_ms !== undefined ? `${l.latency_ms.toFixed(1)} ms` : undefined,
          className: 'video-flow',
          markerEnd: { type: MarkerType.ArrowClosed, color },
          style: { stroke: color, strokeWidth: 2 },
          data: { points: [], offset: (k - (cells.length - 1) / 2) * 5 },
        }
      })
    })
    // STANDBY feeds: the stream reaches the switcher but is not selected, so it goes no further.
    // Grey, finer and slower than the ON AIR flow, labelled with the switcher input port only (the
    // line is short: more text would not fit).
    const standbyEdges: Edge<RoutedData>[] = topology.links
      .filter((l) => l.standby)
      .map((l) => ({
        id: `${l.source}->${l.target}#standby`,
        source: l.source,
        target: l.target,
        className: 'video-standby',
        markerEnd: { type: MarkerType.ArrowClosed, color: 'var(--color-ink-subtle)' },
        style: { stroke: 'var(--color-ink-subtle)', strokeWidth: 1.5 },
        data: { points: [], offset: 0 },
      }))
    // Switcher inputs on an ON AIR path: the port, and thick when it is the selected input of a
    // switcher whose output reaches a destination (cascades).
    const live = liveSwitchers(Object.values(switchers))
    for (const sw of Object.values(switchers)) {
      for (const input of sw.inputs) {
        const active = live.has(sw.id) && input.port === sw.active_input
        for (const e of linkEdges.filter((e) => e.source === input.from && e.target === sw.id)) {
          e.label = `${input.port} · ${e.label ?? ''}`
          e.style = { ...e.style, strokeWidth: active ? 3.5 : 2 }
        }
        for (const e of standbyEdges.filter((e) => e.source === input.from && e.target === sw.id))
          e.label = input.port
      }
    }
    const edges = [...standbyEdges, ...linkEdges]
    const layout = layoutGraph(
      topology.nodes.map((n) => n.id),
      col,
      edges,
    )
    const nodes: Node<Data>[] = topology.nodes.map((n) => ({
      id: n.id,
      type: 'topo',
      position: layout.positions[n.id],
      data: {
        node: n,
        switcher: Boolean(switchers[n.id]),
        pools: pools.filter((p) => p.node_id === n.id),
      },
      width: NODE_W,
      height: NODE_H,
      draggable: false,
      selectable: false,
      connectable: false,
    }))
    return {
      nodes,
      edges: edges.map((e) => ({
        ...e,
        type: 'routed',
        data: { points: layout.waypoints[e.id] ?? [], offset: e.data?.offset ?? 0 },
      })),
      legend: [...colors],
    }
  }, [topology, switchers, pools, t])

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      <header className="border-line border-b px-5 py-4">
        <h1 className="text-xl font-bold">{t('topology.overview')}</h1>
        <p className="text-ink-subtle text-xs">{t('topology.overviewHint')}</p>
      </header>
      {topology ? (
        <div className="relative min-h-0 flex-1" aria-label={t('topology.overview')}>
          {legend.length > 0 && (
            <ul
              aria-label={t('topology.streamLegend')}
              className="border-line bg-surface absolute top-3 left-3 z-10 flex flex-col gap-1 rounded-md border px-3 py-2 text-xs shadow-sm"
            >
              <li className="text-ink-subtle text-[0.625rem] font-semibold tracking-wider uppercase">
                {t('topology.streamLegend')}
              </li>
              {legend.map(([cell, color]) => (
                <li key={cell} className="flex items-center gap-2">
                  <span className="h-1 w-6 rounded" style={{ background: color }} aria-hidden />
                  {cell}
                </li>
              ))}
            </ul>
          )}
          <ReactFlow
            nodes={nodes}
            edges={edges}
            nodeTypes={nodeTypes}
            edgeTypes={edgeTypes}
            nodesDraggable={false}
            nodesConnectable={false}
            elementsSelectable={false}
            colorMode={theme === 'dark' ? 'dark' : 'light'}
            fitView
            fitViewOptions={{ padding: 0.2, maxZoom: 1 }}
            proOptions={{ hideAttribution: true }}
          >
            <Background gap={24} />
          </ReactFlow>
        </div>
      ) : (
        <p className="text-ink-muted px-5 py-4 text-sm">{t('topology.loading')}</p>
      )}
    </div>
  )
}
