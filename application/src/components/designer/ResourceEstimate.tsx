import { hasMessage, useT } from '../../i18n'
import { issueText } from '../../lib/issues'
import { cx } from '../../lib/cx'
import type { PresetResourceEstimate, PresetValidationResult } from '../../lib/types'

/**
 * Bottom bar of the designer: what applying the preset being designed would
 * take with the current resources, from POST /presets/validate (`resources`). Feedback only: TED
 * resources never block designing or saving; they are checked when the preset is assigned to a cell.
 */
export function ResourceEstimate({
  result,
  pending,
}: {
  result: PresetValidationResult | null
  pending: boolean
}) {
  const t = useT()
  const r = result?.resources
  // VVF requirements carry the VVF name; other kinds use the translated kind when the UI knows it.
  const requirementLabel = (req: PresetResourceEstimate['requirements'][number]) => {
    const key = `resource.kind.${req.kind}`
    return req.kind !== 'vvf_node' && hasMessage(key) ? t(key) : req.label
  }
  return (
    <section
      aria-label={t('estimate.title')}
      aria-busy={pending || undefined}
      className={cx(
        'border-line bg-surface shrink-0 border-t px-4 py-2.5 text-sm',
        pending && 'opacity-70',
      )}
    >
      <h3 className="text-ink-subtle mb-1.5 text-[0.6875rem] font-semibold tracking-wider uppercase">
        {t('estimate.title')}
      </h3>
      {!r ? (
        <p className="text-ink-muted">{t('estimate.needsValidChain')}</p>
      ) : (
        <div className="flex flex-wrap items-start gap-x-8 gap-y-2">
          <div>
            <p
              className={cx(
                'font-bold',
                r.available_streams === 0 ? 'text-health-critical' : 'text-ink',
              )}
            >
              {r.available_streams === null
                ? t('estimate.newUnlimited')
                : t('estimate.newStreams', { count: r.available_streams })}
            </p>
            <p
              className={cx(
                'text-xs',
                r.max_streams === 0 ? 'text-health-critical font-semibold' : 'text-ink-muted',
              )}
            >
              {r.max_streams === null
                ? t('estimate.maxUnlimited')
                : r.max_streams === 0
                  ? t('estimate.neverFits')
                  : t('estimate.maxStreams', { count: r.max_streams })}
            </p>
          </div>
          {r.requirements.length > 0 && (
            <ul className="flex flex-col gap-0.5 text-xs">
              {r.requirements.map((req) => (
                <li key={`${req.kind}:${req.group ?? ''}`}>
                  <span className="font-semibold">{requirementLabel(req)}</span>
                  <span className="text-ink-muted">
                    {' '}
                    {t('estimate.requirement', {
                      perStream: req.per_stream,
                      free: req.free_count,
                      total: req.pool_count,
                    })}
                  </span>
                </li>
              ))}
            </ul>
          )}
          <div className="min-w-0 flex-1 text-xs">
            <p>
              <span className="font-semibold">
                {t('estimate.assignable', { count: r.assignable_cell_ids.length })}
              </span>
              {r.assignable_cell_ids.length > 0 && (
                <span className="text-ink-muted"> {r.assignable_cell_ids.join(', ')}</span>
              )}
            </p>
            {r.blocker && <p className="text-health-critical">{issueText(t, r.blocker)}</p>}
          </div>
        </div>
      )}
    </section>
  )
}
