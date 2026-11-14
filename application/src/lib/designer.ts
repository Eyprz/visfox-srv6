import type { ChainConstraints, FunctionCatalogItem, FunctionInstance, FunctionType } from './types'

// Pure model of the SFC designer canvas. The canvas holds function nodes and
// links between them plus the reserved IN / OUT endpoints; the chain sent to the IEL is the path
// walked from IN. Structural rules (linear, one link in / out, no loops) are enforced on connect.
// VVF membership, limits and formats always come from the IEL (catalog / constraints).

/** Reserved layout ids of the endpoints (not instance ids, which start with `sf-`). */
export const IN_ID = 'in'
export const OUT_ID = 'out'

export type Link = { source: string; target: string }
export type Position = { x: number; y: number }

/** Result of walking the links from IN. */
export type WalkResult = {
  /** Functions in chain order. */
  chain: FunctionInstance[]
  /** True when the path reaches OUT and every function is on it. */
  complete: boolean
  /** Functions that are not on the IN -> OUT path. */
  unconnected: string[]
}

export function walkChain(functions: FunctionInstance[], links: Link[]): WalkResult {
  const byId = new Map(functions.map((f) => [f.instance_id, f]))
  const next = new Map(links.map((l) => [l.source, l.target]))
  const chain: FunctionInstance[] = []
  const seen = new Set<string>()
  let reachedOut = false
  let id = next.get(IN_ID)
  while (id && !seen.has(id)) {
    if (id === OUT_ID) {
      reachedOut = true
      break
    }
    const fn = byId.get(id)
    if (!fn) break
    seen.add(id)
    chain.push(fn)
    id = next.get(id)
  }
  const unconnected = functions.map((f) => f.instance_id).filter((i) => !seen.has(i))
  return { chain, complete: reachedOut && unconnected.length === 0, unconnected }
}

/** Links the video flows through: the path followed from IN (dangling links are excluded). */
export function flowingLinks(links: Link[]): Link[] {
  const next = new Map(links.map((l) => [l.source, l]))
  const result: Link[] = []
  const seen = new Set<string>()
  for (let link = next.get(IN_ID); link && !seen.has(link.source); link = next.get(link.target)) {
    seen.add(link.source)
    result.push(link)
  }
  return result
}

/**
 * Whether a link source -> target keeps the canvas a single line: no link into IN or out of OUT,
 * at most one outgoing link per source and one incoming per target, and no loop.
 */
export function canConnect(links: Link[], source: string, target: string): boolean {
  if (source === target || source === OUT_ID || target === IN_ID) return false
  if (links.some((l) => l.source === source || l.target === target)) return false
  // A loop would appear if target already leads to source.
  const next = new Map(links.map((l) => [l.source, l.target]))
  const seen = new Set<string>()
  for (let id = next.get(target); id && !seen.has(id); id = next.get(id)) {
    if (id === source) return false
    seen.add(id)
  }
  return true
}

/** Next `sf-{type}-{n}` instance id that is unused in the preset. */
export function nextInstanceId(type: FunctionType, existing: Iterable<string>): string {
  const used = new Set(existing)
  let n = 1
  while (used.has(`sf-${type}-${n}`)) n++
  return `sf-${type}-${n}`
}

/** Links of a straight chain IN -> f1 -> ... -> OUT. */
export function linksFromChain(chain: FunctionInstance[]): Link[] {
  const ids = [IN_ID, ...chain.map((f) => f.instance_id), OUT_ID]
  return ids.slice(1).map((target, i) => ({ source: ids[i], target }))
}

/**
 * Appends functions at the end of the connected path (just before OUT when the path reaches it) and
 * wires them in. Used by palette clicks; drag-and-drop places unconnected nodes instead.
 */
export function appendToChain(links: Link[], ids: string[]): Link[] {
  if (ids.length === 0) return links
  const next = new Map(links.map((l) => [l.source, l.target]))
  let tail = IN_ID
  const seen = new Set<string>()
  for (let id = next.get(IN_ID); id && id !== OUT_ID && !seen.has(id); id = next.get(id)) {
    seen.add(id)
    tail = id
  }
  const toOut = next.get(tail) === OUT_ID
  const kept = links.filter((l) => !(l.source === tail && l.target === OUT_ID))
  const added = [tail, ...ids].slice(1).map((target, i) => ({ source: [tail, ...ids][i], target }))
  const last = ids[ids.length - 1]
  // Only rewire to OUT when OUT is free (it was the tail's target or nothing links to it yet).
  const outFree = toOut || !links.some((l) => l.target === OUT_ID)
  return [...kept, ...added, ...(outFree ? [{ source: last, target: OUT_ID }] : [])]
}

/** Removes a node's links and bridges its neighbors (prev -> next) so the line stays connected. */
export function removeFromChain(links: Link[], id: string): Link[] {
  const prev = links.find((l) => l.target === id)?.source
  const next = links.find((l) => l.source === id)?.target
  const kept = links.filter((l) => l.source !== id && l.target !== id)
  return prev && next && canConnect(kept, prev, next)
    ? [...kept, { source: prev, target: next }]
    : kept
}

/** Fixed node size, so layout and VVF frames can be computed from positions without measuring. */
export const NODE_W = 260
export const NODE_H = 64
// Vertical spacing leaves room for VVF frames (padding + label) between nodes of different VVFs.
export const NODE_GAP_Y = 150
/** Horizontal distance between the columns of a wrapped chain and to unconnected nodes. */
export const COLUMN_GAP_X = NODE_W + 140
/** Nodes per column (IN and OUT included) before a long chain wraps into a second column. */
export const ROWS_PER_COLUMN = 7

/**
 * Ids of the line from IN in order (IN, functions..., OUT). OUT is always last, also when the path
 * does not reach it yet; functions off the path are not included.
 */
export function chainOrder(links: Link[]): string[] {
  const next = new Map(links.map((l) => [l.source, l.target]))
  const ids = [IN_ID]
  const seen = new Set(ids)
  for (let id = next.get(IN_ID); id && id !== OUT_ID && !seen.has(id); id = next.get(id)) {
    seen.add(id)
    ids.push(id)
  }
  return [...ids, OUT_ID]
}

/**
 * Vertical layout of the line: one column top to bottom; a long line wraps into two
 * columns of equal length. Ids are in chain order (IN first, OUT last).
 */
export function columnLayout(ids: string[]): Record<string, Position> {
  const rows = ids.length > ROWS_PER_COLUMN ? Math.ceil(ids.length / 2) : ids.length
  return Object.fromEntries(
    ids.map((id, i) => [
      id,
      { x: Math.floor(i / rows) * COLUMN_GAP_X, y: (i % rows) * NODE_GAP_Y },
    ]),
  )
}

/** Column layout of a straight chain (presets and new presets). */
export function autoLayout(chain: FunctionInstance[]): Record<string, Position> {
  return columnLayout([IN_ID, ...chain.map((f) => f.instance_id), OUT_ID])
}

/**
 * Positions after a structural change: the line from IN in its column layout; functions off the line
 * keep their place, or (new, or overlapping the line) go into a column to the right of it.
 */
export function relayout(
  functionIds: string[],
  links: Link[],
  positions: Record<string, Position>,
): Record<string, Position> {
  const order = chainOrder(links)
  const result = columnLayout(order)
  const onLine = new Set(order)
  const right = Math.max(...Object.values(result).map((p) => p.x)) + COLUMN_GAP_X
  const overlaps = (p: Position) => p.x < right - (COLUMN_GAP_X - NODE_W) && p.x > -NODE_W
  let row = 0
  for (const id of functionIds) {
    if (onLine.has(id)) continue
    const p = positions[id]
    result[id] = p && !overlaps(p) ? p : { x: right, y: row++ * NODE_GAP_Y }
  }
  return result
}

/**
 * Wires `ids` into the line right after `after` (IN or a function on the line): after -> ids... ->
 * the node that followed. Nodes already linked elsewhere are taken out first (their neighbors bridged).
 */
export function insertIntoChain(links: Link[], ids: string[], after: string): Link[] {
  let kept = links
  for (const id of ids) kept = removeFromChain(kept, id)
  const next = kept.find((l) => l.source === after)?.target
  kept = kept.filter((l) => l.source !== after)
  const seq = [after, ...ids, ...(next ? [next] : [])]
  return [...kept, ...seq.slice(1).map((target, i) => ({ source: seq[i], target }))]
}

/**
 * Where a drop at `point` (flow coordinates) cuts into the line: the id to insert after, or null when
 * the point is not near a link of the line. `exclude` is the dragged node (its own slot is skipped).
 * A gap inside a VVF frame (`groups`: instance ids of consecutive functions of one VVF) moves to the
 * nearer edge of the frame, unless `inside` allows that VVF (the dropped functions belong to it).
 */
export function findInsertGap(
  links: Link[],
  positions: Record<string, Position>,
  point: Position,
  options: {
    exclude?: string
    groups?: { vvfId: string; ids: string[] }[]
    inside?: string[]
  } = {},
): string | null {
  const order = chainOrder(links).filter((id) => id !== options.exclude)
  const center = (id: string) => {
    const p = positions[id] ?? { x: 0, y: 0 }
    return { x: p.x + NODE_W / 2, y: p.y + NODE_H / 2 }
  }
  let best: { after: string; d: number } | null = null
  for (let i = 0; i < order.length - 1; i++) {
    const a = center(order[i])
    const b = center(order[i + 1])
    const mid = { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 }
    const dx = Math.abs(point.x - mid.x)
    const dy = Math.abs(point.y - mid.y)
    if (dx > NODE_W * 0.75 || dy > NODE_GAP_Y * 0.75) continue
    const d = Math.hypot(dx, dy)
    if (!best || d < best.d) best = { after: order[i], d }
  }
  if (!best) return null
  // Keep VVF frames contiguous: a gap between two members moves before or after the frame.
  const group = options.groups?.find((g) => {
    const i = g.ids.indexOf(best.after)
    return i >= 0 && i < g.ids.length - 1
  })
  if (!group || options.inside?.includes(group.vvfId)) return best.after
  const ids = group.ids.filter((id) => id !== options.exclude)
  const first = order.indexOf(ids[0])
  const before = order[first - 1]
  const last = ids[ids.length - 1]
  const dist = (id: string) => Math.abs(center(id).y - point.y)
  return dist(ids[0]) <= dist(last) && before ? before : last
}

export type PaletteLimit = { disabled: boolean; reason?: 'max_functions' | 'type_limit' }

/** Palette availability per type from the IEL limits (max_functions, max_instances_per_type). */
/**
 * A palette element: a catalog function, bound to a node for pinned VVFs (one element per node, since
 * the nodes differ, e.g. the material a caption node burns in).
 */
export type PaletteEntry = FunctionCatalogItem & { vvf_node_id?: string }

export function paletteLimits(
  catalog: FunctionCatalogItem[],
  constraints: ChainConstraints | null,
  functions: FunctionInstance[],
  adding = 1,
): Record<string, PaletteLimit> {
  const result: Record<string, PaletteLimit> = {}
  for (const item of catalog) {
    const count = functions.filter((f) => f.type === item.type).length
    const typeMax = constraints?.max_instances_per_type?.[item.type]
    // max_functions is optional: without it there is no total limit.
    const max = constraints?.max_functions
    if (max !== undefined && functions.length + adding > max)
      result[item.type] = { disabled: true, reason: 'max_functions' }
    else if (typeMax !== undefined && count + 1 > typeMax)
      result[item.type] = { disabled: true, reason: 'type_limit' }
    else result[item.type] = { disabled: false }
  }
  return result
}
