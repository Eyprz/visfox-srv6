import * as Dialog from '@radix-ui/react-dialog'
import { Menu, X } from 'lucide-react'
import { NavLink } from 'react-router'
import { useT, type MessageKey } from '../../i18n'
import { cx } from '../../lib/cx'
import { useUiStore } from '../../stores/ui'
import { ConnectionIndicator } from './ConnectionIndicator'
import { AlertSoundToggle, LanguageToggle, SettingsButton, ThemeToggle } from './HeaderControls'

// Top bar + collapsible sidebar.
// >= 1280px (Tailwind `xl`): everything in the top bar. Narrower: menu and controls move to a left drawer.

const menu: { to: string; key: MessageKey; end: boolean }[] = [
  { to: '/', key: 'nav.matrix', end: true },
  { to: '/topology', key: 'nav.topology', end: false },
  // Active on /presets and the designer (/presets/new, /presets/:id).
  { to: '/presets', key: 'nav.presets', end: false },
]

function NavItems({
  vertical = false,
  onNavigate,
}: {
  vertical?: boolean
  onNavigate?: () => void
}) {
  const t = useT()
  return (
    <>
      {menu.map((item) => (
        <NavLink
          key={item.to}
          to={item.to}
          end={item.end}
          onClick={onNavigate}
          // Selected item: a flush row filling the bar height / drawer width (no margin, no rounding).
          className={({ isActive }) =>
            cx(
              'flex items-center px-4 text-sm font-medium transition-colors',
              vertical ? 'h-12' : 'h-full',
              isActive
                ? 'bg-selected text-on-selected'
                : 'text-ink-muted hover:bg-surface-muted hover:text-ink',
            )
          }
        >
          {t(item.key)}
        </NavLink>
      ))}
    </>
  )
}

export function Header() {
  const t = useT()
  const sidebarOpen = useUiStore((s) => s.sidebarOpen)
  const setSidebarOpen = useUiStore((s) => s.setSidebarOpen)
  const close = () => setSidebarOpen(false)

  return (
    <header className="border-line bg-surface flex h-14 shrink-0 items-stretch gap-4 border-b px-4">
      <Dialog.Root open={sidebarOpen} onOpenChange={setSidebarOpen}>
        <Dialog.Trigger
          className="text-ink-muted hover:bg-surface-muted self-center rounded-md p-2 xl:hidden"
          aria-label={t('nav.openMenu')}
        >
          <Menu className="size-5" aria-hidden />
        </Dialog.Trigger>
        <Dialog.Portal>
          <Dialog.Overlay className="bg-scrim fixed inset-0 z-40 xl:hidden" />
          <Dialog.Content className="border-line bg-surface fixed inset-y-0 left-0 z-50 flex w-72 flex-col gap-4 border-r py-4 shadow-xl xl:hidden">
            <div className="flex items-center justify-between px-4">
              <Dialog.Title className="font-bold tracking-wide">{t('app.name')}</Dialog.Title>
              <Dialog.Close
                className="text-ink-muted hover:bg-surface-muted rounded p-1"
                aria-label={t('nav.closeMenu')}
              >
                <X className="size-5" aria-hidden />
              </Dialog.Close>
            </div>
            <Dialog.Description className="sr-only">{t('nav.openMenu')}</Dialog.Description>
            <nav className="flex flex-col">
              <NavItems vertical onNavigate={close} />
            </nav>
            <div className="border-line mt-auto flex flex-col items-start gap-3 border-t px-4 pt-4">
              <SettingsButton onClick={close} />
              <div className="flex items-center gap-2">
                <LanguageToggle />
                <ThemeToggle />
                <AlertSoundToggle />
              </div>
            </div>
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>

      <span className="self-center font-bold tracking-wide">{t('app.name')}</span>
      <nav className="hidden items-stretch xl:flex">
        <NavItems />
      </nav>
      <div className="ml-auto flex items-center gap-3 self-center">
        <ConnectionIndicator />
        <div className="hidden items-center gap-2 xl:flex">
          <LanguageToggle />
          <ThemeToggle />
          <AlertSoundToggle />
          <SettingsButton />
        </div>
      </div>
    </header>
  )
}
