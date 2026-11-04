import type {
  AttributionValue,
  ExecutionHistoryPage,
  SessionActivity,
  SessionActor,
  SessionExecution,
  SessionsFrame,
  EpochsFrame,
} from './api'

/** One labelled detail line. `muted` marks an honest absence or uncertainty. */
export type DetailLine = { label: string; value: string; muted?: boolean; source?: string }

function reasonLabel(reason?: string): string {
  return reason ? reason.replace(/-/g, ' ') : 'unspecified'
}

/**
 * attributionText is the single rendering rule for one normalized field. An
 * observed value is exact; unavailable, invalid, and conflict never pretend to
 * have one chosen value, and a conflict shows its bounded source-tagged
 * candidates instead of picking a winner.
 */
export function attributionText(value: AttributionValue): string {
  switch (value.status) {
    case 'observed':
      return value.value ?? 'observed'
    case 'unavailable':
      return `unavailable (${reasonLabel(value.reason)})`
    case 'invalid':
      return `invalid evidence (${reasonLabel(value.reason)})`
    case 'conflict': {
      const candidates = (value.candidates ?? []).map(
        (candidate) => `${candidate.value} · ${candidate.source}`,
      )
      return candidates.length > 0 ? `conflict: ${candidates.join(' / ')}` : 'conflict'
    }
    default:
      return 'unavailable'
  }
}

/** attributionSource keeps the evidence origin visible beside the value. */
function attributionSource(value: AttributionValue): string {
  if (value.status !== 'observed') {
    return value.source === 'none' ? 'no source' : value.source
  }
  return value.field ? `${value.source} · ${value.field}` : value.source
}

/** executionKindLabel names the observation kind without inventing meaning. */
export function executionKindLabel(kind: SessionExecution['kind']): string {
  switch (kind) {
    case 'primary':
      return 'primary'
    case 'selection':
      return 'selection'
    case 'compaction':
      return 'compaction'
    case 'title':
      return 'title'
    case 'generate':
      return 'generate'
    case 'child':
      return 'child'
    default:
      return 'unknown'
  }
}

/**
 * executionRows renders every attribution field separately. Provider and model
 * are never merged into one guessed label, and native role versus protocol role
 * stay distinct vocabularies.
 */
export function executionRows(execution: SessionExecution): DetailLine[] {
  const sourced = (label: string, value: AttributionValue): DetailLine => ({
    label,
    value: attributionText(value),
    source: `${attributionSource(value)}${value.vocabulary ? ` · ${value.vocabulary}` : ''}`,
    muted: value.status === 'unavailable',
  })
  const rows: DetailLine[] = [
    sourced('provider', execution.provider),
    sourced('model', execution.model),
    sourced('variant', execution.variant),
    sourced('native role', execution.role),
    sourced('protocol role', execution.protocolRole),
  ]
  if (execution.agentId) rows.push({ label: 'agent', value: execution.agentId })
  if (execution.turnId) rows.push({ label: 'turn', value: execution.turnId })
  if (execution.requestId) rows.push({ label: 'request', value: execution.requestId })
  return rows
}

/** actorLabel names the stored claimant; it never implies authority. */
export function actorLabel(actor: SessionActor): string {
  return actor.classification === 'generic-llm'
    ? 'generic LLM actor (attribution only)'
    : 'explicit actor'
}

/**
 * livenessRows expose state basis, prompt recency, and broad activity as
 * separate facts. A bound session with no prompt is unknown; the binding is not
 * shown as a prompt, and broad activity is not shown as recency.
 */
export function livenessRows(
  activity: SessionActivity,
  format: (value: string) => string = (value) => value,
): DetailLine[] {
  const time = (value: string | undefined, absent: string) =>
    value ? { value: format(value) } : { value: absent, muted: true }
  const prompt = time(activity.lastPromptAt, 'no prompt recorded')
  const broad = time(activity.lastSeen, 'no activity recorded')
  return [
    { label: 'state', value: activity.state },
    { label: 'basis', value: activity.stateBasis },
    { label: 'last prompt', ...prompt },
    { label: 'broad activity', ...broad },
    {
      label: 'last event',
      value: activity.lastEvent || 'no event recorded',
      muted: !activity.lastEvent,
    },
  ]
}

/**
 * appendExecutionPage appends one snapshot-pinned page and drops any row already
 * present. A newer live observation can never duplicate an occurrence, and the
 * page order stays descending by occurrence id.
 */
export function appendExecutionPage(
  existing: SessionExecution[],
  page: ExecutionHistoryPage,
): SessionExecution[] {
  const seen = new Set(existing.map((item) => item.occurrenceId))
  const merged = [...existing]
  for (const item of page.items) {
    if (seen.has(item.occurrenceId)) continue
    seen.add(item.occurrenceId)
    merged.push(item)
  }
  return merged
}

/** Watch frames are snapshots; equal revisions can carry later clock states. */
export function applySessionsFrame(frame: SessionsFrame) {
  return { items: frame.payload.items, revision: frame.revision, asOf: frame.asOf, truncated: Boolean(frame.payload.next) }
}
export function applyEpochsFrame(frame: EpochsFrame) {
  return { items: frame.payload.items, revision: frame.revision, asOf: frame.asOf }
}
