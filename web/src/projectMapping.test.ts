import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'

import { ProjectMappingPanel } from './App'
import {
  ApiError,
  bindProject,
  fetchProject,
  setPeasantEnrichment,
  unbindProject,
  type EpochView,
  type ProjectBindingInterval,
  type ProjectView,
} from './api'
import type { PeasantIntegration } from './project'
import {
  DEFAULT_PROJECT_ROLE,
  EMPTY_PROJECT_MAPPING,
  classifyProjectMappingError,
  epochOptions,
  expectedProjectBinding,
  loadingProjectMapping,
  mappingEpochLabel,
  mappingIntervalEpochLabel,
  mappingSummary,
  projectMappingBindMessage,
  projectMappingForSelection,
  projectMappingHistory,
  projectMappingNoProjectMessage,
  projectMappingStatusLabel,
  projectBindRequest,
  projectRoleFromValue,
  projectRoleOptions,
  projectUnbindRequest,
  reduceProjectMapping,
  type ProjectMappingState,
  type UNSELECTABLE_PROJECT_ROLES,
} from './projectMapping'

const PROJECT_HASH = 'a'.repeat(64)

const epochs: EpochView[] = [
  {
    id: 'epoch/one',
    title: 'Epoch One',
    phase: 'impl',
    role: 'worker',
    sessionCount: 2,
    activeCount: 1,
  },
  {
    id: 'epoch/two',
    title: '',
    phase: 'review',
    role: 'reviewer',
    sessionCount: 0,
    activeCount: 0,
  },
]

const closedInterval: ProjectBindingInterval = {
  bindingId: 'binding-old',
  boundAt: '2026-09-01T10:00:00Z',
  unboundAt: '2026-09-02T10:00:00Z',
  endReason: 'rebind',
  role: 'worker',
  source: 'project',
  confidence: 'verified',
  epoch: { id: 'epoch/two', title: 'Epoch Two' },
}

const activeInterval: ProjectBindingInterval = {
  bindingId: 'binding-1',
  boundAt: '2026-10-01T10:00:00Z',
  role: 'worker',
  source: 'project',
  confidence: 'verified',
  epoch: { id: 'epoch/one', title: 'Epoch One' },
}

// Deliberately unordered: projectMappingHistory sorts defensively even though
// the API already orders intervals by boundAt.
const boundView: ProjectView = {
  projectHash: PROJECT_HASH,
  binding: {
    bindingId: 'binding-1',
    boundAt: '2026-10-01T10:00:00Z',
    confidence: 'verified',
    role: 'worker',
    source: 'project',
  },
  epoch: { id: 'epoch/one', title: 'Epoch One' },
  intervals: [activeInterval, closedInterval],
}

const unboundView: ProjectView = {
  projectHash: PROJECT_HASH,
  intervals: [closedInterval],
}

const disabledIntegration: PeasantIntegration = {
  enabled: false,
  source: 'none',
  status: 'disabled',
  message: '',
}

const unavailableIntegration: PeasantIntegration = {
  enabled: true,
  source: 'db',
  status: 'unavailable',
  message: 'database is locked',
}

const okIntegration: PeasantIntegration = {
  enabled: true,
  source: 'api',
  status: 'ok',
  message: '',
}

function readyState(view: ProjectView | null): ProjectMappingState {
  return {
    projectHash: PROJECT_HASH,
    load: 'ready',
    view,
    failure: null,
    action: 'idle',
    actionMessage: '',
  }
}

function renderPanel(
  state: ProjectMappingState,
  options: {
    integration?: PeasantIntegration | null
    epochList?: EpochView[]
  } = {},
): string {
  return renderToStaticMarkup(
    createElement(ProjectMappingPanel, {
      state,
      epochs: options.epochList ?? epochs,
      integration: options.integration ?? okIntegration,
      onBind: () => {},
      onUnbind: () => {},
      onRefresh: () => {},
    }),
  )
}

describe('classifyProjectMappingError', () => {
  it('treats a GET 404 as "never mapped" and a bind 404 as an unknown epoch', () => {
    expect(
      classifyProjectMappingError(
        new ApiError(404, 'project_not_found', 'no project mapping is recorded for that project hash'),
      ),
    ).toMatchObject({ kind: 'not-found', status: 404, retry: false })

    const unknownEpoch = classifyProjectMappingError(
      new ApiError(404, 'unknown_epoch', 'epoch/x is not a task in the store'),
    )
    expect(unknownEpoch).toMatchObject({ kind: 'unknown-epoch', status: 404, retry: false })
    expect(unknownEpoch.message).toContain('choose another epoch')
  })

  it('keeps a 409 conflict retryable and a 400 request invalid', () => {
    const conflict = classifyProjectMappingError(
      new ApiError(409, 'binding_conflict', 'expected binding-a, open is binding-b'),
    )
    expect(conflict).toMatchObject({ kind: 'conflict', status: 409, retry: true })
    expect(conflict.message).toContain('refresh the mapping and retry')

    expect(
      classifyProjectMappingError(new ApiError(409, 'no_open_binding', 'no open binding')),
    ).toMatchObject({ kind: 'conflict', status: 409, retry: true })

    expect(
      classifyProjectMappingError(new ApiError(400, 'invalid_body', 'expectedBinding is required')),
    ).toMatchObject({ kind: 'invalid', status: 400, retry: false })
  })

  it('reports an unavailable local API for 503 and a missing answer', () => {
    expect(
      classifyProjectMappingError(
        new ApiError(503, 'project_unavailable', 'project mapping routes are unavailable'),
      ),
    ).toMatchObject({ kind: 'unavailable', status: 503, retry: true })

    expect(classifyProjectMappingError(new TypeError('Failed to fetch'))).toMatchObject({
      kind: 'unavailable',
      status: null,
      retry: true,
    })
  })

  it('keeps server and store failures retryable', () => {
    expect(
      classifyProjectMappingError(new ApiError(500, 'storage_fault', 'database is locked')),
    ).toMatchObject({ kind: 'error', status: 500, retry: true })

    expect(classifyProjectMappingError(new Error('unexpected decode failure'))).toMatchObject({
      kind: 'error',
      status: null,
      retry: true,
    })
  })

  it('classifies a 403 origin refusal as a non-retryable loopback fix', () => {
    const forbidden = classifyProjectMappingError(
      new ApiError(403, 'forbidden_origin', 'session API requests must use a loopback Origin'),
    )
    expect(forbidden).toMatchObject({ kind: 'invalid', status: 403, retry: false })
    expect(forbidden.message).toContain('loopback')
  })
})

describe('reduceProjectMapping', () => {
  it('starts with no project and moves to loading for a project hash', () => {
    expect(projectMappingStatusLabel(EMPTY_PROJECT_MAPPING)).toBe('no project')
    const loading = reduceProjectMapping(EMPTY_PROJECT_MAPPING, {
      type: 'load',
      projectHash: PROJECT_HASH,
    })
    expect(loading.load).toBe('loading')
    expect(projectMappingStatusLabel(loading)).toBe('loading')
  })

  it('loads an active mapping and its history', () => {
    const state = reduceProjectMapping(loadingProjectMapping(PROJECT_HASH), {
      type: 'loaded',
      projectHash: PROJECT_HASH,
      view: boundView,
    })
    expect(state).toMatchObject({
      load: 'ready',
      view: boundView,
      failure: null,
      action: 'idle',
    })
    expect(projectMappingStatusLabel(state)).toBe('mapped')
  })

  it('turns the GET 404 into an unmapped ready state, not an error', () => {
    const state = reduceProjectMapping(loadingProjectMapping(PROJECT_HASH), {
      type: 'load-failed',
      projectHash: PROJECT_HASH,
      error: new ApiError(404, 'project_not_found', 'no project mapping is recorded'),
    })
    expect(state).toMatchObject({
      load: 'ready',
      view: null,
      failure: null,
      action: 'idle',
    })
    expect(projectMappingStatusLabel(state)).toBe('unmapped')
  })

  it('keeps a load failure as an error with a retry hint', () => {
    const state = reduceProjectMapping(loadingProjectMapping(PROJECT_HASH), {
      type: 'load-failed',
      projectHash: PROJECT_HASH,
      error: new ApiError(503, 'project_unavailable', 'project mapping routes are unavailable'),
    })
    expect(state.load).toBe('error')
    expect(state.failure).toMatchObject({ kind: 'unavailable', retry: true })
    expect(projectMappingStatusLabel(state)).toBe('error')
  })

  it('ignores a response for a project the panel has moved away from', () => {
    const otherHash = 'b'.repeat(64)
    const state = loadingProjectMapping(otherHash)
    const staleLoaded = reduceProjectMapping(state, {
      type: 'loaded',
      projectHash: PROJECT_HASH,
      view: boundView,
    })
    expect(staleLoaded).toBe(state)

    const staleFailure = reduceProjectMapping(state, {
      type: 'load-failed',
      projectHash: PROJECT_HASH,
      error: new ApiError(503, 'project_unavailable', 'unavailable'),
    })
    expect(staleFailure).toBe(state)

    const staleSuccess = reduceProjectMapping(state, {
      type: 'bind-succeeded',
      projectHash: PROJECT_HASH,
      view: boundView,
    })
    expect(staleSuccess).toBe(state)
  })

  it('moves through bind, success, unbind, and success', () => {
    const ready = readyState(boundView)
    const binding = reduceProjectMapping(ready, { type: 'bind' })
    expect(binding.action).toBe('binding')
    expect(binding.view).toBe(boundView)

    const bound = reduceProjectMapping(binding, {
      type: 'bind-succeeded',
      projectHash: PROJECT_HASH,
      view: boundView,
    })
    expect(bound.action).toBe('success')
    expect(bound.actionMessage).toBe('project mapped to Epoch One as worker')
    expect(bound.view).toBe(boundView)

    const unbinding = reduceProjectMapping(bound, { type: 'unbind' })
    expect(unbinding.action).toBe('unbinding')

    const unbound = reduceProjectMapping(unbinding, {
      type: 'unbind-succeeded',
      projectHash: PROJECT_HASH,
      view: unboundView,
    })
    expect(unbound.action).toBe('success')
    expect(unbound.actionMessage).toBe('project mapping removed')
    expect(unbound.view).toBe(unboundView)
    expect(projectMappingStatusLabel(unbound)).toBe('unmapped')
  })

  it('keeps the current mapping while reporting a 409 conflict', () => {
    const state = reduceProjectMapping(readyState(boundView), {
      type: 'action-failed',
      projectHash: PROJECT_HASH,
      error: new ApiError(409, 'binding_conflict', 'expected binding-a, open is binding-b'),
    })
    expect(state.action).toBe('error')
    expect(state.failure).toMatchObject({ kind: 'conflict', retry: true })
    expect(state.view).toBe(boundView)
  })

  it('does not start a write without a ready mapping or an active binding', () => {
    const loading = loadingProjectMapping(PROJECT_HASH)
    expect(reduceProjectMapping(loading, { type: 'bind' })).toBe(loading)

    const unmapped = readyState(null)
    expect(reduceProjectMapping(unmapped, { type: 'unbind' })).toBe(unmapped)
  })

  it('does not stack a second write while one is in flight', () => {
    const binding = reduceProjectMapping(readyState(boundView), { type: 'bind' })
    expect(reduceProjectMapping(binding, { type: 'bind' })).toBe(binding)
    expect(reduceProjectMapping(binding, { type: 'unbind' })).toBe(binding)
  })

  it('reflects an in-flight or failed action in the glance label', () => {
    const binding = reduceProjectMapping(readyState(boundView), { type: 'bind' })
    expect(projectMappingStatusLabel(binding)).toBe('binding')

    const failed = reduceProjectMapping(binding, {
      type: 'action-failed',
      projectHash: PROJECT_HASH,
      error: new ApiError(409, 'binding_conflict', 'expected binding-a, open is binding-b'),
    })
    expect(projectMappingStatusLabel(failed)).toBe('error')
  })
})

describe('project mapping labels and options', () => {
  it('labels the mapped epoch and summary honestly', () => {
    expect(mappingEpochLabel(boundView)).toBe('Epoch One')
    expect(mappingEpochLabel({ projectHash: PROJECT_HASH })).toBe('unknown epoch')
    expect(mappingSummary(boundView)).toBe('mapped to Epoch One as worker')
    expect(mappingSummary(null)).toBe('not mapped')
    expect(mappingSummary(unboundView)).toBe('not mapped')
    expect(mappingIntervalEpochLabel(closedInterval)).toBe('Epoch Two')
    expect(
      mappingIntervalEpochLabel({ ...closedInterval, epoch: undefined }),
    ).toBe('unknown epoch')
    expect(projectMappingBindMessage(boundView)).toBe('project mapped to Epoch One as worker')
    expect(projectMappingBindMessage(unboundView)).toBe('project mapping updated')
  })

  it('sorts history oldest first', () => {
    expect(projectMappingHistory(boundView).map((interval) => interval.bindingId)).toEqual([
      'binding-old',
      'binding-1',
    ])
    expect(projectMappingHistory(null)).toEqual([])
  })

  it('uses the active binding as the concurrency token', () => {
    expect(expectedProjectBinding(boundView)).toBe('binding-1')
    expect(expectedProjectBinding(unboundView)).toBeUndefined()
    expect(expectedProjectBinding(null)).toBeUndefined()
  })

  it('builds bind and unbind bodies with the active concurrency token', () => {
    expect(projectBindRequest(boundView, 'epoch/two', 'reviewer')).toEqual({
      epochId: 'epoch/two',
      role: 'reviewer',
      expectedBinding: 'binding-1',
    })
    expect(projectBindRequest(unboundView, 'epoch/one', 'worker')).toEqual({
      epochId: 'epoch/one',
      role: 'worker',
    })
    expect(projectBindRequest(null, 'epoch/one', 'worker')).toEqual({
      epochId: 'epoch/one',
      role: 'worker',
    })

    expect(projectUnbindRequest(boundView)).toEqual({ expectedBinding: 'binding-1' })
    expect(projectUnbindRequest(unboundView)).toEqual({})
    expect(projectUnbindRequest(null)).toEqual({})
  })

  it('offers the protocol roles and validates a select value', () => {
    expect(projectRoleOptions().map((option) => option.value)).toEqual([
      'epoch',
      'architect',
      'reviewer',
      'supervisor',
      'worker',
    ])
    expect(DEFAULT_PROJECT_ROLE).toBe('worker')
    expect(projectRoleFromValue('worker')).toBe('worker')
    expect(projectRoleFromValue('not-a-role')).toBeNull()
  })

  it('offers every generated project role', () => {
    // Compile-time parity: a role added to the generated contract but omitted
    // from PROJECT_ROLES makes this annotation `never` and fails typecheck.
    const noUnselectableRoles: [UNSELECTABLE_PROJECT_ROLES] extends [never] ? true : never = true
    expect(noUnselectableRoles).toBe(true)
  })

  it('keys epoch options by id and falls back to the id for a missing title', () => {
    expect(epochOptions(epochs)).toEqual([
      { value: 'epoch/one', label: 'Epoch One' },
      { value: 'epoch/two', label: 'epoch/two' },
    ])
  })

  it('derives a loading state when selection moves to another hash', () => {
    const state = readyState(boundView)
    expect(projectMappingForSelection(state, PROJECT_HASH)).toBe(state)
    expect(projectMappingForSelection(state, 'b'.repeat(64))).toMatchObject({
      projectHash: 'b'.repeat(64),
      load: 'loading',
    })
    expect(projectMappingForSelection(state, null)).toMatchObject({
      projectHash: null,
      load: 'no-project',
    })
  })

  it('explains a missing project from the integration state', () => {
    expect(projectMappingNoProjectMessage(disabledIntegration)).toContain('--peasant-db')
    expect(projectMappingNoProjectMessage(unavailableIntegration)).toContain('database is locked')
    expect(projectMappingNoProjectMessage(okIntegration)).toContain('has not mapped')
    expect(projectMappingNoProjectMessage(null)).toContain('Reading the Peasant project source')
  })

  it('explains an unrecognized Peasant integration state', () => {
    const future = {
      ...okIntegration,
      status: 'future',
    } as unknown as PeasantIntegration
    const message = projectMappingNoProjectMessage(future)
    expect(message).toContain('unrecognized Peasant integration state')
    expect(message).toContain('future')
  })
})

describe('project mapping API client', () => {
  it('reads, binds, and unbinds through the project routes', async () => {
    const calls: Array<{ url: string; init?: RequestInit }> = []
    const originalFetch = globalThis.fetch
    globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ url: String(input), init })
      return new Response(JSON.stringify(boundView), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      })
    }) as typeof fetch
    try {
      const project = await fetchProject(PROJECT_HASH)
      expect(project.projectHash).toBe(PROJECT_HASH)

      await bindProject(
        PROJECT_HASH,
        projectBindRequest(boundView, 'epoch/one', 'worker'),
      )
      await unbindProject(PROJECT_HASH, projectUnbindRequest(boundView))
    } finally {
      globalThis.fetch = originalFetch
    }

    expect(calls.map((call) => call.url)).toEqual([
      `/api/v1/projects/${PROJECT_HASH}`,
      `/api/v1/projects/${PROJECT_HASH}/bind`,
      `/api/v1/projects/${PROJECT_HASH}/unbind`,
    ])
    expect(calls[1].init).toMatchObject({
      method: 'POST',
      headers: { accept: 'application/json', 'content-type': 'application/json' },
    })
    expect(JSON.parse(String(calls[1].init?.body))).toEqual({
      epochId: 'epoch/one',
      role: 'worker',
      expectedBinding: 'binding-1',
    })
    expect(JSON.parse(String(calls[2].init?.body))).toEqual({
      expectedBinding: 'binding-1',
    })
  })

  it('posts the runtime enrichment toggle and returns the updated state', async () => {
    const calls: Array<{ url: string; init?: RequestInit }> = []
    const originalFetch = globalThis.fetch
    globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ url: String(input), init })
      return new Response(
        JSON.stringify({
          enabled: true,
          source: 'db',
          status: 'ok',
          message: '',
        }),
        { status: 200, headers: { 'content-type': 'application/json' } },
      )
    }) as typeof fetch
    try {
      const updated = await setPeasantEnrichment(true)
      expect(updated).toMatchObject({ enabled: true, source: 'db', status: 'ok' })
    } finally {
      globalThis.fetch = originalFetch
    }
    expect(calls).toHaveLength(1)
    expect(calls[0].url).toBe('/api/v1/settings/peasant')
    expect(calls[0].init).toMatchObject({
      method: 'POST',
      headers: { accept: 'application/json', 'content-type': 'application/json' },
    })
    expect(JSON.parse(String(calls[0].init?.body))).toEqual({ enabled: true })
  })

  it('turns a 409 envelope into an ApiError for the classifier', async () => {
    const originalFetch = globalThis.fetch
    globalThis.fetch = (async () =>
      new Response(
        JSON.stringify({
          error: { code: 'binding_conflict', message: 'expected binding-a, open is binding-b' },
        }),
        { status: 409, headers: { 'content-type': 'application/json' } },
      )) as typeof fetch
    try {
      await expect(
        bindProject(PROJECT_HASH, { epochId: 'epoch/one', role: 'worker' }),
      ).rejects.toMatchObject({
        status: 409,
        code: 'binding_conflict',
        message: 'expected binding-a, open is binding-b',
      })
    } finally {
      globalThis.fetch = originalFetch
    }
  })
})

describe('ProjectMappingPanel render', () => {
  it('explains a missing project hash and offers no bind controls', () => {
    const html = renderPanel(EMPTY_PROJECT_MAPPING, { integration: disabledIntegration })
    expect(html).toContain('project-level mapping')
    expect(html).toContain('never overrides')
    expect(html).toContain('--peasant-db')
    expect(html).not.toContain('bind project')
    expect(html).not.toContain('<select')
  })

  it('explains an unmapped session from an enabled source', () => {
    const html = renderPanel(EMPTY_PROJECT_MAPPING, { integration: okIntegration })
    expect(html).toContain('Peasant has not mapped')
    expect(html).not.toContain('bind project')
  })

  it('shows a loading state', () => {
    const html = renderPanel(loadingProjectMapping(PROJECT_HASH))
    expect(html).toContain('reading the project-level mapping')
  })

  it('shows an unavailable API with a retry control', () => {
    const state = reduceProjectMapping(loadingProjectMapping(PROJECT_HASH), {
      type: 'load-failed',
      projectHash: PROJECT_HASH,
      error: new ApiError(503, 'project_unavailable', 'project mapping routes are unavailable'),
    })
    const html = renderPanel(state)
    expect(html).toContain('project mapping routes are unavailable')
    expect(html).toContain('retry project mapping')
  })

  it('offers bind controls when the project has no mapping', () => {
    const html = renderPanel(readyState(null))
    expect(html).toContain('no active project mapping')
    expect(html).toContain('bind project')
    expect(html).not.toContain('>unbind</button>')
    expect(html).toContain('Epoch One')
    expect(html).toContain('worker')
  })

  it('renders the GET 404 as bind controls rather than an error', () => {
    const state = reduceProjectMapping(loadingProjectMapping(PROJECT_HASH), {
      type: 'load-failed',
      projectHash: PROJECT_HASH,
      error: new ApiError(404, 'project_not_found', 'no project mapping is recorded'),
    })
    const html = renderPanel(state)
    expect(html).toContain('no active project mapping')
    expect(html).toContain('bind project')
    expect(html).not.toContain('no project mapping is recorded')
    expect(html).not.toContain('retry project mapping')
  })

  it('defaults a new binding to worker and a rebind to the recorded role', () => {
    const fresh = renderPanel(readyState(null))
    expect(fresh).toContain('value="worker" selected')

    const supervisorView: ProjectView = {
      ...boundView,
      binding: boundView.binding
        ? { ...boundView.binding, role: 'supervisor' }
        : undefined,
    }
    const rebound = renderPanel(readyState(supervisorView))
    expect(rebound).toContain('value="supervisor" selected')
  })

  it('selects the current epoch, and the first option for a fresh mapping', () => {
    const secondEpochView: ProjectView = {
      ...boundView,
      epoch: { id: 'epoch/two', title: '' },
    }
    expect(renderPanel(readyState(secondEpochView))).toContain(
      'value="epoch/two" selected',
    )
    expect(renderPanel(readyState(null))).toContain('value="epoch/one" selected')
    expect(
      renderPanel(readyState({ ...boundView, epoch: { id: 'epoch/missing' } })),
    ).toContain('value="epoch/one" selected')
  })

  it('keeps the session-override disclaimer in every panel state', () => {
    for (const state of [
      EMPTY_PROJECT_MAPPING,
      loadingProjectMapping(PROJECT_HASH),
      readyState(null),
      readyState(boundView),
    ]) {
      const html = renderPanel(state)
      expect(html).toContain('separate from')
      expect(html).toContain('never overrides')
    }
  })

  it('marks disabled and unavailable integration states', () => {
    const disabled = renderPanel(EMPTY_PROJECT_MAPPING, {
      integration: disabledIntegration,
    })
    expect(disabled).toContain('data-integration="disabled"')
    expect(disabled).toContain('enrichment is disabled')
    expect(disabled).toContain('--peasant-db')

    const unavailable = renderPanel(EMPTY_PROJECT_MAPPING, {
      integration: unavailableIntegration,
    })
    expect(unavailable).toContain('data-integration="unavailable"')
    expect(unavailable).toContain('source is unavailable')
    expect(unavailable).toContain('database is locked')
  })

  it('shows the active mapping, provenance, and interval history', () => {
    const html = renderPanel(readyState(boundView))
    expect(html).toContain('active project mapping')
    expect(html).toContain('Epoch One')
    expect(html).toContain('binding-1') // detail history data attribute
    expect(html).toContain('rebind project')
    expect(html).toContain('>unbind</button>')
    expect(html).toContain('mapping history')
    expect(html).toContain('Epoch Two')
    expect(html).toContain('rebind')
    expect(html).toContain('verified')
  })

  it('disables writes and marks the button busy while binding', () => {
    const state = reduceProjectMapping(readyState(boundView), { type: 'bind' })
    const html = renderPanel(state)
    expect(html).toContain('aria-busy="true"')
    expect(html).toContain('binding the project mapping')
    expect(html).toContain('rebind project')
  })

  it('shows an unbinding state', () => {
    const state = reduceProjectMapping(readyState(boundView), { type: 'unbind' })
    const html = renderPanel(state)
    expect(html).toContain('removing the project mapping')
    expect(html).toContain('aria-busy="true"')
  })

  it('reports bind success', () => {
    const state = reduceProjectMapping(readyState(boundView), {
      type: 'bind-succeeded',
      projectHash: PROJECT_HASH,
      view: boundView,
    })
    const html = renderPanel(state)
    expect(html).toContain('project mapped to Epoch One as worker')
    expect(html).toContain('role="status"')
  })

  it('reports a 409 conflict and offers a refresh', () => {
    const state = reduceProjectMapping(readyState(boundView), {
      type: 'action-failed',
      projectHash: PROJECT_HASH,
      error: new ApiError(409, 'binding_conflict', 'expected binding-a, open is binding-b'),
    })
    const html = renderPanel(state)
    expect(html).toContain('changed while this page was open')
    expect(html).toContain('refresh project mapping')
  })

  it('shows the closed interval after unbind and keeps bind controls', () => {
    const state = reduceProjectMapping(readyState(boundView), {
      type: 'unbind-succeeded',
      projectHash: PROJECT_HASH,
      view: unboundView,
    })
    const html = renderPanel(state)
    expect(html).toContain('project mapping removed')
    expect(html).toContain('no active project mapping')
    expect(html).toContain('bind project')
    expect(html).not.toContain('>unbind</button>')
    expect(html).toContain('mapping history')
    expect(html).toContain('closed')
  })

  it('explains that no epoch exists instead of offering an empty bind form', () => {
    const html = renderPanel(readyState(null), { epochList: [] })
    expect(html).toContain('Start an epoch before binding')
    expect(html).not.toContain('<select')
  })

  it('keeps unbind available when the epoch list is empty', () => {
    const html = renderPanel(readyState(boundView), { epochList: [] })
    expect(html).toContain('>unbind</button>')
    expect(html).toContain('no projected epochs are available to bind')
    expect(html).not.toContain('rebind project')
    expect(html).not.toContain('<select')
  })
})
