import { describe, expect, it } from 'vitest'
import { translate, type Translate } from '../i18n'
import { ApiError } from './api'
import { describeError, isNotImplemented } from './errors'
import type { RollbackInfo } from './types'

const t: Translate = (key, vars) => translate('en', key, vars)
const rollback = (cell: string, result: RollbackInfo['result']) =>
  ({ cell_id: cell, result }) as RollbackInfo
const routerError = (rollbacks: RollbackInfo[]) =>
  new ApiError(502, { code: 'ROUTER_ERROR', message: 'Router returned an error', rollbacks })

describe('describeError for router failures', () => {
  it('says the last known good config was restored when every rollback succeeded', () => {
    expect(describeError(t, routerError([rollback('IN1-OUT1', 'succeeded')])).title).toBe(
      'The router returned an error. The last known good config was restored.',
    )
  })
  it('names the cells whose rollback failed', () => {
    expect(
      describeError(
        t,
        routerError([rollback('IN1-OUT1', 'succeeded'), rollback('IN1-OUT2', 'failed')]),
      ).title,
    ).toBe(
      'The router returned an error. Rollback to the last known good config failed (IN1-OUT2).',
    )
  })
  it('does not claim a rollback without one', () => {
    expect(describeError(t, routerError([])).title).toBe('The router returned an error.')
  })
})

describe('describeError for operations the IEL does not implement', () => {
  it('uses the NOT_IMPLEMENTED text and keeps the IEL message', () => {
    const e = new ApiError(501, {
      code: 'NOT_IMPLEMENTED',
      message: 'Preset editing is not implemented yet',
    })
    expect(isNotImplemented(e)).toBe(true)
    expect(describeError(t, e)).toEqual({
      title: 'The IEL does not support this operation yet.',
      detail: 'Preset editing is not implemented yet',
    })
  })
  it('treats a bare 501 the same way', () => {
    const e = new ApiError(501, null)
    expect(isNotImplemented(e)).toBe(true)
    expect(describeError(t, e).title).toBe('The IEL does not support this operation yet.')
  })
  it('does not flag other errors', () => {
    expect(isNotImplemented(new ApiError(409, { code: 'PRESET_IN_USE', message: 'x' }))).toBe(false)
  })
})
