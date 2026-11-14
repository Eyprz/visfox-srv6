import { useEffect, useRef, useState } from 'react'
import { api, request } from '../lib/api'
import type { FunctionInstance, PresetValidationResult } from '../lib/types'

const DEBOUNCE_MS = 300

export type ChainValidation = {
  result: PresetValidationResult | null
  pending: boolean
  /** Network / IEL failure of the validation request itself (not chain issues). */
  failed: boolean
}

/**
 * Validates the chain with the IEL on every change (POST /presets/validate).
 * Debounced; only the latest request's answer is kept. The IEL result is the authority for saving.
 */
export function useChainValidation(name: string, chain: FunctionInstance[]): ChainValidation {
  const [state, setState] = useState<ChainValidation>({
    result: null,
    pending: true,
    failed: false,
  })
  // Stable key so equal chains do not re-validate on unrelated re-renders. The name does not affect
  // chain validity, so it is read through a ref instead of re-validating on every keystroke.
  const key = JSON.stringify(chain)
  const nameRef = useRef(name)
  useEffect(() => {
    nameRef.current = name
  })

  useEffect(() => {
    let alive = true
    setState((s) => ({ ...s, pending: true }))
    const timer = setTimeout(() => {
      const body = { chain: JSON.parse(key) as FunctionInstance[] }
      request(
        api.POST('/presets/validate', {
          body: { name: nameRef.current || '-', chain: body.chain },
        }),
      )
        .then((result) => alive && setState({ result, pending: false, failed: false }))
        .catch(() => alive && setState({ result: null, pending: false, failed: true }))
    }, DEBOUNCE_MS)
    return () => {
      alive = false
      clearTimeout(timer)
    }
  }, [key])

  return state
}
