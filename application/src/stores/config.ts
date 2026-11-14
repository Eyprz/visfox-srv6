import { create } from 'zustand'
import type { DynamicConfig } from '../lib/types'

// GUI-editable system config (P5 settings modal). Kept in sync by SSE `config_changed`.

type ConfigState = {
  dynamic: DynamicConfig | null
  setDynamic: (dynamic: DynamicConfig) => void
}

export const useConfigStore = create<ConfigState>((set) => ({
  dynamic: null,
  setDynamic: (dynamic) => set({ dynamic }),
}))
