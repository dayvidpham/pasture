import { parseSessionDetailPayloadValue } from '@peasant-labs/schema'
import { adaptTranscript } from '@peasant-labs/fairtrade/ui'

import { ApiError } from './api'

/** The cooked view model every fairtrade transcript component renders. */
export type TranscriptViewModel = ReturnType<typeof adaptTranscript>

/**
 * TranscriptValidationError marks a payload that crossed the HTTP boundary but
 * did not match the shared durable session contract. It is deliberately
 * distinct from an HTTP failure so the console can name the contract mismatch.
 */
export class TranscriptValidationError extends Error {
  constructor(message: string, options?: ErrorOptions) {
    super(message, options)
    this.name = 'TranscriptValidationError'
  }
}

/**
 * adaptTranscriptPayload is the ONLY wire-parse boundary for transcripts.
 *
 * Pasture's proxy returns Peasant's SessionDetailPayload bytes unchanged. This
 * function validates that value with the shared schema package and then hands
 * the validated value to fairtrade's adaptTranscript, so no component ever
 * reads an unchecked wire field.
 */
export function adaptTranscriptPayload(value: unknown): TranscriptViewModel {
  let payload: ReturnType<typeof parseSessionDetailPayloadValue>
  try {
    payload = parseSessionDetailPayloadValue(value)
  } catch (cause) {
    throw new TranscriptValidationError(
      `Peasant returned a transcript that does not match the shared session contract: ${
        cause instanceof Error ? cause.message : 'schema validation failed'
      }`,
      { cause },
    )
  }
  try {
    // Pasture validates with its pinned @peasant-labs/schema copy; fairtrade
    // re-validates the same durable contract with its own copy. The two
    // packages can resolve different schema versions, so this assertion
    // crosses only the shared-contract boundary, never an unchecked shape.
    return adaptTranscript(payload as Parameters<typeof adaptTranscript>[0])
  } catch (cause) {
    throw new TranscriptValidationError(
      `fairtrade refused the validated transcript: ${
        cause instanceof Error ? cause.message : 'adaptTranscript failed'
      }`,
      { cause },
    )
  }
}

/** One actionable transcript failure the rail can render. */
export type TranscriptFailure = {
  title: string
  message: string
  retry: boolean
}

/**
 * describeTranscriptError turns a proxy refusal into copy a reader can act on.
 * The status codes mirror the OpenAPI contract: 404 no transcript, 409 a
 * Peasant refusal (incomplete attempt, incompatible store, identity conflict),
 * 503 Peasant is unavailable, 504 the bounded export wait ended.
 */
export function describeTranscriptError(error: unknown): TranscriptFailure {
  if (error instanceof TranscriptValidationError) {
    return {
      title: 'transcript contract mismatch',
      message: `${error.message}. Update Pasture and Peasant so both use the same @peasant-labs/schema version, then retry.`,
      retry: true,
    }
  }
  if (error instanceof ApiError) {
    switch (error.status) {
      case 400:
        return {
          title: 'session identity rejected',
          message: `${error.message}. Pasture keeps host session ids it cannot express to Peasant, so retrying will not change this result; export the transcript from Peasant directly.`,
          retry: false,
        }
      case 404:
        return {
          title: 'no transcript for this session',
          message:
            'Peasant has not stored a durable transcript for this session yet. Capture it with Peasant (harvest the session), then retry.',
          retry: true,
        }
      case 409:
        return {
          title: 'Peasant refused this transcript',
          message:
            error.code === 'transcript_incomplete'
              ? 'Peasant is holding a newer incomplete attempt beside the last-good transcript for this session, so Pasture will not serve either as the session. Finish or re-run the capture in Peasant, or export the last-good transcript explicitly with `peasant export sessions --last-good`, then retry.'
              : error.code === 'transcript_unsupported'
                ? 'Peasant refused this session for a reason Pasture cannot serve: an incompatible store, a native session id the shared contract rejects, or an export that did not record a harness. Install the Peasant version that wrote its store, or re-harvest the session in Peasant so the harness is recorded, then retry.'
                : `Peasant answered with a different session identity, so Pasture refused the transcript. ${error.message}`,
          retry: true,
        }
      case 503:
        return {
          title: 'Peasant is unavailable',
          message:
            'Pasture could not run `peasant` beside the local API. Install Peasant and ensure it is on PATH for `pasture serve`, then retry.',
          retry: true,
        }
      case 504:
        return {
          title: 'transcript export timed out',
          message:
            'Peasant did not finish exporting this session within Pasture’s bounded wait. Retry, or export it directly with `peasant export sessions` to check the store.',
          retry: true,
        }
      default:
        return {
          title: 'transcript unavailable',
          message: `${error.message}. Export the session directly with \`peasant export sessions\` to see the full diagnostic, then retry.`,
          retry: true,
        }
    }
  }
  return {
    title: 'transcript unavailable',
    message:
      error instanceof Error
        ? `${error.message}. Check that \`pasture serve\` is running beside the console, then retry.`
        : 'The local API did not answer the transcript request. Check that `pasture serve` is running, then retry.',
    retry: true,
  }
}
