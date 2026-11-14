import type { Topology } from '../../lib/types'

// Layout of the L7 topology overview: a small layered (Sugiyama-style) layout.
// Nodes go into columns; a link that skips columns gets a lane in every column it passes, so it is
// drawn between nodes instead of across them; each column is ordered by the positions of its
// neighbors to reduce crossings. Pure functions; the graph itself comes from the IEL.

export const COL_W = 300
export const NODE_W = 220
export const NODE_H = 56
/** Vertical slot of a node (node height + gap) and of a lane for a passing link. */
export const ROW_H = 90
export const LANE_H = 28

/**
 * Columns from the links: sources first, then switchers (cascaded ones one column per stage, from
 * `switcherInputs`: switcher id -> ids its inputs come from), processing VVF nodes by their depth
 * along the paths (unlinked ones in the first VVF column), destinations last.
 */
export function layoutColumns(
  topology: Topology,
  switcherInputs: ReadonlyMap<string, readonly string[]> = new Map(),
): Record<string, number> {
  // Switcher stage: 1 + the deepest upstream switcher feeding it (bounded against cycles).
  const stage = (id: string, depth = 0): number => {
    const upstream = (switcherInputs.get(id) ?? []).filter((from) => switcherInputs.has(from))
    if (upstream.length === 0 || depth > switcherInputs.size) return 1
    return 1 + Math.max(...upstream.map((from) => stage(from, depth + 1)))
  }
  const switcherCol = new Map([...switcherInputs.keys()].map((id) => [id, stage(id)]))
  // Processing VVFs start after the last switcher stage.
  const firstVvf = 1 + Math.max(0, ...switcherCol.values())
  const switcherIds = new Set(switcherCol.keys())
  const col: Record<string, number> = {}
  for (const n of topology.nodes)
    col[n.id] = n.type === 'source' ? 0 : (switcherCol.get(n.id) ?? firstVvf)
  // Relax along links (paths are short and acyclic; bound the passes anyway).
  for (let pass = 0; pass < topology.nodes.length; pass++) {
    let changed = false
    for (const l of topology.links) {
      if (col[l.source] === undefined || col[l.target] === undefined) continue
      const target = topology.nodes.find((n) => n.id === l.target)
      if (target?.type !== 'vvf' || switcherIds.has(target.id)) continue
      if (col[l.target] < col[l.source] + 1) {
        col[l.target] = col[l.source] + 1
        changed = true
      }
    }
    if (!changed) break
  }
  const lastVvf = Math.max(
    firstVvf,
    ...topology.nodes.filter((n) => n.type === 'vvf').map((n) => col[n.id]),
  )
  for (const n of topology.nodes) if (n.type === 'destination') col[n.id] = lastVvf + 1
  return col
}

export type Point = { x: number; y: number }
export type GraphLayout = {
  /** Top-left of every node. */
  positions: Record<string, Point>
  /** For links that skip columns: points the link passes through (lane entry / exit per column). */
  waypoints: Record<string, Point[]>
}

type Item = { id: string; lane: boolean }

/**
 * Positions nodes and routes links. `links` are all drawn links (id, source, target); links that
 * skip columns get one lane per skipped column.
 */
export function layoutGraph(
  nodeIds: string[],
  col: Record<string, number>,
  links: { id: string; source: string; target: string }[],
): GraphLayout {
  const maxCol = Math.max(0, ...nodeIds.map((id) => col[id] ?? 0))
  const columns: Item[][] = Array.from({ length: maxCol + 1 }, () => [])
  for (const id of nodeIds) columns[col[id] ?? 0].push({ id, lane: false })

  // Chains of segments: source -> lanes ... -> target. Adjacency between consecutive columns.
  const preds = new Map<string, string[]>()
  const succs = new Map<string, string[]>()
  const connect = (a: string, b: string) => {
    succs.set(a, [...(succs.get(a) ?? []), b])
    preds.set(b, [...(preds.get(b) ?? []), a])
  }
  const laneIds: Record<string, string[]> = {}
  for (const l of links) {
    const from = col[l.source]
    const to = col[l.target]
    if (from === undefined || to === undefined || to <= from) continue
    let prev = l.source
    laneIds[l.id] = []
    for (let c = from + 1; c < to; c++) {
      const lane = `${l.id}#${c}`
      columns[c].push({ id: lane, lane: true })
      laneIds[l.id].push(lane)
      connect(prev, lane)
      prev = lane
    }
    connect(prev, l.target)
  }

  // Order each column by the mean position of its neighbors (barycenter), sweeping both ways.
  const index = new Map<string, number>()
  const reindex = (c: number) => columns[c].forEach((it, i) => index.set(it.id, i))
  columns.forEach((_, c) => reindex(c))
  const sweep = (c: number, neighbors: Map<string, string[]>) => {
    const key = (it: Item) => {
      const ns = (neighbors.get(it.id) ?? []).filter((n) => index.has(n))
      return ns.length === 0
        ? (index.get(it.id) ?? 0)
        : ns.reduce((sum, n) => sum + index.get(n)!, 0) / ns.length
    }
    const keyed = columns[c].map((it, i) => ({ it, k: key(it), i }))
    keyed.sort((a, b) => a.k - b.k || a.i - b.i)
    columns[c] = keyed.map((x) => x.it)
    reindex(c)
  }
  // Sources (first column) and destinations (last column) keep their IN / OUT order; only the columns
  // in between move.
  for (let pass = 0; pass < 3; pass++) {
    for (let c = 1; c < maxCol; c++) sweep(c, preds)
    for (let c = maxCol - 1; c >= 1; c--) sweep(c, succs)
  }

  // Stack each column (nodes ROW_H, lanes LANE_H) and center the columns vertically.
  const height = (items: Item[]) => items.reduce((h, it) => h + (it.lane ? LANE_H : ROW_H), 0)
  const tallest = Math.max(...columns.map(height))
  const positions: Record<string, Point> = {}
  const laneY: Record<string, number> = {}
  columns.forEach((items, c) => {
    let y = (tallest - height(items)) / 2
    for (const it of items) {
      if (it.lane) {
        laneY[it.id] = y + LANE_H / 2
        y += LANE_H
      } else {
        positions[it.id] = { x: c * COL_W, y: y + (ROW_H - NODE_H) / 2 }
        y += ROW_H
      }
    }
  })

  const waypoints: Record<string, Point[]> = {}
  for (const [linkId, lanes] of Object.entries(laneIds)) {
    waypoints[linkId] = lanes.flatMap((lane) => {
      const c = Number(lane.slice(lane.lastIndexOf('#') + 1))
      return [
        { x: c * COL_W, y: laneY[lane] },
        { x: c * COL_W + NODE_W, y: laneY[lane] },
      ]
    })
  }
  return { positions, waypoints }
}

/** SVG path through the points: straight runs inside lanes, smooth S-curves between columns. */
export function routedPath(points: Point[]): string {
  let d = `M ${points[0].x} ${points[0].y}`
  for (let i = 1; i < points.length; i++) {
    const a = points[i - 1]
    const b = points[i]
    if (a.y === b.y) {
      d += ` L ${b.x} ${b.y}`
      continue
    }
    const mid = (a.x + b.x) / 2
    d += ` C ${mid} ${a.y} ${mid} ${b.y} ${b.x} ${b.y}`
  }
  return d
}

type SwitcherLike = {
  id: string
  inputs: readonly { port: string; from: string }[]
  destination_ids: readonly string[]
  active_input?: string | null
}

/**
 * Switchers whose output reaches a destination now: one feeding a destination with an active input,
 * or an upstream one selected by a live downstream switcher. An upstream switcher keeps its own
 * active input when the downstream one moves to another input, but its stream then goes nowhere.
 */
export function liveSwitchers(switchers: readonly SwitcherLike[]): Set<string> {
  const live = new Set(
    switchers.filter((s) => s.destination_ids.length > 0 && s.active_input).map((s) => s.id),
  )
  for (let changed = true; changed;) {
    changed = false
    for (const sw of switchers) {
      if (!live.has(sw.id)) continue
      const from = sw.inputs.find((i) => i.port === sw.active_input)?.from
      const upstream = switchers.find((s) => s.id === from)
      if (upstream?.active_input && !live.has(upstream.id)) {
        live.add(upstream.id)
        changed = true
      }
    }
  }
  return live
}

const STREAM_COLORS = 6

/**
 * Colors per ON AIR stream (cell id -> CSS color) when some node carries several streams, so each
 * stream can be followed through it; empty when every node carries at most one (one color suffices).
 */
export function streamColors(
  links: readonly { target: string; cell_ids?: readonly string[] }[],
): Map<string, string> {
  const into = new Map<string, Set<string>>()
  for (const l of links)
    for (const c of l.cell_ids ?? [])
      into.set(l.target, new Set([...(into.get(l.target) ?? []), c]))
  if (![...into.values()].some((cells) => cells.size > 1)) return new Map()
  const cells = [...new Set(links.flatMap((l) => l.cell_ids ?? []))].sort()
  return new Map(cells.map((c, i) => [c, `var(--color-stream-${(i % STREAM_COLORS) + 1})`]))
}
