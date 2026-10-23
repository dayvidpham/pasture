import type { components } from './generated/api'

export type SessionView = components['schemas']['SessionView']
export type EpochView = components['schemas']['EpochView']
export type InspectPayload = components['schemas']['InspectPayload']
export type SessionPage = components['schemas']['Page']
export type EpochPage = components['schemas']['EpochPage']

export type SessionsFrame = {
  type: 'sessions'
  revision: number
  asOf: string
  payload: { items: SessionView[]; next?: string }
}

export type EpochsFrame = {
  type: 'epochs'
  revision: number
  asOf: string
  payload: { items: EpochView[] }
}

export type WireFrame =
  | { type: 'ready'; topics: string[] }
  | SessionsFrame
  | EpochsFrame
  | { type: 'error'; error: { code: string; message: string } }
  | { type: 'pong' }
  | { type: 'unsubscribed'; topic: string }

type ErrorEnvelope = { error?: { code?: string; message?: string } }

/** ApiError keeps the HTTP status and machine code beside the message so the
 *  console can explain a specific refusal instead of printing one generic line. */
export class ApiError extends Error {
  readonly status: number
  readonly code: string

  constructor(status: number, code: string, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }
}

async function requestJSON<T>(path: string, signal?: AbortSignal): Promise<T> {
  const response = await fetch(path, { signal, headers: { accept: 'application/json' } })
  if (!response.ok) {
    let code = 'http_error'
    let detail = `${response.status} ${response.statusText}`
    try {
      const envelope = (await response.json()) as ErrorEnvelope
      if (envelope.error?.code) code = envelope.error.code
      if (envelope.error?.message) detail = envelope.error.message
    } catch {
      // keep the status line when the body is not an error envelope.
    }
    throw new ApiError(response.status, code, detail)
  }
  return (await response.json()) as T
}

export function fetchInspect(signal?: AbortSignal): Promise<InspectPayload> {
  return requestJSON<InspectPayload>('/api/v1/inspect', signal)
}

export function fetchSessions(epoch?: string, signal?: AbortSignal): Promise<SessionPage> {
  const params = new URLSearchParams({ limit: '100' })
  if (epoch) params.set('epoch', epoch)
  return requestJSON<SessionPage>(`/api/v1/sessions?${params.toString()}`, signal)
}

/**
 * Fetch one durable Peasant transcript through Pasture's server-side proxy.
 * The return type is deliberately `unknown`: the payload crosses the schema
 * boundary in transcript.ts before any component sees it.
 */
export function fetchTranscript(
  harness: string,
  nativeSessionId: string,
  signal?: AbortSignal,
): Promise<unknown> {
  const path = `/api/v1/sessions/${encodeURIComponent(harness)}/${encodeURIComponent(nativeSessionId)}/transcript`
  return requestJSON<unknown>(path, signal)
}

export function socketURL(): string {
  const scheme = window.location.protocol === 'https:' ? 'wss' : 'ws'
  return `${scheme}://${window.location.host}/api/v1/ws`
}

export function subscribe(ws: WebSocket, topic: 'sessions' | 'epochs') {
  ws.send(JSON.stringify({ type: 'subscribe', topic }))
}
