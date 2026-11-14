import * as RadixPopover from '@radix-ui/react-popover'
import type { ReactNode } from 'react'
import { cx } from '../../lib/cx'

export type PopoverProps = {
  /** Element that opens the popover (rendered with asChild, so pass a single element). */
  trigger: ReactNode
  children: ReactNode
  open?: boolean
  onOpenChange?: (open: boolean) => void
  side?: 'top' | 'right' | 'bottom' | 'left'
  className?: string
}

/** Click-to-open panel anchored to its trigger (used by the cell menu). */
export function Popover({
  trigger,
  children,
  open,
  onOpenChange,
  side = 'bottom',
  className,
}: PopoverProps) {
  return (
    <RadixPopover.Root open={open} onOpenChange={onOpenChange}>
      <RadixPopover.Trigger asChild>{trigger}</RadixPopover.Trigger>
      <RadixPopover.Portal>
        <RadixPopover.Content
          side={side}
          sideOffset={6}
          collisionPadding={8}
          className={cx(
            'border-line bg-surface z-30 rounded-lg border p-4 shadow-lg focus:outline-none',
            className,
          )}
        >
          {children}
        </RadixPopover.Content>
      </RadixPopover.Portal>
    </RadixPopover.Root>
  )
}
