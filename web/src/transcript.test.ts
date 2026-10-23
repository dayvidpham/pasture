import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { BindingHistory } from './App'
import { ApiError } from './api'
import {
  adaptTranscriptPayload,
  describeTranscriptError,
  TranscriptValidationError,
} from './transcript'

const VALID_SESSION_ID = '6a46bcd8-b365-4608-be7d-5601e2fdd805'

// The smallest durable SessionDetailPayload the shared schema accepts. It is
// deliberately built here rather than imported from a producer fixture so the
// test pins the exact contract the web boundary expects.
const VALID_PAYLOAD = {
  id: VALID_SESSION_ID,
  harness: 'claude-code',
  startTime: '2026-01-01T10:00:00Z',
  endTime: '2026-01-01T10:00:03Z',
  durationMins: 0.05,
  totalTokens: 0,
  tokensIn: 0,
  tokensOut: 0,
  turnCount: 0,
  toolCallCount: 0,
  turns: [],
}

describe('adaptTranscriptPayload', () => {
  it('cooks a valid durable SessionDetailPayload through the schema boundary', () => {
    const viewModel = adaptTranscriptPayload(VALID_PAYLOAD)

    expect(viewModel.session.id).toBe(VALID_SESSION_ID)
    expect(viewModel.session.harness).toBe('claude-code')
    expect(viewModel.turns).toEqual([])
  })

  it('rejects a schema-invalid payload with a validation error and its cause', () => {
    const invalid = { ...VALID_PAYLOAD, harness: 'not-a-harness' }

    let failure: unknown
    try {
      adaptTranscriptPayload(invalid)
    } catch (error) {
      failure = error
    }

    expect(failure).toBeInstanceOf(TranscriptValidationError)
    expect((failure as TranscriptValidationError).cause).toBeInstanceOf(Error)
    expect((failure as TranscriptValidationError).message).toContain('shared session contract')
  })

  it('rejects a non-object payload instead of handing it to a component', () => {
    expect(() => adaptTranscriptPayload(null)).toThrow(TranscriptValidationError)
  })
})

describe('BindingHistory', () => {
  it('renders inferred provenance, interval state, and epoch', () => {
    const html = renderToStaticMarkup(
      createElement(BindingHistory, {
        bindings: [
          {
            bindingId: 'inferred-ghost',
            role: 'owner-responsibility',
            boundAt: '2026-01-01T10:00:00Z',
            source: 'inferred',
            confidence: 'correlated',
            epoch: { id: 'file://epoch/one', title: 'Epoch One' },
          },
          {
            bindingId: 'explicit-old',
            role: 'reviewer',
            boundAt: '2026-01-01T09:00:00Z',
            unboundAt: '2026-01-01T09:30:00Z',
            endReason: 'unbind',
            source: 'explicit',
            confidence: 'verified',
            epoch: { id: 'file://epoch/two' },
          },
        ],
      }),
    )

    expect(html).toContain('binding history')
    expect(html).toContain('inferred')
    expect(html).toContain('correlated')
    expect(html).toContain('Epoch One')
    expect(html).toContain('open')
    expect(html).toContain('explicit')
    expect(html).toContain('verified')
    expect(html).toContain('unbind')
  })
})

describe('describeTranscriptError', () => {
  it('maps 400 to a non-retryable shared-contract rejection', () => {
    const failure = describeTranscriptError(
      new ApiError(400, 'invalid_native_session_id', 'the shared session contract rejects native session id'),
    )

    expect(failure.title).toBe('session identity rejected')
    expect(failure.message).toContain('retrying will not change this result')
    expect(failure.retry).toBe(false)
  })

  it('maps 404 to an actionable missing-transcript state', () => {
    const failure = describeTranscriptError(
      new ApiError(404, 'transcript_not_found', 'Peasant has no transcript for that session'),
    )

    expect(failure.title).toBe('no transcript for this session')
    expect(failure.message).toContain('Capture it with Peasant')
    expect(failure.retry).toBe(true)
  })

  it('maps 409 transcript_incomplete to the last-good refusal wording', () => {
    const failure = describeTranscriptError(
      new ApiError(409, 'transcript_incomplete', 'Peasant refused an incomplete latest attempt'),
    )

    expect(failure.title).toBe('Peasant refused this transcript')
    expect(failure.message).toContain('newer incomplete attempt')
    expect(failure.message).toContain('--last-good')
    expect(failure.retry).toBe(true)
  })

  it('maps 409 transcript_unsupported to every cause the exporter can report', () => {
    const failure = describeTranscriptError(
      new ApiError(409, 'transcript_unsupported', 'Peasant cannot serve this session'),
    )

    expect(failure.title).toBe('Peasant refused this transcript')
    expect(failure.message).toContain('incompatible store')
    expect(failure.message).toContain('native session id the shared contract rejects')
    expect(failure.message).toContain('did not record a harness')
    expect(failure.message).toContain('re-harvest the session')
    expect(failure.message).toContain('Install the Peasant version')
  })

  it('maps 503 to the disconnected Peasant state', () => {
    const failure = describeTranscriptError(
      new ApiError(503, 'peasant_unavailable', 'Peasant is unavailable'),
    )

    expect(failure.title).toBe('Peasant is unavailable')
    expect(failure.message).toContain('PATH')
    expect(failure.retry).toBe(true)
  })

  it('maps 504 to the bounded-wait state', () => {
    const failure = describeTranscriptError(
      new ApiError(504, 'transcript_timeout', 'The bounded Peasant export wait ended'),
    )

    expect(failure.title).toBe('transcript export timed out')
    expect(failure.message).toContain('bounded wait')
    expect(failure.retry).toBe(true)
  })

  it('maps a generic Error to a local-API hint', () => {
    const failure = describeTranscriptError(new Error('network down'))

    expect(failure.title).toBe('transcript unavailable')
    expect(failure.message).toContain('network down')
    expect(failure.message).toContain('pasture serve')
    expect(failure.retry).toBe(true)
  })

  it('maps a validation error to the contract-mismatch copy', () => {
    const failure = describeTranscriptError(
      new TranscriptValidationError('Peasant returned a transcript that does not match'),
    )

    expect(failure.title).toBe('transcript contract mismatch')
    expect(failure.message).toContain('@peasant-labs/schema')
  })

  it('maps a non-Error value to the fallback copy', () => {
    const failure = describeTranscriptError('not an error')

    expect(failure.title).toBe('transcript unavailable')
    expect(failure.message).toContain('pasture serve')
  })
})
