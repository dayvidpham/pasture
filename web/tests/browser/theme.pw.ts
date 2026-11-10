import { test, expect, type Page } from '@playwright/test'
import AxeBuilder from '@axe-core/playwright'
import { spawn, execFileSync, type ChildProcess } from 'node:child_process'
import { mkdtemp, rm, readFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
const transcript = JSON.parse(await readFile(new URL('../../src/testdata/synthetic-transcript.json', import.meta.url), 'utf8')) as { id: string }

let server: ChildProcess
let directory: string
let origin: string
let errors: string[]
const observed = (value: string, field: string) => ({ status: 'observed', value, source: 'host', field })
const execution = {
  occurrenceId: 12, observedAt: '2026-11-03T00:00:00Z', kind: 'selection',
  provider: observed('synthetic-provider', 'model.providerID'), model: observed('synthetic-model', 'model.id'),
  variant: { status: 'unavailable', source: 'none', reason: 'no-evidence' },
  role: { ...observed('build', 'agent'), vocabulary: 'native-agent' },
  protocolRole: { ...observed('worker', 'PASTURE_EXECUTION_ROLE'), vocabulary: 'pasture-role' },
}
const session = { harness: 'claude-code', nativeSessionId: transcript.id, activity: { state: 'unknown', stateBasis: 'none' }, bindings: [], execution }
const snapshot = { revision: 1, asOf: '2026-01-01T10:00:00Z', sessions: [session], epochs: [], truncated: false }

test.beforeAll(async () => {
  directory = await mkdtemp(join(tmpdir(), 'pasture-browser-'))
  try {
  const binary = join(directory, 'pasture')
  execFileSync('go', ['build', '-o', binary, './cmd/pasture'], { cwd: resolve('..'), timeout: 120000 })
  server = spawn(binary, ['serve', '--db', join(directory, 'private.db'), '--listen', '127.0.0.1:0'])
  origin = await new Promise<string>((accept, reject) => {
    let output = ''
    const timer = setTimeout(() => reject(new Error(`server startup timed out: ${output}`)), 30000)
    server.stdout!.on('data', chunk => {
      output += chunk.toString()
      const match = output.match(/pasture serve listening on (http:\/\/\S+)/)
      if (match) { clearTimeout(timer); accept(match[1]) }
    })
    server.stderr!.on('data', chunk => { output += chunk.toString() })
    server.once('error', error => { clearTimeout(timer); reject(error) })
    server.once('exit', code => { clearTimeout(timer); reject(new Error(`server exited ${code}: ${output}`)) })
  })
  console.log(`real embedded binary: ${origin}; isolated temporary database`)
  } catch (error) {
    await cleanup()
    throw error
  }
})

async function cleanup() {
  try {
    if (server && server.exitCode === null && server.signalCode === null) {
      const exited = new Promise<void>(done => server.once('exit', () => done()))
      server.kill('SIGTERM')
      let timer: ReturnType<typeof setTimeout> | undefined
      try {
        await Promise.race([exited, new Promise<void>(done => { timer = setTimeout(done, 5000) })])
      } finally { clearTimeout(timer) }
      if (server.exitCode === null && server.signalCode === null) {
        server.kill('SIGKILL')
        await Promise.race([exited, new Promise<never>((_, reject) => {
          const timeout = setTimeout(() => reject(new Error('server did not exit after SIGKILL')), 5000)
          exited.finally(() => clearTimeout(timeout))
        })])
      }
    }
  } finally {
    if (directory) await rm(directory, { recursive: true, force: true })
  }
}

test.afterAll(cleanup)

test.beforeEach(async ({ page }) => {
  errors = []
  page.on('pageerror', error => errors.push(`pageerror: ${error.message}`))
  page.on('response', response => {
    const url = new URL(response.url())
    if (url.origin === origin && (url.pathname.startsWith('/assets/') || response.request().resourceType() === 'document') && (response.status() < 200 || response.status() >= 400)) {
      errors.push(`asset: ${response.url()} ${response.status()}`)
    }
  })
  page.on('requestfailed', request => {
    if (new URL(request.url()).pathname.startsWith('/assets/')) errors.push(`asset: ${request.url()} ${request.failure()?.errorText}`)
  })
})

test.afterEach(async () => { expect(errors).toEqual([]) })

async function mockAPI(
  page: Page,
  sessions = [session],
  catalog: { items: string[]; subagents?: string[]; truncated?: boolean } = { items: sessions.map(item => item.nativeSessionId) },
) {
  await page.route('**/api/v1/inspect', route => route.fulfill({ json: { ...snapshot, sessions } }))
  await page.route('**/api/v1/transcripts', route => route.fulfill({
    json: {
      revision: 1,
      asOf: snapshot.asOf,
      items: catalog.items,
      subagents: catalog.subagents ?? [],
      truncated: catalog.truncated ?? false,
    },
  }))
  await page.route('**/api/v1/sessions/*/*/transcript', route => route.fulfill({ json: transcript }))
  await page.route('**/api/v1/sessions/*/*/execution**', route => route.fulfill({ json: { revision: 1, asOf: snapshot.asOf, items: [execution] } }))
  await page.routeWebSocket('**/api/v1/ws', ws => {
    ws.send(JSON.stringify({ type: 'ready', topics: [] }))
    ws.onMessage(message => {
      const { topic } = JSON.parse(String(message))
      ws.send(JSON.stringify({ type: topic, revision: 1, asOf: snapshot.asOf, payload: { items: topic === 'sessions' ? sessions : [] } }))
    })
  })
}

async function open(page: Page) {
  await mockAPI(page)
  await page.goto(origin)
  await page.getByRole('button', { name: transcript.id }).click()
  await expect(page.locator('.transcript-host').getByText('Synthetic user asks for a greeting.', { exact: true }).locator('visible=true').first()).toBeVisible()
}

async function agrees(page: Page, theme: string) {
  await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
  await expect(page.locator('.transcript-host [data-theme]').first()).toHaveAttribute('data-theme', theme)
}

async function assertPageGutters(page: Page) {
  const padding = await page.locator('.rs-main').evaluate(el => {
    const style = getComputedStyle(el)
    return { left: parseFloat(style.paddingLeft), right: parseFloat(style.paddingRight) }
  })
  expect(padding.left).toBeGreaterThanOrEqual(20)
  expect(padding.right).toBeGreaterThanOrEqual(20)
  expect(Math.abs(padding.left - padding.right)).toBeLessThanOrEqual(1)
  return padding
}

for (const theme of ['light', 'dark'] as const) {
  test(`pre-paint System ${theme} and mounted viewer`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: theme })
    await mockAPI(page)
    let release!: () => void
    const paused = new Promise<void>(resolve => { release = resolve })
    let reached!: () => void
    const requested = new Promise<void>(resolve => { reached = resolve })
    await page.route('**/assets/index-*.js', async route => { reached(); await paused; await route.continue() })
    const navigation = page.goto(origin, { waitUntil: 'commit' })
    try {
      await requested
      await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
      expect(await page.locator('html').evaluate(el => el.style.colorScheme)).toBe(theme)
    } finally { release() }
    await navigation
    await page.getByRole('button', { name: transcript.id }).click()
    await expect(page.locator('.transcript-host').getByText('Synthetic user asks for a greeting.', { exact: true }).locator('visible=true').first()).toBeVisible()
    await agrees(page, theme)
    await expect(page.getByLabel('Theme')).toHaveValue('system')
  })

  test(`saved ${theme} overrides system and survives reload`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: theme === 'light' ? 'dark' : 'light' })
    await page.addInitScript(value => {
      if (!sessionStorage.getItem('test-seeded')) {
        localStorage.setItem('pasture.theme.preference', value)
        sessionStorage.setItem('test-seeded', 'yes')
      }
    }, theme)
    await open(page)
    await agrees(page, theme)
    await page.emulateMedia({ colorScheme: theme })
    await page.emulateMedia({ colorScheme: theme === 'light' ? 'dark' : 'light' })
    await agrees(page, theme)
    await page.reload()
    await agrees(page, theme)
  })

  test(`axe shell and transcript ${theme}`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: theme })
    await mockAPI(page)
    await page.goto(origin)
    const shell = await new AxeBuilder({ page }).include('body').withRules(['color-contrast']).analyze()
    expect(shell.violations).toEqual([])
    expect(shell.incomplete.filter(rule => rule.id === 'color-contrast')).toEqual([])
    await page.getByRole('button', { name: transcript.id }).click()
    await expect(page.locator('.transcript-host')).toBeVisible()
    for (const selector of ['body', '.transcript-host']) {
      const result = await new AxeBuilder({ page }).include(selector).withRules(['color-contrast']).analyze()
      console.log(`${theme} ${selector}: ${result.violations.length} violations, ${result.incomplete.length} incomplete`)
      expect(result.violations).toEqual([])
      const unresolved: string[] = []
      const violations: string[] = []
      for (const rule of result.incomplete.filter(rule => rule.id === 'color-contrast')) {
        for (const node of rule.nodes) {
          const target = node.target.join(' >> ')
          const locator = page.locator(target).first()
          // Center each sample so the sticky toolbar cannot obscure its pixels.
          await locator.evaluate(el => el.scrollIntoView({ block: 'center', inline: 'nearest' }))
          const focused = await new AxeBuilder({ page }).include(node.target[0]).withRules(['color-contrast']).analyze()
          const violation = focused.violations.flatMap(rule => rule.nodes).find(item => item.target.join(' >> ') === target)
          const incomplete = focused.incomplete.flatMap(rule => rule.nodes).find(item => item.target.join(' >> ') === target)
          const passed = focused.passes.flatMap(rule => rule.nodes).some(item => item.target.join(' >> ') === target)
          const outcome = violation ? 'violation' : incomplete || !passed ? 'still-incomplete' : 'resolved-pass'
          const summary = (violation?.failureSummary ?? incomplete?.failureSummary ?? (passed ? 'axe passed' : node.failureSummary) ?? 'axe did not report a pass').replace(/\s+/g, ' ')
          console.log(`incomplete ${theme} ${selector} ${target} -> ${outcome}: ${summary}`)
          if (violation) violations.push(`${target}: ${summary}; ${JSON.stringify(violation.any)}`)
          if (outcome === 'still-incomplete') unresolved.push(`${target}: ${summary}`)
        }
      }
      expect(unresolved, `UNRESOLVED ${theme} ${selector}`).toEqual([])
      expect(violations, `color-contrast violations ${theme} ${selector}`).toEqual([])
    }
  })
}

test('system tracks device and resumes following it after explicit override', async ({ page }) => {
  await page.emulateMedia({ colorScheme: 'light' })
  await open(page)
  await page.emulateMedia({ colorScheme: 'dark' })
  await agrees(page, 'dark')
  await page.getByLabel('Theme').selectOption('light')
  await agrees(page, 'light')
  await page.reload()
  await expect(page.getByLabel('Theme')).toHaveValue('light')
  await agrees(page, 'light')
  await page.getByLabel('Theme').selectOption('system')
  await agrees(page, 'dark')
  await page.reload()
  await expect(page.getByLabel('Theme')).toHaveValue('system')
})

test('storage failures preserve in-page choice and announce failure', async ({ page }) => {
  await page.addInitScript(() => {
    Storage.prototype.getItem = () => { throw new Error('read denied') }
    Storage.prototype.setItem = () => { throw new Error('write denied') }
  })
  await open(page)
  await page.getByLabel('Theme').selectOption('light')
  await agrees(page, 'light')
  await expect(page.getByRole('status').filter({ hasText: 'not saved' })).toBeVisible()
})

for (const width of [1280, 420]) {
  test(`keyboard focus and unclipped control at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 800 })
    await mockAPI(page)
    await page.goto(origin)
    const control = page.getByLabel('Theme')
    await control.focus()
    await expect(control).toBeFocused()
    await page.keyboard.press('ArrowUp')
    expect(await control.inputValue()).not.toBe('system')
    const box = await control.boundingBox()
    expect(box).not.toBeNull()
    expect(box!.x).toBeGreaterThanOrEqual(0)
    expect(box!.x + box!.width).toBeLessThanOrEqual(width)
    const focus = await control.evaluate(el => { const s = getComputedStyle(el); return { outline: s.outlineStyle, shadow: s.boxShadow } })
    expect(focus.outline !== 'none' || focus.shadow !== 'none').toBeTruthy()
  })
}

test('desktop layout keeps a wide internally scrollable rail visible', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  const epoch = { id: 'https://github.com/dayvidpham/pasture/01a1b370-5462-70d1-a16d-cc8c3c6b7251', title: 'REQUEST: Pasture web light theme and dark readability' }
  const sessions = Array.from({ length: 40 }, (_, i) => ({ ...session, nativeSessionId: `synthetic-${i}`, epoch }))
  await mockAPI(page, sessions)
  await page.goto(origin)
  await expect(page.getByText('no epochs are projected in this store; session rows still show their own epoch binding when one is recorded.').first()).toBeVisible()
  await expect(page.getByRole('row').filter({ has: page.getByRole('button', { name: 'synthetic-0' }) }).getByText(epoch.title)).toBeVisible()
  const rail = page.locator('.rs-rail')
  await expect(page.getByRole('button', { name: 'synthetic-0' })).toBeVisible()
  await page.getByRole('button', { name: 'synthetic-0' }).click()
  await page.getByRole('link', { name: '← back to sessions' }).click()
  await expect(page.locator('.rs-rail .transcript-host')).toHaveCount(0)
  const before = await rail.boundingBox()
  expect(before!.width).toBeGreaterThanOrEqual(400)
  const padding = await assertPageGutters(page)
  expect((await page.locator('.hero h1').boundingBox())!.width).toBeGreaterThan(100)
  await page.evaluate(() => window.scrollTo(0, 1000))
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBeGreaterThanOrEqual(900)
  const after = await rail.boundingBox()
  const card = await page.locator('.rs-rail-card').evaluate(el => {
    const initial = el.scrollTop
    el.scrollTop = 100
    return { height: el.clientHeight, scrollHeight: el.scrollHeight, scrollTop: el.scrollTop, initial }
  })
  const dimensions = await page.evaluate(() => ({ width: document.documentElement.clientWidth, scrollWidth: document.documentElement.scrollWidth, scrollY: window.scrollY }))
  console.log('desktop layout', { before, after, padding, card, dimensions })
  expect(after!.y).toBeGreaterThanOrEqual(0)
  expect(after!.y + after!.height).toBeLessThanOrEqual(800)
  expect(card.scrollHeight).toBeGreaterThan(card.height)
  expect(card.scrollTop).toBeGreaterThan(card.initial)
  expect(dimensions.scrollWidth).toBeLessThanOrEqual(dimensions.width)
})

test('transcript column shows available and none', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  const missing = { ...session, nativeSessionId: 'session-without-transcript' }
  await mockAPI(page, [session, missing], { items: [transcript.id], truncated: false })
  await page.goto(origin)
  const available = page.getByRole('row').filter({ has: page.getByRole('button', { name: transcript.id }) })
  const none = page.getByRole('row').filter({ has: page.getByRole('button', { name: 'session-without-transcript' }) })
  await expect(available.locator('[data-availability="available"]')).toHaveText('available')
  await expect(available.locator('[data-availability="available"]')).toHaveAttribute('data-mark', '✓')
  await expect(none.locator('[data-availability="none"]')).toHaveText('none')
  await expect(none.locator('[data-availability="none"]')).toHaveAttribute('data-mark', '—')
})

test('toggling Peasant enrichment refreshes the transcript catalog', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  const off = { enabled: false, source: 'none', status: 'disabled', message: '' }
  const on = { enabled: true, source: 'db', status: 'ok', message: '' }
  let phase: 'initial' | 'on' | 'off' = 'initial'
  const integration = () => (phase === 'on' ? on : off)
  await mockAPI(page)
  await page.route('**/api/v1/inspect', route => route.fulfill({
    json: { ...snapshot, sessions: [session], peasantIntegration: integration() },
  }))
  await page.route('**/api/v1/settings/peasant', async route => {
    const body = route.request().postDataJSON() as { enabled: boolean }
    phase = body.enabled ? 'on' : 'off'
    await route.fulfill({ json: integration() })
  })
  await page.route('**/api/v1/transcripts', route => {
    if (phase === 'initial') {
      return route.fulfill({
        status: 503,
        contentType: 'application/json',
        body: JSON.stringify({ error: { code: 'transcript_catalog_unavailable', message: 'the Peasant CLI catalog is unavailable' } }),
      })
    }
    const items = phase === 'on' ? [transcript.id] : []
    return route.fulfill({
      json: { revision: 1, asOf: snapshot.asOf, items, truncated: false },
    })
  })
  await page.routeWebSocket('**/api/v1/ws', ws => {
    ws.send(JSON.stringify({ type: 'ready', topics: [] }))
    ws.onMessage(message => {
      const { topic } = JSON.parse(String(message))
      ws.send(JSON.stringify({
        type: topic,
        revision: 1,
        asOf: snapshot.asOf,
        payload: {
          items: topic === 'sessions' ? [session] : [],
          ...(topic === 'sessions' ? { peasantIntegration: integration() } : {}),
        },
      }))
    })
  })

  const catalogGet = (response: { url: () => string; request: () => { method: () => string } }) => {
    const url = new URL(response.url())
    return url.pathname === '/api/v1/transcripts' && response.request().method() === 'GET'
  }
  const first = page.waitForResponse(catalogGet)
  await page.goto(origin)
  expect((await first).status()).toBe(503)
  const row = page.getByRole('row').filter({ has: page.getByRole('button', { name: transcript.id }) })
  await expect(row.locator('[data-availability="unknown"]')).toHaveText('unknown')

  const toggle = page.getByRole('switch', { name: 'Peasant project enrichment' })
  await expect(toggle).toBeEnabled()
  const second = page.waitForResponse(catalogGet)
  await toggle.click()
  const enabledCatalog = await second
  expect(enabledCatalog.status()).toBe(200)
  expect((await enabledCatalog.json()).items).toEqual([transcript.id])
  await expect(row.locator('[data-availability="available"]')).toHaveText('available')
  await expect(row.locator('[data-availability="available"]')).toHaveAttribute('data-mark', '✓')

  await expect(toggle).toBeEnabled()
  const third = page.waitForResponse(catalogGet)
  await toggle.click()
  const disabledCatalog = await third
  expect(disabledCatalog.status()).toBe(200)
  expect((await disabledCatalog.json()).items).toEqual([])
  await expect(row.locator('[data-availability="none"]')).toHaveText('none')
  await expect(row.locator('[data-availability="none"]')).toHaveAttribute('data-mark', '—')
})

test('roots filter hides subagents and all sessions shows them', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  const child = { ...session, nativeSessionId: 'ses_child' }
  const unknown = { ...session, nativeSessionId: 'not-in-peasant' }
  await mockAPI(page, [session, child, unknown], {
    items: [transcript.id, 'ses_child'],
    subagents: ['ses_child'],
    truncated: false,
  })
  await page.goto(origin)
  await expect(page.getByRole('button', { name: transcript.id })).toBeVisible()
  await expect(page.getByRole('button', { name: 'not-in-peasant' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'ses_child' })).toHaveCount(0)
  await page.getByRole('button', { name: 'all sessions' }).click()
  await expect(page.getByRole('button', { name: 'ses_child' })).toBeVisible()
})

test('a session with no transcript stays on the list and shows a toast', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  await mockAPI(page, [session], { items: [], truncated: false })
  await page.goto(origin)
  const row = page.getByRole('row').filter({ has: page.getByRole('button', { name: transcript.id }) })
  await expect(row.locator('[data-availability="none"]')).toHaveText('none')
  await row.getByRole('button', { name: transcript.id }).click()
  expect(new URL(page.url()).hash).toBe('')
  const toast = page.getByRole('alert').filter({ hasText: transcript.id })
  await expect(toast).toBeVisible()
  await expect(toast).toContainText('nothing was opened')
  await expect(page.locator('.transcript-page')).toHaveCount(0)
})

test('a failed transcript catalog stays unknown', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  await mockAPI(page)
  await page.route('**/api/v1/transcripts', route => route.fulfill({
    status: 503,
    contentType: 'application/json',
    body: JSON.stringify({ error: { code: 'peasant_unavailable', message: 'Peasant is unavailable: install Peasant and retry' } }),
  }))
  const catalogResponse = page.waitForResponse(response => {
    const url = new URL(response.url())
    return url.pathname === '/api/v1/transcripts' && response.request().method() === 'GET'
  })
  await page.goto(origin)
  expect((await catalogResponse).status()).toBe(503)
  const row = page.getByRole('row').filter({ has: page.getByRole('button', { name: transcript.id }) })
  await expect(row.locator('[data-availability="unknown"]')).toHaveText('unknown')
  await expect(page.locator('[data-availability="none"]')).toHaveCount(0)
  await expect(page.locator('[data-availability="available"]')).toHaveCount(0)
})

test('a missing transcript shows a dismissible bottom-right toast and keeps retry', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  await mockAPI(page)
  await page.route('**/api/v1/sessions/*/*/transcript', route => route.fulfill({
    status: 404,
    contentType: 'application/json',
    body: JSON.stringify({ error: { code: 'transcript_not_found', message: 'Peasant has no transcript for that session' } }),
  }))
  await page.goto(origin)
  await page.getByRole('button', { name: transcript.id }).click()
  const message = 'Peasant has not stored a durable transcript for this session yet. Capture it with Peasant (harvest the session), then retry.'
  const toast = page.getByRole('alert').filter({ hasText: message })
  await expect(toast).toBeVisible()
  await expect(page.getByRole('button', { name: 'retry transcript' })).toBeVisible()
  const host = page.locator('.tsx-host')
  await expect(host).toHaveClass(/tsx-bottom-right/)
  const box = await host.boundingBox()
  const viewport = page.viewportSize()
  expect(box).not.toBeNull()
  expect(viewport).not.toBeNull()
  expect(box!.x).toBeGreaterThan(viewport!.width / 2)
  expect(box!.y).toBeGreaterThan(viewport!.height / 2)
  await expect(toast).toBeHidden({ timeout: 7000 })
  await expect(page.getByRole('button', { name: 'retry transcript' })).toBeVisible()
})

test('hero text keeps width at 1180 without horizontal overflow', async ({ page }) => {
  await page.setViewportSize({ width: 1180, height: 900 })
  await mockAPI(page)
  await page.goto(origin)
  await expect(page.getByRole('heading', { name: 'session epochs' })).toBeVisible()
  await expect(page.locator('.hero .lede')).toHaveText('Active, idle, ended, and unbound sessions from the local Pasture store.')
  const measured = await page.evaluate(() => {
    const hero = document.querySelector('.hero') as HTMLElement
    const heading = hero.querySelector('h1') as HTMLElement
    const lede = hero.querySelector('.lede') as HTMLElement
    const columns = getComputedStyle(hero).gridTemplateColumns
    return {
      columns,
      heading: heading.getBoundingClientRect().width,
      lede: lede.getBoundingClientRect().width,
      scroll: document.documentElement.scrollWidth,
      viewport: document.documentElement.clientWidth,
    }
  })
  console.log('hero 1180', measured)
  expect(measured.heading).toBeGreaterThanOrEqual(300)
  expect(measured.lede).toBeGreaterThanOrEqual(300)
  expect(measured.columns.startsWith('0px')).toBe(false)
  expect(measured.scroll).toBeLessThanOrEqual(measured.viewport)
})

test('hero lede is one line at 1280', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  await mockAPI(page)
  await page.goto(origin)
  const lede = page.locator('.hero .lede')
  await expect(lede).toHaveText('Active, idle, ended, and unbound sessions from the local Pasture store.')
  const lines = await lede.evaluate(element => {
    const style = getComputedStyle(element)
    const fontSize = parseFloat(style.fontSize)
    const lineHeight = style.lineHeight.endsWith('px') ? parseFloat(style.lineHeight) : parseFloat(style.lineHeight) * fontSize
    return element.getBoundingClientRect().height / lineHeight
  })
  expect(lines).toBeLessThanOrEqual(1.05)
})

test('the details rail collapses and expands from the keyboard', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  await mockAPI(page)
  await page.goto(origin)
  const toggle = page.getByRole('button', { name: 'hide details' })
  await expect(toggle).toHaveAttribute('aria-expanded', 'true')
  const rail = page.locator('.rs-rail')
  await expect(toggle).toHaveText('hide details')
  await expect(rail).toBeVisible()
  await expect(rail.getByText('peasant projects', { exact: true })).toBeVisible()
  await toggle.click()
  const collapsed = page.getByRole('button', { name: 'show details' })
  await expect(collapsed).toHaveAttribute('aria-expanded', 'false')
  await expect(rail).toBeHidden()
  await expect(rail.getByText('peasant projects', { exact: true })).toBeHidden()
  await collapsed.focus()
  await expect(collapsed).toBeFocused()
  await page.keyboard.press('Enter')
  await expect(page.getByRole('button', { name: 'hide details' })).toHaveAttribute('aria-expanded', 'true')
  await expect(rail).toBeVisible()
  await expect(rail.getByText('peasant projects', { exact: true })).toBeVisible()
})

test('collapsed rail contents leave the tab order until expanded', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  const integration = { enabled: false, source: 'none', status: 'disabled', message: '' }
  const epoch = { id: 'epoch-wave-6', title: 'wave six epoch', phase: 'impl', role: 'worker', sessionCount: 1, activeCount: 0 }
  await mockAPI(page)
  await page.route('**/api/v1/inspect', route => route.fulfill({
    json: { ...snapshot, sessions: [session], epochs: [epoch], peasantIntegration: integration },
  }))
  await page.routeWebSocket('**/api/v1/ws', ws => {
    ws.send(JSON.stringify({ type: 'ready', topics: [] }))
    ws.onMessage(message => {
      const { topic } = JSON.parse(String(message))
      const payload = topic === 'sessions'
        ? { items: [session], peasantIntegration: integration }
        : { items: topic === 'epochs' ? [epoch] : [] }
      ws.send(JSON.stringify({ type: topic, revision: 1, asOf: snapshot.asOf, payload }))
    })
  })
  await page.goto(origin)
  const peasant = page.getByRole('switch', { name: 'Peasant project enrichment' })
  const epochButton = page.getByRole('button', { name: /wave six epoch/ })
  await expect(peasant).toBeVisible()
  await expect(epochButton).toBeVisible()
  await page.getByRole('button', { name: 'hide details' }).click()
  await expect(page.locator('.rs-rail')).toBeHidden()

  const focusedWhileCollapsed = async () => {
    const names: string[] = []
    await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
    for (let step = 0; step < 40; step++) {
      await page.keyboard.press('Tab')
      names.push(await page.evaluate(() => {
        const current = document.activeElement
        if (!current || current === document.body) return ''
        return `${current.getAttribute('aria-label') ?? ''} ${current.textContent ?? ''}`
      }))
    }
    return names.join('\n')
  }
  const collapsedNames = await focusedWhileCollapsed()
  expect(collapsedNames).not.toContain('Peasant project enrichment')
  expect(collapsedNames).not.toContain('wave six epoch')
  expect(collapsedNames).not.toContain('all epochs')

  await page.getByRole('button', { name: 'show details' }).click()
  await expect(peasant).toBeVisible()
  await expect(epochButton).toBeVisible()
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
  const restored: string[] = []
  for (let step = 0; step < 40; step++) {
    await page.keyboard.press('Tab')
    restored.push(await page.evaluate(() => {
      const current = document.activeElement
      if (!current || current === document.body) return ''
      return `${current.getAttribute('aria-label') ?? ''} ${current.textContent ?? ''}`
    }))
    if (restored.some(name => name.includes('Peasant project enrichment')) && restored.some(name => name.includes('wave six epoch'))) break
  }
  expect(restored.join('\n')).toContain('Peasant project enrichment')
  expect(restored.join('\n')).toContain('wave six epoch')
})

test('a collapsed desktop rail leaves the mobile sheet usable', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  const integration = { enabled: false, source: 'none', status: 'disabled', message: '' }
  await mockAPI(page)
  await page.route('**/api/v1/inspect', route => route.fulfill({
    json: { ...snapshot, sessions: [session], peasantIntegration: integration },
  }))
  await page.routeWebSocket('**/api/v1/ws', ws => {
    ws.send(JSON.stringify({ type: 'ready', topics: [] }))
    ws.onMessage(message => {
      const { topic } = JSON.parse(String(message))
      const payload = topic === 'sessions'
        ? { items: [session], peasantIntegration: integration }
        : { items: [] }
      ws.send(JSON.stringify({ type: topic, revision: 1, asOf: snapshot.asOf, payload }))
    })
  })
  await page.goto(origin)
  await expect(page.getByRole('switch', { name: 'Peasant project enrichment' })).toBeVisible()
  await page.getByRole('button', { name: 'hide details' }).click()
  await page.setViewportSize({ width: 420, height: 800 })
  await page.locator('.rs-sheet-toggle').click()
  const sheetSwitch = page.locator('.rs-sheet-body').getByRole('switch', { name: 'Peasant project enrichment' })
  await expect(sheetSwitch).toBeVisible()
  expect(await sheetSwitch.evaluate(element => element.closest('[inert]') !== null)).toBe(false)
  await page.locator('.rs-sheet-toggle').focus()
  let reached = false
  for (let step = 0; step < 20; step++) {
    await page.keyboard.press('Tab')
    reached = await page.evaluate(() => {
      const current = document.activeElement
      return current?.getAttribute('aria-label') === 'Peasant project enrichment'
        && current.closest('.rs-sheet-body') !== null
        && current.closest('[inert]') === null
    })
    if (reached) break
  }
  expect(reached).toBe(true)
})

test('mobile layout preserves the sheet without horizontal overflow', async ({ page }) => {
  await page.setViewportSize({ width: 420, height: 800 })
  await mockAPI(page)
  await page.goto(origin)
  await expect(page.locator('.rail-toggle')).toBeHidden()
  await expect(page.locator('.rs-rail')).toBeHidden()
  await expect(page.locator('.rs-sheet')).toBeVisible()
  const toggle = page.locator('.rs-sheet-toggle')
  await toggle.click()
  await expect(page.locator('.rs-sheet-body')).toBeVisible()
  const padding = await assertPageGutters(page)
  const dimensions = await page.evaluate(() => ({ width: document.documentElement.clientWidth, scrollWidth: document.documentElement.scrollWidth }))
  console.log('mobile layout', { ...dimensions, padding })
  expect(dimensions.scrollWidth).toBeLessThanOrEqual(dimensions.width)
})

for (const width of [1280, 420]) {
  test(`full-page transcript navigation and responsive width at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 800 })
    await open(page)
    await expect(page).toHaveURL(new RegExp(`#/sessions/claude-code/${encodeURIComponent(transcript.id)}$`))
    const measure = async () => page.evaluate(() => {
      const main = document.querySelector('.transcript-page')!.getBoundingClientRect()
      const host = document.querySelector('.transcript-host')!.getBoundingClientRect()
      return { main: main.width, host: host.width, viewport: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }
    })
    const dimensions = await measure()
    console.log(`transcript ${width}`, dimensions)
    expect(dimensions.host).toBeGreaterThanOrEqual(width === 1280 ? dimensions.viewport * 0.9 : dimensions.viewport - 2 * 24 - 2)
    expect(dimensions.scroll).toBeLessThanOrEqual(dimensions.viewport)
    expect(await page.evaluate(() => document.querySelector('.rs-rail .transcript-host'))).toBeNull()
    if (width === 1280) {
      await page.goBack()
      await expect(page.locator('.rs-rail .execution-summary')).toContainText('synthetic-provider')
      await expect(page.locator('.rs-rail .execution-summary')).toContainText('synthetic-model')
      await expect(page.locator('.rs-rail .execution-history')).toContainText('#12')
      await expect(page.locator('.rs-rail .execution-history')).toContainText('synthetic-provider')
      await expect(page.locator('.rs-rail .execution-history')).toContainText('synthetic-model')
      await page.goForward()
      await expect(page.locator('.transcript-host').getByText('Synthetic user asks for a greeting.', { exact: true }).locator('visible=true').first()).toBeVisible()
    }
    await page.getByLabel('Theme').selectOption('light')
    await agrees(page, 'light')
    await page.reload()
    await expect(page.locator('.transcript-host').getByText('Synthetic user asks for a greeting.', { exact: true }).locator('visible=true').first()).toBeVisible()
    await agrees(page, 'light')
    await page.goBack()
    await expect(page.getByRole('heading', { name: 'sessions', exact: true })).toBeVisible()
    await page.goForward()
    await expect(page.locator('.transcript-host').getByText('Synthetic user asks for a greeting.', { exact: true }).locator('visible=true').first()).toBeVisible()
    await page.getByRole('link', { name: '← back to sessions' }).click()
    await expect(page.getByRole('heading', { name: 'sessions', exact: true })).toBeVisible()
    await expect(page.locator('.rs-rail .transcript-host')).toHaveCount(0)
  })
}

test('fresh direct transcript load renders without list navigation', async ({ page }) => {
  await mockAPI(page, [])
  await page.goto(`${origin}/#/sessions/opencode/${encodeURIComponent(transcript.id)}`)
  await expect(page.locator('.transcript-host').getByText('Synthetic user asks for a greeting.', { exact: true }).locator('visible=true').first()).toBeVisible()
  await page.reload()
  await expect(page.locator('.transcript-host').getByText('Synthetic user asks for a greeting.', { exact: true }).locator('visible=true').first()).toBeVisible()
})
