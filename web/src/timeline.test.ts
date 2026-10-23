import { describe, expect, it } from 'vitest'

import type { SessionBindingInterval } from './api'
import {
  epochLabel,
  formatTimelineDuration,
  projectSessionTimeline,
} from './timeline'

function interval(
  bindingId: string,
  boundAt: string,
  overrides: Partial<SessionBindingInterval> = {},
): SessionBindingInterval {
  return {
    bindingId,
    boundAt,
    source: 'explicit',
    confidence: 'verified',
    role: 'worker',
    epoch: { id: 'file://epoch/one', title: 'Epoch One' },
    ...overrides,
  }
}

describe('projectSessionTimeline', () => {
  it('orders segments by boundAt and breaks ties by bindingId', () => {
    const segments = projectSessionTimeline([
      interval('b-late', '2026-01-01T12:00:00Z'),
      interval('b-early', '2026-01-01T10:00:00Z'),
      interval('b-tie-b', '2026-01-01T11:00:00Z'),
      interval('b-tie-a', '2026-01-01T11:00:00Z'),
    ])

    expect(segments.map((segment) => segment.bindingId)).toEqual([
      'b-early',
      'b-tie-a',
      'b-tie-b',
      'b-late',
    ])
    expect(segments.map((segment) => segment.startMs)).toEqual([
      Date.parse('2026-01-01T10:00:00Z'),
      Date.parse('2026-01-01T11:00:00Z'),
      Date.parse('2026-01-01T11:00:00Z'),
      Date.parse('2026-01-01T12:00:00Z'),
    ])
  })

  it('keeps an open interval open with no invented duration', () => {
    const [segment] = projectSessionTimeline([
      interval('b-open', '2026-01-01T10:00:00Z', {
        source: 'inferred',
        confidence: 'correlated',
        role: 'owner-responsibility',
      }),
    ])

    expect(segment.open).toBe(true)
    expect(segment.start).toBe('2026-01-01T10:00:00Z')
    expect(segment.end).toBeNull()
    expect(segment.startMs).toBe(Date.parse('2026-01-01T10:00:00Z'))
    expect(segment.endMs).toBeNull()
    expect(segment.durationMs).toBeNull()
    expect(segment.endReason).toBeNull()
    expect(segment.source).toBe('inferred')
    expect(segment.confidence).toBe('correlated')
    expect(segment.role).toBe('owner-responsibility')
    expect(formatTimelineDuration(segment.durationMs)).toBe('open')
  })

  it('closes an interval and carries its end reason and duration', () => {
    const [segment] = projectSessionTimeline([
      interval('b-closed', '2026-01-01T10:00:00Z', {
        unboundAt: '2026-01-01T11:30:00Z',
        endReason: '  unbind  ',
      }),
    ])

    expect(segment.open).toBe(false)
    expect(segment.end).toBe('2026-01-01T11:30:00Z')
    expect(segment.endMs).toBe(Date.parse('2026-01-01T11:30:00Z'))
    expect(segment.durationMs).toBe(90 * 60 * 1000)
    expect(segment.endReason).toBe('unbind')
    expect(formatTimelineDuration(segment.durationMs)).toBe('1h 30m')
  })

  it('keeps explicit and inferred provenance distinct with epoch label/id', () => {
    const segments = projectSessionTimeline([
      interval('explicit', '2026-01-01T10:00:00Z', {
        source: 'explicit',
        confidence: 'verified',
        epoch: { id: 'file://epoch/explicit', title: 'Explicit Epoch' },
      }),
      interval('inferred', '2026-01-01T11:00:00Z', {
        source: 'inferred',
        confidence: 'observed',
        role: 'owner-responsibility',
        epoch: { id: 'file://epoch/inferred' },
      }),
    ])

    expect(segments.map((segment) => segment.bindingId)).toEqual(['explicit', 'inferred'])
    expect(segments[0]).toMatchObject({
      source: 'explicit',
      confidence: 'verified',
      epochId: 'file://epoch/explicit',
      epochLabel: 'Explicit Epoch',
    })
    expect(segments[1]).toMatchObject({
      source: 'inferred',
      confidence: 'observed',
      role: 'owner-responsibility',
      epochId: 'file://epoch/inferred',
      epochLabel: 'file://epoch/inferred',
    })
  })

  it('clamps an inverted end to a zero-duration closed segment', () => {
    const [segment] = projectSessionTimeline([
      interval('b-inverted', '2026-01-01T12:00:00Z', {
        unboundAt: '2026-01-01T11:00:00Z',
        endReason: 'unbind',
      }),
    ])

    expect(segment.open).toBe(false)
    expect(segment.endMs).toBe(Date.parse('2026-01-01T12:00:00Z'))
    expect(segment.durationMs).toBe(0)
    expect(formatTimelineDuration(segment.durationMs)).toBe('0ms')
  })

  it('returns an empty projection for no intervals', () => {
    expect(projectSessionTimeline([])).toEqual([])
  })
})

describe('epochLabel', () => {
  it('uses the title when present and falls back to the id', () => {
    expect(epochLabel(undefined)).toBe('unbound')
    expect(epochLabel({ id: 'file://epoch/raw' })).toBe('file://epoch/raw')
    expect(epochLabel({ id: 'file://epoch/titled', title: '  Named  ' })).toBe('Named')
  })
})

describe('formatTimelineDuration', () => {
  it('renders open and closed durations without a clock', () => {
    expect(formatTimelineDuration(null)).toBe('open')
    expect(formatTimelineDuration(0)).toBe('0ms')
    expect(formatTimelineDuration(-10)).toBe('0ms')
    expect(formatTimelineDuration(250)).toBe('250ms')
    expect(formatTimelineDuration(1500)).toBe('1s')
    expect(formatTimelineDuration(61_000)).toBe('1m 1s')
    expect(formatTimelineDuration(90 * 60 * 1000)).toBe('1h 30m')
  })
})
