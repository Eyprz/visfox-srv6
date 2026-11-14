import * as Dialog from '@radix-ui/react-dialog'
import { X } from 'lucide-react'
import type { ReactNode } from 'react'
import { useT } from '../../i18n'
import { cx } from '../../lib/cx'

export type ModalProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: ReactNode
  description?: ReactNode
  children?: ReactNode
  footer?: ReactNode
  /** Tailwind max-width class, e.g. 'max-w-lg'. */
  widthClassName?: string
  /** Called when the dialog opens; use it to move focus (default: first focusable element). */
  onOpenAutoFocus?: (event: Event) => void
}

/** Accessible modal dialog (focus trap, Esc to close) built on Radix Dialog. */
export function Modal({
  open,
  onOpenChange,
  title,
  description,
  children,
  footer,
  widthClassName = 'max-w-lg',
  onOpenAutoFocus,
}: ModalProps) {
  const t = useT()
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="bg-scrim fixed inset-0 z-40" />
        <Dialog.Content
          onOpenAutoFocus={onOpenAutoFocus}
          className={cx(
            'fixed top-1/2 left-1/2 z-50 w-[calc(100vw-2rem)] -translate-x-1/2 -translate-y-1/2',
            'border-line bg-surface rounded-lg border p-6 shadow-xl',
            widthClassName,
          )}
        >
          <div className="flex items-start justify-between gap-4">
            <Dialog.Title className="text-lg font-semibold">{title}</Dialog.Title>
            <Dialog.Close
              className="text-ink-muted hover:bg-surface-muted rounded p-1"
              aria-label={t('action.close')}
            >
              <X className="size-5" aria-hidden />
            </Dialog.Close>
          </div>
          {description ? (
            <Dialog.Description className="text-ink-muted mt-2 text-sm">
              {description}
            </Dialog.Description>
          ) : (
            <Dialog.Description className="sr-only">{title}</Dialog.Description>
          )}
          {children && <div className="mt-4">{children}</div>}
          {footer && <div className="mt-6 flex justify-end gap-2">{footer}</div>}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}
