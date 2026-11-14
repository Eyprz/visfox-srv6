import createClient from 'openapi-fetch'
import type { paths } from '../../../api/generated/ts/schema'
import type { ErrorCode, ErrorResponse, RollbackInfo } from './types'

// Typed client for the IEL REST API (api/openapi.yaml). Never call routers or other infrastructure directly.
// `fetch` is looked up on every call (not captured at module load) so the MSW mock can patch it later
// when it cannot use a Service Worker (insecure origins such as http://<LAN IP>).
export const api = createClient<paths>({
  baseUrl: '/api/v1',
  fetch: (request) => globalThis.fetch(request),
})

/** Error thrown by request(): an IEL ErrorResponse (status >= 400) or a network failure (status 0). */
export class ApiError extends Error {
  readonly status: number
  readonly code: ErrorCode | null
  readonly details: Record<string, unknown>
  readonly rollbacks: RollbackInfo[]

  constructor(status: number, body?: Partial<ErrorResponse> | null, cause?: unknown) {
    super(body?.message ?? (status === 0 ? 'Network error' : `HTTP ${status}`), { cause })
    this.name = 'ApiError'
    this.status = status
    this.code = body?.code ?? null
    this.details = body?.details ?? {}
    this.rollbacks = body?.rollbacks ?? []
  }

  get isNetworkError(): boolean {
    return this.status === 0
  }
}

type FetchResult<T> = { data?: T; error?: unknown; response: Response }

/**
 * Unwraps an openapi-fetch call: returns `data`, or throws ApiError.
 *
 *   const cells = await request(api.GET('/matrix'))
 */
export async function request<T>(call: Promise<FetchResult<T>>): Promise<T> {
  let result: FetchResult<T>
  try {
    result = await call
  } catch (cause) {
    throw new ApiError(0, null, cause)
  }
  if (!result.response.ok) {
    const body = isErrorResponse(result.error) ? result.error : null
    throw new ApiError(result.response.status, body)
  }
  return result.data as T
}

function isErrorResponse(value: unknown): value is ErrorResponse {
  return typeof value === 'object' && value !== null && 'code' in value && 'message' in value
}
