import { describe, expect, it } from 'vitest'
import { translate, type Translate } from '../i18n'
import { ApiError, request } from './api'
import { describeError } from './errors'

const t: Translate = (key, vars) => translate('en', key, vars)
const response = (status: number) => new Response(null, { status })

describe('request', () => {
  it('returns data on success', async () => {
    await expect(request(Promise.resolve({ data: 42, response: response(200) }))).resolves.toBe(42)
  })

  it('throws ApiError with the IEL error body', async () => {
    const error = {
      code: 'PRESET_IN_USE',
      message: 'Preset preset-1 is applied to IN1-OUT1',
      details: { cell_ids: ['IN1-OUT1'] },
    }
    const thrown = await request(Promise.resolve({ error, response: response(409) })).catch(
      (e) => e,
    )
    expect(thrown).toBeInstanceOf(ApiError)
    expect(thrown).toMatchObject({ status: 409, code: 'PRESET_IN_USE' })
    expect(describeError(t, thrown).title).toBe('The preset is applied to cells: IN1-OUT1.')
  })

  it('maps fetch failures to a network ApiError', async () => {
    const thrown = await request(Promise.reject(new TypeError('Failed to fetch'))).catch((e) => e)
    expect(thrown).toMatchObject({ status: 0, isNetworkError: true })
    expect(describeError(t, thrown).title).toBe('Cannot reach the IEL. Check the connection.')
  })

  it('keeps rollbacks from router errors', async () => {
    const error = { code: 'ROUTER_ERROR', message: 'x', rollbacks: [{ cell_id: 'IN1-OUT1' }] }
    const thrown = await request(Promise.resolve({ error, response: response(502) })).catch(
      (e: unknown) => e,
    )
    expect(thrown).toBeInstanceOf(ApiError)
    expect((thrown as ApiError).rollbacks).toHaveLength(1)
  })
})
