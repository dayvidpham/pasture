import type { InspectPayload, SessionView } from './api'

/** The tri-state Peasant project integration reported by /api/v1/inspect. */
export type PeasantIntegration = InspectPayload['peasantIntegration']

/** The Peasant-owned project fields carried by one session. */
export type ProjectIdentity = Pick<SessionView, 'project' | 'projectHash'>

/** One selectable project, keyed by the exact Peasant project hash. */
export type ProjectOption = {
  hash: string
  label: string
}

/** The honest fallback when Peasant has not mapped a session to a project. */
export const UNKNOWN_PROJECT_LABEL = 'unknown project'

/**
 * projectLabel is the display label for a session's Peasant-owned project: the
 * project name when present, otherwise the exact project hash, otherwise an
 * honest unknown marker. Pasture never derives a project of its own.
 */
export function projectLabel(session: ProjectIdentity): string {
  const name = session.project?.trim()
  if (name) return name
  if (session.projectHash) return session.projectHash
  return UNKNOWN_PROJECT_LABEL
}

function shortProjectHash(hash: string): string {
  return hash.length > 8 ? hash.slice(0, 8) : hash
}

/**
 * projectOptions builds the project filter choices from the current sessions.
 * The hash is the identity: sessions that share a project name but carry
 * distinct hashes stay distinct options, and duplicate labels get a short hash
 * suffix so a reader can tell them apart. Sessions with no hash are not
 * options; they remain visible in the unfiltered inventory.
 */
export function projectOptions(sessions: ProjectIdentity[]): ProjectOption[] {
  const byHash = new Map<string, string>()
  for (const session of sessions) {
    const hash = session.projectHash
    if (!hash) continue
    const name = session.project?.trim() || ''
    const existing = byHash.get(hash)
    // Prefer a recorded name over a hash-only label even when the hash-only
    // session appears first; one hash is one project.
    if (existing === undefined || (existing === hash && name)) {
      byHash.set(hash, name || hash)
    }
  }

  const labelCounts = new Map<string, number>()
  for (const label of byHash.values()) {
    labelCounts.set(label, (labelCounts.get(label) ?? 0) + 1)
  }

  return [...byHash.entries()]
    .map(([hash, label]) => ({
      hash,
      label:
        (labelCounts.get(label) ?? 0) > 1 && label !== hash
          ? `${label} · ${shortProjectHash(hash)}`
          : label,
    }))
    .sort((left, right) => {
      if (left.label !== right.label) return left.label < right.label ? -1 : 1
      if (left.hash === right.hash) return 0
      return left.hash < right.hash ? -1 : 1
    })
}

/**
 * filterSessionsByProject narrows sessions to one exact Peasant project hash.
 * A null/empty selection is the unfiltered inventory, which keeps sessions
 * with no project visible.
 */
export function filterSessionsByProject<T extends ProjectIdentity>(
  sessions: T[],
  projectHash: string | null,
): T[] {
  if (!projectHash) return sessions
  return sessions.filter((session) => session.projectHash === projectHash)
}

/** A human label for the configured Peasant project source. */
export function integrationSourceLabel(source: PeasantIntegration['source']): string {
  switch (source) {
    case 'db':
      return 'Peasant SQLite database'
    case 'api':
      return 'Peasant HTTP API'
    case 'none':
      return 'none'
    default:
      return String(source)
  }
}

/** The presentation the rail renders for one integration state. */
export type PeasantIntegrationPresentation = {
  status: PeasantIntegration['status']
  title: string
  message: string
  commands: string[]
  sourceLabel: string
}

/**
 * peasantIntegrationPresentation turns the backend's tri-state health into
 * actionable rail copy. Disabled is a configuration choice and explains the two
 * opt-in flags; unavailable is a configured source that failed and names the
 * source plus the backend detail; ok is the quiet normal state.
 */
export function peasantIntegrationPresentation(
  integration: PeasantIntegration,
): PeasantIntegrationPresentation {
  const sourceLabel = integrationSourceLabel(integration.source)
  switch (integration.status) {
    case 'disabled':
      return {
        status: 'disabled',
        title: 'Peasant project enrichment is off',
        message:
          'Project names are not loaded, so every session shows an unknown project. Restart pasture serve with one of these flags to enable the opt-in source:',
        commands: [
          'pasture serve --peasant-db <path>',
          'pasture serve --peasant-url <url>',
        ],
        sourceLabel,
      }
    case 'unavailable':
      return {
        status: 'unavailable',
        title: 'Peasant project source is unavailable',
        message:
          integration.message?.trim() ||
          'The configured Peasant project source could not be read. Sessions still render with an unknown project; fix the source and refresh.',
        commands: [],
        sourceLabel,
      }
    case 'ok':
      return {
        status: 'ok',
        title: 'Peasant project enrichment is on',
        message: '',
        commands: [],
        sourceLabel,
      }
    default:
      return {
        status: 'unavailable',
        title: 'Peasant project state is unrecognized',
        message: `Pasture reported an integration status this console does not understand: ${String(integration.status)}. Refresh after upgrading Pasture.`,
        commands: [],
        sourceLabel,
      }
  }
}
