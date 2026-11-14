import { create } from 'zustand'
import type { SwitcherNode, SwitcherState } from '../lib/types'

// TED switchers (GET /resources/switchers): VVF nodes that pick one input stream for their destinations.
// The active input changes on Take and arrives by the Take response and SSE `switcher_update`.

type SwitchersState = {
  switchers: Record<string, SwitcherNode>
  setSwitchers: (switchers: SwitcherNode[]) => void
  /** Applies active-input changes; unknown switchers are ignored until the next snapshot. */
  applyStates: (states: SwitcherState[]) => void
}

export const useSwitchersStore = create<SwitchersState>((set) => ({
  switchers: {},
  setSwitchers: (switchers) =>
    set({ switchers: Object.fromEntries(switchers.map((s) => [s.id, s])) }),
  applyStates: (states) =>
    set((s) => {
      const switchers = { ...s.switchers }
      for (const st of states) {
        const sw = switchers[st.switcher_id]
        if (sw) switchers[st.switcher_id] = { ...sw, active_input: st.active_input }
      }
      return { switchers }
    }),
}))
