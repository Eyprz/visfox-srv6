import { useCallback, useEffect } from 'react'
import type { ChainIssue, ErrorCode, ValidationIssue } from '../lib/types'
import { useUiStore, type Locale } from '../stores/ui'
import { en, type MessageKey } from './en'
import { ja } from './ja'

export type { MessageKey }
export type TranslateVars = Record<string, string | number>
export type Translate = (key: MessageKey, vars?: TranslateVars) => string

const dictionaries: Record<Locale, Record<MessageKey, string>> = { en, ja }

/** Pure translation used by the hook and by non-React code (e.g. tests). */
export function translate(locale: Locale, key: MessageKey, vars?: TranslateVars): string {
  const template = dictionaries[locale][key] ?? en[key]
  if (!vars) return template
  return template.replace(/\{(\w+)\}/g, (match, name: string) =>
    name in vars ? String(vars[name]) : match,
  )
}

/** Returns t(key, vars) for the current locale; components re-render when the locale changes. */
export function useT(): Translate {
  const locale = useUiStore((s) => s.locale)
  return useCallback((key, vars) => translate(locale, key, vars), [locale])
}

/** Keeps <html lang> in sync with the selected locale. */
export function useDocumentLang(): void {
  const locale = useUiStore((s) => s.locale)
  useEffect(() => {
    document.documentElement.lang = locale
  }, [locale])
}

// Codes returned by the IEL are translated by the UI; `message` from the IEL is supplementary.
export const errorCodeKey = (code: ErrorCode): MessageKey => `errorCode.${code}`
export const chainIssueKey = (code: ChainIssue['code']): MessageKey => `chainIssue.${code}`

/** Whether the dictionary has a key built at run time (codes and kinds the IEL may add later). */
export const hasMessage = (key: string): key is MessageKey => key in en

/** Translated reason for a known validation issue code, or undefined for codes the UI does not know. */
export function validationIssueText(t: Translate, issue: ValidationIssue): string | undefined {
  const kind = issue.resource_kind ?? undefined
  if (issue.code === 'RESOURCE_EXHAUSTED' && kind) {
    const specific = `resource.exhausted.${kind}`
    if (hasMessage(specific)) return t(specific)
    const kindKey = `resource.kind.${kind}`
    return t('validationIssue.RESOURCE_EXHAUSTED', {
      kind: hasMessage(kindKey) ? t(kindKey) : kind,
    })
  }
  const key = `validationIssue.${issue.code}`
  return hasMessage(key) ? t(key) : undefined
}

/** Short label of an issue for a matrix cell (per resource kind, with a generic fallback). */
export function issueShortLabel(t: Translate, issue: ValidationIssue): string {
  const key = `resource.short.${issue.resource_kind ?? ''}`
  return t(hasMessage(key) ? key : 'resource.short')
}
