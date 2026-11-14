import { describe, expect, it } from 'vitest'
import { translate } from '.'
import { en, type MessageKey } from './en'
import { ja } from './ja'

const placeholders = (s: string) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort()

describe('i18n dictionaries', () => {
  it('ja has exactly the same keys as en', () => {
    expect(Object.keys(ja).sort()).toEqual(Object.keys(en).sort())
  })

  it('ja uses the same placeholders as en', () => {
    for (const key of Object.keys(en) as MessageKey[]) {
      expect(placeholders(ja[key]), key).toEqual(placeholders(en[key]))
    }
  })
})

describe('translate', () => {
  it('fills placeholders', () => {
    expect(translate('en', 'errorCode.PRESET_IN_USE', { cellIds: 'IN1-OUT1' })).toBe(
      'The preset is applied to cells: IN1-OUT1.',
    )
  })

  it('keeps unknown placeholders as-is', () => {
    expect(translate('en', 'error.unknown')).toBe('Unexpected error ({status}).')
  })

  it('switches language', () => {
    expect(translate('ja', 'nav.matrix')).toBe('マトリクス')
  })
})
