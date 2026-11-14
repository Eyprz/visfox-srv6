import { describe, expect, it } from 'vitest'
import type { Topology } from '../../lib/types'
import { layoutColumns, layoutGraph, liveSwitchers, NODE_H, streamColors } from './topologyLayout'

const node = (id: string, type: 'source' | 'vvf' | 'destination') => ({ id, label: id, type })

describe('layoutColumns', () => {
  it('places VVF nodes by depth and destinations after the deepest VVF', () => {
    const topology = {
      nodes: [
        node('in-1', 'source'),
        node('caption-1', 'vvf'),
        node('caption-2', 'vvf'),
        node('ctc-1', 'vvf'),
        node('out-1', 'destination'),
      ],
      links: [
        { source: 'in-1', target: 'caption-1' },
        { source: 'caption-1', target: 'ctc-1' },
        { source: 'ctc-1', target: 'out-1' },
      ],
    } as Topology
    expect(layoutColumns(topology)).toEqual({
      'in-1': 0,
      'caption-1': 1,
      'caption-2': 1,
      'ctc-1': 2,
      'out-1': 3,
    })
  })
  it('gives switchers their own column before the processing VVFs', () => {
    const topology = {
      nodes: [
        node('in-1', 'source'),
        node('sw-1', 'vvf'),
        node('caption-1', 'vvf'),
        node('caption-2', 'vvf'),
        node('out-1', 'destination'),
      ],
      links: [
        { source: 'in-1', target: 'sw-1' },
        { source: 'sw-1', target: 'caption-1' },
        { source: 'caption-1', target: 'out-1' },
      ],
    } as Topology
    expect(layoutColumns(topology, new Map([['sw-1', ['in-1']]]))).toEqual({
      'in-1': 0,
      'sw-1': 1,
      'caption-1': 2,
      'caption-2': 2,
      'out-1': 3,
    })
  })
  it('puts cascaded switchers one column per stage', () => {
    const topology = {
      nodes: [
        node('in-1', 'source'),
        node('in-3', 'source'),
        node('sw-a', 'vvf'),
        node('sw-b', 'vvf'),
        node('caption-1', 'vvf'),
        node('out-1', 'destination'),
      ],
      links: [
        { source: 'in-3', target: 'sw-b' },
        { source: 'sw-b', target: 'sw-a' },
        { source: 'sw-a', target: 'caption-1' },
        { source: 'caption-1', target: 'out-1' },
      ],
    } as Topology
    const inputs = new Map([
      ['sw-a', ['in-1', 'sw-b']],
      ['sw-b', ['in-3']],
    ])
    expect(layoutColumns(topology, inputs)).toEqual({
      'in-1': 0,
      'in-3': 0,
      'sw-b': 1,
      'sw-a': 2,
      'caption-1': 3,
      'out-1': 4,
    })
  })
})

describe('layoutGraph', () => {
  const col = { 'in-1': 0, 'in-2': 0, cap: 1, ctc: 2, out: 3 }
  const links = [
    { id: 'in-1->cap', source: 'in-1', target: 'cap' },
    { id: 'cap->ctc', source: 'cap', target: 'ctc' },
    { id: 'ctc->out', source: 'ctc', target: 'out' },
    { id: 'in-2->ctc', source: 'in-2', target: 'ctc' },
  ]
  const layout = layoutGraph(['in-1', 'in-2', 'cap', 'ctc', 'out'], col, links)

  it('routes a link that skips a column through a lane beside the node there', () => {
    const lane = layout.waypoints['in-2->ctc']
    expect(lane).toHaveLength(2)
    const cap = layout.positions.cap
    expect(lane[0].y < cap.y || lane[0].y > cap.y + NODE_H).toBe(true)
  })

  it('does not add waypoints to links between adjacent columns', () => {
    expect(layout.waypoints['in-1->cap']).toEqual([])
  })

  it('keeps the first and last columns in their given order', () => {
    const ys = ['in-1', 'in-2'].map((id) => layout.positions[id].y)
    expect(ys[0]).toBeLessThan(ys[1])
  })
})

describe('liveSwitchers', () => {
  const swA = (active: string | null) => ({
    id: 'sw-a',
    inputs: [
      { port: 'a', from: 'in-1' },
      { port: 'c', from: 'sw-b' },
    ],
    destination_ids: ['out-1'],
    active_input: active,
  })
  const swB = {
    id: 'sw-b',
    inputs: [{ port: 'a', from: 'in-3' }],
    destination_ids: [],
    active_input: 'a',
  }

  it('keeps the upstream switcher live while the downstream one selects it', () => {
    expect([...liveSwitchers([swA('c'), swB])].sort()).toEqual(['sw-a', 'sw-b'])
  })
  it('drops the upstream switcher when the downstream one moves to another input', () => {
    expect([...liveSwitchers([swA('a'), swB])]).toEqual(['sw-a'])
  })
})

describe('streamColors', () => {
  it('stays off while every node carries one stream', () => {
    const links = [
      { target: 'caption-1', cell_ids: ['IN1-OUT1'] },
      { target: 'caption-2', cell_ids: ['IN2-OUT2'] },
    ]
    expect(streamColors(links).size).toBe(0)
  })
  it('colors every stream when a node carries several', () => {
    const links = [
      { target: 'caption-1', cell_ids: ['IN1-OUT1'] },
      { target: 'caption-1', cell_ids: ['IN3-OUT2'] },
      { target: 'out-2', cell_ids: ['IN3-OUT2'] },
    ]
    const colors = streamColors(links)
    expect([...colors.keys()]).toEqual(['IN1-OUT1', 'IN3-OUT2'])
    expect(colors.get('IN1-OUT1')).not.toBe(colors.get('IN3-OUT2'))
  })
})
