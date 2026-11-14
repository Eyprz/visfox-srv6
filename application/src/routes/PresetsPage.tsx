import { Plus } from 'lucide-react'
import { Link, useNavigate } from 'react-router'
import { Button } from '../components/common/Button'
import { useT } from '../i18n'
import { groupChainByVvf } from '../lib/vvf'
import { usePresetsStore } from '../stores/presets'
import { useUiStore } from '../stores/ui'

/**
 * Preset list: name, the chain summarized by VVF, cells using the preset, and
 * the last update. A row opens the designer (/presets/:presetId); [ + New preset ] opens /presets/new.
 * Rows are separated by rules inside one framed work area (no per-row cards).
 */
export function PresetsPage() {
  const t = useT()
  const navigate = useNavigate()
  const locale = useUiStore((s) => s.locale)
  const presetMap = usePresetsStore((s) => s.presets)
  const catalog = usePresetsStore((s) => s.catalog)
  const constraints = usePresetsStore((s) => s.constraints)
  const editingUnsupported = usePresetsStore((s) => s.editingUnsupported)
  const presets = Object.values(presetMap).sort((a, b) => a.name.localeCompare(b.name, locale))
  const dateFormat = new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' })

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-auto p-5">
      <div className="flex items-center gap-3">
        <h1 className="text-xl font-bold">{t('nav.presets')}</h1>
        <span className="text-ink-subtle text-sm">
          {t('presets.count', { count: presets.length })}
        </span>
        <Button
          variant="primary"
          className="ml-auto"
          disabled={editingUnsupported}
          title={editingUnsupported ? t('designer.editingUnsupported') : undefined}
          onClick={() => navigate('/presets/new')}
        >
          <Plus className="size-4" aria-hidden />
          {t('presets.new')}
        </Button>
      </div>

      {presets.length === 0 ? (
        <p className="text-ink-muted text-sm">{t('presets.empty')}</p>
      ) : (
        <div className="border-line bg-surface overflow-x-auto rounded-lg border shadow-sm">
          <table className="w-full min-w-[40rem] border-collapse text-sm">
            <thead>
              <tr className="border-line text-ink-subtle border-b text-left text-[0.6875rem] font-semibold tracking-wider uppercase">
                <th scope="col" className="px-4 py-2.5">
                  {t('presets.name')}
                </th>
                <th scope="col" className="px-4 py-2.5">
                  {t('presets.chain')}
                </th>
                <th scope="col" className="px-4 py-2.5">
                  {t('presets.inUse')}
                </th>
                <th scope="col" className="px-4 py-2.5">
                  {t('presets.updated')}
                </th>
              </tr>
            </thead>
            <tbody>
              {presets.map((p) => {
                const groups = groupChainByVvf(p.chain, catalog, constraints)
                const inUse = p.in_use_cell_ids ?? []
                return (
                  <tr
                    key={p.id}
                    // The whole row opens the designer; the name link keeps it keyboard reachable.
                    onClick={() => navigate(`/presets/${p.id}`)}
                    className="border-line hover:bg-surface-muted cursor-pointer border-b last:border-b-0"
                  >
                    <td className="px-4 py-3 align-top">
                      <Link
                        to={`/presets/${p.id}`}
                        onClick={(e) => e.stopPropagation()}
                        className="text-ink focus-visible:outline-accent font-semibold hover:underline focus-visible:outline-2"
                      >
                        {p.name}
                      </Link>
                      {p.description && (
                        <p className="text-ink-subtle mt-0.5 text-xs">{p.description}</p>
                      )}
                    </td>
                    <td className="px-4 py-3 align-top">
                      {groups.length === 0 ? (
                        <span className="text-ink-subtle">{t('presets.emptyChain')}</span>
                      ) : (
                        <ol className="flex flex-wrap items-center gap-1.5">
                          {groups.map((g, i) => (
                            <li key={`${g.vvfId}-${i}`} className="flex items-center gap-1.5">
                              {i > 0 && (
                                <span aria-hidden className="text-ink-subtle">
                                  →
                                </span>
                              )}
                              <span className="border-line-strong rounded border px-2 py-0.5 whitespace-nowrap">
                                <span className="font-semibold">{g.name}</span>
                                {g.functions.join(', ') !== g.name && (
                                  <span className="text-ink-muted">: {g.functions.join(', ')}</span>
                                )}
                              </span>
                            </li>
                          ))}
                        </ol>
                      )}
                    </td>
                    <td className="px-4 py-3 align-top whitespace-nowrap">
                      {inUse.length > 0 ? (
                        <span title={inUse.join(', ')}>
                          {t('presets.inUseCells', { count: inUse.length })}
                        </span>
                      ) : (
                        <span className="text-ink-subtle">{t('presets.notInUse')}</span>
                      )}
                    </td>
                    <td className="text-ink-muted px-4 py-3 align-top whitespace-nowrap tabular-nums">
                      {p.updated_at ? dateFormat.format(new Date(p.updated_at)) : '-'}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
