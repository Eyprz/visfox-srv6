import { useEffect } from 'react'
import { useUiStore, type Theme } from '../stores/ui'

/** Applies the theme to <html data-theme>; the dark palette in index.css keys off this attribute. */
export function applyTheme(theme: Theme): void {
  if (theme === 'dark') document.documentElement.dataset.theme = 'dark'
  else delete document.documentElement.dataset.theme
}

/** Keeps <html data-theme> in sync with the selected theme. */
export function useDocumentTheme(): void {
  const theme = useUiStore((s) => s.theme)
  useEffect(() => applyTheme(theme), [theme])
}
