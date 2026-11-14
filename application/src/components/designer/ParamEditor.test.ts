import { describe, expect, it } from 'vitest'
import { numericParams } from './ParamEditor'

describe('numericParams', () => {
  it('keeps numeric properties with a finite range, in schema order', () => {
    const schema = {
      properties: {
        r: { type: 'integer', minimum: -255, maximum: 255 },
        text: { type: 'string' },
        open: { type: 'number', minimum: 0 },
        g: { type: 'number', minimum: 0, maximum: 1 },
      },
    }
    expect(numericParams(schema).map(([k]) => k)).toEqual(['r', 'g'])
  })
  it('handles a missing schema', () => {
    expect(numericParams(undefined)).toEqual([])
  })
})
