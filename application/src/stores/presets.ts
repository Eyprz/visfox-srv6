import { create } from 'zustand'
import type { ChainConstraints, FunctionCatalogItem, Preset } from '../lib/types'

// Presets (SSoT) plus the function catalog and chain constraints from the IEL's TED.

type PresetsState = {
  presets: Record<string, Preset>
  catalog: FunctionCatalogItem[]
  constraints: ChainConstraints | null
  /**
   * The IEL answered a preset write with 501 NOT_IMPLEMENTED: it serves
   * predefined presets only. Creating, saving and deleting are disabled until the page reloads.
   */
  editingUnsupported: boolean
  setEditingUnsupported: () => void
  setPresets: (presets: Preset[]) => void
  upsertPreset: (preset: Preset) => void
  removePreset: (presetId: string) => void
  setCatalog: (catalog: FunctionCatalogItem[], constraints: ChainConstraints) => void
}

export const usePresetsStore = create<PresetsState>((set) => ({
  presets: {},
  catalog: [],
  constraints: null,
  editingUnsupported: false,
  setEditingUnsupported: () => set({ editingUnsupported: true }),
  setPresets: (presets) => set({ presets: Object.fromEntries(presets.map((p) => [p.id, p])) }),
  upsertPreset: (preset) => set((s) => ({ presets: { ...s.presets, [preset.id]: preset } })),
  removePreset: (presetId) =>
    set((s) => {
      const { [presetId]: _removed, ...rest } = s.presets
      return { presets: rest }
    }),
  setCatalog: (catalog, constraints) => set({ catalog, constraints }),
}))
