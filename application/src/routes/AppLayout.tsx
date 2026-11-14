import { Outlet } from 'react-router'
import { Button } from '../components/common/Button'
import { StaleBanner } from '../components/common/ConnectionIndicator'
import { Header } from '../components/common/Header'
import { Toaster } from '../components/common/Toaster'
import { TooltipProvider } from '../components/common/Tooltip'
import { SettingsModal } from '../components/config/SettingsModal'
import { useCriticalAlert } from '../hooks/useCriticalAlert'
import { useLiveSync } from '../hooks/useLiveSync'
import { useDocumentLang, useT } from '../i18n'
import { useDocumentTheme } from '../lib/theme'
import { useConnectionStore } from '../stores/connection'

export function AppLayout() {
  useLiveSync()
  useCriticalAlert()
  useDocumentLang()
  useDocumentTheme()
  const t = useT()
  const bootstrap = useConnectionStore((s) => s.bootstrap)

  return (
    <TooltipProvider>
      <div className="bg-canvas text-ink flex h-screen flex-col">
        <Header />
        <StaleBanner />
        <main className="flex min-h-0 flex-1 flex-col">
          {bootstrap === 'loading' && (
            <p className="text-ink-muted p-4">{t('bootstrap.loading')}</p>
          )}
          {bootstrap === 'failed' && (
            <div role="alert" className="flex items-center gap-4 p-4">
              <p className="text-log-error font-medium">{t('bootstrap.failed')}</p>
              <Button onClick={() => window.location.reload()}>{t('action.retry')}</Button>
            </div>
          )}
          {bootstrap === 'ready' && <Outlet />}
        </main>
      </div>
      <SettingsModal />
      <Toaster />
    </TooltipProvider>
  )
}
