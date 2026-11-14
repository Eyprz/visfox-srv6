import { useT, type MessageKey } from '../../i18n'
import { cx } from '../../lib/cx'
import { useConnectionStore, type ConnectionStatus } from '../../stores/connection'

const view: Record<ConnectionStatus, { key: MessageKey; dot: string }> = {
  connecting: { key: 'connection.connecting', dot: 'bg-health-warning animate-pulse' },
  connected: { key: 'connection.connected', dot: 'bg-exec-standby' },
  reconnecting: { key: 'connection.reconnecting', dot: 'bg-health-warning animate-pulse' },
  disconnected: { key: 'connection.disconnected', dot: 'bg-health-critical' },
}

/** SSE connection status for the top bar. */
export function ConnectionIndicator() {
  const t = useT()
  const status = useConnectionStore((s) => s.status)
  const { key, dot } = view[status]
  return (
    <span role="status" className="text-ink-muted inline-flex items-center gap-2 text-sm">
      <span className={cx('size-2.5 rounded-full', dot)} aria-hidden />
      <span>SSE: {t(key)}</span>
    </span>
  )
}

/** Banner under the header while live updates are not flowing. */
export function StaleBanner() {
  const t = useT()
  const status = useConnectionStore((s) => s.status)
  const bootstrap = useConnectionStore((s) => s.bootstrap)
  if (bootstrap !== 'ready' || status === 'connected' || status === 'connecting') return null
  return (
    <div
      role="alert"
      className="border-health-warning bg-health-warning/15 border-b px-4 py-2 text-sm font-medium"
    >
      {t('connection.stale')}
    </div>
  )
}
