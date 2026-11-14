import { create } from 'zustand'
import {
  appendToChain,
  autoLayout,
  canConnect,
  IN_ID,
  insertIntoChain,
  linksFromChain,
  NODE_GAP_Y,
  nextInstanceId,
  OUT_ID,
  relayout,
  removeFromChain,
  type Link,
  type PaletteEntry,
  type Position,
} from '../lib/designer'
import type { FunctionInstance, Preset, PresetLayout } from '../lib/types'

// Editing state of the SFC designer page. Reset on every page load.

type DesignerState = {
  /** Saved preset id; null while creating a new preset. */
  presetId: string | null
  name: string
  description: string
  functions: FunctionInstance[]
  links: Link[]
  positions: Record<string, Position>
  /** Cells using the preset: structural edits are locked while non-empty. */
  inUseCellIds: string[]
  selectedId: string | null
  dirty: boolean
  /** Instance ids that exist in the saved preset: their params are edited live (PATCH). */
  savedInstanceIds: string[]
  /** Operator approved live editing of an ON AIR preset (valid until leaving the page). */
  liveEditEnabled: boolean

  reset: (preset?: Preset, layout?: PresetLayout | null) => void
  setName: (name: string) => void
  setDescription: (description: string) => void
  /**
   * Adds functions with their default params: wired after `place.after` (a drop on the line), left
   * unconnected at `place.at` (a drop elsewhere), or appended before OUT (palette click).
   */
  addFunctions: (items: PaletteEntry[], place?: { after: string } | { at: Position }) => string[]
  /**
   * End of a node drag: wire the node into the line after `after`, or (null) put the line back in
   * its column; a node off the line stays where it was dropped.
   */
  dropNode: (id: string, after: string | null) => void
  removeFunction: (instanceId: string) => void
  connect: (source: string, target: string) => boolean
  disconnect: (source: string, target: string) => void
  move: (id: string, position: Position) => void
  /** Updates a function's params. `dirty` is false for live edits already saved by PATCH. */
  setParams: (instanceId: string, params: Record<string, unknown>, dirty: boolean) => void
  enableLiveEdit: () => void
  select: (id: string | null) => void
  /** After a successful save: adopt the saved preset and clear the dirty flag. */
  markSaved: (preset: Preset) => void
}

const empty = {
  presetId: null,
  name: '',
  description: '',
  functions: [],
  links: [{ source: IN_ID, target: OUT_ID }],
  positions: autoLayout([]),
  inUseCellIds: [],
  selectedId: null,
  dirty: false,
  savedInstanceIds: [],
  liveEditEnabled: false,
}

export const useDesignerStore = create<DesignerState>((set, get) => ({
  ...empty,

  reset: (preset, layout) => {
    if (!preset) return set({ ...empty, positions: autoLayout([]) })
    // The line is always laid out in its column; saved positions only matter for nodes
    // off the line, which a saved preset does not have.
    const auto = autoLayout(preset.chain)
    const saved = Object.fromEntries(
      (layout?.nodes ?? []).filter((n) => !(n.node_id in auto)).map((n) => [n.node_id, n.position]),
    )
    set({
      presetId: preset.id,
      name: preset.name,
      description: preset.description ?? '',
      functions: preset.chain,
      links: linksFromChain(preset.chain),
      // Saved positions win; nodes missing from the layout fall back to the column.
      positions: { ...auto, ...saved },
      inUseCellIds: preset.in_use_cell_ids ?? [],
      selectedId: null,
      dirty: false,
      savedInstanceIds: preset.chain.map((f) => f.instance_id),
      liveEditEnabled: false,
    })
  },
  setName: (name) => set({ name, dirty: true }),
  setDescription: (description) => set({ description, dirty: true }),

  addFunctions: (items, place) => {
    const { functions, links, positions } = get()
    const ids: string[] = []
    const added = items.map((item) => {
      const id = nextInstanceId(item.type, [...functions.map((f) => f.instance_id), ...ids])
      ids.push(id)
      return {
        instance_id: id,
        type: item.type,
        // Pinned VVFs: the palette element names the node; dynamic VVFs leave it to the IEL.
        ...(item.vvf_node_id ? { vvf_node_id: item.vvf_node_id } : {}),
        params: structuredClone(item.default_params ?? {}),
      }
    })
    const at = place && 'at' in place ? place.at : undefined
    const nextLinks =
      place && 'after' in place
        ? insertIntoChain(links, ids, place.after)
        : at
          ? links
          : appendToChain(links, ids)
    // Loose drops keep the drop point (stacked); the line goes back into its column layout.
    const placed = { ...positions }
    if (at) ids.forEach((id, i) => (placed[id] = { x: at.x, y: at.y + i * NODE_GAP_Y }))
    const nextFunctions = [...functions, ...added]
    set({
      functions: nextFunctions,
      links: nextLinks,
      positions: relayout(
        nextFunctions.map((f) => f.instance_id),
        nextLinks,
        placed,
      ),
      selectedId: ids[ids.length - 1] ?? null,
      dirty: true,
    })
    return ids
  },
  dropNode: (id, after) => {
    const { functions, links, positions } = get()
    const nextLinks = after && after !== id ? insertIntoChain(links, [id], after) : links
    set({
      links: nextLinks,
      positions: relayout(
        functions.map((f) => f.instance_id),
        nextLinks,
        positions,
      ),
      dirty: true,
    })
  },
  removeFunction: (instanceId) => {
    const { functions, links, positions, selectedId } = get()
    const { [instanceId]: _removed, ...rest } = positions
    const nextFunctions = functions.filter((f) => f.instance_id !== instanceId)
    const nextLinks = removeFromChain(links, instanceId)
    set({
      functions: nextFunctions,
      links: nextLinks,
      positions: relayout(
        nextFunctions.map((f) => f.instance_id),
        nextLinks,
        rest,
      ),
      selectedId: selectedId === instanceId ? null : selectedId,
      dirty: true,
    })
  },
  connect: (source, target) => {
    const { links, functions, positions } = get()
    if (!canConnect(links, source, target)) return false
    const nextLinks = [...links, { source, target }]
    set({
      links: nextLinks,
      positions: relayout(
        functions.map((f) => f.instance_id),
        nextLinks,
        positions,
      ),
      dirty: true,
    })
    return true
  },
  disconnect: (source, target) =>
    set((s) => {
      const links = s.links.filter((l) => !(l.source === source && l.target === target))
      return {
        links,
        positions: relayout(
          s.functions.map((f) => f.instance_id),
          links,
          s.positions,
        ),
        dirty: true,
      }
    }),
  move: (id, position) =>
    set((s) => ({ positions: { ...s.positions, [id]: position }, dirty: true })),
  setParams: (instanceId, params, dirty) =>
    set((s) => ({
      functions: s.functions.map((f) => (f.instance_id === instanceId ? { ...f, params } : f)),
      dirty: s.dirty || dirty,
    })),
  enableLiveEdit: () => set({ liveEditEnabled: true }),
  select: (selectedId) => set({ selectedId }),
  markSaved: (preset) =>
    set({
      presetId: preset.id,
      inUseCellIds: preset.in_use_cell_ids ?? [],
      savedInstanceIds: preset.chain.map((f) => f.instance_id),
      dirty: false,
    }),
}))
