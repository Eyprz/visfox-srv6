import { create } from 'zustand'
import type { LogEntry } from '../lib/types'

// System log for the log / alert bar. Newest first, bounded so a long session cannot grow memory.

export const MAX_LOGS = 500

type LogsState = {
  entries: LogEntry[]
  setEntries: (entries: LogEntry[]) => void
  addEntry: (entry: LogEntry) => void
}

export const useLogsStore = create<LogsState>((set) => ({
  entries: [],
  setEntries: (entries) => set({ entries: entries.slice(0, MAX_LOGS) }),
  addEntry: (entry) =>
    set((s) =>
      // SSE may replay events after a reconnect, so ignore ids we already have.
      s.entries.some((e) => e.id === entry.id)
        ? s
        : { entries: [entry, ...s.entries].slice(0, MAX_LOGS) },
    ),
}))
