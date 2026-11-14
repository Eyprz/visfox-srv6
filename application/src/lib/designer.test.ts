import { describe, expect, it } from 'vitest'
import {
  appendToChain,
  autoLayout,
  chainOrder,
  COLUMN_GAP_X,
  findInsertGap,
  IN_ID,
  insertIntoChain,
  NODE_H,
  NODE_W,
  canConnect,
  flowingLinks,
  linksFromChain,
  nextInstanceId,
  OUT_ID,
  paletteLimits,
  removeFromChain,
  walkChain,
} from './designer'
import type { ChainConstraints, FunctionCatalogItem, FunctionInstance } from './types'

const fn = (id: string) =>
  ({ instance_id: id, type: id.split('-')[1], params: {} }) as FunctionInstance
const a = fn('sf-caption-1')
const b = fn('sf-color-1')
const c = fn('sf-crop-1')

describe('walkChain', () => {
  it('walks IN -> ... -> OUT and reports completeness', () => {
    const r = walkChain([a, b], linksFromChain([a, b]))
    expect(r.chain.map((f) => f.instance_id)).toEqual(['sf-caption-1', 'sf-color-1'])
    expect(r.complete).toBe(true)
  })
  it('lists functions off the path and is incomplete without OUT', () => {
    const r = walkChain([a, b, c], [{ source: IN_ID, target: a.instance_id }])
    expect(r.chain).toEqual([a])
    expect(r.complete).toBe(false)
    expect(r.unconnected).toEqual(['sf-color-1', 'sf-crop-1'])
  })
})

describe('canConnect', () => {
  const links = [
    { source: IN_ID, target: 'x' },
    { source: 'x', target: 'y' },
  ]
  it('rejects links into IN, out of OUT, and self links', () => {
    expect(canConnect([], 'x', IN_ID)).toBe(false)
    expect(canConnect([], OUT_ID, 'x')).toBe(false)
    expect(canConnect([], 'x', 'x')).toBe(false)
  })
  it('allows one link out of and into each node', () => {
    expect(canConnect(links, 'x', 'z')).toBe(false)
    expect(canConnect(links, 'z', 'y')).toBe(false)
    expect(canConnect(links, 'y', OUT_ID)).toBe(true)
  })
  it('rejects loops', () => {
    expect(canConnect(links, 'y', 'x')).toBe(false)
  })
})

describe('chain editing helpers', () => {
  it('numbers instance ids per type', () => {
    expect(nextInstanceId('color', ['sf-color-1', 'sf-color-3'])).toBe('sf-color-2')
  })
  it('appends before OUT and keeps the line connected', () => {
    const links = appendToChain(linksFromChain([a]), ['sf-color-1', 'sf-crop-1'])
    expect(walkChain([a, b, c], links).chain.map((f) => f.instance_id)).toEqual([
      'sf-caption-1',
      'sf-color-1',
      'sf-crop-1',
    ])
    expect(walkChain([a, b, c], links).complete).toBe(true)
  })
  it('bridges neighbors when a node is removed', () => {
    const links = removeFromChain(linksFromChain([a, b, c]), 'sf-color-1')
    expect(walkChain([a, c], links)).toMatchObject({ complete: true })
  })
  it('lays out a vertical column', () => {
    const p = autoLayout([a])
    expect(p[IN_ID].y).toBeLessThan(p['sf-caption-1'].y)
    expect(p['sf-caption-1'].y).toBeLessThan(p[OUT_ID].y)
  })
})

describe('paletteLimits', () => {
  const catalog = [{ type: 'color' }, { type: 'crop' }] as FunctionCatalogItem[]
  const constraints = {
    max_functions: 2,
    max_instances_per_type: { color: 1 },
  } as never as ChainConstraints
  it('disables types at their limit and everything at max_functions', () => {
    expect(paletteLimits(catalog, constraints, [b])).toEqual({
      color: { disabled: true, reason: 'type_limit' },
      crop: { disabled: false },
    })
    expect(paletteLimits(catalog, constraints, [b, c]).crop).toEqual({
      disabled: true,
      reason: 'max_functions',
    })
  })
})

describe('paletteLimits without a total limit', () => {
  it('only applies the per-type limits when max_functions is omitted', () => {
    const catalog = [{ type: 'caption' }] as FunctionCatalogItem[]
    const constraints = { max_instances_per_type: { caption: 2 } } as never as ChainConstraints
    const one = [fn('sf-caption-1')]
    const two = [fn('sf-caption-1'), fn('sf-caption-2')]
    expect(paletteLimits(catalog, constraints, one).caption).toEqual({ disabled: false })
    expect(paletteLimits(catalog, constraints, two).caption).toEqual({
      disabled: true,
      reason: 'type_limit',
    })
    expect(paletteLimits(catalog, { max_instances_per_type: {} } as never, two).caption).toEqual({
      disabled: false,
    })
  })
})

describe('flowingLinks', () => {
  it('follows the path from IN and leaves dangling links out', () => {
    const links = [
      { source: IN_ID, target: 'a' },
      { source: 'a', target: 'b' },
      { source: 'x', target: OUT_ID },
    ]
    expect(flowingLinks(links)).toEqual(links.slice(0, 2))
  })
})

describe('column layout and insertion', () => {
  const f = (n: number) => ({ instance_id: `sf-color-${n}`, type: 'color' as const, params: {} })

  it('wraps a long line into two columns of equal length', () => {
    const chain = [1, 2, 3, 4, 5, 6, 7, 8].map(f)
    const p = autoLayout(chain)
    // 10 nodes (IN + 8 + OUT): 5 per column.
    expect(p.in).toEqual({ x: 0, y: 0 })
    expect(p['sf-color-4'].x).toBe(0)
    expect(p['sf-color-5'].x).toBe(COLUMN_GAP_X)
    expect(p['sf-color-5'].y).toBe(0)
    expect(p.out.x).toBe(COLUMN_GAP_X)
  })

  it('inserts after a node and bridges a moved node', () => {
    const links = linksFromChain([f(1), f(2), f(3)])
    expect(chainOrder(insertIntoChain(links, ['sf-color-3'], IN_ID))).toEqual([
      IN_ID,
      'sf-color-3',
      'sf-color-1',
      'sf-color-2',
      OUT_ID,
    ])
  })

  it('finds the link nearest to a drop point and keeps VVF frames together', () => {
    const chain = [f(1), f(2), f(3)]
    const links = linksFromChain(chain)
    const p = autoLayout(chain)
    const between = (a: string, b: string) => ({
      x: NODE_W / 2,
      y: (p[a].y + p[b].y) / 2 + NODE_H / 2,
    })
    expect(findInsertGap(links, p, between('sf-color-1', 'sf-color-2'))).toBe('sf-color-1')
    expect(findInsertGap(links, p, { x: 2000, y: 0 })).toBeNull()
    // sf-color-1..3 as one VVF: a drop between 1 and 2 goes before the frame (nearer edge).
    const groups = [{ vvfId: 'v', ids: ['sf-color-1', 'sf-color-2', 'sf-color-3'] }]
    expect(findInsertGap(links, p, between('sf-color-1', 'sf-color-2'), { groups })).toBe(IN_ID)
    expect(findInsertGap(links, p, between('sf-color-2', 'sf-color-3'), { groups })).toBe(
      'sf-color-3',
    )
    // Members of that VVF may go inside.
    expect(
      findInsertGap(links, p, between('sf-color-1', 'sf-color-2'), { groups, inside: ['v'] }),
    ).toBe('sf-color-1')
  })
})
