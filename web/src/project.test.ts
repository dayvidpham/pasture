import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'

import App, {
  PeasantIntegrationState,
  ProjectCell,
  ProjectDetail,
  ProjectFilter,
} from './App'
import type { SessionsFrame } from './api'
import {
  filterSessionsByProject,
  integrationFromSessionsFrame,
  integrationSourceLabel,
  peasantIntegrationPresentation,
  projectDetailMessage,
  projectLabel,
  projectOptions,
  UNKNOWN_PROJECT_LABEL,
  type PeasantIntegration,
  type ProjectIdentity,
} from './project'

function render(element: Parameters<typeof renderToStaticMarkup>[0]): string {
  return renderToStaticMarkup(element)
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

describe('projectLabel', () => {
  it('prefers the project name, then the exact hash, then unknown', () => {
    expect(projectLabel({ project: 'pasture', projectHash: 'hash-1' })).toBe('pasture')
    expect(projectLabel({ project: '  pasture  ', projectHash: 'hash-1' })).toBe('pasture')
    expect(projectLabel({ project: '', projectHash: 'hash-1' })).toBe('hash-1')
    expect(projectLabel({ project: '   ', projectHash: 'hash-2' })).toBe('hash-2')
    expect(projectLabel({})).toBe(UNKNOWN_PROJECT_LABEL)
    expect(projectLabel({ project: '   ' })).toBe(UNKNOWN_PROJECT_LABEL)
  })
})

describe('projectOptions', () => {
  const sessions: ProjectIdentity[] = [
    { project: 'Pasture', projectHash: 'aaaa1111' },
    { project: 'Pasture', projectHash: 'bbbb2222' },
    { project: 'village', projectHash: 'cccc3333' },
    { project: 'Pasture', projectHash: 'aaaa1111' }, // same hash is one option
    { project: 'no hash' }, // not a filter option
  ]

  it('keys options by the exact hash and keeps duplicate names distinct', () => {
    const options = projectOptions(sessions)

    expect(options.map((option) => option.hash)).toEqual([
      'aaaa1111',
      'bbbb2222',
      'cccc3333',
    ])
    expect(options.map((option) => option.label)).toEqual([
      'Pasture · aaaa1111',
      'Pasture · bbbb2222',
      'village',
    ])
    expect(new Set(options.map((option) => option.label)).size).toBe(options.length)
  })

  it('does not suffix a unique name or a hash-only label', () => {
    expect(projectOptions([{ project: 'solo', projectHash: 'dddd4444' }])).toEqual([
      { hash: 'dddd4444', label: 'solo' },
    ])
    expect(projectOptions([{ projectHash: 'eeee5555' }])).toEqual([
      { hash: 'eeee5555', label: 'eeee5555' },
    ])
  })

  it('prefers a recorded name when the same hash appears hash-only first', () => {
    expect(
      projectOptions([
        { projectHash: 'ffff6666' },
        { project: 'named later', projectHash: 'ffff6666' },
      ]),
    ).toEqual([{ hash: 'ffff6666', label: 'named later' }])
  })
})

describe('filterSessionsByProject', () => {
  const sessions: ProjectIdentity[] = [
    { project: 'a', projectHash: 'aaaa1111' },
    { project: 'b', projectHash: 'bbbb2222' },
    { project: 'unknown' },
  ]

  it('keeps every session, including no-project sessions, when unfiltered', () => {
    expect(filterSessionsByProject(sessions, null)).toHaveLength(3)
    expect(filterSessionsByProject(sessions, '')).toHaveLength(3)
  })

  it('filters by the exact hash and never matches a no-project session', () => {
    expect(filterSessionsByProject(sessions, 'aaaa1111')).toEqual([sessions[0]])
    expect(filterSessionsByProject(sessions, 'missing')).toEqual([])
  })
})

describe('peasantIntegrationPresentation', () => {
  it('explains disabled with the toggle and the exact fallback commands', () => {
    const presentation = peasantIntegrationPresentation(disabledIntegration)

    expect(presentation.status).toBe('disabled')
    expect(presentation.title).toContain('off')
    expect(presentation.message).toContain('unknown project')
    expect(presentation.message).toContain('toggle')
    expect(presentation.commands).toEqual([
      'pasture serve --peasant-db <path>',
      'pasture serve --peasant-url <url>',
    ])
    expect(presentation.sourceLabel).toBe('none')
  })

  it('names the configured source and keeps the backend detail when unavailable', () => {
    const presentation = peasantIntegrationPresentation(unavailableIntegration)

    expect(presentation.status).toBe('unavailable')
    expect(presentation.title).toContain('unavailable')
    expect(presentation.message).toContain('database is locked')
    expect(presentation.message).toContain('Toggle enrichment off, then on to retry')
    expect(presentation.sourceLabel).toBe('Peasant SQLite database')
    expect(presentation.commands).toEqual([])
  })

  it('falls back to an actionable unavailable message when the source is silent', () => {
    const presentation = peasantIntegrationPresentation({
      ...unavailableIntegration,
      message: '',
    })

    expect(presentation.message).toContain('could not be read')
  })

  it('maps the ok state quietly and labels the API source', () => {
    const presentation = peasantIntegrationPresentation(okIntegration)

    expect(presentation.status).toBe('ok')
    expect(presentation.title).toContain('is on')
    expect(presentation.message).toBe('')
    expect(presentation.sourceLabel).toBe('Peasant HTTP API')
    expect(presentation.commands).toEqual([])
    expect(integrationSourceLabel('db')).toBe('Peasant SQLite database')
    expect(integrationSourceLabel('none')).toBe('none')
  })
})

describe('project rendering', () => {
  it('wires the project filter and integration rail into the app shell', () => {
    const html = render(createElement(App))

    expect(html).toContain('peasant projects')
    expect(html).toContain('reading the Peasant project source')
    expect(html).toContain('all projects')
  })

  it('renders the project column honestly, including unknown', () => {
    const named = render(createElement(ProjectCell, { project: 'pasture', projectHash: 'hash-1' }))
    expect(named).toContain('pasture')
    expect(named).toContain('title="pasture · hash-1"')
    expect(render(createElement(ProjectCell, { projectHash: 'hash-only' }))).toContain('hash-only')

    const unknown = render(createElement(ProjectCell, {}))
    expect(unknown).toContain(UNKNOWN_PROJECT_LABEL)
    expect(unknown).toContain('is-unknown')
  })

  it('labels the detail rail as Peasant-owned and shows name plus hash', () => {
    const html = render(
      createElement(ProjectDetail, {
        session: { project: 'pasture', projectHash: 'hash-1' },
        integration: okIntegration,
      }),
    )

    expect(html).toContain('peasant-owned project')
    expect(html).toContain('pasture')
    expect(html).toContain('hash-1')
    expect(html).toContain('not a Pasture epoch binding')
  })

  it('does not repeat a hash-only project identity', () => {
    const html = render(
      createElement(ProjectDetail, {
        session: { projectHash: 'hash-only' },
        integration: okIntegration,
      }),
    )

    expect(html).toContain('hash-only')
    expect(html.match(/hash-only/g)).toHaveLength(1)
  })

  it('renders the detail unknown state without inventing a project', () => {
    const html = render(
      createElement(ProjectDetail, { session: {}, integration: okIntegration }),
    )

    expect(html).toContain(UNKNOWN_PROJECT_LABEL)
    expect(html).toContain('Peasant has not mapped this session to a project.')
  })

  it('does not claim Peasant failed to map a session when enrichment is off', () => {
    const disabled = render(
      createElement(ProjectDetail, { session: {}, integration: disabledIntegration }),
    )
    expect(disabled).toContain('enrichment is disabled')
    expect(disabled).toContain('--peasant-db')
    expect(disabled).toContain('data-integration="disabled"')
    expect(disabled).not.toContain('Peasant has not mapped this session')

    const unavailable = render(
      createElement(ProjectDetail, { session: {}, integration: unavailableIntegration }),
    )
    expect(unavailable).toContain('source is unavailable')
    expect(unavailable).toContain('database is locked')
    expect(unavailable).toContain('data-integration="unavailable"')
    expect(unavailable).not.toContain('Peasant has not mapped this session')

    const unknownIntegration = render(
      createElement(ProjectDetail, { session: {}, integration: null }),
    )
    expect(unknownIntegration).toContain('Reading the Peasant project source')
    expect(unknownIntegration).toContain('data-integration="unknown"')
    expect(unknownIntegration).not.toContain('Peasant has not mapped this session')
  })

  it('explains a missing project per integration state', () => {
    expect(projectDetailMessage({}, okIntegration)).toContain('has not mapped')
    expect(projectDetailMessage({}, disabledIntegration)).toContain('disabled')
    expect(projectDetailMessage({}, unavailableIntegration)).toContain('unavailable')
    expect(projectDetailMessage({}, null)).toContain('Reading')
    expect(
      projectDetailMessage({ project: 'pasture', projectHash: 'hash-1' }, disabledIntegration),
    ).toContain('not a Pasture epoch binding')
  })

  it('renders all projects plus duplicate-name options keyed by hash', () => {
    const options = projectOptions([
      { project: 'Pasture', projectHash: 'aaaa1111' },
      { project: 'Pasture', projectHash: 'bbbb2222' },
      { project: 'village', projectHash: 'cccc3333' },
    ])
    const html = render(
      createElement(ProjectFilter, { options, value: null, onChange: () => {} }),
    )

    expect(html).toContain('all projects')
    expect(html).toContain('value="aaaa1111"')
    expect(html).toContain('value="bbbb2222"')
    expect(html).toContain('Pasture · aaaa1111')
    expect(html).toContain('Pasture · bbbb2222')

    const labelFor = html.match(/<label[^>]*for="([^"]+)"/)?.[1]
    const selectId = html.match(/<select[^>]*id="([^"]+)"/)?.[1]
    expect(labelFor).toBeDefined()
    expect(labelFor).toBe(selectId)
  })

  it('renders the disabled integration state with the runtime switch and fallback commands', () => {
    const html = render(
      createElement(PeasantIntegrationState, { integration: disabledIntegration }),
    )

    expect(html).toContain('data-status="disabled"')
    expect(html).toContain('Peasant project enrichment is off')
    expect(html).toContain('role="switch"')
    expect(html).toContain('aria-checked="false"')
    expect(html).toContain('aria-label="Peasant project enrichment"')
    expect(html).toContain('pasture serve --peasant-db &lt;path&gt;')
    expect(html).toContain('pasture serve --peasant-url &lt;url&gt;')
  })

  it('renders the switch on for ok and unavailable sources', () => {
    const ok = render(
      createElement(PeasantIntegrationState, { integration: okIntegration }),
    )
    expect(ok).toContain('role="switch"')
    expect(ok).toContain('aria-checked="true"')

    const unavailable = render(
      createElement(PeasantIntegrationState, { integration: unavailableIntegration }),
    )
    expect(unavailable).toContain('role="switch"')
    expect(unavailable).toContain('aria-checked="true"')
  })

  it('wires the switch click to onToggle with the next state', () => {
    const onToggle = vi.fn()
    const element = PeasantIntegrationState({
      integration: disabledIntegration,
      onToggle,
    }) as unknown as { props: { children?: unknown } }

    const findSwitch = (node: unknown): { props: Record<string, unknown> } | null => {
      if (!node || typeof node !== 'object') return null
      const element = node as { props?: Record<string, unknown> }
      if (element.props?.role === 'switch') {
        return element as { props: Record<string, unknown> }
      }
      const children = element.props?.children
      const list = Array.isArray(children) ? children : [children]
      for (const child of list) {
        const found = findSwitch(child)
        if (found) return found
      }
      return null
    }

    const button = findSwitch(element)
    expect(button).not.toBeNull()
    ;(button!.props.onClick as () => void)()
    expect(onToggle).toHaveBeenCalledWith(true)
  })

  it('disables the switch and announces errors while busy', () => {
    const html = render(
      createElement(PeasantIntegrationState, {
        integration: okIntegration,
        onToggle: () => {},
        busy: true,
        error: 'the default Peasant database is missing',
      }),
    )

    expect(html).toContain('disabled=""')
    expect(html).toContain('aria-busy="true"')
    expect(html).toContain('updating')
    expect(html).toContain('role="alert"')
    expect(html).toContain('the default Peasant database is missing')
  })

  it('renders the unavailable integration state with its source and message', () => {
    const html = render(
      createElement(PeasantIntegrationState, { integration: unavailableIntegration }),
    )

    expect(html).toContain('data-status="unavailable"')
    expect(html).toContain('Peasant project source is unavailable')
    expect(html).toContain('source: Peasant SQLite database')
    expect(html).toContain('database is locked')
  })

  it('renders the ok integration state quietly', () => {
    const html = render(
      createElement(PeasantIntegrationState, { integration: okIntegration }),
    )

    expect(html).toContain('data-status="ok"')
    expect(html).toContain('Peasant project enrichment is on')
    expect(html).toContain('source: Peasant HTTP API')
    expect(html).not.toContain('peasant-integration-commands')
  })

  it('updates the integration state from a live sessions frame', () => {
    const frame: SessionsFrame = {
      type: 'sessions',
      revision: 3,
      asOf: '2026-01-01T00:00:00Z',
      payload: { items: [], peasantIntegration: unavailableIntegration },
    }
    const integration = integrationFromSessionsFrame(frame.payload)
    expect(integration).toEqual(unavailableIntegration)
    expect(integrationFromSessionsFrame({ items: [] })).toBeNull()

    const html = render(createElement(PeasantIntegrationState, { integration }))
    expect(html).toContain('data-status="unavailable"')
    expect(html).toContain('database is locked')
  })
})
