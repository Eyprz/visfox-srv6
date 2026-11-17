import { Lock } from 'lucide-react'
import { useEffect, useState, type ReactNode } from 'react'
import { useT } from '../../i18n'
import { api, ApiError, request } from '../../lib/api'
import { notifyError } from '../../lib/errors'
import type {
  DriftAction,
  DynamicConfig,
  DynamicConfigPatch,
  FieldError,
  LogLevel,
  SystemConfig,
} from '../../lib/types'
import { useConfigStore } from '../../stores/config'
import { useToastStore } from '../../stores/toasts'
import { useUiStore } from '../../stores/ui'
import { Button } from '../common/Button'
import { Modal } from '../common/Modal'

const LOG_LEVELS: LogLevel[] = ['debug', 'info', 'warn', 'error']
const DRIFT_ACTIONS: DriftAction[] = ['repair', 'report']

/** Form values as strings so number fields can be empty while typing. */
export type ConfigForm = {
  sse_interval_ms: string
  metrics_dump_interval_s: string
  log_level: LogLevel
  assurance_interval_s: string
  drift_action: DriftAction
}

export const toForm = (d: DynamicConfig): ConfigForm => ({
  sse_interval_ms: String(d.sse_interval_ms),
  metrics_dump_interval_s: String(d.metrics_dump_interval_s),
  log_level: d.log_level,
  assurance_interval_s: String(d.assurance_interval_s),
  drift_action: d.drift_action,
})

/** Only the changed fields (PATCH semantics); null when a number field is not a number. */
export function diffConfig(base: DynamicConfig, form: ConfigForm): DynamicConfigPatch | null {
  const num = (v: string) => (v.trim() === '' ? NaN : Number(v))
  const sse = num(form.sse_interval_ms)
  const dump = num(form.metrics_dump_interval_s)
  const assurance = num(form.assurance_interval_s)
  if ([sse, dump, assurance].some(Number.isNaN)) return null
  const patch: DynamicConfigPatch = {}
  if (sse !== base.sse_interval_ms) patch.sse_interval_ms = sse
  if (dump !== base.metrics_dump_interval_s) patch.metrics_dump_interval_s = dump
  if (form.log_level !== base.log_level) patch.log_level = form.log_level
  if (assurance !== base.assurance_interval_s) patch.assurance_interval_s = assurance
  if (form.drift_action !== base.drift_action) patch.drift_action = form.drift_action
  return patch
}

const inputClass =
  'border-line-strong bg-surface text-ink focus-visible:outline-accent w-full rounded-md border px-2.5 py-1.5 text-sm tabular-nums focus-visible:outline-2 aria-invalid:border-health-critical'

function Field({
  label,
  hint,
  error,
  children,
}: {
  label: string
  hint?: string
  error?: string
  children: ReactNode
}) {
  return (
    <label className="block text-sm">
      <span className="text-ink-muted mb-1 block text-xs font-semibold">{label}</span>
      {children}
      {error ? (
        <span className="text-health-critical mt-0.5 block text-xs">{error}</span>
      ) : (
        hint && <span className="text-ink-subtle mt-0.5 block text-xs">{hint}</span>
      )}
    </label>
  )
}

/**
 * System settings (GET / PATCH /config). The dynamic, non-critical settings are
 * editable and take effect at once (the IEL broadcasts config_changed); the CLI / environment settings
 * are read-only. Only changed fields are sent. A change from another client refreshes an untouched form.
 */
export function SettingsModal() {
  const t = useT()
  const open = useUiStore((s) => s.settingsOpen)
  const setOpen = useUiStore((s) => s.setSettingsOpen)
  const live = useConfigStore((s) => s.dynamic)
  const [config, setConfig] = useState<SystemConfig | null>(null)
  const [form, setForm] = useState<ConfigForm | null>(null)
  const [touched, setTouched] = useState(false)
  const [saving, setSaving] = useState(false)
  const [errors, setErrors] = useState<FieldError[]>([])

  // Load on open (the immutable part is only available here).
  useEffect(() => {
    if (!open) return
    let alive = true
    setConfig(null)
    setForm(null)
    setTouched(false)
    setErrors([])
    request(api.GET('/config'))
      .then((c) => {
        if (!alive) return
        setConfig(c)
        setForm(toForm(c.dynamic))
      })
      .catch((e) => alive && notifyError(t, e))
    return () => {
      alive = false
    }
  }, [open, t])

  // Another client changed the config (SSE config_changed): follow it unless the user is editing.
  useEffect(() => {
    if (!open || !live || !config) return
    if (JSON.stringify(live) === JSON.stringify(config.dynamic)) return
    setConfig({ ...config, dynamic: live })
    if (!touched) setForm(toForm(live))
  }, [live, open, config, touched])

  const update = (patch: Partial<ConfigForm>) => {
    setForm((f) => (f ? { ...f, ...patch } : f))
    setTouched(true)
  }
  const patch = config && form ? diffConfig(config.dynamic, form) : null
  const changed = patch !== null && Object.keys(patch).length > 0
  const errorOf = (field: string) =>
    errors.find((e) => e.field === field || e.field?.startsWith(`${field}.`))?.message

  const save = async () => {
    if (!patch || !changed) return
    setSaving(true)
    setErrors([])
    try {
      const result = await request(api.PATCH('/config', { body: patch }))
      useConfigStore.getState().setDynamic(result.dynamic)
      useToastStore.getState().push({ tone: 'success', title: t('settings.saved') })
      setOpen(false)
    } catch (e) {
      if (e instanceof ApiError && Array.isArray(e.details.errors))
        setErrors(e.details.errors as FieldError[])
      notifyError(t, e)
    } finally {
      setSaving(false)
    }
  }

  const numberInput = (key: keyof ConfigForm, min: number, step = 1) => (
    <input
      type="number"
      min={min}
      step={step}
      className={inputClass}
      aria-invalid={Boolean(errorOf(key)) || undefined}
      value={form?.[key] ?? ''}
      onChange={(e) => update({ [key]: e.target.value })}
    />
  )

  return (
    <Modal
      open={open}
      onOpenChange={setOpen}
      title={t('settings.title')}
      widthClassName="max-w-2xl"
      footer={
        <>
          <Button onClick={() => setOpen(false)}>{t('action.cancel')}</Button>
          <Button variant="primary" disabled={!changed} pending={saving} onClick={save}>
            {t('action.save')}
          </Button>
        </>
      }
    >
      {!config || !form ? (
        <p className="text-ink-muted text-sm">{t('settings.loading')}</p>
      ) : (
        <div className="flex flex-col gap-5">
          <section>
            <h3 className="mb-1 text-sm font-bold">{t('settings.dynamic')}</h3>
            <p className="text-ink-subtle mb-3 text-xs">{t('settings.dynamicHint')}</p>
            <div className="grid grid-cols-2 gap-3">
              <Field
                label={t('settings.sseInterval')}
                hint={t('settings.sseIntervalHint')}
                error={errorOf('sse_interval_ms')}
              >
                {numberInput('sse_interval_ms', 100, 100)}
              </Field>
              <Field
                label={t('settings.dumpInterval')}
                hint={t('settings.dumpIntervalHint')}
                error={errorOf('metrics_dump_interval_s')}
              >
                {numberInput('metrics_dump_interval_s', 10)}
              </Field>
              <Field label={t('settings.logLevel')} hint={t('settings.logLevelHint')}>
                <select
                  className={inputClass}
                  value={form.log_level}
                  onChange={(e) => update({ log_level: e.target.value as LogLevel })}
                >
                  {LOG_LEVELS.map((l) => (
                    <option key={l} value={l}>
                      {l.toUpperCase()}
                    </option>
                  ))}
                </select>
              </Field>
              <Field
                label={t('settings.assuranceInterval')}
                hint={t('settings.assuranceIntervalHint')}
                error={errorOf('assurance_interval_s')}
              >
                {numberInput('assurance_interval_s', 0)}
              </Field>
              <Field
                label={t('settings.driftAction')}
                hint={t('settings.driftActionHint')}
                error={errorOf('drift_action')}
              >
                <select
                  className={inputClass}
                  value={form.drift_action}
                  onChange={(e) => update({ drift_action: e.target.value as DriftAction })}
                >
                  {DRIFT_ACTIONS.map((a) => (
                    <option key={a} value={a}>
                      {t(`settings.driftAction.${a}`)}
                    </option>
                  ))}
                </select>
              </Field>
            </div>
          </section>

          <section>
            <h3 className="mb-1 flex items-center gap-1.5 text-sm font-bold">
              <Lock className="size-3.5" aria-hidden />
              {t('settings.immutable')}
            </h3>
            <p className="text-ink-subtle mb-2 text-xs">{t('settings.immutableHint')}</p>
            <dl className="border-line grid grid-cols-[auto_1fr] gap-x-4 rounded-md border px-3 py-2 text-sm">
              {(
                [
                  ['settings.listenAddr', config.immutable.listen_addr],
                  ['settings.dbPath', config.immutable.db_path],
                  ['settings.seedPath', config.immutable.seed_path ?? '-'],
                  ['settings.routerTimeout', `${config.immutable.router_timeout_ms} ms`],
                ] as const
              ).map(([key, value]) => (
                <div key={key} className="contents">
                  <dt className="text-ink-muted py-1">{t(key)}</dt>
                  <dd className="py-1 font-mono text-xs leading-5">{value}</dd>
                </div>
              ))}
            </dl>
          </section>
        </div>
      )}
    </Modal>
  )
}
