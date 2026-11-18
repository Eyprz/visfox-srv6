import { http, HttpResponse, sse } from 'msw'
import type {
  CellPath,
  DynamicConfigPatch,
  ErrorCode,
  ErrorResponse,
  FunctionType,
  LogLevel,
  MatrixCell,
  PresetInput,
  PresetLayout,
  RollbackInfo,
  SseEventType,
  Topology,
} from '../lib/types'
import {
  applyToRouter,
  cellsUsing,
  clone,
  db,
  healthOf,
  log,
  presetView,
  queryLogs,
  simulateTelemetry,
  structureChanged,
  syncCellChain,
  capacityIssueWith,
  standbyFeeds,
  SWITCHER_VVF,
  cellBlocks,
  refreshVvf,
  hotStandby,
  injectVvfConfig,
  resourcePools,
  takeIssuesOf,
  touch,
  updateConfig,
  validateAssignment,
  validateParams,
  validatePreset,
  setSwitcherFor,
  switcherPathOf,
} from './db'
import { publish, subscribe } from './events'
import { constraints, functionCatalog, processingLatencyMs, vvfNodes } from './seed'

// MSW handlers implementing api/openapi.yaml against the in-memory mock IEL.

const API = '/api/v1'
const startedAt = Date.now()

const statusOf: Record<ErrorCode, number> = {
  INVALID_REQUEST: 400,
  INVALID_PARAMS: 400,
  RESOURCE_NOT_FOUND: 404,
  INVALID_CELL_STATE: 409,
  PRESET_IN_USE: 409,
  INVALID_CHAIN: 422,
  INFEASIBLE_ASSIGNMENT: 422,
  RESOURCE_EXHAUSTED: 409,
  ROUTER_ERROR: 502,
  ROUTER_TIMEOUT: 504,
  INTERNAL_ERROR: 500,
  // The mock implements the final behavior: it never answers 501 (the Go IEL does before stage C).
  NOT_IMPLEMENTED: 501,
}

function error(code: ErrorCode, message: string, extra: Partial<ErrorResponse> = {}) {
  return HttpResponse.json<ErrorResponse>({ code, message, ...extra }, { status: statusOf[code] })
}

const notFound = (what: string) => error('RESOURCE_NOT_FOUND', `${what} not found`)

function routerError(status: 502 | 504, rollbacks: RollbackInfo[]) {
  const code = status === 504 ? 'ROUTER_TIMEOUT' : 'ROUTER_ERROR'
  return error(code, rollbacks[0]?.reason ?? 'Router apply failed', { rollbacks })
}

async function body<T>(request: Request): Promise<T | null> {
  try {
    return (await request.json()) as T
  } catch {
    return null
  }
}

const findCell = (cellId: string | readonly string[] | undefined) => db.cells.get(String(cellId))
const presetCellIds = (presetId: string) =>
  [...db.cells.values()].filter((c) => c.preset_id === presetId).map((c) => c.cell_id)

const systemConfig = () => ({
  immutable: {
    listen_addr: ':8080',
    db_path: '/var/lib/visfox/visfox.db',
    seed_path: 'db/seed.yaml',
    router_timeout_ms: 3000,
  },
  dynamic: db.config,
})

/** Turns a cell OFF: no preset, no health, its LKG is OFF. */
function turnOff(cell: MatrixCell) {
  cell.execution_state = 'off'
  cell.preset_id = null
  syncCellChain(cell)
  cell.telemetry = undefined
  cell.health_state = undefined
  db.pinnedHealth.delete(cell.cell_id)
  db.lkg.set(cell.cell_id, { preset_id: null, preset_name: null, live_chain: [] })
  touch(cell)
}

/** release_cells must name other cells that are not ON AIR (api/openapi.yaml applyPresetToCell). */
function releaseError(cell: MatrixCell, release: string[]) {
  for (const id of release) {
    const other = db.cells.get(id)
    if (!other) return notFound(`Cell ${id}`)
    if (id === cell.cell_id)
      return error('INVALID_REQUEST', `${cell.cell_id} cannot release itself`)
    if (other.execution_state === 'active')
      return error('INVALID_CELL_STATE', `${id} is on air and cannot be released`)
  }
  return null
}

export const handlers = [
  // ------------------------------------------------------------ resources
  http.get(`${API}/resources/sources`, () => HttpResponse.json(db.sources)),
  http.get(`${API}/resources/destinations`, () => HttpResponse.json(db.destinations)),
  // The seed's switcher limits are TED data exposed as resource pools, not switcher fields.
  http.get(`${API}/resources/switchers`, () =>
    HttpResponse.json(db.switchers.map(({ max_inputs: _i, max_outputs: _o, ...sw }) => clone(sw))),
  ),
  http.get(`${API}/resources/pools`, () => HttpResponse.json(resourcePools())),
  http.get(`${API}/resources/topology`, () => {
    const online = (status: 'online' | 'offline') =>
      status === 'online' ? ('healthy' as const) : ('offline' as const)
    const topology: Topology = {
      nodes: [
        ...db.sources.map((s) => ({
          id: s.id,
          label: s.name,
          type: 'source' as const,
          status: online(s.status),
        })),
        // One topology node per TED VVF node (End.AN); its limits and use are in the resource pools.
        ...vvfNodes.map((n) => ({
          id: n.id,
          label: n.label,
          type: 'vvf' as const,
          vvf_id: n.vvf_id,
          function_types: constraints.vvfs.find((v) => v.id === n.vvf_id)?.function_types ?? [],
          status: 'healthy' as const,
        })),
        // Switchers are VVF nodes too (behavior select, no chain functions).
        ...db.switchers.map((sw) => ({
          id: sw.id,
          label: sw.label,
          type: 'vvf' as const,
          vvf_id: SWITCHER_VVF,
          function_types: [],
          status: 'healthy' as const,
        })),
        ...db.destinations.map((d) => ({
          id: d.id,
          label: d.name,
          type: 'destination' as const,
          status: online(d.status),
        })),
      ],
      links: [],
    }
    // Only what streams flow on: the links of the ON AIR paths, each with the streams (cells)
    // passing it, and with hot standby the feeds of STANDBY INs up to their switcher (standby).
    const byKey = new Map<string, Topology['links'][number]>()
    const link = (from: string, to: string, cellId: string, standby: boolean, latency: boolean) => {
      const key = `${from}>${to}>${standby}`
      const existing = byKey.get(key)
      if (existing) {
        existing.cell_ids = [...(existing.cell_ids ?? []), cellId]
        return
      }
      const added: Topology['links'][number] = {
        source: from,
        target: to,
        cell_ids: [cellId],
        standby,
        ...(latency ? { latency_ms: Math.round((1 + Math.random() * 3) * 10) / 10 } : {}),
      }
      byKey.set(key, added)
      topology.links.push(added)
    }
    for (const cell of db.cells.values()) {
      if (cell.execution_state !== 'active') continue
      const ids = [
        cell.source_id,
        ...switcherPathOf(cell).map((h) => h.switcher.id),
        ...cellBlocks(cell).map((b) => b.node?.id ?? b.vvfId),
        cell.destination_id,
      ]
      for (let i = 0; i < ids.length - 1; i++) link(ids[i], ids[i + 1], cell.cell_id, false, true)
    }
    for (const { cell, nodes } of standbyFeeds()) {
      const ids = [cell.source_id, ...nodes]
      for (let i = 0; i < ids.length - 1; i++) link(ids[i], ids[i + 1], cell.cell_id, true, false)
    }
    return HttpResponse.json(topology)
  }),

  // ------------------------------------------------------------ presets
  http.get(`${API}/presets`, () => HttpResponse.json([...db.presets.values()].map(presetView))),
  http.get(`${API}/presets/constraints`, () => HttpResponse.json(constraints)),
  http.post(`${API}/presets/validate`, async ({ request }) => {
    const input = await body<PresetInput>(request)
    if (!input || !Array.isArray(input.chain)) return error('INVALID_REQUEST', 'chain is required')
    return HttpResponse.json(validatePreset(input))
  }),
  http.post(`${API}/presets`, async ({ request }) => {
    const input = await body<PresetInput>(request)
    if (!input?.name || !Array.isArray(input.chain))
      return error('INVALID_REQUEST', 'name and chain are required')
    const result = validatePreset(input)
    if (!result.valid)
      return error('INVALID_CHAIN', 'Chain violates constraints', {
        details: { issues: result.issues },
      })
    db.presetSeq += 1
    const now = new Date().toISOString()
    const preset = {
      ...clone(input),
      id: `preset-${db.presetSeq}`,
      created_at: now,
      updated_at: now,
    }
    db.presets.set(preset.id, preset)
    publish({ type: 'preset_changed', action: 'created', preset_id: preset.id })
    log('info', `Preset '${preset.name}' created`)
    return HttpResponse.json(presetView(preset), { status: 201 })
  }),
  http.get(`${API}/presets/:presetId`, ({ params }) => {
    const preset = db.presets.get(String(params.presetId))
    return preset ? HttpResponse.json(presetView(preset)) : notFound('Preset')
  }),
  http.put(`${API}/presets/:presetId`, async ({ params, request }) => {
    const current = db.presets.get(String(params.presetId))
    if (!current) return notFound('Preset')
    const input = await body<PresetInput>(request)
    if (!input?.name || !Array.isArray(input.chain))
      return error('INVALID_REQUEST', 'name and chain are required')
    const cells = cellsUsing(current.id)
    if (cells.length > 0 && structureChanged(current.chain, input.chain)) {
      const cellIds = cells.map((c) => c.cell_id)
      return error(
        'PRESET_IN_USE',
        `Chain structure cannot change while applied to ${cellIds.join(', ')}`,
        { details: { cell_ids: cellIds } },
      )
    }
    const result = validatePreset(input)
    if (!result.valid)
      return error('INVALID_CHAIN', 'Chain violates constraints', {
        details: { issues: result.issues },
      })
    const previous = clone(current)
    const next = {
      ...clone(input),
      id: current.id,
      created_at: current.created_at,
      updated_at: new Date().toISOString(),
    }
    const paramsChanged = JSON.stringify(previous.chain) !== JSON.stringify(next.chain)
    const capacity = capacityIssueWith({
      chain: (c) => (c.preset_id === next.id ? next.chain : (c.live_chain ?? [])),
    })
    if (paramsChanged && capacity)
      return error('RESOURCE_EXHAUSTED', capacity.message, { details: { issues: [capacity] } })
    const applied = await applyToRouter(
      paramsChanged ? cells : [],
      () => {
        db.presets.set(next.id, next)
        cells.forEach(syncCellChain)
      },
      `preset '${next.name}'`,
      () => {
        db.presets.set(previous.id, previous)
      },
    )
    if (!applied.ok) return routerError(applied.status, applied.rollbacks)
    cells.forEach(touch)
    refreshVvf()
    cells.filter((c) => c.execution_state === 'active').forEach(injectVvfConfig)
    publish({ type: 'preset_changed', action: 'updated', preset_id: next.id })
    log('info', `Preset '${next.name}' updated`)
    return HttpResponse.json(presetView(next))
  }),
  http.delete(`${API}/presets/:presetId`, ({ params }) => {
    const id = String(params.presetId)
    const preset = db.presets.get(id)
    if (!preset) return notFound('Preset')
    const cellIds = presetCellIds(id)
    if (cellIds.length > 0)
      return error('PRESET_IN_USE', `Preset ${id} is applied to ${cellIds.join(', ')}`, {
        details: { cell_ids: cellIds },
      })
    db.presets.delete(id)
    db.layouts.delete(id)
    publish({ type: 'preset_changed', action: 'deleted', preset_id: id })
    log('info', `Preset '${preset.name}' deleted`)
    return new HttpResponse(null, { status: 204 })
  }),
  http.patch(`${API}/presets/:presetId/functions/:instanceId`, async ({ params, request }) => {
    const preset = db.presets.get(String(params.presetId))
    if (!preset) return notFound('Preset')
    const target = preset.chain.find((f) => f.instance_id === params.instanceId)
    if (!target) return notFound('Function instance')
    const input = await body<{ params?: Record<string, unknown> }>(request)
    if (!input?.params || typeof input.params !== 'object')
      return error('INVALID_REQUEST', 'params is required')
    const merged = { ...target.params, ...input.params }
    const errors = validateParams(target.type, merged)
    if (errors.length > 0) return error('INVALID_PARAMS', 'Invalid params', { details: { errors } })

    // A VVF node runs one config: the streams must still fit the nodes with the new params.
    const patched = preset.chain.map((fn) =>
      fn.instance_id === target.instance_id ? { ...fn, params: merged } : fn,
    )
    const capacity = capacityIssueWith({
      chain: (c) => (c.preset_id === preset.id ? patched : (c.live_chain ?? [])),
    })
    if (capacity)
      return error('RESOURCE_EXHAUSTED', capacity.message, { details: { issues: [capacity] } })

    const before = clone(target.params)
    const cells = cellsUsing(preset.id)
    const name = functionCatalog.find((f) => f.type === target.type)?.name ?? target.type
    const applied = await applyToRouter(
      cells,
      () => {
        target.params = merged
        preset.updated_at = new Date().toISOString()
        cells.forEach(syncCellChain)
      },
      `${name} params of '${preset.name}'`,
      () => {
        target.params = before
      },
    )
    if (!applied.ok) {
      publish({ type: 'preset_changed', action: 'updated', preset_id: preset.id })
      return routerError(applied.status, applied.rollbacks)
    }
    cells.forEach(touch)
    refreshVvf()
    // ON AIR cells get the new params at once; STANDBY cells get them at their next Take.
    cells.filter((c) => c.execution_state === 'active').forEach(injectVvfConfig)
    publish({ type: 'preset_changed', action: 'updated', preset_id: preset.id })
    return HttpResponse.json({
      instance: clone(target),
      applied_cell_ids: cells.map((c) => c.cell_id),
    })
  }),

  // ------------------------------------------------------------ functions
  http.get(`${API}/functions`, () => HttpResponse.json(functionCatalog)),
  http.get(`${API}/functions/:type/schema`, ({ params }) => {
    const item = functionCatalog.find((f) => f.type === params.type)
    return item ? HttpResponse.json(item) : notFound('Function type')
  }),
  http.post(`${API}/functions/:type/validate`, async ({ params, request }) => {
    const type = String(params.type) as FunctionType
    if (!functionCatalog.some((f) => f.type === type)) return notFound('Function type')
    const input = await body<{ params?: Record<string, unknown> }>(request)
    if (!input?.params || typeof input.params !== 'object')
      return error('INVALID_REQUEST', 'params is required')
    const errors = validateParams(type, input.params)
    return HttpResponse.json({ valid: errors.length === 0, errors })
  }),

  // ------------------------------------------------------------ views (SVoT)
  http.get(`${API}/views/presets/:presetId/layout`, ({ params }) => {
    const layout = db.layouts.get(String(params.presetId))
    return layout ? HttpResponse.json(layout) : notFound('Layout')
  }),
  http.put(`${API}/views/presets/:presetId/layout`, async ({ params, request }) => {
    const id = String(params.presetId)
    if (!db.presets.has(id)) return notFound('Preset')
    const input = await body<PresetLayout>(request)
    if (!input || !Array.isArray(input.nodes)) return error('INVALID_REQUEST', 'nodes is required')
    const layout = { ...clone(input), preset_id: id }
    db.layouts.set(id, layout)
    return HttpResponse.json(layout)
  }),

  // ------------------------------------------------------------ matrix
  http.get(`${API}/matrix`, () =>
    HttpResponse.json({ cells: [...db.cells.values()], updated_at: new Date().toISOString() }),
  ),
  http.post(`${API}/matrix/validate`, async ({ request }) => {
    const input = await body<{ cell_id?: string; preset_id?: string; release_cells?: string[] }>(
      request,
    )
    if (!input?.cell_id || !input.preset_id)
      return error('INVALID_REQUEST', 'cell_id and preset_id are required')
    const cell = findCell(input.cell_id)
    const preset = db.presets.get(input.preset_id)
    if (!cell) return notFound('Cell')
    if (!preset) return notFound('Preset')
    const release = input.release_cells ?? []
    const bad = releaseError(cell, release)
    if (bad) return bad
    return HttpResponse.json(validateAssignment(cell, preset, release))
  }),
  http.get(`${API}/matrix/cells/:cellId`, ({ params }) => {
    const cell = findCell(params.cellId)
    return cell ? HttpResponse.json(cell) : notFound('Cell')
  }),
  http.post(`${API}/matrix/cells/:cellId/take`, async ({ params }) => {
    const cell = findCell(params.cellId)
    if (!cell) return notFound('Cell')
    if (cell.execution_state !== 'standby')
      return error('INVALID_CELL_STATE', `${cell.cell_id} is not in standby state`)
    // Not enough resources (IN unicast, switcher output, VVF nodes): refused before the routers.
    const issues = takeIssuesOf(cell)
    if (issues.length > 0)
      return error('RESOURCE_EXHAUSTED', issues[0].message, { details: { issues } })
    const previous = [...db.cells.values()].find(
      (c) => c.destination_id === cell.destination_id && c.execution_state === 'active',
    )
    const applied = await applyToRouter([cell], () => undefined, 'take')
    if (!applied.ok) {
      cell.execution_state = 'standby'
      return routerError(applied.status, applied.rollbacks)
    }
    cell.execution_state = 'active'
    touch(cell)
    if (previous) {
      previous.execution_state = 'standby'
      touch(previous)
    }
    // Reallocate with the taken stream preferring nodes that do not run the ON AIR config, then
    // inject the preset's config into its VVF nodes before the switch (the picture and the VVF
    // operation change together). The headend policy and the switchers' active inputs follow.
    refreshVvf({ fresh: new Set([cell.destination_id]) })
    const configuredNodes = injectVvfConfig(cell)
    if (configuredNodes.length > 0)
      log(
        'info',
        `${cell.cell_id}: VVF config applied to ${configuredNodes.join(', ')} before switching`,
        cell.cell_id,
      )
    const switched = setSwitcherFor(cell, true)
    if (switched.length > 0) publish({ type: 'switcher_update', switchers: switched })
    // Cold standby: the stream stepping down is withdrawn after the switch.
    if (!hotStandby()) refreshVvf()
    const message = previous
      ? `Take executed: ${cell.cell_id} is now ACTIVE, ${previous.cell_id} demoted to STANDBY`
      : `Take executed: ${cell.cell_id} is now ACTIVE`
    log('info', message, cell.cell_id)
    return HttpResponse.json({
      activated_cell: cell.cell_id,
      deactivated_cell: previous?.cell_id ?? null,
      switchers: switched,
      message,
    })
  }),
  http.post(`${API}/matrix/cells/:cellId/standby`, async ({ params }) => {
    const cell = findCell(params.cellId)
    if (!cell) return notFound('Cell')
    if (cell.execution_state !== 'active')
      return error('INVALID_CELL_STATE', `${cell.cell_id} is not on air`)
    const applied = await applyToRouter([cell], () => undefined, 'standby')
    if (!applied.ok) return routerError(applied.status, applied.rollbacks)
    cell.execution_state = 'standby'
    touch(cell)
    const stopped = setSwitcherFor(cell, false)
    if (stopped.length > 0) publish({ type: 'switcher_update', switchers: stopped })
    refreshVvf()
    log('info', `${cell.cell_id} stopped output and is now STANDBY`, cell.cell_id)
    return HttpResponse.json(cell)
  }),
  http.put(`${API}/matrix/cells/:cellId/preset`, async ({ params, request }) => {
    const cell = findCell(params.cellId)
    if (!cell) return notFound('Cell')
    const input = await body<{ preset_id?: string; release_cells?: string[] }>(request)
    if (!input?.preset_id) return error('INVALID_REQUEST', 'preset_id is required')
    const preset = db.presets.get(input.preset_id)
    if (!preset) return notFound('Preset')
    if (cell.execution_state === 'disconnected')
      return error('INVALID_CELL_STATE', `${cell.cell_id} is disconnected`)
    const release = input.release_cells ?? []
    const bad = releaseError(cell, release)
    if (bad) return bad
    const validation = validateAssignment(cell, preset, release)
    if (!validation.feasible)
      return error('INFEASIBLE_ASSIGNMENT', validation.reason ?? 'Infeasible', {
        details: validation,
      })
    const wasOff = cell.execution_state === 'off'
    const released = release
      .map((id) => db.cells.get(id)!)
      .filter((c) => c.execution_state === 'standby')
    const applied = await applyToRouter(
      [cell],
      () => {
        cell.preset_id = preset.id
        syncCellChain(cell)
        if (cell.execution_state === 'off') cell.execution_state = 'standby'
      },
      `preset '${preset.name}'`,
    )
    if (!applied.ok) {
      if (wasOff) {
        cell.execution_state = 'off'
        cell.health_state = undefined
      }
      return routerError(applied.status, applied.rollbacks)
    }
    for (const c of released) {
      turnOff(c)
      log('info', `${c.cell_id}: released (OFF) for ${cell.cell_id}`, c.cell_id)
    }
    touch(cell)
    refreshVvf()
    log('info', `${cell.cell_id}: preset '${preset.name}' applied`, cell.cell_id)
    return HttpResponse.json(cell)
  }),
  http.delete(`${API}/matrix/cells/:cellId/preset`, ({ params }) => {
    const cell = findCell(params.cellId)
    if (!cell) return notFound('Cell')
    if (cell.execution_state === 'off' || cell.execution_state === 'disconnected')
      return HttpResponse.json(cell)
    if (cell.execution_state === 'active') {
      const stopped = setSwitcherFor(cell, false)
      if (stopped.length > 0) publish({ type: 'switcher_update', switchers: stopped })
    }
    turnOff(cell)
    refreshVvf()
    log('info', `${cell.cell_id}: disconnected (OFF)`, cell.cell_id)
    return HttpResponse.json(cell)
  }),
  http.get(`${API}/matrix/cells/:cellId/path`, ({ params }) => {
    const cell = findCell(params.cellId)
    if (!cell) return notFound('Cell')
    if (cell.execution_state === 'off' || cell.execution_state === 'disconnected')
      return error('INVALID_CELL_STATE', `${cell.cell_id} has no active path`)
    return HttpResponse.json(buildPath(cell))
  }),

  // ------------------------------------------------------------ events (SSE)
  sse<{ [K in SseEventType]: string }>(`${API}/events`, ({ client, request }) => {
    const unsubscribe = subscribe((frame) => {
      client.send({ id: frame.id, event: frame.event, data: frame.data })
    }, request.headers.get('last-event-id'))
    request.signal.addEventListener('abort', unsubscribe)
  }),

  // ------------------------------------------------------------ system
  http.get(`${API}/config`, () => HttpResponse.json(systemConfig())),
  http.patch(`${API}/config`, async ({ request }) => {
    const patch = await body<DynamicConfigPatch>(request)
    if (!patch || Object.keys(patch).length === 0)
      return error('INVALID_REQUEST', 'At least one property is required')
    const errors = [
      patch.sse_interval_ms !== undefined &&
        patch.sse_interval_ms < 100 && { field: 'sse_interval_ms', message: 'Must be 100 or more' },
      patch.metrics_dump_interval_s !== undefined &&
        patch.metrics_dump_interval_s < 10 && {
          field: 'metrics_dump_interval_s',
          message: 'Must be 10 or more',
        },
      patch.assurance_interval_s !== undefined &&
        patch.assurance_interval_s < 0 && {
          field: 'assurance_interval_s',
          message: 'Must be 0 or more',
        },
      patch.drift_action !== undefined &&
        !['repair', 'report'].includes(patch.drift_action) && {
          field: 'drift_action',
          message: 'Must be repair or report',
        },
    ].filter(Boolean)
    if (errors.length > 0) return error('INVALID_PARAMS', 'Invalid config', { details: { errors } })
    updateConfig({ ...db.config, ...patch })
    publish({ type: 'config_changed', config: clone(db.config) })
    log('info', 'System config updated')
    return HttpResponse.json(systemConfig())
  }),
  http.get(`${API}/logs`, ({ request }) => {
    const url = new URL(request.url)
    const level = (url.searchParams.get('level') ?? undefined) as LogLevel | undefined
    const cellId = url.searchParams.get('cell_id') ?? undefined
    const limit = Math.min(Number(url.searchParams.get('limit') ?? 100) || 100, 1000)
    return HttpResponse.json(queryLogs(level, cellId, limit))
  }),
  http.get(`${API}/healthz`, () =>
    HttpResponse.json({
      status: 'ok',
      version: '0.2.0-mock',
      uptime_s: Math.round((Date.now() - startedAt) / 1000),
    }),
  ),
]

// ---------------------------------------------------------------- telemetry loop

let telemetryTimer: ReturnType<typeof setTimeout> | undefined

export function startTelemetry(): void {
  const tick = () => {
    const cells = [...db.cells.values()].filter(
      (c) => c.execution_state === 'active' || c.execution_state === 'standby',
    )
    const changed: MatrixCell[] = []
    for (const cell of cells) {
      cell.telemetry = simulateTelemetry(cell)
      const next = healthOf(cell)
      if (next !== cell.health_state) {
        cell.health_state = next
        changed.push(cell)
      }
    }
    publish({
      type: 'telemetry_update',
      cells: cells.map((c) => ({
        cell_id: c.cell_id,
        health_state: c.health_state ?? 'healthy',
        telemetry: c.telemetry!,
      })),
    })
    changed.forEach((cell) => {
      touch(cell)
      if (cell.health_state === 'warning')
        log('warn', `${cell.cell_id}: Intent drift detected`, cell.cell_id)
    })
    telemetryTimer = setTimeout(tick, db.config.sse_interval_ms)
  }
  clearTimeout(telemetryTimer)
  tick()
}

// ---------------------------------------------------------------- path

function buildPath(cell: MatrixCell): CellPath {
  const blocks = cellBlocks(cell)
  const swHops = switcherPathOf(cell)
  const nodes: CellPath['nodes'] = [
    {
      node_id: cell.source_id,
      role: 'headend',
      label: `${cell.source_id.toUpperCase().replace('-', ' ')} (Headend)`,
      router_id: 'rtr-01',
      sid: null,
      health_state: 'healthy',
    },
    // Switchers on the way, upstream first (role vvf, switcher info in its own field).
    ...swHops.map(({ switcher, port }) => ({
      node_id: switcher.id,
      vvf_id: null,
      role: 'vvf' as const,
      label: switcher.label,
      router_id: switcher.router_id,
      sid: switcher.sid ?? null,
      processing_latency_ms: 0.2,
      switcher: { switcher_id: switcher.id, input_port: port },
      health_state: 'healthy' as const,
    })),
    ...blocks.map((block) => ({
      node_id: block.node?.id ?? block.vvfId,
      vvf_id: block.vvfId,
      role: 'vvf' as const,
      label: block.node?.label ?? block.name,
      unassigned: !block.node,
      instance_ids: block.instances.map((fn) => fn.instance_id),
      router_id: block.node?.router_id,
      sid: block.node?.sid ?? null,
      processing_latency_ms: block.instances.reduce(
        (sum, fn) => sum + processingLatencyMs[fn.type],
        0,
      ),
      health_state: cell.health_state,
    })),
    {
      node_id: cell.destination_id,
      role: 'egress',
      label: `${cell.destination_id.toUpperCase().replace('-', ' ')} (Egress)`,
      router_id: 'rtr-04',
      sid: 'fc00:1::200',
      health_state: 'healthy',
    },
  ]
  const e2e = cell.telemetry?.e2e_latency_ms ?? 0
  const processing = nodes.reduce((sum, n) => sum + (n.processing_latency_ms ?? 0), 0)
  const perHop = Math.max(e2e - processing, 0) / (nodes.length - 1)
  const hops: CellPath['hops'] = nodes.slice(0, -1).map((n, i) => ({
    from: n.node_id,
    to: nodes[i + 1].node_id,
    latency_ms: Math.round(perHop * (0.7 + Math.random() * 0.6) * 10) / 10,
    packet_loss_rate: cell.telemetry?.packet_loss_rate ?? 0,
    bandwidth_utilization: Math.round((0.3 + Math.random() * 0.4) * 100) / 100,
  }))
  const worst = hops.reduce((a, b) => ((b.latency_ms ?? 0) > (a.latency_ms ?? 0) ? b : a), hops[0])
  return {
    cell_id: cell.cell_id,
    e2e_latency_ms: e2e,
    nodes,
    hops,
    segment_list: [
      ...swHops.flatMap((h) => (h.switcher.sid ? [h.switcher.sid] : [])),
      ...blocks.map((b) => b.node?.sid ?? ''),
      'fc00:1::200',
    ],
    bottleneck:
      cell.health_state === 'healthy' || !worst
        ? null
        : { kind: 'hop', ref: `${worst.from}->${worst.to}` },
  }
}
