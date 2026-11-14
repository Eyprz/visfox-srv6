import { beforeEach, describe, expect, it } from 'vitest'
import { IN_ID, OUT_ID, walkChain } from '../lib/designer'
import type { FunctionCatalogItem, Preset } from '../lib/types'
import { useDesignerStore } from './designer'

const item = (type: string) =>
  ({ type, name: type, default_params: { v: 1 } }) as unknown as FunctionCatalogItem
const preset = {
  id: 'preset-9',
  name: 'P',
  chain: [
    { instance_id: 'sf-caption-1', type: 'caption', params: {} },
    { instance_id: 'sf-crop-1', type: 'crop', params: {} },
  ],
  in_use_cell_ids: ['IN1-OUT1'],
} as Preset

const chainIds = () => {
  const s = useDesignerStore.getState()
  return walkChain(s.functions, s.links).chain.map((f) => f.instance_id)
}

beforeEach(() => useDesignerStore.getState().reset())

describe('designer store', () => {
  it('starts with IN linked to OUT and nothing dirty', () => {
    const s = useDesignerStore.getState()
    expect(s.links).toEqual([{ source: IN_ID, target: OUT_ID }])
    expect(s.dirty).toBe(false)
  })

  it('appends palette functions with default params and numbered ids', () => {
    const { addFunctions } = useDesignerStore.getState()
    addFunctions([item('color')])
    addFunctions([item('color')])
    expect(chainIds()).toEqual(['sf-color-1', 'sf-color-2'])
    expect(useDesignerStore.getState().functions[0].params).toEqual({ v: 1 })
    expect(useDesignerStore.getState().dirty).toBe(true)
  })

  it('places functions dropped off the line unconnected at the drop point', () => {
    useDesignerStore.getState().addFunctions([item('crop')], { at: { x: 900, y: 20 } })
    const s = useDesignerStore.getState()
    expect(walkChain(s.functions, s.links).unconnected).toEqual(['sf-crop-1'])
    expect(s.positions['sf-crop-1']).toEqual({ x: 900, y: 20 })
  })

  it('wires functions dropped on the line in after the given node', () => {
    const { addFunctions } = useDesignerStore.getState()
    addFunctions([item('color')])
    addFunctions([item('crop')])
    addFunctions([item('caption')], { after: 'sf-color-1' })
    expect(chainIds()).toEqual(['sf-color-1', 'sf-caption-1', 'sf-crop-1'])
    // The line stays a single column, top to bottom.
    const p = useDesignerStore.getState().positions
    expect(new Set([p.in.x, p['sf-color-1'].x, p['sf-caption-1'].x, p.out.x]).size).toBe(1)
    expect(p['sf-caption-1'].y).toBeGreaterThan(p['sf-color-1'].y)
    expect(p['sf-crop-1'].y).toBeGreaterThan(p['sf-caption-1'].y)
  })

  it('moves a dragged node into the line or back into its column', () => {
    const { addFunctions, dropNode, move } = useDesignerStore.getState()
    addFunctions([item('color')])
    addFunctions([item('crop')])
    dropNode('sf-crop-1', 'in')
    expect(chainIds()).toEqual(['sf-crop-1', 'sf-color-1'])
    move('sf-color-1', { x: 500, y: 999 })
    dropNode('sf-color-1', null)
    expect(useDesignerStore.getState().positions['sf-color-1'].x).toBe(0)
  })

  it('loads a preset in the column layout', () => {
    useDesignerStore.getState().reset(preset, {
      preset_id: 'preset-9',
      nodes: [{ node_id: 'sf-crop-1', position: { x: 300, y: 40 } }],
    })
    const s = useDesignerStore.getState()
    expect(chainIds()).toEqual(['sf-caption-1', 'sf-crop-1'])
    expect(s.positions['sf-crop-1'].x).toBe(s.positions['sf-caption-1'].x)
    expect(s.positions['sf-crop-1'].y).toBeGreaterThan(s.positions['sf-caption-1'].y)
    expect(s.inUseCellIds).toEqual(['IN1-OUT1'])
  })

  it('removes a function and keeps the chain connected', () => {
    useDesignerStore.getState().reset(preset)
    useDesignerStore.getState().removeFunction('sf-caption-1')
    const s = useDesignerStore.getState()
    expect(walkChain(s.functions, s.links)).toMatchObject({ complete: true })
  })

  it('refuses links that break the line', () => {
    useDesignerStore.getState().reset(preset)
    expect(useDesignerStore.getState().connect('sf-crop-1', 'sf-caption-1')).toBe(false)
  })

  it('tracks saved instances and marks only unsaved param edits dirty', () => {
    useDesignerStore.getState().reset(preset)
    const st = useDesignerStore.getState()
    expect(st.savedInstanceIds).toEqual(['sf-caption-1', 'sf-crop-1'])
    st.setParams('sf-crop-1', { x: 10 }, false)
    expect(useDesignerStore.getState().dirty).toBe(false)
    expect(useDesignerStore.getState().functions[1].params).toEqual({ x: 10 })
    st.setParams('sf-crop-1', { x: 20 }, true)
    expect(useDesignerStore.getState().dirty).toBe(true)
  })
})
