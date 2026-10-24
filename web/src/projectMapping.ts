import {
  ApiError,
  type EpochView,
  type ProjectBindingInterval,
  type ProjectRole,
  type ProjectView,
} from './api'
import type { PeasantIntegration } from './project'
import { peasantDisabledRemedy, peasantUnavailableRemedy } from './project'
import { epochLabel } from './timeline'

/** The protocol roles offered by the project bind request. */
export const PROJECT_ROLES = [
  'epoch',
  'architect',
  'reviewer',
  'supervisor',
  'worker',
] as const satisfies readonly ProjectRole[]

/**
 * UNSELECTABLE_PROJECT_ROLES is the compile-time parity check for
 * PROJECT_ROLES: it must stay `never`. If the generated contract grows a role
 * that PROJECT_ROLES does not offer, this becomes that role's union and the
 * AssertNever constraint below fails the build, so a new role cannot be
 * silently unselectable.
 */
export type UNSELECTABLE_PROJECT_ROLES = Exclude<
  ProjectRole,
  (typeof PROJECT_ROLES)[number]
>

type AssertNever<T extends never> = T
type PROJECT_ROLES_ARE_EXHAUSTIVE = AssertNever<UNSELECTABLE_PROJECT_ROLES>

/** The bind request defaults to worker when the caller does not pick a role. */
export const DEFAULT_PROJECT_ROLE: ProjectRole = 'worker'

export type SelectOption = {
  value: string
  label: string
}

/**
 * ProjectMappingFailure is the classified reason a mapping read or write did
 * not complete. `retry` says whether asking again can help; a malformed request
 * or an unknown epoch cannot be fixed by a refresh.
 */
export type ProjectMappingFailureKind =
  | 'not-found'
  | 'unknown-epoch'
  | 'conflict'
  | 'unavailable'
  | 'invalid'
  | 'error'

export type ProjectMappingFailure = {
  kind: ProjectMappingFailureKind
  status: number | null
  message: string
  retry: boolean
}

/**
 * ProjectMappingState is one panel's complete state. `ready` with no `view` is
 * an honest "this project was never mapped" (the GET's 404), not an error.
 */
export type ProjectMappingState = {
  projectHash: string | null
  load: 'no-project' | 'loading' | 'ready' | 'error'
  view: ProjectView | null
  failure: ProjectMappingFailure | null
  action: 'idle' | 'binding' | 'unbinding' | 'success' | 'error'
  actionMessage: string
}

export const EMPTY_PROJECT_MAPPING: ProjectMappingState = {
  projectHash: null,
  load: 'no-project',
  view: null,
  failure: null,
  action: 'idle',
  actionMessage: '',
}

/** A loading state for one project, used before the fetch effect runs. */
export function loadingProjectMapping(projectHash: string | null): ProjectMappingState {
  if (!projectHash) return { ...EMPTY_PROJECT_MAPPING }
  return {
    projectHash,
    load: 'loading',
    view: null,
    failure: null,
    action: 'idle',
    actionMessage: '',
  }
}

/**
 * projectMappingForSelection never paints the previous project's mapping under
 * a newly selected project: a state for another hash reads as loading (or no
 * project) until the fetch effect replaces it.
 */
export function projectMappingForSelection(
  state: ProjectMappingState,
  projectHash: string | null,
): ProjectMappingState {
  if (state.projectHash === projectHash) return state
  return loadingProjectMapping(projectHash)
}

export type ProjectMappingEvent =
  | { type: 'load'; projectHash: string | null }
  | { type: 'loaded'; projectHash: string; view: ProjectView }
  | { type: 'load-failed'; projectHash: string; error: unknown }
  | { type: 'bind' }
  | { type: 'unbind' }
  | { type: 'bind-succeeded'; projectHash: string; view: ProjectView }
  | { type: 'unbind-succeeded'; projectHash: string; view: ProjectView }
  | { type: 'action-failed'; projectHash: string; error: unknown }

/**
 * reduceProjectMapping is the pure panel state machine. Events carrying a
 * project hash are ignored when they belong to a project the panel has since
 * moved away from, so a slow response can never overwrite the current mapping.
 */
export function reduceProjectMapping(
  state: ProjectMappingState,
  event: ProjectMappingEvent,
): ProjectMappingState {
  switch (event.type) {
    case 'load':
      return loadingProjectMapping(event.projectHash)
    case 'loaded':
      if (state.projectHash !== event.projectHash) return state
      return {
        projectHash: event.projectHash,
        load: 'ready',
        view: event.view,
        failure: null,
        action: 'idle',
        actionMessage: '',
      }
    case 'load-failed': {
      if (state.projectHash !== event.projectHash) return state
      const failure = classifyProjectMappingError(event.error)
      // A 404 project_not_found is the ordinary "never mapped" answer. The
      // panel offers bind controls instead of an error.
      if (failure.kind === 'not-found') {
        return {
          projectHash: event.projectHash,
          load: 'ready',
          view: null,
          failure: null,
          action: 'idle',
          actionMessage: '',
        }
      }
      return {
        projectHash: event.projectHash,
        load: 'error',
        view: null,
        failure,
        action: 'idle',
        actionMessage: '',
      }
    }
    case 'bind':
      if (
        state.load !== 'ready' ||
        state.action === 'binding' ||
        state.action === 'unbinding'
      ) {
        return state
      }
      return { ...state, action: 'binding', failure: null, actionMessage: '' }
    case 'unbind':
      if (
        state.load !== 'ready' ||
        !state.view?.binding ||
        state.action === 'binding' ||
        state.action === 'unbinding'
      ) {
        return state
      }
      return { ...state, action: 'unbinding', failure: null, actionMessage: '' }
    case 'bind-succeeded':
      if (state.projectHash !== event.projectHash) return state
      return {
        projectHash: event.projectHash,
        load: 'ready',
        view: event.view,
        failure: null,
        action: 'success',
        actionMessage: projectMappingBindMessage(event.view),
      }
    case 'unbind-succeeded':
      if (state.projectHash !== event.projectHash) return state
      return {
        projectHash: event.projectHash,
        load: 'ready',
        view: event.view,
        failure: null,
        action: 'success',
        actionMessage: 'project mapping removed',
      }
    case 'action-failed':
      if (state.projectHash !== event.projectHash) return state
      return {
        ...state,
        action: 'error',
        failure: classifyProjectMappingError(event.error),
      }
    default:
      return state
  }
}

/**
 * classifyProjectMappingError turns the API's status/code into an actionable
 * state. It distinguishes the GET's 404 (never mapped) from a bind's 404
 * (unknown epoch), keeps 409 as a retryable conflict, and treats a missing
 * answer as an unavailable local API.
 */
export function classifyProjectMappingError(error: unknown): ProjectMappingFailure {
  if (error instanceof ApiError) {
    const message = error.message.trim()
    switch (error.status) {
      case 404:
        if (error.code === 'unknown_epoch') {
          return {
            kind: 'unknown-epoch',
            status: 404,
            message:
              'the selected epoch is not a task in this store; choose another epoch and retry.',
            retry: false,
          }
        }
        return {
          kind: 'not-found',
          status: 404,
          message: message || 'this project has no epoch mapping yet; bind an epoch to create one.',
          retry: false,
        }
      case 409:
        return {
          kind: 'conflict',
          status: 409,
          message:
            'the project mapping changed while this page was open; refresh the mapping and retry.',
          retry: true,
        }
      case 503:
        return {
          kind: 'unavailable',
          status: 503,
          message:
            'project mapping routes are unavailable; restart pasture serve with the unified store, then retry.',
          retry: true,
        }
      case 400:
        return {
          kind: 'invalid',
          status: 400,
          message: message || 'the local API refused the project mapping request; correct it and retry.',
          retry: false,
        }
      case 403:
        return {
          kind: 'invalid',
          status: 403,
          message:
            'the local API refused this console origin; open the console from the loopback address printed by pasture serve, then retry.',
          retry: false,
        }
      default:
        return {
          kind: 'error',
          status: error.status,
          message:
            message ||
            `the project mapping request failed with HTTP ${error.status}; inspect the serve log and retry.`,
          retry: true,
        }
    }
  }
  if (error instanceof TypeError) {
    return {
      kind: 'unavailable',
      status: null,
      message:
        'the local API did not answer while reading the project mapping; check that pasture serve is running, then retry.',
      retry: true,
    }
  }
  const message = error instanceof Error ? error.message.trim() : String(error)
  return {
    kind: 'error',
    status: null,
    message:
      message || 'the project mapping request failed without a classified cause; retry and inspect the serve log if it repeats.',
    retry: true,
  }
}

/** The epoch label for a project mapping, honest when the epoch ref is absent. */
export function mappingEpochLabel(view: ProjectView | null): string {
  if (!view?.epoch) return 'unknown epoch'
  return epochLabel(view.epoch)
}

/** The epoch label for one historical interval. */
export function mappingIntervalEpochLabel(interval: ProjectBindingInterval): string {
  if (!interval.epoch) return 'unknown epoch'
  return epochLabel(interval.epoch)
}

/** A one-line description of the active project mapping. */
export function mappingSummary(view: ProjectView | null): string {
  if (!view?.binding) return 'not mapped'
  return `mapped to ${mappingEpochLabel(view)} as ${view.binding.role}`
}

/** The success copy after a bind request, phrased from the returned view. */
export function projectMappingBindMessage(view: ProjectView): string {
  if (!view.binding) return 'project mapping updated'
  return `project mapped to ${mappingEpochLabel(view)} as ${view.binding.role}`
}

/**
 * projectMappingHistory returns the interval history oldest first. The API
 * already orders it; sorting locally keeps the panel deterministic even when a
 * future response or a test fixture is unordered.
 */
export function projectMappingHistory(view: ProjectView | null): ProjectBindingInterval[] {
  const intervals = [...(view?.intervals ?? [])]
  return intervals.sort((left, right) => {
    const leftAt = Date.parse(left.boundAt)
    const rightAt = Date.parse(right.boundAt)
    const leftKnown = Number.isFinite(leftAt)
    const rightKnown = Number.isFinite(rightAt)
    if (leftKnown && rightKnown && leftAt !== rightAt) return leftAt - rightAt
    if (leftKnown !== rightKnown) return leftKnown ? -1 : 1
    return left.bindingId < right.bindingId ? -1 : left.bindingId > right.bindingId ? 1 : 0
  })
}

/** The active binding ID, used as the optimistic-concurrency token for writes. */
export function expectedProjectBinding(view: ProjectView | null): string | undefined {
  return view?.binding?.bindingId
}

/** A glanceable status for the rail section meta. */
export function projectMappingStatusLabel(state: ProjectMappingState): string {
  if (state.action === 'binding') return 'binding'
  if (state.action === 'unbinding') return 'unbinding'
  if (state.action === 'error') return 'error'
  switch (state.load) {
    case 'no-project':
      return 'no project'
    case 'loading':
      return 'loading'
    case 'error':
      return 'error'
    case 'ready':
      return state.view?.binding ? 'mapped' : 'unmapped'
    default:
      return 'unknown'
  }
}

/** The role choices in protocol order; the panel selects worker for a new binding. */
export function projectRoleOptions(): SelectOption[] {
  return PROJECT_ROLES.map((value) => ({ value, label: value }))
}

/** Validate a Select value before treating it as a protocol role. */
export function projectRoleFromValue(value: string): ProjectRole | null {
  return (PROJECT_ROLES as readonly string[]).includes(value)
    ? (value as ProjectRole)
    : null
}

/** Epoch choices reuse the projected epoch list already held by the shell. */
export function epochOptions(epochs: EpochView[]): SelectOption[] {
  return epochs.map((epoch) => ({ value: epoch.id, label: epochLabel(epoch) }))
}

/**
 * projectMappingNoProjectMessage explains a missing project hash without
 * blaming Peasant when the integration is disabled, unavailable, or still
 * being read. The panel prints this instead of bind controls.
 */
export function projectMappingNoProjectMessage(
  integration: PeasantIntegration | null,
): string {
  if (integration === null) {
    return 'Reading the Peasant project source… A project-level mapping appears once a session carries a Peasant project hash.'
  }
  switch (integration.status) {
    case 'disabled':
      return `${peasantDisabledRemedy()} This session has no project hash, so no project-level mapping can be shown or edited here.`
    case 'unavailable':
      return `${peasantUnavailableRemedy(integration)} This session has no project hash, so no project-level mapping can be shown or edited here.`
    case 'ok':
      return 'Peasant has not mapped this session to a project, so there is no project hash to look up a project-level mapping.'
    default:
      return `Pasture reported an unrecognized Peasant integration state (${String(
        integration.status,
      )}), so a missing project hash cannot be explained. Refresh after upgrading Pasture.`
  }
}
