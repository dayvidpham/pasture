export type TranscriptAvailability = 'available' | 'none' | 'unknown'

/** A catalog the session table can paint. Loading and error are both unknown:
 *  absence is "none" only after a complete catalog has loaded. */
export type TranscriptCatalogView =
  | { status: 'loading' }
  | { status: 'error' }
  | { status: 'ready'; ids: ReadonlySet<string>; truncated: boolean }

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
