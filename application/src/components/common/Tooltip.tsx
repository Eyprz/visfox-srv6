import * as RadixTooltip from '@radix-ui/react-tooltip'
import type { ReactNode } from 'react'

/** Wrap the app once so all tooltips share open/close delays. */
export function TooltipProvider({ children }: { children: ReactNode }) {
  return <RadixTooltip.Provider delayDuration={300}>{children}</RadixTooltip.Provider>
}

export type TooltipProps = {
  content: ReactNode
  /** Single element; rendered with asChild. */
  children: ReactNode
  side?: 'top' | 'right' | 'bottom' | 'left'
}

export function Tooltip({ content, children, side = 'top' }: TooltipProps) {
  return (
    <RadixTooltip.Root>
      <RadixTooltip.Trigger asChild>{children}</RadixTooltip.Trigger>
      <RadixTooltip.Portal>
        <RadixTooltip.Content
          side={side}
          sideOffset={6}
          className="bg-selected text-on-selected z-50 max-w-xs rounded-md px-3 py-2 text-xs shadow-lg"
        >
          {content}
          <RadixTooltip.Arrow className="fill-selected" />
        </RadixTooltip.Content>
      </RadixTooltip.Portal>
    </RadixTooltip.Root>
  )
}
