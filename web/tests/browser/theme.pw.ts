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

async function mockAPI(page: Page, sessions = [session]) {
  await page.route('**/api/v1/inspect', route => route.fulfill({ json: { ...snapshot, sessions } }))
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
  await expect(page.getByText('Synthetic user asks for a greeting.').first()).toBeVisible()
  await expect(page.locator('.execution-summary:visible').getByText('synthetic-model')).toBeVisible()
  await expect(page.locator('.execution-history:visible').getByText('synthetic-provider · synthetic-model')).toBeVisible()
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
    await expect(page.getByText('Synthetic user asks for a greeting.').first()).toBeVisible()
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
    await page.getByRole('button', { name: transcript.id }).click()
    await agrees(page, theme)
  })

  test(`axe shell and transcript ${theme}`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: theme })
    await open(page)
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
  await page.getByRole('button', { name: transcript.id }).click()
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
  await expect(page.locator('.rs-rail .transcript-host')).toBeVisible()
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

test('mobile layout preserves the sheet without horizontal overflow', async ({ page }) => {
  await page.setViewportSize({ width: 420, height: 800 })
  await mockAPI(page)
  await page.goto(origin)
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
