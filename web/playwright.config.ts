import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './tests/browser',
  testMatch: '**/*.pw.ts',
  // The beforeAll hook builds the real pasture binary with `go build` before
  // starting the embedded server. A cold module/build cache can exceed the
  // 30s Playwright default, so give the hook the same headroom as its own
  // execFileSync timeout.
  timeout: 180_000,
  retries: 0,
  reporter: 'list',
  workers: 1,
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
})
