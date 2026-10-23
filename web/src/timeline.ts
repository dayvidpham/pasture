import type { SessionBindingInterval } from './api'

/** The provenance vocabulary carried by one binding interval. */
export type TimelineSource = SessionBindingInterval['source']
export type TimelineConfidence = SessionBindingInterval['confidence']

/**
 * One chronological segment of a session's binding history. `endMs` and
 * `durationMs` are null while the interval is open: the projection is pure and
 * does not invent a clock, so an open segment has no known duration.
 */
export type TimelineSegment = {
  bindingId: string
  start: string
  end: string | null
  startMs: number
  endMs: number | null
  durationMs: number | null
  open: boolean
  epochId: string | null
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
 * segments. Ordering is boundAt ascending, then bindingId ascending; an end
 * before its start is clamped to a zero-duration closed segment rather than
 * producing a negative band.
 */
export function projectSessionTimeline(
  bindings: SessionBindingInterval[],
): TimelineSegment[] {
  return bindings
    .map((interval) => {
      const startMs = parseTimestamp(interval.boundAt) ?? 0
      const open = !interval.unboundAt
      const parsedEnd = open ? null : parseTimestamp(interval.unboundAt)
      const endMs = open ? null : Math.max(startMs, parsedEnd ?? startMs)
      return {
        bindingId: interval.bindingId,
        start: interval.boundAt,
        end: open ? null : interval.unboundAt ?? null,
        startMs,
        endMs,
        durationMs: endMs === null ? null : endMs - startMs,
        open,
        epochId: interval.epoch?.id ?? null,
        epochLabel: epochLabel(interval.epoch),
        source: interval.source,
        confidence: interval.confidence,
        endReason: open ? null : interval.endReason?.trim() || null,
        role: interval.role,
      }
    })
    .sort((left, right) => {
      if (left.startMs !== right.startMs) return left.startMs - right.startMs
      if (left.bindingId === right.bindingId) return 0
      return left.bindingId < right.bindingId ? -1 : 1
    })
}

/** formatTimelineDuration renders a closed duration in the rail's short form. */
export function formatTimelineDuration(durationMs: number | null): string {
  if (durationMs === null) return 'open'
  if (!Number.isFinite(durationMs) || durationMs <= 0) return '0ms'
  if (durationMs < 1000) return `${Math.round(durationMs)}ms`
  const totalSeconds = Math.floor(durationMs / 1000)
  const hours = Math.floor(totalSeconds / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  const seconds = totalSeconds % 60
  if (hours > 0) return `${hours}h ${minutes}m`
  if (minutes > 0) return `${minutes}m ${seconds}s`
  return `${seconds}s`
}
