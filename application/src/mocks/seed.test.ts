import { describe, expect, it } from 'vitest'
import type { FunctionInstance } from '../lib/types'
import {
  allocateNodes,
  capacityIssueWith,
  nodesOf,
  clone,
  db,
  switcherPaths,
  resourcePools,
  takeIssuesOf,
  validateAssignment,
  validatePreset,
  type Stream,
} from './db'
import { constraints, functionCatalog, switchers } from './seed'

describe('mock domain constraints', () => {
  it('does not limit function counts by TED resources', () => {
    expect(constraints.max_instances_per_type).toBeUndefined()
    expect(constraints.max_functions).toBeUndefined()
  })
  it('requires a switcher on every path and wires every IN to both switchers', () => {
    expect(constraints.domain.switcher_required).toBe(true)
    for (const sw of switchers)
      for (const n of [1, 2, 3, 4]) expect(sw.inputs.map((i) => i.from)).toContain(`in-${n}`)
  })
  it('inserts captions only into 8K DG (before transcode)', () => {
    const chain = (types: FunctionInstance['type'][]) =>
      types.map((type, i) => ({
        instance_id: `sf-${type}-${i}`,
        type,
        vvf_node_id: type === 'caption' ? 'caption-1' : undefined,
        params: functionCatalog.find((f) => f.type === type)!.default_params ?? {},
      }))
    expect(
      validatePreset({ name: 'a', chain: chain(['caption', 'transcode', 'crop', 'color']) }).valid,
    ).toBe(true)
    const after = validatePreset({
      name: 'b',
      chain: chain(['transcode', 'crop', 'color', 'caption']),
    })
    expect(after.issues?.map((i) => [i.code, i.instance_id])).toContainEqual([
      'FORMAT_MISMATCH',
      'sf-caption-3',
    ])
  })
})

describe('VVF rules per instance', () => {
  const fn = (type: FunctionInstance['type'], n = 1): FunctionInstance => ({
    instance_id: `sf-${type}-${n}`,
    type,
    vvf_node_id: type === 'caption' ? `caption-${n}` : undefined,
    params: functionCatalog.find((f) => f.type === type)!.default_params ?? {},
  })
  const codes = (chain: FunctionInstance[]) =>
    (validatePreset({ name: 'x', chain }).issues ?? []).map((i) => [i.code, i.instance_id ?? null])

  it('rejects a lone color after a complete color / transcode / crop VVF', () => {
    const chain = [fn('transcode'), fn('crop'), fn('color'), fn('color', 2)]
    expect(codes(chain)).toEqual([['VVF_INCOMPLETE', 'sf-color-2']])
  })
  it('reports members split by another function as not contiguous', () => {
    const chain = [fn('transcode'), fn('caption'), fn('crop'), fn('color')]
    expect(codes(chain).map((c) => c[0])).toContain('VVF_NOT_CONTIGUOUS')
  })
  it('requires a node for pinned VVFs and refuses one for dynamic VVFs', () => {
    const bare = { ...fn('caption'), vvf_node_id: undefined }
    const wrong = { ...fn('caption'), vvf_node_id: 'color-transcode-crop-1' }
    const named = { ...fn('transcode'), vvf_node_id: 'color-transcode-crop-1' }
    expect(codes([bare])).toContainEqual(['VVF_NODE_REQUIRED', 'sf-caption-1'])
    expect(codes([wrong])).toContainEqual(['VVF_NODE_UNKNOWN', 'sf-caption-1'])
    expect(codes([named, fn('crop'), fn('color')])).toContainEqual([
      'VVF_NODE_UNKNOWN',
      'sf-transcode-1',
    ])
  })
  it('accepts two complete instances and repeated captions', () => {
    const chain = [
      fn('caption'),
      fn('caption', 2),
      fn('transcode'),
      fn('crop'),
      fn('color'),
      fn('crop', 2),
      fn('color', 2),
      fn('transcode', 2),
    ]
    expect(codes(chain).filter((c) => String(c[0]).startsWith('VVF'))).toEqual([])
  })
})

describe('resource estimate of a draft preset', () => {
  it('reports no new stream, the TED maximum, and the IN unicasts as the bottleneck', () => {
    const r = validatePreset(clone(db.presets.get('preset-3')!)).resources!
    expect(r.available_streams).toBe(0)
    // Two switchers, one stream each.
    expect(r.max_streams).toBe(2)
    expect(r.requirements).toEqual([
      {
        kind: 'vvf_node',
        group: 'vvf-color-transcode-crop',
        label: 'Color / Transcode / Crop',
        per_stream: 1,
        pool_count: 3,
        free_count: 1,
      },
      {
        kind: 'vvf_output',
        group: 'vvf-switcher',
        label: 'Switcher',
        per_stream: 1,
        pool_count: 2,
        free_count: 0,
      },
    ])
    // Hot standby: every IN is already sent to OUT 1 or OUT 2, so no new stream has an IN.
    expect(r.blocker?.resource_kind).toBe('source_unicast')
    expect(r.blocker?.bottleneck_nodes?.map((n) => [n.node_id, n.destination_ids])).toEqual([
      ['in-1', ['out-1']],
    ])
    // Only the cells of the INs each OUT already receives can be assigned.
    expect(r.assignable_cell_ids).toEqual(['IN1-OUT1', 'IN2-OUT1', 'IN3-OUT1', 'IN4-OUT2'])
  })
})

describe('switcher paths', () => {
  it('reaches an OUT through SW1, SW2, or SW2 into SW1', () => {
    const paths = switcherPaths('in-3', 'out-1').map((p) =>
      p.map((h) => `${h.switcher.id}:${h.port}`).join(' > '),
    )
    expect(paths).toEqual(['sw-1:c', 'sw-2:c', 'sw-2:c > sw-1:e'])
  })
})

describe('VVF node allocation (one config per node)', () => {
  const ctc = (r: number): FunctionInstance[] => [
    { instance_id: 'sf-transcode-1', type: 'transcode', params: {} },
    { instance_id: 'sf-crop-1', type: 'crop', params: { x: 0, y: 0 } },
    { instance_id: 'sf-color-1', type: 'color', params: { r, g: 0, b: 0 } },
  ]
  const caption = (text: string, node = 'caption-1'): FunctionInstance[] => [
    {
      instance_id: 'sf-caption-1',
      type: 'caption',
      vvf_node_id: node,
      params: { text, position: 'top' },
    },
  ]
  // Destinations without a current allocation, so nothing is kept from the seeded state.
  const stream = (n: number, chain: FunctionInstance[]): Stream => ({
    destinationId: `test-${n}`,
    cell: db.cells.get('IN1-OUT1')!,
    chain,
    onAir: false,
    standby: [],
  })

  it('needs one transcoder per stream, even with the same config', () => {
    const { failed } = allocateNodes(
      [1, 2, 3, 4].map((n) => stream(n, ctc(0))),
      { switchers: false },
    )
    expect(failed).toEqual([
      {
        destinationId: 'test-4',
        vvfId: 'vvf-color-transcode-crop',
        vvfName: 'Color / Transcode / Crop',
      },
    ])
  })
  it('shares a pinned caption node between streams with the same caption up to max_streams', () => {
    const { nodes, failed } = allocateNodes(
      [1, 2, 3, 4].map((n) => stream(n, caption('LIVE'))),
      { switchers: false },
    )
    // The fourth stream cannot move to another caption node: they burn in other materials.
    expect(failed).toEqual([
      {
        destinationId: 'test-4',
        vvfId: 'vvf-caption',
        vvfName: 'Caption: Logo',
        nodeId: 'caption-1',
      },
    ])
    expect([1, 2, 3].map((n) => nodes.get(`test-${n}`)![0]!.id)).toEqual([
      'caption-1',
      'caption-1',
      'caption-1',
    ])
  })
  it('puts different captions on one pinned node only on different nodes', () => {
    const same = allocateNodes(
      ['A', 'B'].map((t, n) => stream(n, caption(t))),
      { switchers: false },
    )
    expect(same.failed).toHaveLength(1)
    const apart = allocateNodes([stream(1, caption('A')), stream(2, caption('B', 'caption-2'))], {
      switchers: false,
    })
    expect(apart.failed).toEqual([])
  })
  it('moves a taken stream off the node that runs the ON AIR config', () => {
    // IN1-OUT1 is seeded ON AIR: its nodes run preset-1's config.
    const onAir = db.cells.get('IN1-OUT1')!
    const ctcNow = nodesOf(onAir).find((n) => n?.vvf_id === 'vvf-color-transcode-crop')!.id
    const chain = clone(onAir.live_chain!).map((f) =>
      f.type === 'color' ? { ...f, params: { r: 99, g: 0, b: 0 } } : f,
    )
    const st: Stream = { destinationId: 'out-1', cell: onAir, chain, onAir: true, standby: [] }
    const ids = (fresh?: Set<string>) =>
      allocateNodes([st], { switchers: false, fresh })
        .nodes.get('out-1')!
        .map((n) => n?.id)
    // Without a Take the stream keeps its nodes; a Take prefers a node it can configure in advance.
    expect(ids()).toContain(ctcNow)
    const taken = ids(new Set(['out-1']))
    expect(taken).not.toContain(ctcNow)
    // The caption config is unchanged, so its node stays.
    expect(taken[0]).toBe('caption-1')
  })
})

describe('mock streams (hot standby)', () => {
  it('starts with OUT 1 holding IN 1-3 on SW1 and OUT 2 holding IN 4 on SW2', () => {
    expect(capacityIssueWith({})).toBeNull()
    const pools = resourcePools()
    const users = (id: string) =>
      pools.find((p) => p.id === id)!.used_by.map((u) => u.destination_id)
    expect(users('vvf_input:sw-1')).toEqual(['out-1'])
    expect(users('vvf_output:sw-2')).toEqual(['out-2'])
  })
  it('lets every STANDBY cell be taken: its IN already reaches the switcher', () => {
    const blocked = [...db.cells.values()]
      .filter((c) => c.execution_state === 'standby')
      .map((c) => [c.cell_id, takeIssuesOf(c).map((i) => i.resource_kind)])
    expect(blocked).toEqual([
      ['IN2-OUT1', []],
      ['IN3-OUT1', []],
    ])
  })
  it('refuses an IN sent to another OUT and offers to release its STANDBY cell', () => {
    const result = validateAssignment(db.cells.get('IN2-OUT2')!, db.presets.get('preset-3')!)
    expect(result.feasible).toBe(false)
    const issue = result.errors![0]
    expect([issue.resource_kind, issue.releasable_cells]).toEqual(['source_unicast', ['IN2-OUT1']])
    // With the release it fits.
    const released = validateAssignment(db.cells.get('IN2-OUT2')!, db.presets.get('preset-3')!, [
      'IN2-OUT1',
    ])
    expect(released.feasible).toBe(true)
  })
  it('never offers an ON AIR cell', () => {
    const result = validateAssignment(db.cells.get('IN1-OUT2')!, db.presets.get('preset-3')!)
    expect(result.feasible).toBe(false)
    expect(result.errors![0].releasable_cells).toBeUndefined()
  })
  it('gives a third OUT neither a switcher nor an IN, the switcher first', () => {
    const result = validateAssignment(db.cells.get('IN3-OUT3')!, db.presets.get('preset-3')!)
    expect(result.feasible).toBe(false)
    expect(result.errors!.map((e) => e.resource_kind)).toEqual(['vvf_output', 'source_unicast'])
    // The ON AIR cells hold both switchers: no release solves it.
    expect(result.errors!.some((e) => e.releasable_cells)).toBe(false)
  })
})

describe('resource pools', () => {
  it('exposes every limit in one shape with its use', () => {
    const pools = resourcePools()
    const of = (id: string) => pools.find((p) => p.id === id)!
    expect([...new Set(pools.map((p) => p.kind))]).toEqual([
      'vvf_node',
      'vvf_output',
      'vvf_input',
      'source_unicast',
    ])
    expect(of('vvf_node:color-transcode-crop-1')).toMatchObject({
      capacity: 1,
      sharing: 'same_config',
      reserved_at: 'standby',
    })
    // Hot standby: the switchers and IN unicasts are reserved at STANDBY.
    expect(of('vvf_output:sw-1')).toMatchObject({ capacity: 1, reserved_at: 'standby' })
    expect(of('vvf_output:sw-1').used_by.map((u) => u.cell_id)).toEqual(['IN1-OUT1'])
    expect(of('source_unicast:in-1').used_by.map((u) => u.destination_id)).toEqual(['out-1'])
    expect(of('source_unicast:in-2').used_by).toEqual([
      { destination_id: 'out-1', cell_id: 'IN2-OUT1', on_air: false },
    ])
  })
})
