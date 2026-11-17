import { useConfigStore } from '../stores/config'
import { useConnectionStore } from '../stores/connection'
import { useLogsStore } from '../stores/logs'
import { useMatrixStore } from '../stores/matrix'
import { usePresetsStore } from '../stores/presets'
import { useResourcesStore } from '../stores/resources'
import { useSwitchersStore } from '../stores/switchers'
import { api, request } from './api'
import type {
  ChainConstraints,
  DestinationNode,
  DynamicConfig,
  FunctionCatalogItem,
  LogEntry,
  MatrixCell,
  Preset,
  SourceNode,
  SseEvent,
  SseEventType,
  SwitcherNode,
} from './types'

// Live sync with the IEL: initial REST snapshot + SSE (`GET /api/v1/events`)

export const SSE_URL = '/api/v1/events'
const EVENT_TYPES: SseEventType[] = [
  'matrix_update',
  'telemetry_update',
  'rollback',
  'preset_changed',
  'config_changed',
  'switcher_update',
  'topology_changed',
  'log',
  'heartbeat',
]
/** The IEL sends a heartbeat after 15 s of silence; treat 20 s without any event as a dead stream. */
export const STALE_AFTER_MS = 20_000
const WATCHDOG_INTERVAL_MS = 5_000
const RETRY_AFTER_MS = 5_000

export type Snapshot = {
  sources: SourceNode[]
  destinations: DestinationNode[]
  cells: MatrixCell[]
  presets: Preset[]
  catalog: FunctionCatalogItem[]
  constraints: ChainConstraints
  logs: LogEntry[]
  config: DynamicConfig
  switchers: SwitcherNode[]
}

/** Loads everything the UI needs in parallel (REST). */
export async function loadSnapshot(): Promise<Snapshot> {
  const [sources, destinations, matrix, presets, catalog, constraints, logs, config, switchers] =
    await Promise.all([
      request(api.GET('/resources/sources')),
      request(api.GET('/resources/destinations')),
      request(api.GET('/matrix')),
      request(api.GET('/presets')),
      request(api.GET('/functions')),
      request(api.GET('/presets/constraints')),
      request(api.GET('/logs', { params: { query: { limit: 200 } } })),
      request(api.GET('/config')),
      request(api.GET('/resources/switchers')),
    ])
  return {
    sources,
    destinations,
    cells: matrix.cells,
    presets,
    catalog,
    constraints,
    logs,
    config: config.dynamic,
    switchers,
  }
}

export function applySnapshot(s: Snapshot): void {
  useResourcesStore.getState().setResources(s.sources, s.destinations)
  useMatrixStore.getState().setCells(s.cells)
  usePresetsStore.getState().setPresets(s.presets)
  usePresetsStore.getState().setCatalog(s.catalog, s.constraints)
  useLogsStore.getState().setEntries(s.logs)
  useConfigStore.getState().setDynamic(s.config)
  useSwitchersStore.getState().setSwitchers(s.switchers)
}

/** Reloads what the TED determines (INs / OUTs, switchers, catalog with pinned VVF nodes). */
export async function reloadTopology(): Promise<void> {
  const [sources, destinations, switchers, catalog, constraints] = await Promise.all([
    request(api.GET('/resources/sources')),
    request(api.GET('/resources/destinations')),
    request(api.GET('/resources/switchers')),
    request(api.GET('/functions')),
    request(api.GET('/presets/constraints')),
  ])
  useResourcesStore.getState().setResources(sources, destinations)
  useSwitchersStore.getState().setSwitchers(switchers)
  usePresetsStore.getState().setCatalog(catalog, constraints)
}

/** Routes one SSE event to the stores. */
export async function dispatchSseEvent(
  event: SseEvent,
  fetchPreset: (id: string) => Promise<Preset> = (id) =>
    request(api.GET('/presets/{presetId}', { params: { path: { presetId: id } } })),
  reload: () => Promise<void> = reloadTopology,
): Promise<void> {
  switch (event.type) {
    case 'matrix_update':
      useMatrixStore.getState().upsertCell(event.cell)
      break
    case 'telemetry_update':
      useMatrixStore.getState().applyTelemetry(event.cells)
      break
    case 'preset_changed':
      if (event.action === 'deleted') usePresetsStore.getState().removePreset(event.preset_id)
      else usePresetsStore.getState().upsertPreset(await fetchPreset(event.preset_id))
      break
    case 'config_changed':
      useConfigStore.getState().setDynamic(event.config)
      break
    case 'switcher_update':
      useSwitchersStore.getState().applyStates(event.switchers)
      break
    case 'topology_changed':
      // The TED changed (BGP-LS): the cells come with matrix_update, the rest is reloaded.
      await reload()
      break
    case 'log':
      useLogsStore.getState().addEntry(event.log)
      break
    case 'rollback':
      // The cell's last_rollback arrives with matrix_update and the text with `log`; nothing extra to store.
      break
    case 'heartbeat':
      break
  }
}

export type LiveSyncOptions = {
  createEventSource?: (url: string) => EventSource
  loadSnapshot?: () => Promise<Snapshot>
  dispatch?: (event: SseEvent) => Promise<void>
}

/**
 * Starts live sync and returns a stop function (call it from a React effect cleanup).
 *
 * Order matters: the EventSource is opened first and its events are buffered while the snapshot loads,
 * then the buffer is replayed on top of the snapshot so nothing between the two is lost.
 * After a reconnect the snapshot is reloaded the same way (the IEL may not replay everything).
 */
export function startLiveSync(options: LiveSyncOptions = {}): () => void {
  const createEventSource = options.createEventSource ?? ((url) => new EventSource(url))
  const load = options.loadSnapshot ?? loadSnapshot
  const dispatch = options.dispatch ?? ((event) => dispatchSseEvent(event))
  const connection = useConnectionStore.getState()

  let source: EventSource | null = null
  let buffer: SseEvent[] | null = null
  let hadError = false
  let stopped = false
  let retryTimer: ReturnType<typeof setTimeout> | undefined
  // Dispatch sequentially so async handlers (preset fetch) keep event order.
  let queue = Promise.resolve()

  const enqueue = (event: SseEvent) => {
    queue = queue.then(() => dispatch(event)).catch((err) => console.error('[sse] dispatch', err))
  }

  const resync = async () => {
    buffer = []
    try {
      applySnapshot(await load())
      connection.setBootstrap('ready')
    } catch (err) {
      console.error('[sse] snapshot', err)
      if (useConnectionStore.getState().bootstrap !== 'ready') connection.setBootstrap('failed')
    } finally {
      const pending = buffer ?? []
      buffer = null
      pending.forEach(enqueue)
    }
  }

  const onMessage = (msg: MessageEvent<string>) => {
    useConnectionStore.getState().markEvent()
    let event: SseEvent
    try {
      event = JSON.parse(msg.data) as SseEvent
    } catch {
      console.error('[sse] invalid event', msg.data)
      return
    }
    if (buffer) buffer.push(event)
    else enqueue(event)
  }

  const open = () => {
    if (stopped) return
    source = createEventSource(SSE_URL)
    EVENT_TYPES.forEach((type) => source!.addEventListener(type, onMessage as EventListener))
    source.onopen = () => {
      useConnectionStore.getState().markEvent()
      connection.setStatus('connected')
      if (hadError) {
        hadError = false
        void resync()
      }
    }
    source.onerror = () => {
      hadError = true
      if (source?.readyState === EventSource.CLOSED) {
        // The browser gave up; retry ourselves.
        connection.setStatus('disconnected')
        source.close()
        retryTimer = setTimeout(open, RETRY_AFTER_MS)
      } else {
        // EventSource reconnects automatically and sends Last-Event-ID.
        connection.setStatus('reconnecting')
      }
    }
  }

  // Watchdog: no event (not even a heartbeat) for too long means the stream is dead.
  const watchdog = setInterval(() => {
    const { status, lastEventAt } = useConnectionStore.getState()
    if (status === 'connected' && lastEventAt && Date.now() - lastEventAt > STALE_AFTER_MS) {
      connection.setStatus('disconnected')
      hadError = true
      source?.close()
      open()
    }
  }, WATCHDOG_INTERVAL_MS)

  connection.setStatus('connecting')
  connection.setBootstrap('loading')
  open()
  void resync()

  return () => {
    stopped = true
    clearInterval(watchdog)
    clearTimeout(retryTimer)
    source?.close()
  }
}
