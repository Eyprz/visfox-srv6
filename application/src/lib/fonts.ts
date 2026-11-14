import { ja } from '../i18n/ja'
import type { Locale } from '../stores/ui'

// Waits for the self-hosted fonts (public/fonts/fonts.css) so the first paint and language switches never show a
// fallback font. M PLUS 2 is split by unicode-range, so for Japanese we ask for exactly the glyphs of the
// Japanese dictionary, which downloads only the subsets the UI needs.

const FONT_TIMEOUT_MS = 3000
const jaText = Object.values(ja).join('')

export async function loadFonts(locale: Locale): Promise<void> {
  if (typeof document === 'undefined' || !document.fonts) return
  const loads = [document.fonts.load('400 1em Play'), document.fonts.load('700 1em Play')]
  if (locale === 'ja') loads.push(document.fonts.load("400 1em 'M PLUS 2'", jaText))
  // Never block the UI for long on a slow network; font-display: block covers the rest.
  await Promise.race([
    Promise.all(loads).catch(() => undefined),
    new Promise((resolve) => setTimeout(resolve, FONT_TIMEOUT_MS)),
  ])
}
