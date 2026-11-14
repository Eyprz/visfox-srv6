import { create } from 'zustand'
import type { DestinationNode, SourceNode } from '../lib/types'

// IN / OUT catalog (SSoT). Loaded at startup; rarely changes.

type ResourcesState = {
  sources: SourceNode[]
  destinations: DestinationNode[]
  setResources: (sources: SourceNode[], destinations: DestinationNode[]) => void
}

export const useResourcesStore = create<ResourcesState>((set) => ({
  sources: [],
  destinations: [],
  setResources: (sources, destinations) => set({ sources, destinations }),
}))
