import { useRef, useState, type ReactNode } from 'react'
import { useT } from '../../i18n'
import { Button } from './Button'
import { Modal } from './Modal'

export type ConfirmDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: ReactNode
  description?: ReactNode
  confirmLabel: string
  /** danger: destructive (delete). primary: normal confirmation. */
  tone?: 'danger' | 'primary'
  /** May be async; the dialog shows a busy state and closes when it resolves. */
  onConfirm: () => void | Promise<void>
}

/**
 * Confirmation dialog for risky operations (ON AIR preset change, live edit, delete).
 * Focus starts on Cancel so an accidental Enter never confirms.
 */
export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel,
  tone = 'primary',
  onConfirm,
}: ConfirmDialogProps) {
  const t = useT()
  const cancelRef = useRef<HTMLButtonElement>(null)
  const [pending, setPending] = useState(false)

  const confirm = async () => {
    setPending(true)
    try {
      await onConfirm()
      onOpenChange(false)
    } finally {
      setPending(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={(next) => !pending && onOpenChange(next)}
      title={title}
      description={description}
      onOpenAutoFocus={(event) => {
        event.preventDefault()
        cancelRef.current?.focus()
      }}
      footer={
        <>
          <Button ref={cancelRef} onClick={() => onOpenChange(false)} disabled={pending}>
            {t('action.cancel')}
          </Button>
          <Button
            variant={tone === 'danger' ? 'danger' : 'primary'}
            pending={pending}
            onClick={confirm}
          >
            {confirmLabel}
          </Button>
        </>
      }
    />
  )
}
