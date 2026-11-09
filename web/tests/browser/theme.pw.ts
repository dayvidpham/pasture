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
  page.on('requestfailed', request => {
    if (new URL(request.url()).pathname.startsWith('/assets/')) errors.push(`asset: ${request.url()} ${request.failure()?.errorText}`)
  })
})

test.afterEach(async () => { expect(errors).toEqual([]) })

async function mockAPI(page: Page) {
  await page.route('**/api/v1/inspect', route => route.fulfill({ json: snapshot }))
  await page.route('**/api/v1/sessions/*/*/transcript', route => route.fulfill({ json: transcript }))
  await page.route('**/api/v1/sessions/*/*/execution**', route => route.fulfill({ json: { revision: 1, asOf: snapshot.asOf, items: [execution] } }))
  await page.routeWebSocket('**/api/v1/ws', ws => {
    ws.send(JSON.stringify({ type: 'ready', topics: [] }))
    ws.onMessage(message => {
      const { topic } = JSON.parse(String(message))
      ws.send(JSON.stringify({ type: topic, revision: 1, asOf: snapshot.asOf, payload: { items: topic === 'sessions' ? [session] : [] } }))
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
    for (const selector of ['.state-basis', '.session-link', '.attribution-source', '.execution-list', '.transcript-host .txn-md-prose', '.theme-control select']) {
      const locator = page.locator(selector).first()
      console.log('paint', theme, selector, await locator.evaluate(el => {
        const chain = []
        for (let node: Element | null = el; node; node = node.parentElement) {
          const style = getComputedStyle(node)
          if (style.backgroundColor !== 'rgba(0, 0, 0, 0)' || style.backgroundImage !== 'none') chain.push([node.tagName, style.backgroundColor, style.backgroundImage])
        }
        return { color: getComputedStyle(el).color, backgrounds: chain }
      }))
    }
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
      console.log('incomplete:', JSON.stringify(result.incomplete.map(item => ({ id: item.id, nodes: item.nodes.map(node => ({ target: node.target, summary: node.failureSummary })) }))))
      expect(result.violations).toEqual([])
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
