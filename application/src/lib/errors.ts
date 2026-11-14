import { errorCodeKey, type Translate } from '../i18n'
import { useToastStore } from '../stores/toasts'
import { ApiError } from './api'
import { issueText } from './issues'
import type { ValidationIssue } from './types'

/**
 * Whether the IEL does not implement the operation at its current stage (501, e.g. preset writes
 * while it serves predefined presets). A 501 without a body counts too.
 */
export function isNotImplemented(error: unknown): boolean {
  return error instanceof ApiError && (error.status === 501 || error.code === 'NOT_IMPLEMENTED')
}

/** Localized, operator-facing text for any thrown error. The IEL `message` is kept as the detail. */
export function describeError(t: Translate, error: unknown): { title: string; detail?: string } {
  if (error instanceof ApiError) {
    if (error.isNetworkError) return { title: t('error.network') }
    if (!error.code && isNotImplemented(error)) {
      return { title: t('errorCode.NOT_IMPLEMENTED'), detail: error.message }
    }
    if (error.code) {
      const cellIds = Array.isArray(error.details.cell_ids) ? error.details.cell_ids.join(', ') : ''
      const base = t(errorCodeKey(error.code), { cellIds })
      // Router failures carry the rollback outcome: say whether the LKG was restored.
      const failed = error.rollbacks.filter((r) => r.result === 'failed').map((r) => r.cell_id)
      const title =
        failed.length > 0
          ? t('error.rollbackFailed', { base, cells: failed.join(', ') })
          : error.rollbacks.length > 0
            ? t('error.rollbackRestored', { base })
            : base
      // A resource shortage carries its issues (with the bottleneck nodes): show those.
      const issues = (error.details.issues as ValidationIssue[] | undefined) ?? []
      return {
        title,
        detail: issues.length > 0 ? issues.map((i) => issueText(t, i)).join(' ') : error.message,
      }
    }
    return { title: t('error.unknown', { status: error.status }), detail: error.message }
  }
  return { title: t('error.unknown', { status: '-' }), detail: String(error) }
}

/** Shows an error toast for a failed operation. */
export function notifyError(t: Translate, error: unknown): void {
  const { title, detail } = describeError(t, error)
  useToastStore.getState().push({ tone: 'error', title, description: detail })
}
