import createClient from 'openapi-fetch'
import type { components, paths } from './schema'
import { clearToken, getToken } from './token'

export class ApiError extends Error {
  status: number
  detail: string

  constructor(status: number, detail: string) {
    super(detail)
    this.name = 'ApiError'
    this.status = status
    this.detail = detail
  }
}

let unauthorizedHandler: (() => void) | undefined

export function setUnauthorizedHandler(handler: () => void): void {
  unauthorizedHandler = handler
}

export const client = createClient<paths>({ baseUrl: window.location.origin })

client.use({
  onRequest({ request }) {
    request.headers.set('Accept', 'application/json')
    const token = getToken()
    if (token) request.headers.set('Authorization', `Bearer ${token}`)
    return request
  },
})

type FetchResult<T> = {
  response: Response
  data?: T
  error?: unknown
}

export async function unwrap<T>(
  result: Promise<FetchResult<T>>,
  options: { allowUnauthorized?: boolean } = {},
): Promise<T> {
  let response: FetchResult<T>
  try {
    response = await result
  } catch {
    throw new ApiError(0, 'Unable to reach the gateway.')
  }

  const { response: res, data, error } = response
  if (res.status === 401 && !options.allowUnauthorized) {
    clearToken()
    unauthorizedHandler?.()
    throw new ApiError(401, 'Your session expired. Sign in again.')
  }
  if (error !== undefined) {
    throw new ApiError(res.status, errorMessage(error, res))
  }
  if (!res.ok) {
    throw new ApiError(res.status, res.statusText || 'Request failed.')
  }
  return data as T
}

function errorMessage(error: unknown, response: Response): string {
  const model = error as components['schemas']['ErrorModel'] | undefined
  return model?.errors?.[0]?.message || model?.detail || model?.title || response.statusText || 'Request failed.'
}

export function messageOf(error: unknown, fallback: string): string {
  if (!error) return ''
  return error instanceof ApiError ? error.detail : fallback
}
