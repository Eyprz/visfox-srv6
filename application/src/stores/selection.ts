import { create } from 'zustand'

// Selected crosspoint on the matrix (shown in the crosspoint inspector). Not persisted.

type SelectionState = {
  selectedCellId: string | null
  select: (cellId: string | null) => void
}

export const useSelectionStore = create<SelectionState>((set) => ({
  selectedCellId: null,
  select: (selectedCellId) => set({ selectedCellId }),
}))
