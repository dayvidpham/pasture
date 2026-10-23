import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  Activity,
  Clock3,
  Database,
  Radio,
  RefreshCw,
  Waypoints,
} from 'lucide-react'
import {
  Button,
  ConnectionPill,
  DataTable,
  EmptyState,
  FilterChip,
  RailSection,
  RailShell,
  StatGrid,
  StatusDot,
  TranscriptViewer,
} from '@peasant-labs/fairtrade/ui'

import {
  fetchInspect,
  fetchTranscript,
  socketURL,
  subscribe,
  type EpochView,
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
          no binding intervals are recorded for this session. If it predates this
          console's history, restart `pasture serve` with `--backfill`.
        </p>
      ) : (
        <ol className="timeline-list" aria-label="binding timeline">
          {segments.map((segment) => (
            <li
              key={segment.bindingId}
              className="timeline-band"
              data-source={segment.source}
              data-open={segment.open ? 'true' : 'false'}
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
              <p className="timeline-range">
                {formatTime(segment.start)} → {segment.open ? 'open' : formatTime(segment.end)}
              </p>
              <p className="timeline-duration">
                {formatTimelineDuration(segment.durationMs)}
                {segment.endReason ? ` · ended: ${segment.endReason}` : ''}
              </p>
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
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [transcript, setTranscript] = useState<TranscriptState>({ status: 'idle' })
  const [transcriptRetry, setTranscriptRetry] = useState(0)
  const [transcriptLeftRail, setTranscriptLeftRail] = useState(false)
  const [transcriptRightRail, setTranscriptRightRail] = useState(false)
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

  const rows = useMemo<SessionRow[]>(() => {
    return sessions
      .filter((session) => {
        if (stateFilter !== 'all' && session.activity.state !== stateFilter) return false
        if (epochFilter && session.epoch?.id !== epochFilter) return false
        return true
      })
      .map((session) => ({
        id: sessionKey(session),
        harness: session.harness,
        nativeSessionId: session.nativeSessionId,
        state: session.activity.state,
        // session.epoch is the effective binding projection: the latest
        // explicit binding, or the open inferred derivation. Inferred-only
        // sessions therefore show their epoch here without a table change.
        epoch: epochLabel(session.epoch),
        epochId: session.epoch?.id ?? '',
        lastEvent: session.activity.lastEvent ?? '—',
        lastSeen: formatTime(session.activity.lastSeen),
        lastSeenSort: session.activity.lastSeen ? Date.parse(session.activity.lastSeen) || 0 : 0,
      }))
  }, [sessions, stateFilter, epochFilter])

  const selectedSession = useMemo(
    () => sessions.find((session) => sessionKey(session) === selectedId) ?? null,
    [sessions, selectedId],
  )

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
                    <span>{epoch.title?.trim() || epoch.id}</span>
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

                <BindingHistory bindings={selectedSession.bindings ?? []} />
              </div>
            ) : (
              <p className="rail-copy muted">choose a session in the table to inspect its binding.</p>
            )}
          </RailSection>

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
                : 'Adjust the state or epoch filter to widen the inventory.'
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
