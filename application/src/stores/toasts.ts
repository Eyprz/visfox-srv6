import { create } from 'zustand'

// Short-lived notifications for the operator's own actions (errors, saves).
// Server-side events (rollbacks, drift) are shown in the log bar from SSE `log` events instead.

export type ToastTone = 'info' | 'success' | 'error'
export type Toast = { id: number; tone: ToastTone; title: string; description?: string }

type ToastState = {
  toasts: Toast[]
  push: (toast: Omit<Toast, 'id'>) => number
  dismiss: (id: number) => void
}

let seq = 0

export const useToastStore = create<ToastState>((set) => ({
  toasts: [],
  push: (toast) => {
    const id = ++seq
    // Keep the newest 5 so a burst of errors cannot flood the screen.
    set((s) => ({ toasts: [...s.toasts, { ...toast, id }].slice(-5) }))
    return id
  },
  dismiss: (id) => set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) })),
}))
