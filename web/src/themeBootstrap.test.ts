// @vitest-environment happy-dom
import { readFileSync } from 'node:fs'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { parseThemePreference, resolveTheme, THEME_STORAGE_KEY } from './theme'

const html = readFileSync('index.html', 'utf8')
const bootstrap = html.match(/<script>([\s\S]*?)<\/script>/)?.[1]
afterEach(() => vi.unstubAllGlobals())

describe('pre-paint theme bootstrap', () => {
  it('declares a dark no-script fallback without overriding the bootstrap', () => {
    expect(html).toContain('<meta name="color-scheme" content="dark" />')
    expect(html.indexOf('name="color-scheme"')).toBeLessThan(html.indexOf('<script>'))
  })
  it.each([
    { name: 'system light', value: null, media: 'light' },
    { name: 'system dark', value: null, media: 'dark' },
    { name: 'stored light', value: 'light', media: 'dark' },
    { name: 'stored dark', value: 'dark', media: 'light' },
    { name: 'stored system', value: 'system', media: 'light' },
    { name: 'invalid value', value: 'invalid', media: 'light' },
    { name: 'storage throws', value: null, media: 'light', storageError: true },
    { name: 'matchMedia missing', value: null, media: 'missing' },
    { name: 'matchMedia throws', value: null, media: 'throws' },
  ])('$name matches runtime resolution', ({ value, media, storageError }) => {
    expect(bootstrap).toBeTruthy()
    const doc = document.implementation.createHTMLDocument('test')
    const matchMedia = media === 'missing' ? undefined : () => {
      if (media === 'throws') throw new Error('media unavailable')
      return { matches: media === 'dark' }
    }
    const storage = { getItem: (key: string) => {
      expect(key).toBe(THEME_STORAGE_KEY)
      if (storageError) throw new Error('storage unavailable')
      return value
    } }
    vi.stubGlobal('matchMedia', matchMedia)
    const expected = resolveTheme(parseThemePreference(value))
    new Function('window', 'document', bootstrap!)({ localStorage: storage, matchMedia }, doc)
    expect(doc.documentElement.dataset.theme).toBe(expected)
    expect(doc.documentElement.style.colorScheme).toBe(expected)
  })
})
