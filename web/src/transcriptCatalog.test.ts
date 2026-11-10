import { describe, expect, it } from 'vitest'

import {
  shouldRefetchTranscriptCatalog,
  transcriptAvailability,
  transcriptMark,
  type TranscriptCatalogView,
} from './transcriptCatalog'

const ready = (ids: string[], truncated = false): TranscriptCatalogView => ({
  status: 'ready',
  ids: new Set(ids),
  truncated,
})

describe('shouldRefetchTranscriptCatalog', () => {
  it('refetches only on a transition out of disconnected', () => {
    expect(shouldRefetchTranscriptCatalog('connecting', 'live')).toBe(false)
    expect(shouldRefetchTranscriptCatalog('live', 'disconnected')).toBe(false)
    expect(shouldRefetchTranscriptCatalog('disconnected', 'disconnected')).toBe(false)
    expect(shouldRefetchTranscriptCatalog('disconnected', 'connecting')).toBe(true)
    expect(shouldRefetchTranscriptCatalog('disconnected', 'live')).toBe(true)
  })
})

describe('transcriptAvailability', () => {
  it('marks a listed id available and an absent id none when the catalog is complete', () => {
    const catalog = ready(['ses_fixture'])
    expect(transcriptAvailability('ses_fixture', catalog)).toBe('available')
    expect(transcriptAvailability('ses_other', catalog)).toBe('none')
    expect(transcriptMark('available')).toBe('✓')
    expect(transcriptMark('none')).toBe('—')
  })

  it('does not treat absence as none while the catalog is truncated, loading, or failed', () => {
    expect(transcriptAvailability('ses_other', ready(['ses_fixture'], true))).toBe('unknown')
    expect(transcriptAvailability('ses_fixture', ready(['ses_fixture'], true))).toBe('available')
    expect(transcriptAvailability('ses_fixture', { status: 'loading' })).toBe('unknown')
    expect(transcriptAvailability('ses_fixture', { status: 'error' })).toBe('unknown')
    expect(transcriptMark('unknown')).toBe('unknown')
  })
})
