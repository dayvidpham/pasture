import { describe, expect, it } from 'vitest'

import {
  missingTranscriptMessage,
  sessionVisible,
  shouldRefetchTranscriptCatalog,
  transcriptAvailability,
  transcriptMark,
  type TranscriptCatalogView,
} from './transcriptCatalog'

const ready = (ids: string[], truncated = false, subagents: string[] = []): TranscriptCatalogView => ({
  status: 'ready',
  ids: new Set(ids),
  subagents: new Set(subagents),
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

describe('sessionVisible', () => {
  const catalog = ready(['root', 'child'], false, ['child'])

  it('hides a subagent from the roots list and treats an unknown id as a root', () => {
    expect(sessionVisible('child', catalog, 'roots')).toBe(false)
    expect(sessionVisible('root', catalog, 'roots')).toBe(true)
    expect(sessionVisible('not-in-peasant', catalog, 'roots')).toBe(true)
  })

  it('shows every row when the scope is all, including before the catalog is ready', () => {
    expect(sessionVisible('child', catalog, 'all')).toBe(true)
    expect(sessionVisible('child', { status: 'loading' }, 'roots')).toBe(true)
    expect(sessionVisible('child', { status: 'error' }, 'roots')).toBe(true)
  })

  it('names the refused session in the none-click toast', () => {
    expect(missingTranscriptMessage('ses_missing')).toBe(
      'no ingested transcript for ses_missing; nothing was opened',
    )
  })
})
