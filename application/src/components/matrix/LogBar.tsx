import { ChevronDown, ChevronUp } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useT, type MessageKey } from '../../i18n'
import { cx } from '../../lib/cx'
import type { LogEntry, LogLevel } from '../../lib/types'
import { useLogsStore } from '../../stores/logs'
import { useSelectionStore } from '../../stores/selection'
import { useUiStore } from '../../stores/ui'

/** Entries shown when expanded. */
export const LOG_BAR_LIMIT = 50

type Filter = 'all' | 'info' | 'warn' | 'error'
const filters: Filter[] = ['all', 'info', 'warn', 'error']

const levelKeys: Record<LogLevel, MessageKey> = {
  debug: 'log.debug',
  info: 'log.info',
  warn: 'log.warn',
  error: 'log.error',
}
const levelText: Record<LogLevel, string> = {
  debug: 'text-ink-subtle',
  info: 'text-log-info',
  warn: 'text-log-warn',
  error: 'text-log-error',
}

/**
 * Bottom log / alert bar. Collapsed: the latest entry on one line.
 * Expanded: the newest 50 entries, filterable by level; error rows are highlighted in red.
 * Entries come from GET /logs at start and SSE `log` afterwards (lib/sse.ts).
 */
export function LogBar() {
  const t = useT()
  const entries = useLogsStore((s) => s.entries)
  const [open, setOpen] = useState(false)
  const [filter, setFilter] = useState<Filter>('all')
  // The expanded list is an overlay. It stays open while the operator works on the matrix (to watch
  // results); the bar or Esc closes it.
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && setOpen(false)
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [open])
  const latest = entries[0]
  const shown = entries
    .filter((e) => (filter === 'all' ? e.level !== 'debug' : e.level === filter))
    .slice(0, LOG_BAR_LIMIT)

  return (
    <section
      aria-label={t('logbar.title')}
      className="border-line bg-surface relative flex shrink-0 flex-col border-t text-sm"
    >
      {open && (
        // Overlay above the bar: it never changes the layout of the screen above it, however many
        // entries arrive while it is open (fixed height, scrolls inside).
        <div className="border-line bg-surface absolute inset-x-0 bottom-full z-30 flex h-72 max-h-[60vh] flex-col border-t shadow-[0_-8px_24px_rgb(15_23_42/0.15)]">
          <div
            role="radiogroup"
            aria-label={t('logbar.filter')}
            className="border-line flex shrink-0 items-center gap-1 border-b px-4 py-1.5"
          >
            {filters.map((f) => (
              <button
                key={f}
                type="button"
                role="radio"
                aria-checked={filter === f}
                onClick={() => setFilter(f)}
                className={cx(
                  'rounded px-2.5 py-1 text-xs font-semibold',
                  filter === f
                    ? 'bg-selected text-on-selected'
                    : 'text-ink-muted hover:bg-surface-muted',
                )}
              >
                {f === 'all' ? t('logbar.all') : t(levelKeys[f])}
              </button>
            ))}
          </div>
          {shown.length === 0 ? (
            <p className="text-ink-subtle px-4 py-3">{t('logbar.empty')}</p>
          ) : (
            <ol className="min-h-0 overflow-auto">
              {shown.map((e) => (
                <li
                  key={e.id}
                  className={cx(
                    'border-line border-b px-4 py-1.5 last:border-b-0',
                    e.level === 'error' && 'bg-log-error/10',
                  )}
                >
                  <LogLine entry={e} />
                </li>
              ))}
            </ol>
          )}
        </div>
      )}
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        className={cx(
          'hover:bg-surface-muted flex h-10 w-full min-w-0 items-center gap-3 px-4 text-left',
          latest?.level === 'error' && !open && 'bg-log-error/10',
        )}
      >
        <span className="text-ink-subtle shrink-0 text-[0.6875rem] font-semibold tracking-wider uppercase">
          {t('logbar.title')}
        </span>
        <span className="min-w-0 flex-1 truncate">
          {latest ? (
            <LogLine entry={latest} inline />
          ) : (
            <span className="text-ink-subtle">{t('logbar.empty')}</span>
          )}
        </span>
        <span className="sr-only">{t(open ? 'logbar.collapse' : 'logbar.expand')}</span>
        {open ? (
          <ChevronDown className="text-ink-subtle size-4 shrink-0" aria-hidden />
        ) : (
          <ChevronUp className="text-ink-subtle size-4 shrink-0" aria-hidden />
        )}
      </button>
    </section>
  )
}

/** One entry: time, level, cell (selects it on the matrix), and the IEL message as is. */
function LogLine({ entry, inline = false }: { entry: LogEntry; inline?: boolean }) {
  const t = useT()
  const locale = useUiStore((s) => s.locale)
  const select = useSelectionStore((s) => s.select)
  const time = new Date(entry.timestamp).toLocaleTimeString(locale, { hour12: false })
  return (
    <span className={cx('flex min-w-0 items-baseline gap-2', inline && 'truncate')}>
      <span className="text-ink-subtle shrink-0 tabular-nums">{time}</span>
      <span className={cx('w-12 shrink-0 font-semibold', levelText[entry.level])}>
        {t(levelKeys[entry.level])}
      </span>
      {entry.cell_id && !inline && (
        <button
          type="button"
          onClick={() => select(entry.cell_id ?? null)}
          className="text-accent shrink-0 font-semibold hover:underline"
        >
          {entry.cell_id}
        </button>
      )}
      <span className={cx('min-w-0', inline ? 'truncate' : 'break-words')}>{entry.message}</span>
    </span>
  )
}
