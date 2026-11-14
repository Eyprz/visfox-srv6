import { Trash2 } from 'lucide-react'
import type { ReactNode } from 'react'
import { chainIssueKey, useT } from '../../i18n'
import { videoFormatLabel } from '../../lib/format'
import type { ChainIssue, VideoFormat } from '../../lib/types'
import { functionLabel, pinnedNodeLabel } from '../../lib/vvf'
import { useDesignerStore } from '../../stores/designer'
import { usePresetsStore } from '../../stores/presets'
import { Button } from '../common/Button'
import { ParamEditor } from './ParamEditor'

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="border-line border-b px-4 py-3">
      <h3 className="text-ink-subtle mb-2 text-[0.6875rem] font-semibold tracking-wider uppercase">
        {title}
      </h3>
      {children}
    </section>
  )
}

const inputClass =
  'border-line-strong bg-surface text-ink focus-visible:outline-accent w-full rounded-md border px-2.5 py-1.5 text-sm focus-visible:outline-2'

/**
 * Right panel of the designer: the selected function node, or the preset settings (name, description)
 * with the IEL validation result when nothing is selected. v1 has no SLA.
 */
export function DesignerSidebar({
  issues,
  localIssues,
  validating,
  locked,
}: {
  issues: ChainIssue[]
  /** Issues the UI detects before asking the IEL (e.g. functions off the IN -> OUT path). */
  localIssues: string[]
  validating: boolean
  locked: boolean
}) {
  const selectedId = useDesignerStore((s) => s.selectedId)
  return (
    <aside className="border-line bg-surface flex w-80 shrink-0 flex-col overflow-auto border-l">
      {selectedId ? (
        <NodeDetails instanceId={selectedId} issues={issues} locked={locked} />
      ) : (
        <PresetSettings
          issues={issues}
          localIssues={localIssues}
          validating={validating}
          locked={locked}
        />
      )}
    </aside>
  )
}

function PresetSettings({
  issues,
  localIssues,
  validating,
  locked,
}: {
  issues: ChainIssue[]
  localIssues: string[]
  validating: boolean
  locked: boolean
}) {
  const t = useT()
  const name = useDesignerStore((s) => s.name)
  const description = useDesignerStore((s) => s.description)
  const inUse = useDesignerStore((s) => s.inUseCellIds)
  const { setName, setDescription } = useDesignerStore.getState()

  return (
    <>
      <Section title={t('designer.preset')}>
        <label className="mb-2 block text-sm">
          <span className="text-ink-muted mb-1 block text-xs">{t('designer.name')}</span>
          <input
            className={inputClass}
            value={name}
            required
            aria-invalid={name.trim() === '' || undefined}
            onChange={(e) => setName(e.target.value)}
          />
        </label>
        <label className="block text-sm">
          <span className="text-ink-muted mb-1 block text-xs">{t('designer.description')}</span>
          <textarea
            className={inputClass}
            rows={2}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
          />
        </label>
      </Section>
      {locked && (
        <Section title={t('designer.inUse')}>
          <p className="text-sm">{t('designer.lockedBody', { cells: inUse.join(', ') })}</p>
        </Section>
      )}
      <Section title={t('designer.validation')}>
        {validating ? (
          <p className="text-ink-muted text-sm">{t('designer.validating')}</p>
        ) : issues.length === 0 && localIssues.length === 0 ? (
          <p className="text-exec-standby text-sm font-semibold">{t('designer.valid')}</p>
        ) : (
          <ul className="flex flex-col gap-1.5 text-sm">
            {localIssues.map((m) => (
              <li key={m} className="text-health-critical">
                {m}
              </li>
            ))}
            {issues.map((i, n) => (
              <li key={`${i.code}-${n}`} className="text-health-critical">
                {t(chainIssueKey(i.code))}
                <span className="text-ink-subtle block text-xs">{i.message}</span>
              </li>
            ))}
          </ul>
        )}
      </Section>
    </>
  )
}

function NodeDetails({
  instanceId,
  issues,
  locked,
}: {
  instanceId: string
  issues: ChainIssue[]
  locked: boolean
}) {
  const t = useT()
  const fn = useDesignerStore((s) => s.functions.find((f) => f.instance_id === instanceId))
  const removeFunction = useDesignerStore((s) => s.removeFunction)
  const catalog = usePresetsStore((s) => s.catalog)
  const constraints = usePresetsStore((s) => s.constraints)
  if (!fn) return null
  const item = catalog.find((c) => c.type === fn.type)
  const vvf = constraints?.vvfs.find((v) => v.id === item?.vvf_id)
  const transitions = item?.format_transitions
  const own = issues.filter((i) => i.instance_id === instanceId)

  return (
    <>
      <header className="border-line border-b px-4 py-3">
        <p className="text-lg leading-tight font-bold">{functionLabel(fn, catalog, constraints)}</p>
        <p className="text-ink-subtle text-xs">{fn.instance_id}</p>
      </header>
      <Section title={t('designer.function')}>
        <p className="text-sm">{item?.description}</p>
        <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
          <dt className="text-ink-subtle">VVF</dt>
          <dd>{vvf?.name ?? '-'}</dd>
          {/* Pinned VVFs run on the node the preset names; dynamic ones on a node the IEL assigns. */}
          <dt className="text-ink-subtle">{t('designer.node')}</dt>
          <dd>
            {vvf?.allocation === 'pinned'
              ? (pinnedNodeLabel(constraints, vvf.id, fn.vvf_node_id) ?? '-')
              : t('designer.nodeDynamic')}
          </dd>
          {/* Pass-through when the catalog has no video_format map. */}
          <dt className="text-ink-subtle">{t('designer.format')}</dt>
          <dd>
            {transitions?.video_format
              ? Object.entries(transitions.video_format)
                  .map(([from, to]) =>
                    // Same in and out: the function only takes that format (e.g. caption: 8K DG).
                    from === to
                      ? t('designer.formatOnly', { format: videoFormatLabel(to) })
                      : `${videoFormatLabel(from as VideoFormat)} -> ${videoFormatLabel(to)}`,
                  )
                  .join(', ')
              : t('designer.passThrough')}
          </dd>
        </dl>
      </Section>
      {own.length > 0 && (
        <Section title={t('designer.validation')}>
          <ul className="text-health-critical flex flex-col gap-1 text-sm">
            {own.map((i, n) => (
              <li key={n}>{t(chainIssueKey(i.code))}</li>
            ))}
          </ul>
        </Section>
      )}
      <Section title={t('designer.params')}>
        <ParamEditor key={fn.instance_id} fn={fn} item={item} />
      </Section>
      <div className="px-4 py-3">
        <Button
          variant="secondary"
          size="sm"
          disabled={locked}
          onClick={() => removeFunction(instanceId)}
        >
          <Trash2 className="size-4" aria-hidden />
          {t('designer.removeNode')}
        </Button>
      </div>
    </>
  )
}
