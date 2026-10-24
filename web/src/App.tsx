import { useCallback, useEffect, useId, useMemo, useReducer, useRef, useState } from 'react'
import {
  Activity,
  Clock3,
  Database,
  Folder,
  Radio,
  RefreshCw,
  Waypoints,
} from 'lucide-react'
import {
  Button,
  Chip,
  ConnectionPill,
  DataTable,
  EmptyState,
  FilterChip,
  RailSection,
  RailShell,
  Select,
  StatGrid,
  StatusDot,
  TranscriptViewer,
} from '@peasant-labs/fairtrade/ui'

import {
  bindProject,
  fetchInspect,
  fetchProject,
  fetchTranscript,
  socketURL,
  subscribe,
  unbindProject,
  type EpochView,
  type ProjectRole,
  type ProjectView,
  type SessionBindingInterval,
  type SessionView,
  type WireFrame,
} from './api'
import {
  adaptTranscriptPayload,
  describeTranscriptError,
  type TranscriptFailure,
  type TranscriptViewModel,
} from './transcript'
import {
  epochLabel,
  formatTimelineDuration,
  projectSessionTimeline,
} from './timeline'
import {
  filterSessionsByProject,
  integrationFromSessionsFrame,
  peasantIntegrationPresentation,
  projectDetailMessage,
  projectLabel,
  projectOptions,
  type PeasantIntegration,
  type ProjectIdentity,
  type ProjectOption,
} from './project'
import {
  DEFAULT_PROJECT_ROLE,
  EMPTY_PROJECT_MAPPING,
  epochOptions,
  mappingIntervalEpochLabel,
  mappingSummary,
  projectBindRequest,
  projectMappingForSelection,
  projectMappingHistory,
  projectMappingNoProjectMessage,
  projectMappingStatusLabel,
  projectRoleFromValue,
  projectRoleOptions,
  projectUnbindRequest,
  reduceProjectMapping,
  type ProjectMappingState,
} from './projectMapping'

type Connection = 'connecting' | 'live' | 'disconnected'
type StateFilter = 'all' | 'active' | 'idle' | 'ended' | 'unknown'

// Every non-idle state carries the session it belongs to, so a selection
// change can never paint the previous session's transcript for a frame.
type TranscriptState =
  | { status: 'idle' }
  | { status: 'loading'; key: string }
  | { status: 'ready'; key: string; viewModel: TranscriptViewModel }
  | { status: 'error'; key: string; failure: TranscriptFailure }

// The console is a read-only observer: labelling, editing, contributing,
// visibility changes, and export belong to producers with their own authority.
const READ_ONLY_TRANSCRIPT_CAPABILITIES = {
  canEdit: false,
  canLabel: false,
  canContribute: false,
  canChangeVisibility: false,
  canExport: false,
} as const

type SessionRow = {
  id: string
  harness: string
  nativeSessionId: string
  state: string
  project?: string
  projectHash?: string
  projectSort: string
  epoch: string
  epochId: string
  lastEvent: string
  lastSeen: string
  lastSeenSort: number
}

const STATE_FILTERS: StateFilter[] = ['all', 'active', 'idle', 'ended', 'unknown']

const STATE_COLOR: Record<string, string> = {
  active: 'var(--success)',
  idle: 'var(--warning)',
  ended: 'var(--ink-4)',
  unknown: 'var(--ink-5)',
}

function sessionKey(session: SessionView): string {
  return `${session.harness}/${session.nativeSessionId}`
}

function formatTime(value?: string | null): string {
  if (!value) return '—'
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return value
  return parsed.toLocaleString()
}

/**
 * BindingHistory is the selected session's historical timeline: one band per
 * interval, chronologically ordered by the pure projection in timeline.ts. It
 * keeps inferred bands visually distinct so a provisional epoch never looks
 * authoritative. It is exported for the provenance render test.
 */
export function BindingHistory({ bindings }: { bindings: SessionBindingInterval[] }) {
  const segments = projectSessionTimeline(bindings)
  return (
    <div className="binding-history">
      <p className="eyebrow">binding history</p>
      {segments.length === 0 ? (
        <p className="rail-copy muted">
          no binding interval is recorded for this session. A band appears after an
          explicit bind; an inferred band appears only while an assignment epoch is
          currently derived.
        </p>
      ) : (
        <ol className="timeline-list" aria-label="binding timeline">
          {segments.map((segment) => (
            <li
              key={segment.bindingId}
              className="timeline-band"
              data-binding-id={segment.bindingId}
              data-source={segment.source}
              data-open={segment.open ? 'true' : 'false'}
              data-inverted={segment.inverted ? 'true' : 'false'}
            >
              <div className="timeline-band-head">
                <span className="timeline-epoch">{segment.epochLabel}</span>
                <span className="timeline-marker">{segment.open ? 'open' : 'closed'}</span>
              </div>
              <div className="timeline-chips">
                <span className="timeline-chip timeline-chip-source" data-source={segment.source}>
                  {segment.source}
                </span>
                <span className="timeline-chip timeline-chip-confidence">
                  {segment.confidence}
                </span>
                <span className="timeline-chip timeline-chip-role">{segment.role}</span>
              </div>
              {segment.startMs === null ? (
                <p className="timeline-range">start unknown</p>
              ) : (
                <p className="timeline-range">
                  {formatTime(segment.start)} →{' '}
                  {segment.open
                    ? 'open'
                    : segment.endKnown
                      ? formatTime(segment.end)
                      : 'end unknown'}
                </p>
              )}
              {segment.end !== null && (segment.startMs === null || !segment.endKnown) && (
                <p className="timeline-note">recorded end: {formatTime(segment.end)}</p>
              )}
              {!segment.open && segment.durationMs !== null && (
                <p className="timeline-duration">
                  {formatTimelineDuration(segment.durationMs)}
                  {segment.endReason ? ` · ended: ${segment.endReason}` : ''}
                </p>
              )}
              {segment.endReason && segment.durationMs === null && (
                <p className="timeline-note">ended: {segment.endReason}</p>
              )}
              {segment.inverted && (
                <p className="timeline-note">end precedes start; clamped to zero duration</p>
              )}
              {segment.epochId && segment.epochId !== segment.epochLabel && (
                <p className="timeline-epoch-id">epoch id: {segment.epochId}</p>
              )}
            </li>
          ))}
        </ol>
      )}
    </div>
  )
}

/**
 * ProjectCell is the table cell for Peasant-owned project identity. It shows the
 * name, the hash when no name was recorded, or an honest unknown marker. The
 * title carries the label and the full hash, so a clipped label or hash stays
 * inspectable.
 */
export function ProjectCell({ project, projectHash }: ProjectIdentity) {
  const label = projectLabel({ project, projectHash })
  const name = project?.trim()
  const unknown = !name && !projectHash
  const title = name && projectHash ? `${name} · ${projectHash}` : projectHash || name || undefined
  return (
    <span
      className={unknown ? 'project-cell is-unknown' : 'project-cell'}
      title={title}
    >
      {label}
    </span>
  )
}

/**
 * ProjectDetail is the selected-session rail block for Peasant-owned project
 * identity. It is deliberately separate from the epoch binding: project fields
 * come from Peasant and are not a Pasture binding. It also receives the
 * integration state so a missing project is never blamed on Peasant when the
 * source is disabled or unavailable.
 */
export function ProjectDetail({
  session,
  integration,
}: {
  session: ProjectIdentity
  integration: PeasantIntegration | null
}) {
  const label = projectLabel(session)
  return (
    <div className="project-detail" data-integration={integration?.status ?? 'unknown'}>
      <p className="eyebrow">peasant-owned project</p>
      <dl className="detail-list">
        <div><dt>project</dt><dd>{label}</dd></div>
        {session.projectHash && label !== session.projectHash && (
          <div><dt>hash</dt><dd className="mono-wrap">{session.projectHash}</dd></div>
        )}
      </dl>
      <p className="rail-copy muted">{projectDetailMessage(session, integration)}</p>
    </div>
  )
}

/**
 * ProjectFilter is the toolbar project selector. Options are keyed by the exact
 * Peasant project hash; an empty value is the unfiltered inventory.
 */
export function ProjectFilter({
  options,
  value,
  onChange,
  disabled = false,
}: {
  options: ProjectOption[]
  value: string | null
  onChange: (hash: string | null) => void
  disabled?: boolean
}) {
  const selectId = useId()
  return (
    <div className="toolbar-group project-filter">
      <label className="eyebrow" htmlFor={selectId}>project</label>
      <Select
        id={selectId}
        value={value ?? ''}
        onChange={(event) => onChange(event.currentTarget.value || null)}
        options={[
          { value: '', label: 'all projects' },
          ...options.map((option) => ({ value: option.hash, label: option.label })),
        ]}
        disabled={disabled}
      />
    </div>
  )
}

/**
 * PeasantIntegrationState renders the tri-state opt-in project integration from
 * /api/v1/inspect. Disabled is a configuration choice, unavailable is a failed
 * configured source, and ok is the quiet normal state.
 */
export function PeasantIntegrationState({
  integration,
}: {
  integration: PeasantIntegration | null
}) {
  if (!integration) {
    return <p className="rail-copy muted">reading the Peasant project source…</p>
  }
  const state = peasantIntegrationPresentation(integration)
  return (
    <div className="peasant-integration" data-status={state.status}>
      <div className="peasant-integration-head">
        <span className="peasant-integration-title">{state.title}</span>
        <span className="peasant-integration-status">{state.status}</span>
      </div>
      {state.sourceLabel !== 'none' && (
        <p className="rail-copy muted">source: {state.sourceLabel}</p>
      )}
      {state.message && <p className="rail-copy">{state.message}</p>}
      {state.commands.length > 0 && (
        <ul className="peasant-integration-commands">
          {state.commands.map((command) => (
            <li key={command}><code>{command}</code></li>
          ))}
        </ul>
      )}
    </div>
  )
}

/**
 * ProjectMappingHistory is the project's interval history. It is deliberately
 * separate from BindingHistory: those intervals belong to the selected
 * session's binding, while these belong to the Peasant project.
 */
export function ProjectMappingHistory({ view }: { view: ProjectView }) {
  const intervals = projectMappingHistory(view)
  if (intervals.length === 0) return null
  return (
    <div className="project-mapping-history">
      <p className="eyebrow">mapping history</p>
      <ol className="project-mapping-intervals" aria-label="project mapping history">
        {intervals.map((interval) => (
          <li
            key={interval.bindingId}
            className="project-mapping-interval"
            data-open={interval.unboundAt ? 'false' : 'true'}
            data-binding-id={interval.bindingId}
          >
            <div className="project-mapping-interval-head">
              <span className="project-mapping-interval-epoch">
                {mappingIntervalEpochLabel(interval)}
              </span>
              <StatusDot
                label={interval.unboundAt ? 'closed' : 'open'}
                color={interval.unboundAt ? 'var(--ink-4)' : 'var(--success)'}
              />
            </div>
            <div className="project-mapping-chips">
              <span className="project-mapping-role">{interval.role}</span>
              <Chip size="sm" chrome>{interval.source}</Chip>
              <Chip size="sm" tone="ok" chrome>{interval.confidence}</Chip>
            </div>
            <p className="project-mapping-range">
              {formatTime(interval.boundAt)} →{' '}
              {interval.unboundAt ? formatTime(interval.unboundAt) : 'open'}
              {interval.endReason ? ` · ${interval.endReason}` : ''}
            </p>
          </li>
        ))}
      </ol>
    </div>
  )
}

/**
 * ProjectMappingPanel shows the project-level epoch mapping for the selected
 * session's Peasant project. It is explicitly not the session's own epoch
 * binding: the note stays visible in every state, and the session binding above
 * remains the authoritative one. A missing project hash is explained from the
 * Peasant integration state instead of offering a control that cannot work.
 */
export function ProjectMappingPanel({
  state,
  epochs,
  integration,
  onBind,
  onUnbind,
  onRefresh,
}: {
  state: ProjectMappingState
  epochs: EpochView[]
  integration: PeasantIntegration | null
  onBind: (epochId: string, role: ProjectRole) => void
  onUnbind: () => void
  onRefresh: () => void
}) {
  const [chosenEpochId, setChosenEpochId] = useState('')
  const [chosenRole, setChosenRole] = useState<ProjectRole | ''>('')

  const epochChoices = epochOptions(epochs)
  const currentEpochId = state.view?.epoch?.id ?? ''
  const defaultEpochId =
    currentEpochId && epochChoices.some((option) => option.value === currentEpochId)
      ? currentEpochId
      : (epochChoices[0]?.value ?? '')
  // A live epoch-list change can remove the previously chosen epoch; fall back
  // rather than offering a bind to an epoch that no longer exists.
  const knownChosenEpochId =
    chosenEpochId && epochChoices.some((option) => option.value === chosenEpochId)
      ? chosenEpochId
      : ''
  const selectedEpochId = knownChosenEpochId || defaultEpochId
  // A new binding starts at the API's worker default; rebinding starts at the
  // role already recorded, so changing only the epoch cannot silently change it.
  const selectedRole = chosenRole || state.view?.binding?.role || DEFAULT_PROJECT_ROLE
  const busy = state.action === 'binding' || state.action === 'unbinding'
  const canBind =
    state.load === 'ready' && selectedEpochId !== '' && epochChoices.length > 0 && !busy
  const canUnbind = state.load === 'ready' && Boolean(state.view?.binding) && !busy

  const handleBind = () => {
    if (!canBind) return
    onBind(selectedEpochId, selectedRole)
  }

  const handleUnbind = () => {
    if (!canUnbind) return
    onUnbind()
  }

  return (
    <div
      className="project-mapping"
      data-status={projectMappingStatusLabel(state)}
      data-integration={integration?.status ?? 'unknown'}
    >
      <p className="eyebrow">project-level mapping</p>
      <p className="rail-copy muted">
        This maps the Peasant project to an epoch. It is separate from, and never
        overrides, the session&apos;s own epoch binding above.
      </p>

      {state.load === 'no-project' && (
        <p className="rail-copy muted">{projectMappingNoProjectMessage(integration)}</p>
      )}

      {state.load === 'loading' && (
        <p className="rail-copy muted">reading the project-level mapping…</p>
      )}

      {state.load === 'error' && state.failure && (
        <div className="project-mapping-error" role="alert">
          <p className="rail-copy">{state.failure.message}</p>
          {state.failure.retry && (
            <button type="button" className="clear-filter" onClick={onRefresh}>
              retry project mapping
            </button>
          )}
        </div>
      )}

      {state.load === 'ready' && (
        <>
          {state.view?.binding ? (
            <div className="project-mapping-current">
              <p className="eyebrow">active project mapping</p>
              <p className="project-mapping-epoch">{mappingSummary(state.view)}</p>
              <dl className="detail-list">
                <div><dt>role</dt><dd>{state.view.binding.role}</dd></div>
                <div><dt>bound</dt><dd>{formatTime(state.view.binding.boundAt)}</dd></div>
                <div>
                  <dt>source</dt>
                  <dd><Chip size="sm" chrome>{state.view.binding.source}</Chip></dd>
                </div>
                <div>
                  <dt>confidence</dt>
                  <dd><Chip size="sm" tone="ok" chrome>{state.view.binding.confidence}</Chip></dd>
                </div>
              </dl>
            </div>
          ) : (
            <p className="rail-copy muted">
              {epochChoices.length === 0
                ? 'no active project mapping. Start an epoch before binding this project.'
                : 'no active project mapping. Choose an epoch and role to bind this project.'}
            </p>
          )}

          {state.action === 'binding' && (
            <p className="rail-copy muted" role="status">binding the project mapping…</p>
          )}

          {state.action === 'unbinding' && (
            <p className="rail-copy muted" role="status">removing the project mapping…</p>
          )}

          {state.action === 'success' && state.actionMessage && (
            <p className="project-mapping-success" role="status">{state.actionMessage}</p>
          )}

          {state.action === 'error' && state.failure && (
            <div className="project-mapping-error" role="alert">
              <p className="rail-copy">{state.failure.message}</p>
              {state.failure.retry && (
                <button type="button" className="clear-filter" onClick={onRefresh}>
                  refresh project mapping
                </button>
              )}
            </div>
          )}

          {(epochChoices.length > 0 || state.view?.binding) && (
            <div className="project-mapping-form">
              {epochChoices.length > 0 && (
                <>
                  <Select
                    label="epoch"
                    options={epochChoices}
                    value={selectedEpochId}
                    onChange={(event) => setChosenEpochId(event.currentTarget.value)}
                    disabled={busy}
                  />
                  <Select
                    label="role"
                    options={projectRoleOptions()}
                    value={selectedRole}
                    onChange={(event) => {
                      const role = projectRoleFromValue(event.currentTarget.value)
                      if (role) setChosenRole(role)
                    }}
                    disabled={busy}
                  />
                </>
              )}
              <div className="project-mapping-actions">
                {epochChoices.length > 0 && (
                  <Button
                    variant="secondary"
                    size="sm"
                    loading={state.action === 'binding'}
                    disabled={!canBind}
                    {...({ onClick: handleBind } as object)}
                  >
                    {state.view?.binding ? 'rebind project' : 'bind project'}
                  </Button>
                )}
                {state.view?.binding && (
                  <Button
                    variant="ghost"
                    size="sm"
                    loading={state.action === 'unbinding'}
                    disabled={!canUnbind}
                    {...({ onClick: handleUnbind } as object)}
                  >
                    unbind
                  </Button>
                )}
              </div>
            </div>
          )}

          {epochChoices.length === 0 && state.view?.binding && (
            <p className="rail-copy muted">
              no projected epochs are available to bind; the current project mapping can
              still be unbound.
            </p>
          )}

          {state.view && <ProjectMappingHistory view={state.view} />}
        </>
      )}
    </div>
  )
}

export default function App() {
  const [sessions, setSessions] = useState<SessionView[]>([])
  const [epochs, setEpochs] = useState<EpochView[]>([])
  const [revision, setRevision] = useState(0)
  const [asOf, setAsOf] = useState<string | null>(null)
  const [connection, setConnection] = useState<Connection>('connecting')
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [truncated, setTruncated] = useState(false)
  const [stateFilter, setStateFilter] = useState<StateFilter>('all')
  const [epochFilter, setEpochFilter] = useState<string | null>(null)
  const [projectFilter, setProjectFilter] = useState<string | null>(null)
  const [peasantIntegration, setPeasantIntegration] = useState<PeasantIntegration | null>(null)
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [transcript, setTranscript] = useState<TranscriptState>({ status: 'idle' })
  const [transcriptRetry, setTranscriptRetry] = useState(0)
  const [transcriptLeftRail, setTranscriptLeftRail] = useState(false)
  const [transcriptRightRail, setTranscriptRightRail] = useState(false)
  const [projectMapping, dispatchProjectMapping] = useReducer(
    reduceProjectMapping,
    EMPTY_PROJECT_MAPPING,
  )
  const [projectMappingRetry, setProjectMappingRetry] = useState(0)
  const socketRef = useRef<WebSocket | null>(null)

  const load = useCallback(async (signal?: AbortSignal) => {
    setLoading(true)
    setLoadError(null)
    try {
      const payload = await fetchInspect(signal)
      setSessions(payload.sessions)
      setEpochs(payload.epochs)
      setRevision(payload.revision)
      setAsOf(payload.asOf)
      setTruncated(payload.truncated)
      setPeasantIntegration(payload.peasantIntegration ?? null)
    } catch (error) {
      if (!signal?.aborted) {
        setLoadError(error instanceof Error ? error.message : 'the local API did not answer')
      }
    } finally {
      if (!signal?.aborted) setLoading(false)
    }
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    void load(controller.signal)
    return () => controller.abort()
  }, [load])

  useEffect(() => {
    let closed = false
    let retry: number | undefined
    let socket: WebSocket | null = null

    const connect = () => {
      if (closed) return
      setConnection('connecting')
      socket = new WebSocket(socketURL())
      socketRef.current = socket

      socket.onopen = () => setConnection('live')
      socket.onmessage = (event) => {
        let frame: WireFrame
        try {
          frame = JSON.parse(event.data) as WireFrame
        } catch {
          return
        }
        switch (frame.type) {
          case 'ready':
            subscribe(socket!, 'sessions')
            subscribe(socket!, 'epochs')
            break
          case 'sessions':
            setSessions(frame.payload.items)
            setRevision(frame.revision)
            setAsOf(frame.asOf)
            setTruncated(Boolean(frame.payload.next))
            setPeasantIntegration(integrationFromSessionsFrame(frame.payload))
            setLoading(false)
            break
          case 'epochs':
            setEpochs(frame.payload.items)
            setRevision(frame.revision)
            setAsOf(frame.asOf)
            break
          case 'error':
            setLoadError(frame.error.message)
            break
        }
      }
      socket.onclose = () => {
        if (closed) return
        setConnection('disconnected')
        retry = window.setTimeout(connect, 2500)
      }
      socket.onerror = () => {
        socket?.close()
      }
    }

    connect()
    return () => {
      closed = true
      if (retry !== undefined) window.clearTimeout(retry)
      socket?.close()
      socketRef.current = null
    }
  }, [])

  // Project options come from the current inventory and are keyed by the exact
  // Peasant project hash. A selected hash that no longer appears falls back to
  // the unfiltered inventory rather than showing an impossible filter.
  const projects = useMemo(() => projectOptions(sessions), [sessions])
  const activeProject =
    projectFilter && projects.some((project) => project.hash === projectFilter)
      ? projectFilter
      : null

  const rows = useMemo<SessionRow[]>(() => {
    const filtered = sessions.filter((session) => {
      if (stateFilter !== 'all' && session.activity.state !== stateFilter) return false
      if (epochFilter && session.epoch?.id !== epochFilter) return false
      return true
    })
    return filterSessionsByProject(filtered, activeProject).map((session) => ({
      id: sessionKey(session),
      harness: session.harness,
      nativeSessionId: session.nativeSessionId,
      state: session.activity.state,
      project: session.project,
      projectHash: session.projectHash,
      projectSort: projectLabel(session),
      // session.epoch is the effective binding projection: the latest
      // explicit binding, or the open inferred derivation. Inferred-only
      // sessions therefore show their epoch here without a table change.
      epoch: epochLabel(session.epoch),
      epochId: session.epoch?.id ?? '',
      lastEvent: session.activity.lastEvent ?? '—',
      lastSeen: formatTime(session.activity.lastSeen),
      lastSeenSort: session.activity.lastSeen ? Date.parse(session.activity.lastSeen) || 0 : 0,
    }))
  }, [sessions, stateFilter, epochFilter, activeProject])

  const selectedSession = useMemo(
    () => sessions.find((session) => sessionKey(session) === selectedId) ?? null,
    [sessions, selectedId],
  )

  // The project mapping is keyed by the Peasant project hash, not by the
  // selected session: two sessions in the same project share one mapping.
  const selectedProjectHash = selectedSession?.projectHash ?? null
  const selectedMapping = projectMappingForSelection(projectMapping, selectedProjectHash)

  const selectedHarness = selectedSession?.harness
  const selectedNativeId = selectedSession?.nativeSessionId
  const transcriptKey =
    selectedHarness && selectedNativeId ? `${selectedHarness}/${selectedNativeId}` : null

  // Fetch once per selected session, not on every WebSocket snapshot: the
  // stable identity strings are the dependencies, so live revision updates
  // never restart the Peasant export.
  useEffect(() => {
    if (!selectedHarness || !selectedNativeId || !transcriptKey) {
      setTranscript({ status: 'idle' })
      return
    }
    const controller = new AbortController()
    setTranscript({ status: 'loading', key: transcriptKey })
    fetchTranscript(selectedHarness, selectedNativeId, controller.signal)
      .then((value) => {
        if (controller.signal.aborted) return
        setTranscript({
          status: 'ready',
          key: transcriptKey,
          viewModel: adaptTranscriptPayload(value),
        })
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return
        setTranscript({ status: 'error', key: transcriptKey, failure: describeTranscriptError(error) })
      })
    return () => controller.abort()
  }, [selectedHarness, selectedNativeId, transcriptKey, transcriptRetry])

  // Fetch the project-level mapping once per selected project hash. A 404 is
  // classified by the reducer as "never mapped" and leaves the bind controls
  // available; it is not surfaced as an error.
  useEffect(() => {
    if (!selectedProjectHash) {
      dispatchProjectMapping({ type: 'load', projectHash: null })
      return
    }
    const controller = new AbortController()
    dispatchProjectMapping({ type: 'load', projectHash: selectedProjectHash })
    fetchProject(selectedProjectHash, controller.signal)
      .then((view) => {
        if (controller.signal.aborted) return
        dispatchProjectMapping({ type: 'loaded', projectHash: selectedProjectHash, view })
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return
        dispatchProjectMapping({ type: 'load-failed', projectHash: selectedProjectHash, error })
      })
    return () => controller.abort()
  }, [selectedProjectHash, projectMappingRetry])

  const bindSelectedProject = useCallback(
    async (epochId: string, role: ProjectRole) => {
      const projectHash = selectedProjectHash
      if (!projectHash) return
      dispatchProjectMapping({ type: 'bind' })
      try {
        const view = await bindProject(
          projectHash,
          projectBindRequest(selectedMapping.view, epochId, role),
        )
        dispatchProjectMapping({ type: 'bind-succeeded', projectHash, view })
      } catch (error) {
        dispatchProjectMapping({ type: 'action-failed', projectHash, error })
      }
    },
    [selectedProjectHash, selectedMapping.view],
  )

  const unbindSelectedProject = useCallback(async () => {
    const projectHash = selectedProjectHash
    if (!projectHash) return
    dispatchProjectMapping({ type: 'unbind' })
    try {
      const view = await unbindProject(
        projectHash,
        projectUnbindRequest(selectedMapping.view),
      )
      dispatchProjectMapping({ type: 'unbind-succeeded', projectHash, view })
    } catch (error) {
      dispatchProjectMapping({ type: 'action-failed', projectHash, error })
    }
  }, [selectedProjectHash, selectedMapping.view])

  const refreshProjectMapping = useCallback(() => {
    setProjectMappingRetry((attempt) => attempt + 1)
  }, [])

  const activeCount = sessions.filter((session) => session.activity.state === 'active').length
  const unknownCount = sessions.filter((session) => session.activity.state === 'unknown').length

  const columns = useMemo(
    () => [
      {
        key: 'nativeSessionId',
        label: 'session',
        sortable: true,
        width: '24rem',
        render: (_: unknown, row: SessionRow) => (
          <div className="session-cell">
            <span className="session-harness">{row.harness}</span>
            <button
              type="button"
              className="session-link"
              onClick={() => setSelectedId(row.id)}
            >
              {row.nativeSessionId}
            </button>
          </div>
        ),
      },
      {
        key: 'projectSort',
        label: 'project',
        sortable: true,
        width: '14rem',
        render: (_: unknown, row: SessionRow) => (
          <ProjectCell project={row.project} projectHash={row.projectHash} />
        ),
      },
      {
        key: 'state',
        label: 'state',
        sortable: true,
        width: '8rem',
        render: (_: unknown, row: SessionRow) => (
          <StatusDot label={row.state} color={STATE_COLOR[row.state]} />
        ),
      },
      {
        key: 'epoch',
        label: 'epoch',
        sortable: true,
        width: '12rem',
        render: (_: unknown, row: SessionRow) => (
          <span className={row.epochId ? 'epoch-value' : 'muted'}>{row.epoch}</span>
        ),
      },
      {
        key: 'lastEvent',
        label: 'last event',
        sortable: true,
        width: '12rem',
        render: (_: unknown, row: SessionRow) => (
          <code className="event-value">{row.lastEvent}</code>
        ),
      },
      {
        key: 'lastSeenSort',
        label: 'last seen',
        sortable: true,
        width: '12rem',
        render: (_: unknown, row: SessionRow) => (
          <span className="muted tnum last-seen">{row.lastSeen}</span>
        ),
      },
    ],
    [],
  )

  const retry = () => {
    void load()
    socketRef.current?.close()
  }

  return (
    <RailShell
      sheetMeta={null}
      toolbar={
        <div className="toolbar">
          <div className="toolbar-group">
            <span className="eyebrow">state</span>
            {STATE_FILTERS.map((state) => (
              <FilterChip
                key={state}
                pressed={stateFilter === state}
                onChange={() => setStateFilter(state === 'all' ? 'all' : state === stateFilter ? 'all' : state)}
              >
                {state}
              </FilterChip>
            ))}
          </div>
          <ProjectFilter
            options={projects}
            value={activeProject}
            onChange={setProjectFilter}
            disabled={projects.length === 0}
          />
          <Button
            variant="ghost"
            size="sm"
            icon={RefreshCw}
            loading={loading}
            {...({ onClick: retry } as object)}
          >
            refresh
          </Button>
        </div>
      }
      rail={
        <>
          <RailSection
            title="peasant projects"
            icon={Folder}
            meta={peasantIntegration?.status ?? 'loading'}
          >
            <PeasantIntegrationState integration={peasantIntegration} />
          </RailSection>

          <RailSection title="epochs" icon={Waypoints} meta={String(epochs.length)}>
            {epochs.length === 0 ? (
              <p className="rail-copy muted">
                no projected epochs in this store. sessions can still appear as unbound.
              </p>
            ) : (
              <div className="epoch-list">
                <button
                  type="button"
                  className="epoch-option"
                  data-selected={epochFilter === null ? 'true' : 'false'}
                  onClick={() => setEpochFilter(null)}
                >
                  <span>all epochs</span>
                  <span className="tnum">{sessions.length}</span>
                </button>
                {epochs.map((epoch) => (
                  <button
                    key={epoch.id}
                    type="button"
                    className="epoch-option"
                    data-selected={epochFilter === epoch.id ? 'true' : 'false'}
                    onClick={() => setEpochFilter(epochFilter === epoch.id ? null : epoch.id)}
                  >
                    <span>{epochLabel(epoch)}</span>
                    <span className="tnum">{epoch.sessionCount}</span>
                  </button>
                ))}
              </div>
            )}
          </RailSection>

          <RailSection title="session" icon={Activity} meta={null}>
            {selectedSession ? (
              <div className="detail">
                <p className="detail-title">{selectedSession.nativeSessionId}</p>
                <dl className="detail-list">
                  <div><dt>harness</dt><dd>{selectedSession.harness}</dd></div>
                  <div><dt>state</dt><dd>{selectedSession.activity.state}</dd></div>
                  <div><dt>epoch</dt><dd>{epochLabel(selectedSession.epoch)}</dd></div>
                  <div><dt>role</dt><dd>{selectedSession.binding?.role ?? '—'}</dd></div>
                  <div><dt>binding</dt><dd className="mono-wrap">{selectedSession.binding?.bindingId ?? '—'}</dd></div>
                  <div><dt>source</dt><dd>{selectedSession.binding?.source ?? '—'}</dd></div>
                  <div><dt>confidence</dt><dd>{selectedSession.binding?.confidence ?? '—'}</dd></div>
                  <div><dt>first seen</dt><dd>{formatTime(selectedSession.activity.firstSeen)}</dd></div>
                  <div><dt>last seen</dt><dd>{formatTime(selectedSession.activity.lastSeen)}</dd></div>
                  {selectedSession.end && (
                    <div><dt>ended</dt><dd>{formatTime(selectedSession.end.endedAt)} · {selectedSession.end.endReason}</dd></div>
                  )}
                </dl>

                <ProjectDetail session={selectedSession} integration={peasantIntegration} />

                <BindingHistory bindings={selectedSession.bindings ?? []} />
              </div>
            ) : (
              <p className="rail-copy muted">choose a session in the table to inspect its binding.</p>
            )}
          </RailSection>

          {selectedSession && (
            <RailSection
              title="project mapping"
              icon={Folder}
              meta={projectMappingStatusLabel(selectedMapping)}
            >
              <ProjectMappingPanel
                // Reset the local epoch/role form when the project changes, so
                // one project's choices never leak into another project's bind.
                key={selectedProjectHash ?? 'no-project'}
                state={selectedMapping}
                epochs={epochs}
                integration={peasantIntegration}
                onBind={bindSelectedProject}
                onUnbind={unbindSelectedProject}
                onRefresh={refreshProjectMapping}
              />
            </RailSection>
          )}

          <RailSection
            title="transcript"
            icon={Database}
            meta={transcript.status === 'ready' && transcript.key === transcriptKey ? 'peasant' : null}
          >
            {!selectedSession || !transcriptKey ? (
              <p className="rail-copy muted">
                choose a session in the table to read its Peasant transcript.
              </p>
            ) : transcript.status === 'error' && transcript.key === transcriptKey ? (
              <div className="transcript-state">
                <EmptyState
                  title={transcript.failure.title}
                  message={transcript.failure.message}
                  action={
                    transcript.failure.retry ? (
                      <button
                        type="button"
                        className="clear-filter"
                        onClick={() => setTranscriptRetry((attempt) => attempt + 1)}
                      >
                        retry transcript
                      </button>
                    ) : undefined
                  }
                />
              </div>
            ) : transcript.status === 'ready' && transcript.key === transcriptKey ? (
              <div className="transcript-host">
                <TranscriptViewer
                  viewModel={transcript.viewModel}
                  capabilities={READ_ONLY_TRANSCRIPT_CAPABILITIES}
                  leftRailOpen={transcriptLeftRail}
                  onLeftRailOpenChange={setTranscriptLeftRail}
                  rightRailOpen={transcriptRightRail}
                  onRightRailOpenChange={setTranscriptRightRail}
                />
              </div>
            ) : (
              <p className="rail-copy muted">exporting the Peasant transcript for this session…</p>
            )}
          </RailSection>
        </>
      }
    >
      <header className="hero">
        <div>
          <p className="eyebrow">pasture / local console</p>
          <h1>session epochs</h1>
          <p className="lede">
            Which harness sessions are active, idle, ended, or waiting for an epoch binding.
            Everything here comes from the local Pasture store.
          </p>
        </div>
        <div className="hero-meta">
          <ConnectionPill status={connection} />
          <span className="mono-meta">rev {revision}</span>
          <span className="mono-meta">{asOf ? formatTime(asOf) : 'no snapshot'}</span>
        </div>
      </header>

      <StatGrid
        tiles={[
          { key: 'sessions', label: 'sessions', value: sessions.length, sub: `${rows.length} shown`, icon: Radio },
          { key: 'active', label: 'active', value: activeCount, sub: 'inside the window', icon: Activity },
          { key: 'epochs', label: 'epochs', value: epochs.length, sub: 'projected', icon: Waypoints },
          { key: 'unbound', label: 'unknown', value: unknownCount, sub: 'no epoch binding', icon: Clock3 },
        ]}
      />

      <section className="sessions-panel" aria-labelledby="sessions-heading">
        <div className="section-head">
          <div>
            <p className="eyebrow">inventory</p>
            <h2 id="sessions-heading">sessions</h2>
          </div>
          <div className="section-meta">
            {truncated && <span className="mono-meta">first 100 shown</span>}
            {epochFilter && (
              <button type="button" className="clear-filter" onClick={() => setEpochFilter(null)}>
                clear epoch filter
              </button>
            )}
            {activeProject && (
              <button type="button" className="clear-filter" onClick={() => setProjectFilter(null)}>
                clear project filter
              </button>
            )}
          </div>
        </div>

        {loadError && <p className="error-line" role="alert">{loadError}</p>}

        {loading && sessions.length === 0 ? (
          <p className="rail-copy muted">reading the local session cache…</p>
        ) : rows.length === 0 ? (
          <EmptyState
            title="no sessions match"
            message={
              sessions.length === 0
                ? 'The local store has no claimed or bound sessions yet. Start a harness session, or restart serve with --backfill to load history.'
                : 'Adjust the state, epoch, or project filter to widen the inventory.'
            }
          />
        ) : (
          <DataTable
            className="sessions-table"
            columns={columns}
            rows={rows}
            rowKey={(row: SessionRow) => row.id}
            caption="harness sessions and their epoch bindings"
            defaultSort={{ key: 'lastSeenSort', dir: 'desc' }}
          />
        )}
      </section>
    </RailShell>
  )
}
