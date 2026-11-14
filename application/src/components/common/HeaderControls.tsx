import { Moon, Settings, Sun, Volume2, VolumeX } from 'lucide-react'
import { useT } from '../../i18n'
import { unlockAudio, playAlert } from '../../lib/alertSound'
import { cx } from '../../lib/cx'
import { loadFonts } from '../../lib/fonts'
import { useUiStore, type Locale } from '../../stores/ui'
import { Button } from './Button'

const locales: Locale[] = ['en', 'ja']

export function LanguageToggle() {
  const t = useT()
  const locale = useUiStore((s) => s.locale)
  const setLocale = useUiStore((s) => s.setLocale)
  return (
    <div
      role="group"
      aria-label={t('header.language')}
      className="border-line-strong inline-flex border"
    >
      {locales.map((l) => (
        <button
          key={l}
          type="button"
          aria-pressed={locale === l}
          // Load the target language's glyphs first so the switch never flashes a fallback font.
          onClick={() => void loadFonts(l).then(() => setLocale(l))}
          className={cx(
            // Same flush selection style as the navigation (no inner rounded pill).
            'px-2.5 py-1 text-xs font-semibold uppercase',
            locale === l ? 'bg-selected text-on-selected' : 'text-ink-muted hover:bg-surface-muted',
          )}
        >
          {l}
        </button>
      ))}
    </div>
  )
}

export function ThemeToggle() {
  const t = useT()
  const theme = useUiStore((s) => s.theme)
  const setTheme = useUiStore((s) => s.setTheme)
  // The label describes the action: in light mode the button switches to dark.
  const label = t(theme === 'light' ? 'header.themeToDark' : 'header.themeToLight')
  return (
    <Button
      variant="ghost"
      size="sm"
      aria-label={label}
      title={label}
      onClick={() => setTheme(theme === 'light' ? 'dark' : 'light')}
    >
      {theme === 'light' ? (
        <Moon className="size-5" aria-hidden />
      ) : (
        <Sun className="size-5" aria-hidden />
      )}
    </Button>
  )
}

export function AlertSoundToggle() {
  const t = useT()
  const on = useUiStore((s) => s.alertSound)
  const setAlertSound = useUiStore((s) => s.setAlertSound)
  const label = t(on ? 'header.alertSoundOn' : 'header.alertSoundOff')
  return (
    <Button
      variant="ghost"
      size="sm"
      aria-pressed={on}
      aria-label={label}
      title={label}
      onClick={() => {
        if (!on) {
          // This click is the user gesture that lets the browser play audio later.
          unlockAudio()
          playAlert()
        }
        setAlertSound(!on)
      }}
    >
      {on ? <Volume2 className="size-5" aria-hidden /> : <VolumeX className="size-5" aria-hidden />}
    </Button>
  )
}

export function SettingsButton({ onClick }: { onClick?: () => void }) {
  const t = useT()
  const setSettingsOpen = useUiStore((s) => s.setSettingsOpen)
  return (
    <Button
      variant="ghost"
      size="sm"
      onClick={() => {
        onClick?.()
        setSettingsOpen(true)
      }}
    >
      <Settings className="size-5" aria-hidden />
      {t('nav.settings')}
    </Button>
  )
}
