// @vitest-environment happy-dom
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createThemeController, parseThemePreference, resolveTheme, THEME_STORAGE_KEY } from './theme'
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); localStorage.clear() })
function system(initial: boolean) {
  let dark = initial
  const listeners = new Set<() => void>()
  const media = { get matches() { return dark }, addEventListener: (_: string, fn: () => void) => listeners.add(fn), removeEventListener: (_: string, fn: () => void) => listeners.delete(fn) }
  vi.stubGlobal('matchMedia', () => media)
  return { listeners, change(value: boolean) { dark = value; listeners.forEach(fn => fn()) } }
}
describe('theme controller', () => {
  it('validates stored values and falls back when media is unavailable or throws', () => {
    expect(parseThemePreference(null)).toBe('system')
    expect(parseThemePreference('LIGHT')).toBe('system')
    localStorage.setItem(THEME_STORAGE_KEY, 'bogus')
    vi.stubGlobal('matchMedia', undefined)
    expect(createThemeController().getSnapshot()).toMatchObject({ preference: 'system', resolvedTheme: 'dark' })
    vi.stubGlobal('matchMedia', () => { throw Error('unavailable') })
    expect(resolveTheme('system')).toBe('dark')
    expect(resolveTheme('light')).toBe('light')
  })
  it('tracks system only while subscribed and re-reads when returning to system', () => {
    const device = system(false)
    const controller = createThemeController()
    const notify = vi.fn()
    const unsubscribe = controller.subscribe(notify)
    expect(controller.getSnapshot().resolvedTheme).toBe('light')
    expect(device.listeners.size).toBe(1)
    device.change(true)
    expect(controller.getSnapshot().resolvedTheme).toBe('dark')
    controller.setPreference('light')
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('light')
    expect(device.listeners.size).toBe(0)
    device.change(false); device.change(true)
    expect(controller.getSnapshot().resolvedTheme).toBe('light')
    controller.setPreference('system')
    expect(controller.getSnapshot().resolvedTheme).toBe('dark')
    expect(device.listeners.size).toBe(1)
    unsubscribe()
    expect(device.listeners.size).toBe(0)
    const again = controller.subscribe(notify)
    expect(device.listeners.size).toBe(1)
    again()
    expect(device.listeners.size).toBe(0)
  })
  it('survives storage failures and applies unsaved selection', () => {
    vi.spyOn(localStorage, 'getItem').mockImplementation(() => { throw Error('denied') })
    vi.spyOn(localStorage, 'setItem').mockImplementation(() => { throw Error('denied') })
    const controller = createThemeController()
    expect(controller.getSnapshot().persistence).toBe('unavailable')
    controller.setPreference('light')
    expect(controller.getSnapshot()).toEqual({ preference: 'light', resolvedTheme: 'light', persistence: 'unavailable' })
  })
})
