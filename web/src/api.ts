import type { components } from './generated/api'

export type SessionView = components['schemas']['SessionView']
export type SessionBindingInterval = components['schemas']['SessionBindingInterval']
export type EpochView = components['schemas']['EpochView']
export type InspectPayload = components['schemas']['InspectPayload']
export type EpochPage = components['schemas']['EpochPage']
export type PeasantIntegration = components['schemas']['PeasantIntegration']
export type ProjectView = components['schemas']['ProjectView']
export type ProjectBindingInterval = components['schemas']['ProjectBindingInterval']
export type ProjectRole = components['schemas']['ProjectBinding']['role']
// Reuse the generated request contracts rather than hand-writing a second
// shape; the OpenAPI artifact is the single source of truth.
export type ProjectBindBody = components['schemas']['ProjectBindRequest']
export type ProjectUnbindBody = components['schemas']['ProjectUnbindRequest']
export type PeasantSettingsBody = components['schemas']['PeasantSettingsRequest']

export type SessionsFrame = {
  type: 'sessions'
  revision: number
  asOf: string
  payload: {
    items: SessionView[]
    next?: string
    // Present on frames from a build that reports integration health. Optional
    // so a browser connected to an older frame shape degrades to "unknown".
    peasantIntegration?: PeasantIntegration
  }
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

async function readResponse<T>(response: Response): Promise<T> {
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

async function requestJSON<T>(path: string, signal?: AbortSignal): Promise<T> {
  const response = await fetch(path, { signal, headers: { accept: 'application/json' } })
  return readResponse<T>(response)
}

async function sendJSON<T>(path: string, body: unknown, signal?: AbortSignal): Promise<T> {
  const response = await fetch(path, {
    method: 'POST',
    signal,
    headers: { accept: 'application/json', 'content-type': 'application/json' },
    body: JSON.stringify(body),
  })
  return readResponse<T>(response)
}

export function fetchInspect(signal?: AbortSignal): Promise<InspectPayload> {
  return requestJSON<InspectPayload>('/api/v1/inspect', signal)
}

/**
 * Turn the runtime Peasant project-enrichment switch on or off. The response is
 * the updated integration state, so the console paints the switch from the
 * server's answer instead of guessing. A 409 carries the source's actionable
 * open failure; the switch stays off on the server.
 */
export function setPeasantEnrichment(
  enabled: boolean,
  signal?: AbortSignal,
): Promise<PeasantIntegration> {
  return sendJSON<PeasantIntegration>('/api/v1/settings/peasant', { enabled }, signal)
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

/**
 * Fetch one project's active mapping and interval history. A 404
 * `project_not_found` is an ordinary "this project was never mapped" answer;
 * callers classify it instead of treating it as a transport failure.
 */
export function fetchProject(projectHash: string, signal?: AbortSignal): Promise<ProjectView> {
  return requestJSON<ProjectView>(
    `/api/v1/projects/${encodeURIComponent(projectHash)}`,
    signal,
  )
}

/**
 * Bind a project to an epoch. `expectedBinding` makes the write conditional on
 * the mapping the caller last saw, so a concurrent rebind is refused with a
 * 409 instead of silently overwriting it.
 */
export function bindProject(
  projectHash: string,
  body: ProjectBindBody,
  signal?: AbortSignal,
): Promise<ProjectView> {
  return sendJSON<ProjectView>(
    `/api/v1/projects/${encodeURIComponent(projectHash)}/bind`,
    body,
    signal,
  )
}

/** Close a project's active mapping, keeping its interval history. */
export function unbindProject(
  projectHash: string,
  body: ProjectUnbindBody = {},
  signal?: AbortSignal,
): Promise<ProjectView> {
  return sendJSON<ProjectView>(
    `/api/v1/projects/${encodeURIComponent(projectHash)}/unbind`,
    body,
    signal,
  )
}

export function socketURL(): string {
  const scheme = window.location.protocol === 'https:' ? 'wss' : 'ws'
  return `${scheme}://${window.location.host}/api/v1/ws`
}

export function subscribe(ws: WebSocket, topic: 'sessions' | 'epochs') {
  ws.send(JSON.stringify({ type: 'subscribe', topic }))
}
