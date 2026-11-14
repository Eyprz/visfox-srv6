import { create } from 'zustand'
import { persist } from 'zustand/middleware'

// UI preferences. `persist` saves them to localStorage so a reload keeps the operator's choice.

export type Locale = 'en' | 'ja'
export type Theme = 'light' | 'dark'

/** Matrix zoom range and step (50% - 200%, 10% steps). */
export const MATRIX_ZOOM = { min: 0.5, max: 2, step: 0.1 } as const
export const clampZoom = (zoom: number) =>
  Math.round(Math.min(MATRIX_ZOOM.max, Math.max(MATRIX_ZOOM.min, zoom)) * 10) / 10

type UiState = {
  locale: Locale
  /** Color theme. Light by default. */
  theme: Theme
  /** CRITICAL alert sound. Off by default; turning it on is the user gesture that unlocks audio. */
  alertSound: boolean
  /** Collapsible navigation sidebar on narrow screens (not persisted). */
  sidebarOpen: boolean
  /** Right inspector panel collapsed to a thin rail (persisted). */
  inspectorCollapsed: boolean
  /** Matrix-only zoom factor applied to the fitted cell size (persisted). 1 = 100%. */
  matrixZoom: number
  settingsOpen: boolean
  setLocale: (locale: Locale) => void
  setTheme: (theme: Theme) => void
  setAlertSound: (on: boolean) => void
  setSidebarOpen: (open: boolean) => void
  setInspectorCollapsed: (collapsed: boolean) => void
  setMatrixZoom: (zoom: number) => void
  setSettingsOpen: (open: boolean) => void
}

export const useUiStore = create<UiState>()(
  persist(
    (set) => ({
      locale: 'en',
      theme: 'light',
      alertSound: false,
      sidebarOpen: false,
      inspectorCollapsed: false,
      matrixZoom: 1,
      settingsOpen: false,
      setLocale: (locale) => set({ locale }),
      setTheme: (theme) => set({ theme }),
      setAlertSound: (alertSound) => set({ alertSound }),
      setSidebarOpen: (sidebarOpen) => set({ sidebarOpen }),
      setInspectorCollapsed: (inspectorCollapsed) => set({ inspectorCollapsed }),
      setMatrixZoom: (matrixZoom) => set({ matrixZoom: clampZoom(matrixZoom) }),
      setSettingsOpen: (settingsOpen) => set({ settingsOpen }),
    }),
    {
      name: 'visfox.ui',
      partialize: (s) => ({
        locale: s.locale,
        theme: s.theme,
        alertSound: s.alertSound,
        inspectorCollapsed: s.inspectorCollapsed,
        matrixZoom: s.matrixZoom,
      }),
    },
  ),
)
