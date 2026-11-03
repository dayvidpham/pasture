import { describe, expect, it } from 'vitest'

import type { AttributionValue, SessionActivity, SessionExecution } from './api'
import {
  actorLabel,
  applySessionsFrame,
  applyEpochsFrame,
  appendExecutionPage,
  historyPageMatches,
  attributionText,
  executionKindLabel,
  executionRows,
  livenessRows,
} from './execution'

const observed = (value: string, field = 'model.id'): AttributionValue => ({
  status: 'observed',
  value,
  source: 'host',
  field,
})

const unavailable: AttributionValue = {
  status: 'unavailable',
  source: 'none',
  reason: 'no-evidence',
}

const unavailableRole: AttributionValue = {
  status: 'unavailable',
  source: 'none',
  reason: 'no-evidence',
  vocabulary: 'native-agent',
}

const unavailableProtocolRole: AttributionValue = {
  status: 'unavailable',
  source: 'none',
  reason: 'no-evidence',
  vocabulary: 'pasture-role',
}

function execution(overrides: Partial<SessionExecution> = {}): SessionExecution {
  return {
    occurrenceId: 12,
    observedAt: '2026-11-03T00:00:00Z',
    kind: 'selection',
    provider: observed('anthropic', 'model.providerID'),
    model: observed('claude-sonnet'),
    variant: unavailable,
    role: {
      status: 'observed',
      value: 'build',
      source: 'host',
      field: 'agent',
      vocabulary: 'native-agent',
    },
    protocolRole: {
      status: 'observed',
      value: 'worker',
      source: 'environment',
      field: 'PASTURE_EXECUTION_ROLE',
      vocabulary: 'pasture-role',
    },
    ...overrides,
  }
}

describe('attribution rendering', () => {
  it('renders one observed value with its exact source and member', () => {
    expect(attributionText(observed('claude-sonnet'))).toBe('claude-sonnet')
    expect(executionRows(execution()).find((row) => row.label === 'model')?.source).toBe('host · model.id')
  })

  it('never invents a value for unavailable or invalid evidence', () => {
    expect(attributionText(unavailable)).toBe('unavailable (no evidence)')
    expect(
      attributionText({ status: 'invalid', source: 'host', field: 'model', reason: 'rejected-input' }),
    ).toBe('invalid evidence (rejected input)')
    expect(executionRows(execution({ model: unavailable })).find((row) => row.label === 'model')?.source).toBe('no source')
  })

  it('shows every bounded candidate for a conflict instead of picking a winner', () => {
    const conflict: AttributionValue = {
      status: 'conflict',
      source: 'none',
      reason: 'valid-disagreement',
      candidates: [
        { source: 'host', field: 'model.id', value: 'model-a' },
        { source: 'environment', field: 'PASTURE_EXECUTION_MODEL', value: 'model-b' },
      ],
    }
    expect(attributionText(conflict)).toBe('conflict: model-a · host / model-b · environment')
  })
})

describe('execution presentation', () => {
  it('keeps provider and model as separate fields and names each kind', () => {
    const rows = executionRows(execution())
    expect(rows.find((row) => row.label === 'provider')?.value).toBe('anthropic')
    expect(rows.find((row) => row.label === 'model')?.value).toBe('claude-sonnet')
    expect(rows.find((row) => row.label === 'kind')?.value).toBe('selection')
    expect(executionKindLabel('child')).toBe('child')
  })

  it('keeps native role and protocol role in their own vocabularies', () => {
    const rows = executionRows(execution())
    expect(rows.find((row) => row.label === 'native role')?.value).toBe('build')
    expect(rows.find((row) => row.label === 'native role')?.source).toBe('host · agent · native-agent')
    expect(rows.find((row) => row.label === 'protocol role')?.value).toBe('worker')
    const unavailableRows = executionRows(
      execution({ provider: unavailable, model: unavailableRole, protocolRole: unavailableProtocolRole }),
    )
    expect(unavailableRows.find((row) => row.label === 'model')?.muted).toBe(true)
    expect(unavailableRows.find((row) => row.label === 'protocol role')?.value).toBe(
      'unavailable (no evidence)',
    )
  })

  it('names the generic actor as attribution-only and an explicit actor plainly', () => {
    expect(actorLabel({ id: 'pasture-system/0001', classification: 'generic-llm' })).toBe(
      'generic LLM actor (attribution only)',
    )
    expect(actorLabel({ id: 'file://actor/one', classification: 'explicit' })).toBe(
      'explicit actor',
    )
  })

  it('keeps state basis, prompt recency, and broad activity separate', () => {
    const activity: SessionActivity = {
      state: 'idle',
      stateBasis: 'prompt',
      lastPromptAt: '2026-11-03T00:00:00Z',
      lastSeen: '2026-11-03T00:30:00Z',
      lastEvent: 'PreToolUse',
    }
    const rows = livenessRows(activity, (value) => `formatted:${value}`)
    expect(rows.find((row) => row.label === 'basis')?.value).toBe('prompt')
    expect(rows.find((row) => row.label === 'last prompt')?.value).toBe('formatted:2026-11-03T00:00:00Z')
    expect(rows.find((row) => row.label === 'broad activity')?.value).toBe(
      'formatted:2026-11-03T00:30:00Z',
    )
    const noPrompt = livenessRows({ state: 'unknown', stateBasis: 'none' })
    expect(noPrompt.find((row) => row.label === 'last prompt')?.muted).toBe(true)
  })

  it('appends a pinned page without duplicating an occurrence', () => {
    const first = execution({ occurrenceId: 12 })
    const second = execution({ occurrenceId: 10, kind: 'primary' })
    const merged = appendExecutionPage(
      [first],
      {
        revision: 7,
        asOf: '2026-11-03T00:00:00Z',
        items: [first, execution({ occurrenceId: 11, kind: 'child' }), second],
      },
    )
    expect(merged.map((item) => item.occurrenceId)).toEqual([12, 11, 10])
  })
})

describe('watch snapshots', () => {
  it('applies later equal-revision session and epoch frames', () => {
    const session = { type: 'sessions' as const, revision: 7, asOf: 'first', payload: { items: [] } }
    const laterSession = { ...session, asOf: 'later', payload: { items: [{ activity: { state: 'idle' } }] as never[] } }
    expect(applySessionsFrame(session).items).toHaveLength(0)
    expect(applySessionsFrame(laterSession)).toMatchObject({ revision: 7, asOf: 'later', items: [{ activity: { state: 'idle' } }] })
    const epoch = { type: 'epochs' as const, revision: 7, asOf: 'first', payload: { items: [] } }
    const laterEpoch = { ...epoch, asOf: 'later', payload: { items: [{ id: 'new' }] as never[] } }
    expect(applyEpochsFrame(epoch).items).toHaveLength(0)
    expect(applyEpochsFrame(laterEpoch)).toMatchObject({ revision: 7, asOf: 'later', items: [{ id: 'new' }] })
  })
})

it('ignores history pages for a previous selection', () => {
  expect(historyPageMatches('claude:a', 'claude:a')).toBe(true)
  expect(historyPageMatches('claude:b', 'claude:a')).toBe(false)
})
