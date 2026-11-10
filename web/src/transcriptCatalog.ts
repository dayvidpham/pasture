export type TranscriptAvailability = 'available' | 'none' | 'unknown'

export type CatalogConnection = 'connecting' | 'live' | 'disconnected'

/**
 * The mount effect owns the first catalog read. Refetch only when the socket
 * leaves `disconnected`. The initial connecting -> live is not a reconnect.
 */
export function shouldRefetchTranscriptCatalog(
  previous: CatalogConnection,
  next: CatalogConnection,
): boolean {
  return previous === 'disconnected' && next !== 'disconnected'
}

/** How long a transcript toast stays before Fairtrade dismisses it. */
export const transcriptToastDurationMs = 5000

/** Roots is the default inventory. All includes Peasant subagents. */
export type SessionScope = 'roots' | 'all'

/** A catalog the session table can paint. Loading and error are both unknown:
 *  absence is "none" only after a complete catalog has loaded. subagents is
 *  the subset of ids whose Peasant parent is set. */
export type TranscriptCatalogView =
  | { status: 'loading' }
  | { status: 'error' }
  | { status: 'ready'; ids: ReadonlySet<string>; subagents: ReadonlySet<string>; truncated: boolean }

/**
 * A roots list hides only ids the catalog names as subagents. A session
 * Peasant does not know, and every session while the catalog is not ready,
 * stays visible: absence from the catalog is not evidence of a parent.
 */
export function sessionVisible(
  nativeSessionId: string,
  catalog: TranscriptCatalogView,
  scope: SessionScope,
): boolean {
  if (scope === 'all' || catalog.status !== 'ready') return true
  return !catalog.subagents.has(nativeSessionId)
}

/** The toast body for a session the catalog says has no transcript. The id is
 *  in the message so the operator can tell which row was refused. */
export function missingTranscriptMessage(nativeSessionId: string): string {
  return `no ingested transcript for ${nativeSessionId}; nothing was opened`
}

export function transcriptAvailability(
  nativeSessionId: string,
  catalog: TranscriptCatalogView,
): TranscriptAvailability {
  if (catalog.status !== 'ready') return 'unknown'
  if (catalog.ids.has(nativeSessionId)) return 'available'
  if (catalog.truncated) return 'unknown'
  return 'none'
}

/** The cell text for one availability. available is a check, none is an em
 *  dash, and unknown stays a word so a missing catalog is not read as none. */
export function transcriptMark(availability: TranscriptAvailability): string {
  switch (availability) {
    case 'available':
      return '✓'
    case 'none':
      return '—'
    case 'unknown':
      return 'unknown'
  }
}
