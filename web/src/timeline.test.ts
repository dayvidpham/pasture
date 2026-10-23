import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { BindingHistory } from './App'
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

function renderBindings(bindings: SessionBindingInterval[]): string {
  return renderToStaticMarkup(createElement(BindingHistory, { bindings }))
}

/** The rendered `<li>` band for one binding id. */
function band(html: string, bindingId: string): string {
  const match = html.match(
    new RegExp(`<li[^>]*data-binding-id="${bindingId}"[^>]*>[\\s\\S]*?</li>`),
  )
  return match?.[0] ?? ''
}

/** Binding ids in rendered order. */
function renderedOrder(html: string): string[] {
  return [...html.matchAll(/data-binding-id="([^"]+)"/g)].map((match) => match[1])
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
    expect(segment.inverted).toBe(false)
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
    expect(segment.end).toBe('2026-01-01T11:30:00.000Z')
    expect(segment.endMs).toBe(Date.parse('2026-01-01T11:30:00Z'))
    expect(segment.durationMs).toBe(90 * 60 * 1000)
    expect(segment.endReason).toBe('unbind')
    expect(segment.inverted).toBe(false)
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
      epochTitle: 'Explicit Epoch',
      epochLabel: 'Explicit Epoch',
    })
    expect(segments[1]).toMatchObject({
      source: 'inferred',
      confidence: 'observed',
      role: 'owner-responsibility',
      epochId: 'file://epoch/inferred',
      epochTitle: null,
      epochLabel: 'file://epoch/inferred',
    })
  })

  it('clamps an inverted end and derives the displayed end from the clamp', () => {
    const [segment] = projectSessionTimeline([
      interval('b-inverted', '2026-01-01T12:00:00Z', {
        unboundAt: '2026-01-01T11:00:00Z',
        endReason: 'unbind',
      }),
    ])

    expect(segment.open).toBe(false)
    expect(segment.inverted).toBe(true)
    expect(segment.endMs).toBe(Date.parse('2026-01-01T12:00:00Z'))
    expect(segment.end).toBe('2026-01-01T12:00:00.000Z')
    expect(segment.end).not.toBe('2026-01-01T11:00:00Z')
    expect(segment.durationMs).toBe(0)
    expect(formatTimelineDuration(segment.durationMs)).toBe('0ms')
  })

  it('keeps an unparseable start unknown, sorts it last, and invents no duration', () => {
    const segments = projectSessionTimeline([
      interval('b-unknown', 'not-a-timestamp', {
        unboundAt: '2026-01-01T11:00:00Z',
        endReason: 'unbind',
      }),
      interval('b-known', '2026-01-01T10:00:00Z'),
    ])

    expect(segments.map((segment) => segment.bindingId)).toEqual(['b-known', 'b-unknown'])
    const unknown = segments[1]
    expect(unknown.startMs).toBeNull()
    expect(unknown.endMs).toBeNull()
    expect(unknown.durationMs).toBeNull()
    expect(unknown.end).toBe('2026-01-01T11:00:00Z')
    expect(unknown.endReason).toBe('unbind')
    expect(formatTimelineDuration(unknown.durationMs)).toBe('open')
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
  it('renders open, unknown, and closed durations without a clock', () => {
    expect(formatTimelineDuration(null)).toBe('open')
    expect(formatTimelineDuration(Number.NaN)).toBe('unknown')
    expect(formatTimelineDuration(Number.POSITIVE_INFINITY)).toBe('unknown')
    expect(formatTimelineDuration(0)).toBe('0ms')
    expect(formatTimelineDuration(-10)).toBe('0ms')
    expect(formatTimelineDuration(250)).toBe('250ms')
    expect(formatTimelineDuration(1500)).toBe('1s')
    expect(formatTimelineDuration(61_000)).toBe('1m 1s')
    expect(formatTimelineDuration(90 * 60 * 1000)).toBe('1h 30m')
  })
})

describe('BindingHistory render', () => {
  it('renders a fact-only empty state and no timeline band', () => {
    const html = renderBindings([])

    expect(html).toContain('binding history')
    expect(html).toContain('no binding interval is recorded for this session')
    expect(html).toContain('A band appears after an explicit bind')
    expect(html).toContain('an inferred band appears only while an assignment epoch is currently derived')
    expect(html).not.toContain('timeline-band')
    expect(html).not.toContain('--backfill')
  })

  it('renders chronological bands with provenance, markers, durations, and ends', () => {
    const html = renderBindings([
      interval('explicit-blank', '2026-01-01T13:00:00Z', {
        unboundAt: '2026-01-01T14:00:00Z',
        endReason: '   ',
        epoch: { id: 'file://epoch/blank' },
      }),
      interval('inferred-open', '2026-01-01T12:00:00Z', {
        source: 'inferred',
        confidence: 'observed',
        role: 'owner-responsibility',
        epoch: { id: 'file://epoch/raw' },
      }),
      interval('explicit-closed', '2026-01-01T10:00:00Z', {
        unboundAt: '2026-01-01T11:30:00Z',
        endReason: 'unbind',
        epoch: { id: 'file://epoch/titled', title: 'Titled Epoch' },
      }),
    ])

    expect(renderedOrder(html)).toEqual([
      'explicit-closed',
      'inferred-open',
      'explicit-blank',
    ])

    const closed = band(html, 'explicit-closed')
    expect(closed).toContain('data-source="explicit"')
    expect(closed).toContain('data-open="false"')
    expect(closed).toContain('Titled Epoch')
    expect(closed).toContain('>closed<')
    expect(closed).toContain('1h 30m')
    expect(closed).toContain('ended: unbind')
    expect(closed).not.toContain('epoch id:')

    const open = band(html, 'inferred-open')
    expect(open).toContain('data-source="inferred"')
    expect(open).toContain('data-open="true"')
    expect(open).toContain('owner-responsibility')
    expect(open).toContain('>open<')
    expect(open).toContain('epoch id: file://epoch/raw')
    expect(open).not.toContain('timeline-duration')

    const blank = band(html, 'explicit-blank')
    expect(blank).toContain('data-open="false"')
    expect(blank).toContain('1h 0m')
    expect(blank).not.toContain('ended:')
    expect(blank).toContain('epoch id: file://epoch/blank')
  })

  it('renders start unknown and the inverted clamp instead of a false range', () => {
    const html = renderBindings([
      interval('explicit-inverted', '2026-01-01T12:00:00Z', {
        unboundAt: '2026-01-01T11:00:00Z',
        endReason: 'unbind',
      }),
      interval('explicit-unknown', 'not-a-timestamp', {
        unboundAt: '2026-01-01T11:00:00Z',
        endReason: 'unbind',
      }),
    ])

    const inverted = band(html, 'explicit-inverted')
    expect(inverted).toContain('data-inverted="true"')
    expect(inverted).toContain('end precedes start; clamped to zero duration')
    expect(inverted).toContain('0ms')

    const unknown = band(html, 'explicit-unknown')
    expect(unknown).toContain('start unknown')
    expect(unknown).toContain('recorded end:')
    expect(unknown).not.toContain('1970')
  })
})
