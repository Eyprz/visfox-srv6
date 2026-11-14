import { describe, expect, it } from 'vitest'
import type { ChainConstraints, FunctionCatalogItem, FunctionInstance } from './types'
import { functionLabel, groupChainByVvf } from './vvf'

const catalog = [
  { type: 'caption', name: 'Caption', vvf_id: 'vvf-a' },
  { type: 'color', name: 'Color', vvf_id: 'vvf-b' },
  { type: 'crop', name: 'Crop', vvf_id: 'vvf-b' },
] as FunctionCatalogItem[]
const constraints = {
  vvfs: [
    { id: 'vvf-a', name: 'Caption' },
    { id: 'vvf-b', name: 'Color / Crop' },
  ],
} as ChainConstraints
const fn = (type: string) => ({ instance_id: `sf-${type}`, type, params: {} }) as FunctionInstance

describe('groupChainByVvf', () => {
  it('groups consecutive functions of the same VVF with names from the IEL', () => {
    expect(groupChainByVvf([fn('caption'), fn('crop'), fn('color')], catalog, constraints)).toEqual(
      [
        {
          vvfId: 'vvf-a',
          name: 'Caption',
          nodeId: null,
          functions: ['Caption'],
          instanceIds: ['sf-caption'],
        },
        {
          vvfId: 'vvf-b',
          name: 'Color / Crop',
          nodeId: null,
          functions: ['Crop', 'Color'],
          instanceIds: ['sf-crop', 'sf-color'],
        },
      ],
    )
  })
  it('falls back to the type for functions the catalog does not know', () => {
    expect(groupChainByVvf([fn('delay')], catalog, null)).toEqual([
      {
        vvfId: 'delay',
        name: 'delay',
        nodeId: null,
        functions: ['delay'],
        instanceIds: ['sf-delay'],
      },
    ])
  })
  it('starts a new VVF instance when a type repeats', () => {
    const a = { ...fn('caption'), instance_id: 'sf-caption-1' }
    const b = { ...fn('caption'), instance_id: 'sf-caption-2' }
    expect(groupChainByVvf([a, b], catalog, constraints).map((g) => g.instanceIds)).toEqual([
      ['sf-caption-1'],
      ['sf-caption-2'],
    ])
  })
})

describe('pinned VVFs', () => {
  const pinned = {
    vvfs: [
      {
        id: 'vvf-a',
        name: 'Caption',
        function_types: ['caption'],
        allocation: 'pinned',
        nodes: [
          { node_id: 'cap-1', label: 'Caption: Logo' },
          { node_id: 'cap-2', label: 'Caption: Score' },
        ],
      },
      {
        id: 'vvf-b',
        name: 'Color / Crop',
        function_types: ['crop', 'color'],
        allocation: 'dynamic',
      },
    ],
  } as ChainConstraints
  const cap = (n: number, node: string) =>
    ({
      instance_id: `sf-caption-${n}`,
      type: 'caption',
      vvf_node_id: node,
      params: {},
    }) as FunctionInstance

  it('names a pinned instance after its node and splits instances on different nodes', () => {
    const groups = groupChainByVvf([cap(1, 'cap-1'), cap(2, 'cap-2')], catalog, pinned)
    expect(groups.map((g) => [g.name, g.nodeId])).toEqual([
      ['Caption: Logo', 'cap-1'],
      ['Caption: Score', 'cap-2'],
    ])
  })
  it('labels functions with the node of a pinned VVF', () => {
    expect(functionLabel(cap(1, 'cap-2'), catalog, pinned)).toBe('Caption: Score')
    expect(functionLabel(fn('crop'), catalog, pinned)).toBe('Crop')
  })
})
