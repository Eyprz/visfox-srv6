import * as Toast from '@radix-ui/react-toast'
import { CircleAlert, CircleCheck, Info, X } from 'lucide-react'
import { useT } from '../../i18n'
import { cx } from '../../lib/cx'
import { useToastStore, type ToastTone } from '../../stores/toasts'

const toneStyle: Record<ToastTone, { icon: typeof Info; className: string }> = {
  info: { icon: Info, className: 'border-l-log-info' },
  success: { icon: CircleCheck, className: 'border-l-exec-standby' },
  error: { icon: CircleAlert, className: 'border-l-log-error' },
}

/** Renders toasts from the toast store (bottom-right). Errors stay longer than other toasts. */
export function Toaster() {
  const t = useT()
  const toasts = useToastStore((s) => s.toasts)
  const dismiss = useToastStore((s) => s.dismiss)

  return (
    <Toast.Provider swipeDirection="right">
      {toasts.map((toast) => {
        const { icon: Icon, className } = toneStyle[toast.tone]
        return (
          <Toast.Root
            key={toast.id}
            duration={toast.tone === 'error' ? 8000 : 4000}
            onOpenChange={(open) => !open && dismiss(toast.id)}
            className={cx(
              'border-line bg-surface flex items-start gap-3 rounded-md border border-l-4 p-4 shadow-lg',
              className,
            )}
          >
            <Icon className="mt-0.5 size-5 shrink-0" aria-hidden />
            <div className="min-w-0 flex-1">
              <Toast.Title className="text-sm font-semibold">{toast.title}</Toast.Title>
              {toast.description && (
                <Toast.Description className="text-ink-muted mt-1 text-xs break-words">
                  {toast.description}
                </Toast.Description>
              )}
            </div>
            <Toast.Close
              aria-label={t('action.close')}
              className="text-ink-muted hover:bg-surface-muted rounded p-0.5"
            >
              <X className="size-4" aria-hidden />
            </Toast.Close>
          </Toast.Root>
        )
      })}
      <Toast.Viewport className="fixed right-4 bottom-4 z-50 flex w-96 max-w-[calc(100vw-2rem)] flex-col gap-2" />
    </Toast.Provider>
  )
}
