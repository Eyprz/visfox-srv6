import { useRef, useState } from 'react'
import { useT } from '../i18n'
import { api, ApiError, request } from '../lib/api'
import { notifyError } from '../lib/errors'
import type { FieldError, FunctionInstance } from '../lib/types'
import { useDesignerStore } from '../stores/designer'
import { useThrottledSend } from './useThrottledSend'

type Params = Record<string, unknown>

/**
 * Parameter editing of one function in the designer.
 * - Nodes of the saved preset: PATCH /presets/{id}/functions/{instanceId}, applied live to the cells
 *   using the preset. Sent every 100 ms at most (latest value only); flush() at the end of a gesture.
 *   On failure (400 / 502 / 504) the IEL keeps or restores the previous value, so the UI reverts to
 *   the last value the IEL accepted and shows the reason (rollback outcome included).
 * - Unsaved nodes: POST /functions/{type}/validate only; the values are saved with the preset.
 */
export function useParamEditor(fn: FunctionInstance) {
  const t = useT()
  const presetId = useDesignerStore((s) => s.presetId)
  const live = useDesignerStore(
    (s) => s.presetId !== null && s.savedInstanceIds.includes(fn.instance_id),
  )
  const setParams = useDesignerStore((s) => s.setParams)
  const [errors, setErrors] = useState<FieldError[]>([])
  // Last params the IEL accepted (or the loaded ones): the revert target after a failure.
  // The caller keys the editor by instance id, so this starts fresh for every node.
  const confirmed = useRef<Params>(fn.params)

  const sender = useThrottledSend<Params>(
    async (params) => {
      if (live && presetId) {
        const result = await request(
          api.PATCH('/presets/{presetId}/functions/{instanceId}', {
            params: { path: { presetId, instanceId: fn.instance_id } },
            body: { params },
          }),
        )
        confirmed.current = result.instance.params
        setErrors([])
      } else {
        const result = await request(
          api.POST('/functions/{type}/validate', {
            params: { path: { type: fn.type } },
            body: { params },
          }),
        )
        setErrors(result.errors ?? [])
      }
    },
    {
      onError: (error) => {
        if (live) setParams(fn.instance_id, confirmed.current, false)
        const fieldErrors =
          error instanceof ApiError && Array.isArray(error.details.errors)
            ? (error.details.errors as FieldError[])
            : []
        setErrors(fieldErrors)
        notifyError(t, error)
      },
    },
  )

  /** Applies a change locally and queues it for the IEL. */
  const change = (patch: Params) => {
    const next = { ...fn.params, ...patch }
    setParams(fn.instance_id, next, !live)
    sender.push(next)
  }

  /** End of a gesture (slider released, field committed): send the final value now. */
  const commit = () => void sender.flush()

  const errorOf = (field: string) =>
    errors.find((e) => e.field === field || e.field === `params.${field}`)?.message

  return { live, change, commit, errorOf }
}
