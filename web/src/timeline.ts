import type { SessionBindingInterval } from './api'

/** The provenance vocabulary carried by one binding interval. */
export type TimelineSource = SessionBindingInterval['source']
export type TimelineConfidence = SessionBindingInterval['confidence']

/**
 * One chronological segment of a session's binding history. `endMs` and
 * `durationMs` are null while the interval is open, and also when the start
 * cannot be parsed: the projection is pure and does not invent a clock or a
 * 1970 epoch. `end` is the clamped end instant for a known start; for an
 * unknown start it preserves the raw recorded end so the rail can show it as
 * evidence without implying a duration.
 */
export type TimelineSegment = {
  bindingId: string
  start: string
  end: string | null
  startMs: number | null
  endMs: number | null
  durationMs: number | null
  open: boolean
  inverted: boolean
  epochId: string | null
  epochTitle: string | null
  epochLabel: string
  source: TimelineSource
  confidence: TimelineConfidence
  endReason: string | null
  role: string
}

function parseTimestamp(value: string | undefined | null): number | null {
  if (!value) return null
  const parsed = Date.parse(value)
  return Number.isFinite(parsed) ? parsed : null
}

/**
 * epochLabel is the display label shared by the table, the detail list, and the
 * timeline: a title when the projection has one, otherwise the durable id.
 */
export function epochLabel(epoch?: { id: string; title?: string }): string {
  if (!epoch) return 'unbound'
  return epoch.title?.trim() || epoch.id
}

/**
 * projectSessionTimeline turns the wire intervals into chronological timeline
 * segments. Ordering is boundAt ascending, then bindingId ascending, with an
 * unparseable start sorted last. The API already orders intervals; this client
 * sort is defense in depth for callers that hand the projection an unsorted
 * array (tests, a future route).
 *
 * An end before its start is clamped to a zero-duration closed segment. The
 * segment's `end` is derived from that clamp, so the rail can never display a
 * start after its own end. An unparseable start keeps `end` as the raw recorded
 * value but reports null timing, so no 1970-based duration is invented.
 */
export function projectSessionTimeline(
  bindings: SessionBindingInterval[],
): TimelineSegment[] {
  return bindings
    .map((interval) => {
      const startMs = parseTimestamp(interval.boundAt)
      const open = !interval.unboundAt
      const parsedEnd = open ? null : parseTimestamp(interval.unboundAt)
      const inverted = startMs !== null && parsedEnd !== null && parsedEnd < startMs
      const endMs = startMs === null || open ? null : Math.max(startMs, parsedEnd ?? startMs)
      const end = open
        ? null
        : endMs !== null
          ? new Date(endMs).toISOString()
          : interval.unboundAt ?? null
      const epochTitle = interval.epoch?.title?.trim() || null
      return {
        bindingId: interval.bindingId,
        start: interval.boundAt,
        end,
        startMs,
        endMs,
        durationMs: startMs === null || endMs === null ? null : endMs - startMs,
        open,
        inverted,
        epochId: interval.epoch?.id ?? null,
        epochTitle,
        epochLabel: epochLabel(interval.epoch),
        source: interval.source,
        confidence: interval.confidence,
        endReason: open ? null : interval.endReason?.trim() || null,
        role: interval.role,
      }
    })
    .sort((left, right) => {
      if (left.startMs !== null && right.startMs !== null && left.startMs !== right.startMs) {
        return left.startMs - right.startMs
      }
      if (left.startMs === null && right.startMs !== null) return 1
      if (left.startMs !== null && right.startMs === null) return -1
      if (left.bindingId === right.bindingId) return 0
      return left.bindingId < right.bindingId ? -1 : 1
    })
}

/** formatTimelineDuration renders a closed duration in the rail's short form. */
export function formatTimelineDuration(durationMs: number | null): string {
  if (durationMs === null) return 'open'
  if (!Number.isFinite(durationMs)) return 'unknown'
  if (durationMs <= 0) return '0ms'
  if (durationMs < 1000) return `${Math.round(durationMs)}ms`
  const totalSeconds = Math.floor(durationMs / 1000)
  const hours = Math.floor(totalSeconds / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  const seconds = totalSeconds % 60
  if (hours > 0) return `${hours}h ${minutes}m`
  if (minutes > 0) return `${minutes}m ${seconds}s`
  return `${seconds}s`
}
