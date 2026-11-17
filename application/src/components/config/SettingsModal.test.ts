import { describe, expect, it } from 'vitest'
import type { DynamicConfig } from '../../lib/types'
import { diffConfig, toForm } from './SettingsModal'

const base: DynamicConfig = {
  sse_interval_ms: 1000,
  metrics_dump_interval_s: 30,
  log_level: 'info',
  assurance_interval_s: 30,
  drift_action: 'repair',
}

describe('diffConfig', () => {
  it('is empty when nothing changed', () => {
    expect(diffConfig(base, toForm(base))).toEqual({})
  })
  it('sends only changed fields', () => {
    const form = { ...toForm(base), sse_interval_ms: '500', log_level: 'warn' as const }
    expect(diffConfig(base, form)).toEqual({ sse_interval_ms: 500, log_level: 'warn' })
  })
  it('sends the assurance settings', () => {
    const form = { ...toForm(base), assurance_interval_s: '0', drift_action: 'report' as const }
    expect(diffConfig(base, form)).toEqual({ assurance_interval_s: 0, drift_action: 'report' })
  })
  it('rejects non-numbers', () => {
    expect(diffConfig(base, { ...toForm(base), metrics_dump_interval_s: '' })).toBeNull()
  })
})
