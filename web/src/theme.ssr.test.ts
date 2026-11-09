// @vitest-environment node
import { expect, it } from 'vitest'

it('imports and resolves deterministically without browser globals', async () => {
  expect(typeof window).toBe('undefined')
  const theme = await import('./theme')
  expect(theme.resolveTheme('system')).toBe('dark')
  expect(theme.parseThemePreference(null)).toBe('system')
})
