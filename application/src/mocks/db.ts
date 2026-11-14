import type {
  CellConfigSnapshot,
  CellTelemetry,
  ChainConstraints,
  ChainIssue,
  DynamicConfig,
  FieldError,
  FunctionInstance,
  FunctionType,
  HealthState,
  LogEntry,
  LogLevel,
  MatrixCell,
  MatrixValidationResult,
  MediaFormat,
  Preset,
  PresetInput,
  PresetLayout,
  PresetResourceEstimate,
  PresetValidationResult,
  RollbackInfo,
  ResourcePool,
  ValidationIssue,
} from '../lib/types'
import { publish } from './events'
import {
  cellSeeds,
  constraints,
  defaultDynamicConfig,
  destinations,
  functionCatalog,
  layouts as seedLayouts,
  presets as seedPresets,
  processingLatencyMs,
  sources,
  STANDBY_FEED,
  switchers as seedSwitchers,
  UNICASTS_PER_SOURCE,
  type SwitcherSeed,
  vvfNodes,
  type VvfNode,
} from './seed'

// Stateful in-memory model of the IEL used by the MSW handlers.
// It follows the rules in api/openapi.yaml so the UI can be developed before the Go implementation.

export const clone = <T>(value: T): T => structuredClone(value)

export const db = {
  sources,
  destinations,
  presets: new Map<string, Preset>(seedPresets.map((p) => [p.id, clone(p)])),
  layouts: new Map<string, PresetLayout>(seedLayouts.map((l) => [l.preset_id, clone(l)])),
  cells: new Map<string, MatrixCell>(),
  /** Last Known Good config per cell. */
  lkg: new Map<string, CellConfigSnapshot>(),
  /** Cells whose health is pinned (seeded faults) until the config is re-applied. */
  pinnedHealth: new Map<string, HealthState>(),
  config: clone(defaultDynamicConfig),
  switchers: clone(seedSwitchers),
  logs: [] as LogEntry[],
  presetSeq: 100,
}

// ---------------------------------------------------------------- presets & cells

/** Cells (standby / active) that currently reference the preset. */
export const cellsUsing = (presetId: string): MatrixCell[] =>
  [...db.cells.values()].filter(
    (c) =>
      c.preset_id === presetId &&
      (c.execution_state === 'standby' || c.execution_state === 'active'),
  )

/** Preset as returned by the API (with the read-only in_use_cell_ids). */
export const presetView = (preset: Preset): Preset => ({
  ...clone(preset),
  in_use_cell_ids: cellsUsing(preset.id).map((c) => c.cell_id),
})

/** Cells reference their preset: copy the preset's current chain into live_chain. */
export function syncCellChain(cell: MatrixCell): void {
  const preset = cell.preset_id ? db.presets.get(cell.preset_id) : undefined
  cell.preset_name = preset?.name ?? null
  cell.live_chain = clone(preset?.chain ?? [])
}

export function snapshotOf(cell: MatrixCell): CellConfigSnapshot {
  return {
    preset_id: cell.preset_id ?? null,
    preset_name: cell.preset_name ?? null,
    live_chain: clone(cell.live_chain ?? []),
  }
}

const cellIdOf = (sourceId: string, destinationId: string) =>
  `IN${sourceId.replace('in-', '')}-OUT${destinationId.replace('out-', '')}`

export function touch(cell: MatrixCell): MatrixCell {
  cell.updated_at = new Date().toISOString()
  publish({ type: 'matrix_update', cell: clone(cell) })
  return cell
}

// ---------------------------------------------------------------- logs

const levelRank: Record<LogLevel, number> = { debug: 0, info: 1, warn: 2, error: 3 }
// crypto.randomUUID() is unavailable in insecure contexts (e.g. http://<LAN IP>:15173), so use a counter.
let logSeq = 0

export function log(level: LogLevel, message: string, cellId?: string, code?: string): void {
  if (levelRank[level] < levelRank[db.config.log_level]) return
  const entry: LogEntry = {
    id: `log-${++logSeq}`,
    timestamp: new Date().toISOString(),
    level,
    cell_id: cellId ?? null,
    code: code ?? null,
    message,
  }
  db.logs.unshift(entry)
  db.logs.length = Math.min(db.logs.length, 1000)
  publish({ type: 'log', log: entry })
}

export function queryLogs(level?: LogLevel, cellId?: string, limit = 100): LogEntry[] {
  return db.logs
    .filter((l) => !level || levelRank[l.level] >= levelRank[level])
    .filter((l) => !cellId || l.cell_id === cellId)
    .slice(0, limit)
}

// ---------------------------------------------------------------- params & formats

export function validateParams(type: FunctionType, params: Record<string, unknown>): FieldError[] {
  const schema = functionCatalog.find((f) => f.type === type)?.param_schema as
    { required?: string[]; properties?: Record<string, Record<string, unknown>> } | undefined
  if (!schema) return [{ message: `Unknown function type: ${type}` }]
  const errors: FieldError[] = []
  for (const key of schema.required ?? []) {
    if (params[key] === undefined) errors.push({ field: key, message: `${key} is required` })
  }
  for (const [key, value] of Object.entries(params)) {
    const rule = schema.properties?.[key]
    if (!rule) {
      errors.push({ field: key, message: `${key} is not a known parameter` })
      continue
    }
    if (rule.type === 'integer' || rule.type === 'number') {
      if (typeof value !== 'number' || (rule.type === 'integer' && !Number.isInteger(value))) {
        errors.push({
          field: key,
          message: `${key} must be ${rule.type === 'integer' ? 'an integer' : 'a number'}`,
        })
      } else if (
        (typeof rule.minimum === 'number' && value < rule.minimum) ||
        (typeof rule.maximum === 'number' && value > rule.maximum)
      ) {
        errors.push({
          field: key,
          message: `${key} must be between ${rule.minimum} and ${rule.maximum}`,
        })
      }
    }
    if (rule.type === 'string') {
      if (typeof value !== 'string') errors.push({ field: key, message: `${key} must be a string` })
      else if (typeof rule.maxLength === 'number' && value.length > rule.maxLength)
        errors.push({ field: key, message: `${key} must be at most ${rule.maxLength} characters` })
      else if (Array.isArray(rule.enum) && !rule.enum.includes(value))
        errors.push({ field: key, message: `${key} must be one of ${rule.enum.join(', ')}` })
    }
  }
  return errors
}

const formatLabel = (f: MediaFormat) => ({ '8k_full': '8K FULL', '8k_dg': '8K DG' })[f.video_format]

/** Propagate a media format through the chain using the catalog's format_transitions. */
export function propagateFormat(
  input: MediaFormat,
  chain: FunctionInstance[],
): { output: MediaFormat | null; issue?: ChainIssue } {
  let current = clone(input)
  for (const fn of chain) {
    const transitions = functionCatalog.find((f) => f.type === fn.type)?.format_transitions
    const video = transitions?.video_format
    if (video && !(current.video_format in video)) {
      return {
        output: null,
        issue: {
          code: 'FORMAT_MISMATCH',
          instance_id: fn.instance_id,
          message: `${fn.instance_id} cannot take ${formatLabel(current)} as input`,
        },
      }
    }
    current = { video_format: video ? video[current.video_format] : current.video_format }
  }
  return { output: current }
}

function formatIssue(
  input: MediaFormat,
  chain: FunctionInstance[],
  required: MediaFormat,
): ChainIssue | undefined {
  const { output, issue } = propagateFormat(input, chain)
  if (issue) return issue
  if (output && output.video_format !== required.video_format) {
    return {
      code: 'FORMAT_MISMATCH',
      instance_id: null,
      message: `Output must be ${formatLabel(required)}, but the chain outputs ${formatLabel(output)}`,
    }
  }
  return undefined
}

// ---------------------------------------------------------------- VVF

const vvfOf = (type: FunctionType) => constraints.vvfs.find((v) => v.function_types.includes(type))

/** Functions of a pinned VVF form one instance only on the same node (dynamic VVFs ignore nodes). */
const sameNode = (a: FunctionInstance, b: FunctionInstance) =>
  vvfOf(a.type)?.allocation !== 'pinned' || (a.vvf_node_id ?? null) === (b.vvf_node_id ?? null)

/**
 * Node rules: a function of a pinned VVF names one of the VVF's nodes (vvf_node_id); a function of a
 * dynamic VVF names none (the IEL assigns its node).
 */
function nodeIssues(chain: FunctionInstance[]): ChainIssue[] {
  const issues: ChainIssue[] = []
  for (const fn of chain) {
    const vvf = vvfOf(fn.type)
    if (!vvf) continue
    const node = fn.vvf_node_id ?? null
    if (vvf.allocation === 'pinned' && node === null)
      issues.push({
        code: 'VVF_NODE_REQUIRED',
        vvf_id: vvf.id,
        instance_id: fn.instance_id,
        message: `Choose which '${vvf.name}' node ${fn.instance_id} uses`,
      })
    else if (
      node !== null &&
      (vvf.allocation !== 'pinned' || !vvf.nodes?.some((n) => n.node_id === node))
    )
      issues.push({
        code: 'VVF_NODE_UNKNOWN',
        vvf_id: vvf.id,
        instance_id: fn.instance_id,
        message:
          vvf.allocation === 'pinned'
            ? `${node} is not a '${vvf.name}' node`
            : `'${vvf.name}' nodes are assigned by the IEL; ${fn.instance_id} must not name one`,
      })
  }
  return issues
}

/**
 * VVF rules, checked per VVF instance (each instance is one End.AN node): consecutive functions of
 * one VVF form an instance until a type repeats, as on the routers (toVvfBlocks). Every instance of a
 * require_all VVF needs all its functions (a lone color after a complete color / transcode / crop is
 * a second, incomplete instance), members split by other functions are not contiguous, and ordered
 * VVFs keep their order inside the instance.
 */
function vvfIssues(chain: FunctionInstance[]): ChainIssue[] {
  type Instance = { vvf: ChainConstraints['vvfs'][number]; fns: FunctionInstance[]; start: number }
  const instances: Instance[] = []
  chain.forEach((fn, i) => {
    const vvf = vvfOf(fn.type)
    if (!vvf) return
    const last = instances[instances.length - 1]
    const adjacent = last && last.start + last.fns.length === i
    if (
      adjacent &&
      last.vvf.id === vvf.id &&
      sameNode(last.fns[0], fn) &&
      !last.fns.some((f) => f.type === fn.type)
    )
      last.fns.push(fn)
    else instances.push({ vvf, fns: [fn], start: i })
  })
  const issues: ChainIssue[] = []
  const notContiguous = new Set<string>()
  for (const inst of instances) {
    const { vvf, fns } = inst
    const types = new Set(fns.map((f) => f.type))
    const missing = vvf.function_types.filter((t) => !types.has(t))
    // Split by other functions: another instance of the VVF that would complete this one.
    const split =
      vvf.contiguous &&
      vvf.function_types.length > 1 &&
      instances.some(
        (o) =>
          o !== inst &&
          o.vvf.id === vvf.id &&
          !o.fns.some((f) => types.has(f.type)) &&
          o.fns.some((f) => missing.includes(f.type)),
      )
    if (split) {
      if (!notContiguous.has(vvf.id))
        issues.push({
          code: 'VVF_NOT_CONTIGUOUS',
          vvf_id: vvf.id,
          message: `Functions of VVF '${vvf.name}' must be adjacent`,
        })
      notContiguous.add(vvf.id)
    } else if (vvf.require_all && missing.length > 0) {
      issues.push({
        code: 'VVF_INCOMPLETE',
        vvf_id: vvf.id,
        instance_id: fns[0].instance_id,
        message: `This '${vvf.name}' VVF also requires ${missing.join(', ')} (each VVF instance needs all of its functions)`,
      })
    }
    if (vvf.ordered) {
      const order = fns.map((f) => vvf.function_types.indexOf(f.type))
      if (order.some((v, i) => i > 0 && v < order[i - 1]))
        issues.push({
          code: 'VVF_ORDER',
          vvf_id: vvf.id,
          instance_id: fns[0].instance_id,
          message: `Functions of VVF '${vvf.name}' must follow ${vvf.function_types.join(' > ')}`,
        })
    }
  }
  return issues
}

export type VvfBlock = {
  vvfId: string
  name: string
  /** Node a pinned VVF instance must run on (from the preset); undefined for dynamic VVFs. */
  pinned: string | undefined
  instances: FunctionInstance[]
  /** TED node that runs this block (set by the node allocation; undefined if none is free). */
  node: VvfNode | undefined
}

/**
 * Group a chain into VVF instances in the End.AN execution order (what the IEL sends southbound).
 * Consecutive functions of one VVF form one instance until a type repeats (two captions are two
 * caption VVFs). Nodes are assigned separately (allocateNodes).
 */
export function toVvfBlocks(chain: FunctionInstance[]): VvfBlock[] {
  const blocks: VvfBlock[] = []
  for (const fn of chain) {
    const vvf = vvfOf(fn.type)
    if (!vvf) continue
    const last = blocks[blocks.length - 1]
    if (
      last?.vvfId === vvf.id &&
      sameNode(last.instances[0], fn) &&
      !last.instances.some((i) => i.type === fn.type)
    )
      last.instances.push(fn)
    else
      blocks.push({
        vvfId: vvf.id,
        name: vvf.name,
        pinned: vvf.allocation === 'pinned' ? (fn.vvf_node_id ?? undefined) : undefined,
        instances: [fn],
        node: undefined,
      })
  }
  for (const block of blocks) {
    const vvf = constraints.vvfs.find((v) => v.id === block.vvfId)!
    block.instances.sort(
      (a, b) => vvf.function_types.indexOf(a.type) - vvf.function_types.indexOf(b.type),
    )
  }
  return blocks
}

// ---------------------------------------------------------------- VVF node allocation

/**
 * The config a VVF node is set to for a block: its function types and params. A node holds one
 * config (pool sharing same_config), so only streams with an equal config can share it.
 */
export const blockConfig = (block: VvfBlock): string =>
  JSON.stringify(block.instances.map((fn) => [fn.type, fn.params ?? {}]))

/**
 * A stream is one OUT with an applied (ACTIVE or STANDBY) cell: the IEL reserves VVF nodes for it
 * when its first cell goes STANDBY and keeps them until the OUT has no applied cell. The nodes run
 * the config of the OUT's ON AIR cell (or, with none ON AIR, of its first STANDBY cell); a Take
 * within the OUT reconfigures them.
 */
export type Stream = {
  destinationId: string
  cell: MatrixCell
  chain: FunctionInstance[]
  /** The stream is ON AIR (its cell is active). */
  onAir: boolean
  /** The INs of the OUT's STANDBY cells (hot standby sends them into the OUT's switchers). */
  standby: string[]
}

/** Hot standby: STANDBY INs are already sent into their switchers (seed STANDBY_FEED). */
export const hotStandby = (): boolean => STANDBY_FEED === 'switcher'

/** The INs a stream sends into its switchers: the ON AIR one, with hot standby the STANDBY ones too. */
export const fedOf = (st: Stream): string[] => [
  ...new Set([...(st.onAir ? [st.cell.source_id] : []), ...(hotStandby() ? st.standby : [])]),
]

/**
 * Per stream (destination id): switcher ids on its ON AIR IN's way (upstream first), node ids per
 * VVF block, and the way ("switcher:port" upstream first) of every IN sent into its switchers.
 */
const allocation = new Map<
  string,
  { switchers: string[]; nodes: (string | undefined)[]; feeds: Record<string, string[]> }
>()

/**
 * Config each VVF node runs now (node id -> blockConfig): set when a Take injects the ON AIR cell's
 * config, or when params of an ON AIR preset change. Nodes are reserved at STANDBY but configured
 * only at Take, right before the switch.
 */
const configured = new Map<string, string>()

/** The VVF of the switchers (behavior select; seed constraints). */
export const SWITCHER_VVF = 'vvf-switcher'
/** Key of a switcher's inputs in the allocation users (its outputs use the switcher id). */
const inputKey = (switcherId: string) => `${switcherId}#in`
/** Key of an IN's unicasts in the allocation users. */
const sourceKey = (sourceId: string) => `${sourceId}#unicast`
/** "switcher:port" of a hop. */
const hopKey = (h: SwitcherHop) => `${h.switcher.id}:${h.port}`

export type SwitcherHop = { switcher: SwitcherSeed; port: string }

export type Allocation = {
  nodes: Map<string, (VvfNode | undefined)[]>
  /** Switchers of each stream's ON AIR IN, upstream first, with the port it enters each one on. */
  switchers: Map<string, SwitcherHop[]>
  /** Per stream, the way of every IN sent into its switchers (one tree whose root feeds the OUT). */
  feeds: Map<string, Map<string, SwitcherHop[]>>
  /**
   * Blocks without a node (destination id and VVF, and the pinned node), or streams without free
   * switchers / IN unicasts (`kind`).
   */
  failed: {
    destinationId: string
    vvfId: string
    vvfName: string
    nodeId?: string
    kind?: 'vvf_output' | 'vvf_input' | 'source_unicast'
    sourceId?: string
  }[]
  /** Streams (destination ids) using each node or switcher. */
  users: Map<string, string[]>
}

/**
 * Every way from a source through switchers to a switcher that feeds the destination (upstream
 * first), shortest first. Cascades: an input may come from an upstream switcher.
 */
export function switcherPaths(sourceId: string, destinationId: string): SwitcherHop[][] {
  const paths: SwitcherHop[][] = []
  const walk = (sw: SwitcherSeed, after: SwitcherHop[], seen: Set<string>) => {
    if (seen.has(sw.id)) return
    for (const input of sw.inputs) {
      const hop = { switcher: sw, port: input.port }
      if (input.from === sourceId) paths.push([hop, ...after])
      const upstream = db.switchers.find((s) => s.id === input.from)
      if (upstream) walk(upstream, [hop, ...after], new Set([...seen, sw.id]))
    }
  }
  for (const sw of db.switchers)
    if (sw.destination_ids.includes(destinationId)) walk(sw, [], new Set())
  return paths.sort((a, b) => a.length - b.length)
}

/**
 * Assign every stream's VVF blocks to TED nodes. A node runs one config: streams whose block config
 * is equal share it up to max_streams, every other config needs a free node. Streams keep their
 * current nodes when still valid (no needless reconfiguration), then the rest prefer nodes already
 * running the same config before taking a free one. One stream never uses a node twice. A pinned
 * block uses only its node.
 *
 * `fresh` names streams being taken: their blocks avoid nodes that run another config now (the ON
 * AIR cell's), so the new config can be injected before the switch without touching the picture on
 * air. Only when no such node is free do they reuse one.
 */
export function allocateNodes(
  streams: Stream[],
  options: { switchers?: boolean; fresh?: Set<string> } = {},
): Allocation {
  const load = new Map<string, { config: string; streams: number }>()
  const users = new Map<string, string[]>()
  const nodes = new Map<string, (VvfNode | undefined)[]>()
  const used = new Map<string, Set<string>>()
  const failed: Allocation['failed'] = []
  // Switchers are skipped only by unit tests of the VVF node rules.
  const feeds: Allocation['feeds'] =
    options.switchers === false ? new Map() : allocateFeeds(streams, users, failed, options.fresh)
  const switchers = new Map(
    streams.map((st) => [
      st.destinationId,
      st.onAir ? (feeds.get(st.destinationId)?.get(st.cell.source_id) ?? []) : [],
    ]),
  )
  const items = streams.map((st) => ({ key: st.destinationId, blocks: toVvfBlocks(st.chain) }))
  const busyElse = (key: string, node: VvfNode, config: string) =>
    Boolean(options.fresh?.has(key)) &&
    configured.has(node.id) &&
    configured.get(node.id) !== config
  const fits = (node: VvfNode, config: string) => {
    const l = load.get(node.id)
    return !l || (l.config === config && l.streams < (node.max_streams ?? Infinity))
  }
  const put = (key: string, i: number, node: VvfNode, config: string) => {
    const l = load.get(node.id)
    load.set(node.id, { config, streams: (l?.streams ?? 0) + 1 })
    users.set(node.id, [...(users.get(node.id) ?? []), key])
    nodes.get(key)![i] = node
    used.get(key)!.add(node.id)
  }
  for (const { key, blocks } of items) {
    nodes.set(
      key,
      blocks.map(() => undefined),
    )
    used.set(key, new Set())
    const previous = allocation.get(key)?.nodes ?? []
    blocks.forEach((block, i) => {
      const node = vvfNodes.find(
        (n) =>
          n.id === previous[i] &&
          n.vvf_id === block.vvfId &&
          (block.pinned === undefined || n.id === block.pinned),
      )
      const config = blockConfig(block)
      if (
        node &&
        !used.get(key)!.has(node.id) &&
        fits(node, config) &&
        !busyElse(key, node, config)
      )
        put(key, i, node, config)
    })
  }
  for (const { key, blocks } of items) {
    blocks.forEach((block, i) => {
      if (nodes.get(key)![i]) return
      const config = blockConfig(block)
      const all = vvfNodes.filter(
        (n) =>
          n.vvf_id === block.vvfId &&
          (block.pinned === undefined || n.id === block.pinned) &&
          !used.get(key)!.has(n.id) &&
          fits(n, config),
      )
      const quiet = all.filter((n) => !busyElse(key, n, config))
      const candidates = quiet.length > 0 ? quiet : all
      const node = candidates.find((n) => load.has(n.id)) ?? candidates[0]
      if (node) put(key, i, node, config)
      else
        failed.push({
          destinationId: key,
          vvfId: block.vvfId,
          vvfName: block.pinned
            ? (vvfNodes.find((n) => n.id === block.pinned)?.label ?? block.name)
            : block.name,
          nodeId: block.pinned,
        })
    })
  }
  return { nodes, switchers, feeds, failed, users }
}

/**
 * The switcher ways of the INs every stream sends (fedOf): each IN takes one of its unicasts
 * (source_unicast), and the ways of one OUT form a tree whose root feeds the OUT: every switcher in it
 * uses one output (max_outputs) and sends to one place, every input port in it counts once
 * (max_inputs). Streams that had an IN or a tree keep them first; an ON AIR IN that is not being
 * taken keeps its way; an IN being taken tries the way it is already fed on first (hot standby: the
 * Take only switches). Every STANDBY feed first tries its previous way too (stable: the same state
 * gives the same ways); only when that leaves a shortage are the STANDBY feeds packed again, shortest
 * first, so a cascade no longer needed frees its switchers. Without a free tree a stream fails when
 * the domain policy requires a switcher.
 */
function allocateFeeds(
  streams: Stream[],
  users: Map<string, string[]>,
  failed: Allocation['failed'],
  fresh?: Set<string>,
): Map<string, Map<string, SwitcherHop[]>> {
  const attempt = (pack: boolean) => {
    const u = new Map([...users].map(([k, v]) => [k, [...v]]))
    const f: Allocation['failed'] = []
    return { result: allocateFeedsWith(streams, u, f, pack, fresh), users: u, failed: f }
  }
  let best = attempt(false)
  if (best.failed.length > 0) {
    const packed = attempt(true)
    if (packed.failed.length < best.failed.length) best = packed
  }
  users.clear()
  for (const [k, v] of best.users) users.set(k, v)
  failed.push(...best.failed)
  return best.result
}

function allocateFeedsWith(
  streams: Stream[],
  users: Map<string, string[]>,
  failed: Allocation['failed'],
  pack: boolean,
  fresh?: Set<string>,
): Map<string, Map<string, SwitcherHop[]>> {
  const count = (id: string) => users.get(id)?.length ?? 0
  const record = (id: string, key: string) => users.set(id, [...(users.get(id) ?? []), key])
  const previous = (st: Stream) => allocation.get(st.destinationId)
  const prevWay = (st: Stream, src: string) => previous(st)?.feeds[src]
  // IN unicasts: streams that had the IN keep it, then the rest in matrix order.
  const fed = new Map<string, string[]>()
  for (const keep of [true, false])
    for (const st of streams)
      for (const src of fedOf(st)) {
        if (Boolean(prevWay(st, src)) !== keep) continue
        if (count(sourceKey(src)) >= UNICASTS_PER_SOURCE) {
          failed.push({
            destinationId: st.destinationId,
            vvfId: '',
            vvfName: src,
            kind: 'source_unicast',
            sourceId: src,
          })
          continue
        }
        record(sourceKey(src), st.destinationId)
        fed.set(st.destinationId, [...(fed.get(st.destinationId) ?? []), src])
      }

  const result = new Map<string, Map<string, SwitcherHop[]>>()
  const ordered = [...streams].sort(
    (a, b) =>
      Number(!previous(a)?.switchers.length && !Object.keys(previous(a)?.feeds ?? {}).length) -
      Number(!previous(b)?.switchers.length && !Object.keys(previous(b)?.feeds ?? {}).length),
  )
  for (const st of ordered) {
    // The switchers are checked for every IN the stream wants, also those without a free unicast:
    // a shortage of both is reported as both. Only the INs that are sent get ways.
    const want = fedOf(st)
    const srcs = fed.get(st.destinationId) ?? []
    if (want.length === 0) continue
    const taken = fresh?.has(st.destinationId) ?? false
    const keepOnAir = st.onAir && !taken ? st.cell.source_id : undefined
    const taking = st.onAir && taken ? st.cell.source_id : undefined
    let tree = feedTree(st, want, keepOnAir, taking, pack, users)
    if ('kind' in tree && constraints.domain.switcher_required)
      failed.push({
        destinationId: st.destinationId,
        vvfId: '',
        vvfName: 'Switcher',
        kind: tree.kind,
      })
    // Keep the ways of the INs that are sent.
    if ('kind' in tree && srcs.length > 0 && srcs.length < want.length)
      tree = feedTree(st, srcs, keepOnAir, taking, pack, users)
    if ('kind' in tree) continue
    for (const src of [...tree.paths.keys()]) if (!srcs.includes(src)) tree.paths.delete(src)
    for (const sw of tree.down.keys()) record(sw, st.destinationId)
    for (const port of tree.ports) record(inputKey(port.split(':')[0]), st.destinationId)
    result.set(st.destinationId, tree.paths)
  }
  return result
}

type FeedTree = {
  paths: Map<string, SwitcherHop[]>
  /** Where each switcher sends its output: the next "switcher:port", "" for the root (the OUT). */
  down: Map<string, string>
  ports: Set<string>
}

/** Switcher ways for the stream's INs that fit the free outputs and inputs (see allocateFeeds). */
function feedTree(
  st: Stream,
  srcs: string[],
  keepOnAir: string | undefined,
  taking: string | undefined,
  pack: boolean,
  users: Map<string, string[]>,
): FeedTree | { kind: 'vvf_output' | 'vvf_input' } {
  const count = (id: string) => users.get(id)?.length ?? 0
  const prev = allocation.get(st.destinationId)?.feeds ?? {}
  const outputFree = (sw: SwitcherSeed) => count(sw.id) < sw.max_outputs
  const add = (t: FeedTree, src: string, way: SwitcherHop[]): boolean => {
    for (const [i, h] of way.entries()) {
      const next = i + 1 < way.length ? hopKey(way[i + 1]) : ''
      const down = t.down.get(h.switcher.id)
      if (down !== undefined) {
        if (down !== next) return false
      } else {
        if (!outputFree(h.switcher)) return false
        t.down.set(h.switcher.id, next)
      }
      if (!t.ports.has(hopKey(h))) {
        const inTree = [...t.ports].filter((p) => p.startsWith(`${h.switcher.id}:`)).length
        if (count(inputKey(h.switcher.id)) + inTree >= h.switcher.max_inputs) return false
        t.ports.add(hopKey(h))
      }
    }
    t.paths.set(src, way)
    return true
  }
  const copy = (t: FeedTree): FeedTree => ({
    paths: new Map(t.paths),
    down: new Map(t.down),
    ports: new Set(t.ports),
  })
  const prevRoot = Object.values(prev)
    .find((w) => w.length > 0)
    ?.at(-1)
    ?.split(':')[0]
  const roots = db.switchers
    .filter((sw) => sw.destination_ids.includes(st.destinationId))
    .sort((a, b) => Number(b.id === prevRoot) - Number(a.id === prevRoot))
  let kind: 'vvf_output' | 'vvf_input' = 'vvf_output'
  for (const root of roots) {
    if (!outputFree(root)) continue
    kind = 'vvf_input'
    const candidates = srcs.map((src) => {
      const ways = switcherPaths(src, st.destinationId).filter(
        (w) => w.at(-1)!.switcher.id === root.id,
      )
      const before = prev[src]?.join()
      const kept = ways.find((w) => w.map(hopKey).join() === before)
      if (src === keepOnAir && before) return kept ? [kept] : []
      if ((src === taking || !pack) && kept) return [kept, ...ways.filter((w) => w !== kept)]
      // Shortest first (switcherPaths sorts by length), the previous way first among equals.
      return kept
        ? [...ways].sort((a, b) => a.length - b.length || Number(b === kept) - Number(a === kept))
        : ways
    })
    const search = (i: number, t: FeedTree): FeedTree | null => {
      if (i === srcs.length) return t
      for (const way of candidates[i]) {
        const next = copy(t)
        if (add(next, srcs[i], way)) {
          const found = search(i + 1, next)
          if (found) return found
        }
      }
      return null
    }
    const found = search(0, { paths: new Map(), down: new Map([[root.id, '']]), ports: new Set() })
    if (found) return found
  }
  return { kind }
}

/** A hypothetical change to the matrix: a cell's state and / or chain (for checks before acting). */
export type CellChange = {
  state?: (cell: MatrixCell) => MatrixCell['execution_state']
  chain?: (cell: MatrixCell) => FunctionInstance[]
}

/** The streams of the matrix, optionally with a change applied. */
export function streamsOf(change: CellChange = {}): Stream[] {
  const stateOf = (c: MatrixCell) => change.state?.(c) ?? c.execution_state
  const byOut = new Map<string, MatrixCell[]>()
  for (const cell of db.cells.values()) {
    const state = stateOf(cell)
    if (state !== 'active' && state !== 'standby') continue
    byOut.set(cell.destination_id, [...(byOut.get(cell.destination_id) ?? []), cell])
  }
  return [...byOut].map(([destinationId, cells]) => {
    const cell = cells.find((c) => stateOf(c) === 'active') ?? cells[0]
    return {
      destinationId,
      cell,
      chain: change.chain?.(cell) ?? cell.live_chain ?? [],
      onAir: stateOf(cell) === 'active',
      standby: cells.filter((c) => stateOf(c) === 'standby').map((c) => c.source_id),
    }
  })
}

/**
 * Every shortage as a validation issue naming the pool nodes that ran out (bottleneck), one per OUT
 * and pool. The shortages of focus (the OUT a change is for) come first, its VVF nodes and switchers
 * before its IN unicasts (what a release of STANDBY cells may not solve); on the other OUTs the IN
 * unicasts come first (the change took an IN they had).
 */
function capacityIssues({ failed, users }: Allocation, focus?: string): ValidationIssue[] {
  const rank = (f: Allocation['failed'][number]) => {
    const unicast = f.kind === 'source_unicast'
    if (f.destinationId === focus) return unicast ? 1 : 0
    return unicast ? 2 : 3
  }
  const seen = new Set<string>()
  return [...failed]
    .sort((a, b) => rank(a) - rank(b))
    .filter((f) => {
      const key = [f.destinationId, f.kind ?? 'vvf_node', f.vvfId, f.nodeId].join('|')
      if (seen.has(key)) return false
      seen.add(key)
      return true
    })
    .map((f) => shortage(f, users))
}

/** The first shortage (see capacityIssues), null when everything fits. */
function capacityIssue(alloc: Allocation): ValidationIssue | null {
  return capacityIssues(alloc)[0] ?? null
}

function shortage(
  { vvfId, vvfName, destinationId, nodeId, kind, sourceId }: Allocation['failed'][number],
  users: Allocation['users'],
): ValidationIssue {
  const others = (key: string) => [
    ...new Set((users.get(key) ?? []).filter((d) => d !== destinationId)),
  ]
  if (kind === 'source_unicast') {
    const src = sources.find((s) => s.id === sourceId)
    const outs = others(sourceKey(sourceId!))
    return {
      code: 'RESOURCE_EXHAUSTED',
      resource_kind: kind,
      message: `${sourceId} is already sent to ${outs.join(', ')}`,
      bottleneck_nodes: [
        { node_id: sourceId!, label: src?.name ?? sourceId!, destination_ids: outs },
      ],
    }
  }
  if (kind) {
    return {
      code: 'RESOURCE_EXHAUSTED',
      resource_kind: kind,
      message: `No free Switcher ${kind === 'vvf_input' ? 'input' : 'output'} for the stream to ${destinationId}`,
      bottleneck_nodes: db.switchers.map((sw) => ({
        node_id: sw.id,
        label: sw.label,
        destination_ids: others(kind === 'vvf_input' ? inputKey(sw.id) : sw.id),
      })),
    }
  }
  // A pinned block can only use its own node, so that node is the whole bottleneck.
  const pool = vvfNodes.filter((n) => (nodeId ? n.id === nodeId : n.vvf_id === vvfId))
  return {
    code: 'RESOURCE_EXHAUSTED',
    resource_kind: 'vvf_node',
    message: `No free ${vvfName} VVF node for the stream to ${destinationId}`,
    bottleneck_nodes: pool.map((n) => ({
      node_id: n.id,
      label: n.label,
      destination_ids: others(n.id),
    })),
  }
}

/** Why the streams would not fit the pools after the change (null: they fit). */
export function capacityIssueWith(change: CellChange): ValidationIssue | null {
  return capacityIssue(allocateNodes(streamsOf(change)))
}

/** Every shortage after the change, those of focus (the OUT the change is for) first. */
export function capacityIssuesWith(change: CellChange, focus?: string): ValidationIssue[] {
  return capacityIssues(allocateNodes(streamsOf(change)), focus)
}

/** The change a Take makes: the cell goes ON AIR, the OUT's ON AIR cell steps down. */
const takeChange = (cell: MatrixCell): CellChange => ({
  state: (c) =>
    c.cell_id === cell.cell_id
      ? 'active'
      : c.destination_id === cell.destination_id && c.execution_state === 'active'
        ? 'standby'
        : c.execution_state,
})

/** Every reason the cell cannot be taken now (standby cells only; empty: Take is possible). */
export function takeIssuesOf(cell: MatrixCell): ValidationIssue[] {
  if (cell.execution_state !== 'standby') return []
  // The IN unicast, switcher and VVF node pools after the Take (with hot standby the IN and the
  // switchers are reserved at STANDBY already).
  return capacityIssuesWith(takeChange(cell), cell.destination_id)
}

/** The STANDBY cells whose IN is sent into the switchers (hot standby) and the switchers on its way. */
export function standbyFeeds(): { cell: MatrixCell; nodes: string[] }[] {
  return [...db.cells.values()].flatMap((cell) => {
    if (cell.execution_state !== 'standby') return []
    const way = allocation.get(cell.destination_id)?.feeds[cell.source_id]
    return way?.length ? [{ cell, nodes: way.map((h) => h.split(':')[0]) }] : []
  })
}

/**
 * Nodes for a cell's path: its stream's nodes for an ON AIR cell, or for a standby cell the nodes it
 * would use if taken now (undefined where no node is free).
 */
export function nodesOf(cell: MatrixCell): (VvfNode | undefined)[] {
  const byId = (ids: (string | undefined)[]) => ids.map((id) => vvfNodes.find((n) => n.id === id))
  if (cell.execution_state === 'active')
    return byId(allocation.get(cell.destination_id)?.nodes ?? [])
  return allocateNodes(streamsOf(takeChange(cell))).nodes.get(cell.destination_id) ?? []
}

/**
 * Switchers on a cell's way (upstream first, with the cell's input ports): its stream's switchers for
 * an ON AIR cell, or for a standby cell those it would use if taken now (empty if none is free).
 */
export function switcherPathOf(cell: MatrixCell): SwitcherHop[] {
  if (cell.execution_state === 'active') {
    const way = allocation.get(cell.destination_id)?.feeds[cell.source_id]?.join()
    return (
      switcherPaths(cell.source_id, cell.destination_id).find(
        (p) => p.map(hopKey).join() === way,
      ) ?? []
    )
  }
  return allocateNodes(streamsOf(takeChange(cell))).switchers.get(cell.destination_id) ?? []
}

/**
 * Injects an ON AIR cell's VVF config into its allocated nodes (Take: before the switch; params
 * change: at once). Returns the labels of the nodes whose config changed.
 */
export function injectVvfConfig(cell: MatrixCell): string[] {
  const changed: string[] = []
  for (const block of cellBlocks(cell)) {
    if (!block.node) continue
    const config = blockConfig(block)
    if (configured.get(block.node.id) === config) continue
    configured.set(block.node.id, config)
    changed.push(block.node.label)
  }
  return changed
}

/** VVF blocks of the cell with their nodes (see nodesOf). */
export function cellBlocks(cell: MatrixCell): VvfBlock[] {
  const blocks = toVvfBlocks(cell.live_chain ?? [])
  const nodes = nodesOf(cell)
  blocks.forEach((block, i) => (block.node = nodes[i]))
  return blocks
}

/**
 * Every resource pool with its TED limit and the streams using it (GET /resources/pools): VVF nodes,
 * switcher outputs and inputs, and IN unicasts. The checks above use the same limits.
 */
export function resourcePools(): ResourcePool[] {
  const streams = new Map(streamsOf().map((st) => [st.destinationId, st]))
  const use = (destinationId: string) => {
    const st = streams.get(destinationId)
    return st ? [{ destination_id: destinationId, cell_id: st.cell.cell_id, on_air: st.onAir }] : []
  }
  const usersOf = (
    match: (a: {
      nodes: (string | undefined)[]
      switchers: string[]
      feeds: Record<string, string[]>
    }) => boolean,
  ) => [...allocation].filter(([, a]) => match(a)).flatMap(([d]) => use(d))
  const onWay = (a: { feeds: Record<string, string[]> }, swId: string) =>
    Object.values(a.feeds).some((w) => w.some((h) => h.startsWith(`${swId}:`)))
  const feedAt = hotStandby() ? ('standby' as const) : ('on_air' as const)
  return [
    ...vvfNodes.map((n) => ({
      id: `vvf_node:${n.id}`,
      kind: 'vvf_node',
      node_id: n.id,
      group: n.vvf_id,
      label: n.label,
      capacity: n.max_streams ?? null,
      sharing: 'same_config' as const,
      reserved_at: 'standby' as const,
      used_by: usersOf((a) => a.nodes.includes(n.id)),
    })),
    ...db.switchers.flatMap((sw) => [
      {
        id: `vvf_output:${sw.id}`,
        kind: 'vvf_output',
        node_id: sw.id,
        group: SWITCHER_VVF,
        label: `${sw.label} output`,
        capacity: sw.max_outputs,
        sharing: 'none' as const,
        reserved_at: feedAt,
        used_by: usersOf((a) => onWay(a, sw.id)),
      },
      {
        id: `vvf_input:${sw.id}`,
        kind: 'vvf_input',
        node_id: sw.id,
        group: SWITCHER_VVF,
        label: `${sw.label} inputs`,
        capacity: sw.max_inputs,
        sharing: 'none' as const,
        reserved_at: feedAt,
        used_by: usersOf((a) => onWay(a, sw.id)),
      },
    ]),
    ...sources.map((src) => ({
      id: `source_unicast:${src.id}`,
      kind: 'source_unicast',
      node_id: src.id,
      group: null,
      label: `${src.name} unicast`,
      capacity: UNICASTS_PER_SOURCE,
      sharing: 'none' as const,
      reserved_at: feedAt,
      // The cells whose IN is sent: ON AIR, with hot standby STANDBY too.
      used_by: [...db.cells.values()]
        .filter(
          (c) =>
            c.source_id === src.id &&
            (c.execution_state === 'active' || (hotStandby() && c.execution_state === 'standby')),
        )
        .map((c) => ({
          destination_id: c.destination_id,
          cell_id: c.cell_id,
          on_air: c.execution_state === 'active',
        })),
    })),
  ]
}

/**
 * Re-allocate the streams after a change and refresh every cell's take_issues (publishing the cells
 * whose issue changed). Callers check feasibility before changing the model.
 */
export function refreshVvf(options: { fresh?: Set<string> } = {}): void {
  const streams = streamsOf()
  const { nodes, switchers, feeds } = allocateNodes(streams, options)
  allocation.clear()
  for (const [key, list] of nodes)
    allocation.set(key, {
      nodes: list.map((n) => n?.id),
      switchers: (switchers.get(key) ?? []).map((h) => h.switcher.id),
      feeds: Object.fromEntries(
        [...(feeds.get(key) ?? [])].map(([src, way]) => [src, way.map(hopKey)]),
      ),
    })
  // Nodes no ON AIR stream uses any more keep no live config.
  const live = new Set(
    streams.filter((st) => st.onAir).flatMap((st) => allocation.get(st.destinationId)?.nodes ?? []),
  )
  for (const id of [...configured.keys()]) if (!live.has(id)) configured.delete(id)
  for (const cell of db.cells.values()) {
    const next = takeIssuesOf(cell)
    if (JSON.stringify(next) === JSON.stringify(cell.take_issues ?? [])) continue
    cell.take_issues = next
    touch(cell)
  }
}

// ---------------------------------------------------------------- preset validation

export function validatePreset(input: PresetInput): PresetValidationResult {
  const issues: ChainIssue[] = []
  const chain = input.chain ?? []
  if (chain.length < constraints.min_functions)
    issues.push({
      code: 'EMPTY_CHAIN',
      message: `At least ${constraints.min_functions} functions are required`,
    })
  if (constraints.max_functions !== undefined && chain.length > constraints.max_functions)
    issues.push({
      code: 'MAX_FUNCTIONS_EXCEEDED',
      message: `At most ${constraints.max_functions} functions are allowed`,
    })

  const ids = new Set<string>()
  const counts = new Map<string, number>()
  for (const fn of chain) {
    if (ids.has(fn.instance_id))
      issues.push({
        code: 'DUPLICATE_INSTANCE_ID',
        instance_id: fn.instance_id,
        message: `${fn.instance_id} is duplicated`,
      })
    ids.add(fn.instance_id)
    counts.set(fn.type, (counts.get(fn.type) ?? 0) + 1)
    if (!functionCatalog.some((f) => f.type === fn.type)) {
      issues.push({
        code: 'FUNCTION_UNAVAILABLE',
        instance_id: fn.instance_id,
        message: `${fn.type} is not offered by any VVF`,
      })
      continue
    }
    for (const err of validateParams(fn.type, fn.params ?? {})) {
      issues.push({ code: 'INVALID_PARAMS', instance_id: fn.instance_id, message: err.message })
    }
  }
  for (const [type, count] of counts) {
    const max = constraints.max_instances_per_type?.[type]
    if (max !== undefined && count > max)
      issues.push({ code: 'TYPE_LIMIT_EXCEEDED', message: `At most ${max} ${type} per chain` })
  }
  issues.push(...nodeIssues(chain))
  issues.push(...vvfIssues(chain))

  // Formats are checked against the domain policy because IN / OUT are not known while designing.
  const { source_format, destination_format } = constraints.domain
  const propagated = propagateFormat(source_format, chain)
  const issue = formatIssue(source_format, chain, destination_format)
  if (issue) issues.push(issue)

  const compatible = [...db.cells.values()]
    .filter((cell) => cell.execution_state !== 'disconnected')
    .filter((cell) => {
      const src = sources.find((s) => s.id === cell.source_id)!
      const dst = destinations.find((d) => d.id === cell.destination_id)!
      return !formatIssue(src, chain, dst)
    })
    .map((cell) => cell.cell_id)

  const valid = issues.length === 0
  return {
    valid,
    issues,
    output_format: propagated.output,
    compatible_cell_ids: compatible,
    // Resources are feedback, not a validity rule: a preset may be saved even if none fit now.
    resources: valid ? estimateResources(chain) : null,
  }
}

/** True if the chain structure (instance ids, types, order) differs. */
export const structureChanged = (a: FunctionInstance[], b: FunctionInstance[]) =>
  a.length !== b.length ||
  a.some(
    (fn, i) =>
      fn.instance_id !== b[i].instance_id ||
      fn.type !== b[i].type ||
      (fn.vvf_node_id ?? null) !== (b[i].vvf_node_id ?? null),
  )

// ---------------------------------------------------------------- matrix validation

export function estimateLatency(chain: FunctionInstance[]): number {
  const blocks = toVvfBlocks(chain)
  const base = 3.2
  const hops = blocks.length * 1.1
  const processing = chain.reduce((sum, fn) => sum + processingLatencyMs[fn.type], 0)
  return Math.round((base + hops + processing) * 10) / 10
}

export function validateAssignment(
  cell: MatrixCell,
  preset: Pick<Preset, 'chain'>,
  release: string[] = [],
): MatrixValidationResult {
  const errors: ValidationIssue[] = []
  const src = sources.find((s) => s.id === cell.source_id)!
  const dst = destinations.find((d) => d.id === cell.destination_id)!
  if (
    cell.execution_state === 'disconnected' ||
    src.status === 'offline' ||
    dst.status === 'offline'
  )
    errors.push({ code: 'NODE_OFFLINE', message: 'Source or destination is disconnected' })
  const issue = formatIssue(src, preset.chain, dst)
  if (issue) errors.push({ code: 'FORMAT_MISMATCH', message: issue.message })
  // The cell becomes (or stays) STANDBY / ON AIR with the preset and the released cells turn OFF:
  // every stream must keep its pools. A new stream (first applied cell of its OUT) needs free nodes,
  // with hot standby a new STANDBY IN its unicast and switcher inputs; a swap on air may need nodes.
  if (cell.execution_state !== 'disconnected') {
    const capacity = capacityIssuesWith(
      assignChange(cell, preset.chain, release),
      cell.destination_id,
    )
    if (capacity.length > 0) {
      // Every shortage is listed; a release is offered only when it solves all of them.
      const releasable = releasableCells(cell, preset.chain, release)
      errors.push(
        ...capacity.map((c) =>
          releasable.length > 0 ? { ...c, releasable_cells: releasable } : c,
        ),
      )
    }
  }
  // v1 has no SLA: the estimated latency is information only.
  return {
    feasible: errors.length === 0,
    estimated_latency_ms: estimateLatency(preset.chain),
    reason: errors[0]?.message,
    errors,
  }
}

/** The change of assigning a chain to a cell: released cells turn OFF, an OFF cell goes STANDBY. */
export const assignChange = (
  cell: MatrixCell,
  chain: FunctionInstance[],
  release: string[] = [],
): CellChange => ({
  state: (c) =>
    release.includes(c.cell_id)
      ? 'off'
      : c.cell_id === cell.cell_id && c.execution_state === 'off'
        ? 'standby'
        : c.execution_state,
  chain: (c) => (c.cell_id === cell.cell_id ? chain : (c.live_chain ?? [])),
})

/**
 * The smallest set of STANDBY cells whose release makes the assignment fit (empty: none does). Only
 * the cell's IN on other OUTs is offered: the release lets the user move an IN that is already sent
 * elsewhere, never turns off another IN's STANDBY for an unrelated shortage. ON AIR cells are never
 * released.
 */
function releasableCells(cell: MatrixCell, chain: FunctionInstance[], release: string[]): string[] {
  const candidates = [...db.cells.values()]
    .filter(
      (c) =>
        c.cell_id !== cell.cell_id &&
        c.source_id === cell.source_id &&
        c.execution_state === 'standby' &&
        !release.includes(c.cell_id),
    )
    .map((c) => c.cell_id)
    .slice(0, 10)
  const subsets = (k: number, start = 0): string[][] =>
    k === 0
      ? [[]]
      : candidates
          .slice(start)
          .flatMap((id, i) => subsets(k - 1, start + i + 1).map((rest) => [id, ...rest]))
  for (let k = 1; k <= candidates.length; k++)
    for (const ids of subsets(k))
      if (
        capacityIssuesWith(assignChange(cell, chain, [...release, ...ids]), cell.destination_id)
          .length === 0
      )
        return ids
  return []
}

// ---------------------------------------------------------------- resource estimate (designer)

/**
 * How many more streams with `chain` fit next to `base`, one per OUT that has no stream yet (the
 * number of OUTs is a limit too), and the allocation that failed (undefined: every free OUT fits).
 * Each new stream comes from an IN that is not sent yet (the IN unicasts are a limit too).
 */
function streamsThatFit(base: Stream[], chain: FunctionInstance[]) {
  const taken = new Set(base.map((st) => st.destinationId))
  const free = destinations.filter((d) => !taken.has(d.id))
  const extra: Stream[] = []
  for (const dst of free) {
    const sent = new Set([...base, ...extra].flatMap(fedOf))
    const src = sources.find((s) => !sent.has(s.id)) ?? sources[0]
    const cell = db.cells.get(cellIdOf(src.id, dst.id))!
    // New streams count as ON AIR: the estimate is how many could be sent out at once.
    extra.push({ destinationId: dst.id, cell, chain, onAir: true, standby: [] })
    const alloc = allocateNodes([...base, ...extra])
    if (alloc.failed.length > 0) return { count: extra.length - 1, failed: alloc }
  }
  return { count: extra.length, failed: undefined }
}

/**
 * Resource estimate of a draft preset against the TED and the current streams (POST
 * /presets/validate): new streams it could start now, streams the whole TED could run, VVF node use,
 * the cells it could be assigned to now, and the bottleneck nodes when no new stream fits.
 */
export function estimateResources(chain: FunctionInstance[]): PresetResourceEstimate {
  const now = streamsThatFit(streamsOf(), chain)
  const total = streamsThatFit([], chain)
  const pools = resourcePools()
  const free = (p: ResourcePool) => p.used_by.length === 0
  const blocks = toVvfBlocks(chain)
  // Dynamic VVFs draw on every node of the VVF; a pinned VVF only on the nodes the chain names.
  const requirements: PresetResourceEstimate['requirements'] = constraints.vvfs
    .filter((v) => blocks.some((b) => b.vvfId === v.id))
    .flatMap((v) => {
      const mine = blocks.filter((b) => b.vvfId === v.id)
      const sets =
        v.allocation === 'pinned'
          ? [...new Set(mine.map((b) => b.pinned))].map((node) => ({
              label: vvfNodes.find((n) => n.id === node)?.label ?? v.name,
              blocks: mine.filter((b) => b.pinned === node),
              match: (p: ResourcePool) => p.node_id === node,
            }))
          : [{ label: v.name, blocks: mine, match: () => true }]
      return sets.map((set) => {
        const group = pools.filter((p) => p.kind === 'vvf_node' && p.group === v.id && set.match(p))
        return {
          kind: 'vvf_node',
          group: v.id,
          label: set.label,
          per_stream: set.blocks.length,
          pool_count: group.length,
          free_count: group.filter(free).length,
        }
      })
    })
  // A new stream goes ON AIR through one switcher output when the domain requires switchers.
  if (constraints.domain.switcher_required) {
    const outputs = pools.filter((p) => p.kind === 'vvf_output' && p.group === SWITCHER_VVF)
    requirements.push({
      kind: 'vvf_output',
      group: SWITCHER_VVF,
      label: 'Switcher',
      per_stream: 1,
      pool_count: outputs.length,
      free_count: outputs.filter(free).length,
    })
  }
  const assignable = [...db.cells.values()]
    .filter((cell) => validateAssignment(cell, { chain }).feasible)
    .map((cell) => cell.cell_id)
  return {
    available_streams: now.count,
    max_streams: total.count,
    requirements,
    assignable_cell_ids: assignable,
    blocker: now.count === 0 && now.failed ? capacityIssue(now.failed) : null,
  }
}

// ---------------------------------------------------------------- telemetry

/** Nominal latency of a degraded path in the simulation (not an SLA: v1 has none). */
const DEGRADED_BASE_MS = 20

/**
 * Simulated measurements. v1 compares them with no SLA, so they never raise a drift by themselves;
 * only the seeded demo faults (pinned health) degrade them.
 */
export function simulateTelemetry(cell: MatrixCell): CellTelemetry | undefined {
  if (cell.execution_state === 'off' || cell.execution_state === 'disconnected') return undefined
  const pinned = db.pinnedHealth.get(cell.cell_id)
  let latency = estimateLatency(cell.live_chain ?? []) + Math.random() * 0.6
  let loss = Math.random() * 0.00001
  let jitter = 0.2 + Math.random() * 0.3
  if (pinned === 'warning') {
    latency = DEGRADED_BASE_MS + 1 + Math.random() * 3
    jitter = 1.5 + Math.random()
  }
  if (pinned === 'critical') {
    latency = DEGRADED_BASE_MS * 2 + Math.random() * 5
    loss = 0.05 + Math.random() * 0.02
    jitter = 4 + Math.random() * 2
  }
  latency = Math.round(latency * 10) / 10
  const degraded = pinned === 'warning' || pinned === 'critical'
  return {
    e2e_latency_ms: latency,
    jitter_ms: Math.round(jitter * 100) / 100,
    packet_loss_rate: loss,
    drift_detected: degraded,
    message: degraded ? `Latency degraded to ${latency}ms` : undefined,
  }
}

export function healthOf(cell: MatrixCell): HealthState {
  if (cell.health_state === 'remediating') return 'remediating'
  const pinned = db.pinnedHealth.get(cell.cell_id)
  if (pinned) return pinned
  return cell.telemetry?.drift_detected ? 'warning' : 'healthy'
}

// ---------------------------------------------------------------- initial cells

for (const src of sources) {
  for (const dst of destinations) {
    const cellId = cellIdOf(src.id, dst.id)
    const seed = cellSeeds[cellId]
    const preset = seed?.preset ? db.presets.get(seed.preset) : undefined
    const cell: MatrixCell = {
      cell_id: cellId,
      source_id: src.id,
      destination_id: dst.id,
      execution_state: seed?.state ?? 'off',
      // Health is judged only for cells with a preset and an established path (active / standby).
      health_state:
        seed?.state === 'active' || seed?.state === 'standby'
          ? (seed.health ?? 'healthy')
          : undefined,
      preset_id: preset?.id ?? null,
      last_rollback: null,
      updated_at: new Date().toISOString(),
    }
    syncCellChain(cell)
    if (seed?.health && seed.health !== 'healthy') db.pinnedHealth.set(cellId, seed.health)
    cell.telemetry = simulateTelemetry(cell)
    db.cells.set(cellId, cell)
    db.lkg.set(cellId, snapshotOf(cell))
  }
}

// ---------------------------------------------------------------- switchers

/**
 * Points every switcher on the cell's way at the cell's stream (after the allocation is refreshed),
 * or (active false) stops the output of the switchers its stream uses. Returns the changed states.
 */
export function setSwitcherFor(cell: MatrixCell, active: boolean) {
  const hops = active
    ? switcherPathOf(cell)
    : (allocation.get(cell.destination_id)?.switchers ?? []).map((id) => ({
        switcher: db.switchers.find((s) => s.id === id)!,
        port: null,
      }))
  return hops.map(({ switcher, port }) => {
    switcher.active_input = active ? port : null
    return { switcher_id: switcher.id, active_input: switcher.active_input ?? null }
  })
}

// Initial allocation of the seeded streams, then the ON AIR cells select their switcher inputs.
refreshVvf()
for (const cell of db.cells.values())
  if (cell.execution_state === 'active') {
    injectVvfConfig(cell)
    setSwitcherFor(cell, true)
  }

// ---------------------------------------------------------------- router (NWF) simulation & rollback

export type FaultMode = 'error' | 'timeout' | 'rollback_fail'

/** Fault injected into the next router apply (set from the browser console via `visfoxMock`). */
export const faults: { next: FaultMode | null } = { next: null }

const wait = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))

export type ApplyResult = { ok: true } | { ok: false; status: 502 | 504; rollbacks: RollbackInfo[] }

/**
 * Apply a config change to the (simulated) routers of the given cells.
 * `apply` mutates the model; `revert` undoes model changes that are not per-cell (e.g. preset params).
 * On failure every affected cell is rolled back to its LKG.
 */
export async function applyToRouter(
  cells: MatrixCell[],
  apply: () => void,
  describe: string,
  revert: () => void = () => undefined,
): Promise<ApplyResult> {
  if (cells.length > 0) await wait(120 + Math.random() * 120)
  const fault = cells.length > 0 ? faults.next : null
  if (cells.length > 0) faults.next = null
  if (!fault) {
    apply()
    for (const cell of cells) {
      db.pinnedHealth.delete(cell.cell_id)
      cell.telemetry = simulateTelemetry(cell)
      cell.health_state = healthOf(cell)
      db.lkg.set(cell.cell_id, snapshotOf(cell))
    }
    return { ok: true }
  }

  apply()
  const reasonCode = fault === 'timeout' ? 'ROUTER_TIMEOUT' : 'ROUTER_ERROR'
  const reason = `${fault === 'timeout' ? 'Router timed out' : 'Router returned an error'} while applying ${describe}`
  const occurredAt = new Date().toISOString()
  const inProgress = cells.map((cell) => {
    const info: RollbackInfo = {
      cell_id: cell.cell_id,
      trigger: 'apply_error',
      result: 'in_progress',
      reason_code: reasonCode,
      reason,
      from: snapshotOf(cell),
      to: db.lkg.get(cell.cell_id) ?? null,
      occurred_at: occurredAt,
    }
    cell.health_state = 'remediating'
    cell.last_rollback = info
    publish({ type: 'rollback', rollback: clone(info) })
    touch(cell)
    return info
  })
  await wait(fault === 'timeout' ? 1200 : 600)

  revert()
  const failed = fault === 'rollback_fail'
  const finished = cells.map((cell, i) => {
    const lkg = db.lkg.get(cell.cell_id)
    if (lkg) {
      cell.preset_id = lkg.preset_id
      cell.preset_name = lkg.preset_name
      cell.live_chain = clone(lkg.live_chain)
    }
    const info: RollbackInfo = { ...inProgress[i], result: failed ? 'failed' : 'succeeded' }
    cell.last_rollback = info
    if (failed) db.pinnedHealth.set(cell.cell_id, 'critical')
    cell.health_state = failed ? 'critical' : 'healthy'
    cell.telemetry = simulateTelemetry(cell)
    return info
  })

  // Response first, then the completion events.
  setTimeout(() => {
    cells.forEach((cell, i) => {
      publish({ type: 'rollback', rollback: clone(finished[i]) })
      touch(cell)
      const lkgName = db.lkg.get(cell.cell_id)?.preset_name ?? 'none'
      if (failed)
        log(
          'error',
          `${cell.cell_id}: ${reason}. Rollback to the last known good config failed.`,
          cell.cell_id,
          reasonCode,
        )
      else
        log(
          'warn',
          `${cell.cell_id}: ${reason}. Rolled back to the last known good config (${lkgName}).`,
          cell.cell_id,
          reasonCode,
        )
    })
    refreshVvf()
  }, 0)

  return { ok: false, status: fault === 'timeout' ? 504 : 502, rollbacks: clone(finished) }
}

/**
 * Demo: simulate a large degradation on a cell and roll it back to its LKG. The API names this
 * trigger sla_violation (v2); v1 has no SLA, so the demo only shows the rollback lifecycle.
 */
export async function degradeCell(cellId: string): Promise<void> {
  const cell = db.cells.get(cellId)
  if (!cell || (cell.execution_state !== 'active' && cell.execution_state !== 'standby')) {
    console.warn(`[visfox mock] ${cellId} has no applied preset`)
    return
  }
  const info: RollbackInfo = {
    cell_id: cell.cell_id,
    trigger: 'sla_violation',
    result: 'in_progress',
    reason_code: null,
    reason: 'Latency degraded significantly after a config change',
    from: snapshotOf(cell),
    to: db.lkg.get(cell.cell_id) ?? null,
    occurred_at: new Date().toISOString(),
  }
  cell.health_state = 'remediating'
  cell.last_rollback = info
  publish({ type: 'rollback', rollback: clone(info) })
  touch(cell)
  await wait(1500)
  const done: RollbackInfo = { ...info, result: 'succeeded' }
  db.pinnedHealth.delete(cell.cell_id)
  cell.last_rollback = done
  cell.health_state = 'healthy'
  cell.telemetry = simulateTelemetry(cell)
  publish({ type: 'rollback', rollback: clone(done) })
  touch(cell)
  log(
    'warn',
    `${cell.cell_id}: Degradation detected. Rolled back to the last known good config.`,
    cell.cell_id,
  )
}

export function updateConfig(next: DynamicConfig): void {
  db.config = next
}
