import { beforeEach, describe, expect, it } from 'vitest'
import type { LogEntry, MatrixCell } from '../lib/types'
import { MAX_LOGS, useLogsStore } from './logs'
import { useMatrixStore } from './matrix'
import { usePresetsStore } from './presets'

const cell = (id: string, extra: Partial<MatrixCell> = {}): MatrixCell => ({
  cell_id: id,
  source_id: 'in-1',
  destination_id: 'out-1',
  execution_state: 'standby',
  health_state: 'healthy',
  ...extra,
})
const log = (id: string): LogEntry => ({ id, timestamp: '', level: 'info', message: id })

beforeEach(() => {
  useMatrixStore.setState({ cells: {}, loaded: false })
  useLogsStore.setState({ entries: [] })
  usePresetsStore.setState({ presets: {} })
})

describe('matrix store', () => {
  it('keys cells by id and replaces a cell on upsert', () => {
    const { setCells, upsertCell } = useMatrixStore.getState()
    setCells([cell('IN1-OUT1'), cell('IN2-OUT1')])
    upsertCell(cell('IN1-OUT1', { execution_state: 'active' }))
    expect(useMatrixStore.getState().cells['IN1-OUT1'].execution_state).toBe('active')
    expect(useMatrixStore.getState().loaded).toBe(true)
  })

  it('applies telemetry to known cells only and keeps other cells identical', () => {
    const { setCells, applyTelemetry } = useMatrixStore.getState()
    setCells([cell('IN1-OUT1'), cell('IN2-OUT1')])
    const untouched = useMatrixStore.getState().cells['IN2-OUT1']
    const telemetry = { e2e_latency_ms: 30, jitter_ms: 1, packet_loss_rate: 0 }
    applyTelemetry([
      { cell_id: 'IN1-OUT1', health_state: 'warning', telemetry },
      { cell_id: 'IN9-OUT9', health_state: 'critical', telemetry },
    ])
    const { cells } = useMatrixStore.getState()
    expect(cells['IN1-OUT1']).toMatchObject({ health_state: 'warning', telemetry })
    expect(cells['IN9-OUT9']).toBeUndefined()
    expect(cells['IN2-OUT1']).toBe(untouched)
  })
})

describe('logs store', () => {
  it('keeps newest first, ignores replayed ids, and is bounded', () => {
    const { addEntry } = useLogsStore.getState()
    addEntry(log('a'))
    addEntry(log('b'))
    addEntry(log('a'))
    expect(useLogsStore.getState().entries.map((e) => e.id)).toEqual(['b', 'a'])
    for (let i = 0; i < MAX_LOGS + 10; i++) addEntry(log(`x${i}`))
    expect(useLogsStore.getState().entries).toHaveLength(MAX_LOGS)
  })
})

describe('presets store', () => {
  it('removes a preset by id', () => {
    const { setPresets, removePreset } = usePresetsStore.getState()
    setPresets([
      { id: 'p1', name: 'A', chain: [] },
      { id: 'p2', name: 'B', chain: [] },
    ])
    removePreset('p1')
    expect(Object.keys(usePresetsStore.getState().presets)).toEqual(['p2'])
  })
})
