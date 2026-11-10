export type Route =
  | { kind: 'list' }
  | { kind: 'session'; harness: string; nativeSessionId: string }

export function listRoute(): string { return '#/' }

export function sessionRoute(harness: string, nativeSessionId: string): string {
  return `#/sessions/${encodeURIComponent(harness)}/${encodeURIComponent(nativeSessionId)}`
}

export function parseRoute(hash: string): Route {
  const match = /^#\/sessions\/([^/]+)\/([^/]+)$/.exec(hash)
  if (!match) return { kind: 'list' }
  try {
    const harness = decodeURIComponent(match[1])
    const nativeSessionId = decodeURIComponent(match[2])
    return harness && nativeSessionId ? { kind: 'session', harness, nativeSessionId } : { kind: 'list' }
  } catch {
    return { kind: 'list' }
  }
}
