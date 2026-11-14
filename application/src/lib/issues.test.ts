import { describe, expect, it } from 'vitest'
import { translate, type Translate } from '../i18n'
import { issueText } from './issues'

describe('issueText', () => {
  const t: Translate = (key, vars) => translate('ja', key, vars)
  it('names the bottleneck VVF nodes and the OUTs using them', () => {
    const text = issueText(t, {
      code: 'RESOURCE_EXHAUSTED',
      resource_kind: 'vvf_node',
      message: 'No free node',
      bottleneck_nodes: [
        { node_id: 'ctc-1', label: 'CTC #1', destination_ids: ['out-1'] },
        { node_id: 'ctc-2', label: 'CTC #2', destination_ids: ['out-2', 'out-4'] },
      ],
    })
    expect(text).toBe(
      'VVF のリソースが足りません。 ボトルネック: CTC #1 (OUT 1), CTC #2 (OUT 2, OUT 4)',
    )
  })
  it('translates known kinds generically and shows unknown codes with the IEL text', () => {
    expect(
      issueText(t, { code: 'RESOURCE_EXHAUSTED', resource_kind: 'vvf_input', message: 'x' }),
    ).toBe('スイッチャー等の入力が足りません。 x')
    expect(
      issueText(t, { code: 'RESOURCE_EXHAUSTED', resource_kind: 'branch_vvf', message: 'y' }),
    ).toBe('branch_vvfが足りません。 y')
    expect(issueText(t, { code: 'SOMETHING_NEW', message: 'IEL text' })).toBe('IEL text')
  })
  it('falls back to the IEL detail', () => {
    expect(issueText(t, { code: 'NODE_OFFLINE', message: 'in-1 offline' })).toContain(
      'in-1 offline',
    )
  })
})
