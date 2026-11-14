import { describe, expect, it } from 'vitest'
import { releasableCells } from './useApplyPreset'

describe('releasableCells', () => {
  it('offers the STANDBY cells only when every issue is solved by a release', () => {
    const shortage = {
      code: 'RESOURCE_EXHAUSTED',
      message: 'in-1 is already sent to out-1',
      resource_kind: 'source_unicast',
      releasable_cells: ['IN1-OUT1'],
    }
    expect(releasableCells([shortage])).toEqual(['IN1-OUT1'])
    expect(
      releasableCells([shortage, { ...shortage, releasable_cells: ['IN1-OUT1', 'IN2-OUT1'] }]),
    ).toEqual(['IN1-OUT1', 'IN2-OUT1'])
    // A format mismatch cannot be released away; nor can a shortage the IEL found no release for.
    expect(releasableCells([shortage, { code: 'FORMAT_MISMATCH', message: 'x' }])).toEqual([])
    expect(releasableCells([{ ...shortage, releasable_cells: undefined }])).toEqual([])
    expect(releasableCells([])).toEqual([])
  })
})
