import { useT, type MessageKey } from '../../i18n'
import { cx } from '../../lib/cx'
import type { ExecutionState } from '../../lib/types'

const styles: Record<ExecutionState, string> = {
  active: 'bg-exec-active text-white',
  standby: 'bg-exec-standby text-white',
  off: 'bg-surface-muted text-ink-subtle border border-line',
  disconnected: 'bg-exec-disconnected text-white',
}
const labels: Record<ExecutionState, MessageKey> = {
  active: 'exec.active',
  standby: 'exec.standby',
  off: 'exec.off',
  disconnected: 'exec.disconnected',
}

export type StateBadgeProps = {
  state: ExecutionState
  /** When set the badge is a button (STANDBY -> Take). */
  onClick?: () => void
  pending?: boolean
  className?: string
}

/** Execution state badge: ON AIR (red tally), STANDBY (green), OFF, DISCONNECTED. */
export function StateBadge({ state, onClick, pending = false, className }: StateBadgeProps) {
  const t = useT()
  const content = <>{t(labels[state])}</>
  const base = cx(
    'inline-flex items-center justify-center gap-1 rounded px-2 py-1 text-xs font-bold tracking-wide',
    styles[state],
    pending && 'animate-pulse',
    className,
  )
  if (!onClick) return <span className={base}>{content}</span>
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={pending}
      aria-busy={pending || undefined}
      className={cx(
        base,
        'focus-visible:outline-accent cursor-pointer hover:brightness-110 focus-visible:outline-2 focus-visible:outline-offset-2 disabled:cursor-wait',
      )}
    >
      {content}
    </button>
  )
}
