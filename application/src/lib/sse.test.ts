import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useConnectionStore } from '../stores/connection'
import { useLogsStore } from '../stores/logs'
import { useMatrixStore } from '../stores/matrix'
import { usePresetsStore } from '../stores/presets'
import { dispatchSseEvent, startLiveSync, type Snapshot } from './sse'
import type { MatrixCell, SseEvent } from './types'

/** Minimal controllable EventSource for tests. */
class FakeEventSource {
  static CONNECTING = 0
  static OPEN = 1
  static CLOSED = 2
  readyState = FakeEventSource.CONNECTING
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  listeners = new Map<string, (e: MessageEvent<string>) => void>()
  closed = false
  addEventListener(type: string, fn: (e: MessageEvent<string>) => void) {
    this.listeners.set(type, fn)
  }
  close() {
    this.closed = true
    this.readyState = FakeEventSource.CLOSED
  }
  open() {
    this.readyState = FakeEventSource.OPEN
    this.onopen?.()
  }
  emit(event: SseEvent) {
    this.listeners.get(event.type)?.(new MessageEvent(event.type, { data: JSON.stringify(event) }))
  }
  fail(closed = false) {
    this.readyState = closed ? FakeEventSource.CLOSED : FakeEventSource.CONNECTING
    this.onerror?.()
  }
}

const cell = (id: string, extra: Partial<MatrixCell> = {}): MatrixCell => ({
  cell_id: id,
  source_id: 'in-1',
  destination_id: 'out-1',
  execution_state: 'standby',
  health_state: 'healthy',
  ...extra,
})

const snapshot = (cells: MatrixCell[]): Snapshot => ({
  sources: [],
  destinations: [],
  cells,
  presets: [],
  catalog: [],
  constraints: {
    topology: 'linear',
    allow_loops: false,
    min_functions: 0,
    max_functions: 4,
    vvfs: [],
    domain: {
      source_format: { video_format: '8k_dg' },
      destination_format: { video_format: '8k_full' },
    },
  },
  logs: [],
  config: {
    sse_interval_ms: 1000,
    metrics_dump_interval_s: 30,
    default_sla: { max_latency_ms: 20, priority: 'normal' },
    log_level: 'info',
    assurance_interval_s: 30,
    drift_action: 'repair',
  },
  switchers: [],
})

const flush = () => new Promise((r) => setTimeout(r, 0))

beforeEach(() => {
  vi.stubGlobal('EventSource', FakeEventSource)
  useMatrixStore.setState({ cells: {}, loaded: false })
  useLogsStore.setState({ entries: [] })
  usePresetsStore.setState({ presets: {} })
  useConnectionStore.setState({ status: 'connecting', bootstrap: 'loading', lastEventAt: null })
})
afterEach(() => vi.useRealTimers())

describe('dispatchSseEvent', () => {
  it('routes matrix, telemetry, log, and preset events', async () => {
    useMatrixStore.getState().setCells([cell('IN1-OUT1')])
    await dispatchSseEvent({
      type: 'matrix_update',
      cell: cell('IN1-OUT1', { execution_state: 'active' }),
    })
    await dispatchSseEvent({
      type: 'telemetry_update',
      cells: [
        {
          cell_id: 'IN1-OUT1',
          health_state: 'warning',
          telemetry: { e2e_latency_ms: 25, jitter_ms: 1, packet_loss_rate: 0 },
        },
      ],
    })
    await dispatchSseEvent({
      type: 'log',
      log: { id: 'l1', timestamp: '', level: 'warn', message: 'drift' },
    })
    const fetchPreset = vi.fn().mockResolvedValue({ id: 'p1', name: 'A', chain: [] })
    await dispatchSseEvent(
      { type: 'preset_changed', action: 'created', preset_id: 'p1' },
      fetchPreset,
    )
    await dispatchSseEvent(
      { type: 'preset_changed', action: 'deleted', preset_id: 'p1' },
      fetchPreset,
    )

    expect(useMatrixStore.getState().cells['IN1-OUT1']).toMatchObject({
      execution_state: 'active',
      health_state: 'warning',
    })
    expect(useLogsStore.getState().entries[0].id).toBe('l1')
    expect(fetchPreset).toHaveBeenCalledWith('p1')
    expect(usePresetsStore.getState().presets).toEqual({})
  })

  it('reloads what the TED determines on topology_changed', async () => {
    const reload = vi.fn().mockResolvedValue(undefined)
    await dispatchSseEvent(
      { type: 'topology_changed', added: [], removed: ['color-transcode-crop-fd77-ca'] },
      undefined,
      reload,
    )
    expect(reload).toHaveBeenCalledOnce()
  })
})

describe('startLiveSync', () => {
  it('buffers events received while the snapshot loads and replays them afterwards', async () => {
    let es!: FakeEventSource
    let resolveSnapshot!: (s: Snapshot) => void
    const stop = startLiveSync({
      createEventSource: () => (es = new FakeEventSource()) as unknown as EventSource,
      loadSnapshot: () => new Promise((r) => (resolveSnapshot = r)),
    })
    es.open()
    // Arrives before the snapshot: must not be overwritten by the older snapshot.
    es.emit({ type: 'matrix_update', cell: cell('IN1-OUT1', { execution_state: 'active' }) })
    resolveSnapshot(snapshot([cell('IN1-OUT1')]))
    await flush()
    await flush()
    expect(useMatrixStore.getState().cells['IN1-OUT1'].execution_state).toBe('active')
    expect(useConnectionStore.getState()).toMatchObject({ status: 'connected', bootstrap: 'ready' })
    stop()
    expect(es.closed).toBe(true)
  })

  it('reports reconnecting and resyncs the snapshot after the stream reopens', async () => {
    let es!: FakeEventSource
    const load = vi.fn().mockResolvedValue(snapshot([cell('IN1-OUT1')]))
    const stop = startLiveSync({
      createEventSource: () => (es = new FakeEventSource()) as unknown as EventSource,
      loadSnapshot: load,
    })
    es.open()
    await flush()
    es.fail()
    expect(useConnectionStore.getState().status).toBe('reconnecting')
    es.open()
    await flush()
    expect(load).toHaveBeenCalledTimes(2)
    expect(useConnectionStore.getState().status).toBe('connected')
    stop()
  })

  it('marks the stream disconnected when no event arrives for too long', async () => {
    vi.useFakeTimers()
    let created = 0
    let es!: FakeEventSource
    const stop = startLiveSync({
      createEventSource: () => {
        created++
        return (es = new FakeEventSource()) as unknown as EventSource
      },
      loadSnapshot: () => Promise.resolve(snapshot([])),
    })
    es.open()
    await vi.advanceTimersByTimeAsync(25_000)
    expect(created).toBe(2)
    expect(useConnectionStore.getState().status).toBe('disconnected')
    stop()
  })

  it('marks bootstrap failed when the first snapshot cannot be loaded', async () => {
    const stop = startLiveSync({
      createEventSource: () => new FakeEventSource() as unknown as EventSource,
      loadSnapshot: () => Promise.reject(new Error('down')),
    })
    await flush()
    expect(useConnectionStore.getState().bootstrap).toBe('failed')
    stop()
  })
})
