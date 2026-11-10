// @vitest-environment happy-dom
import { StrictMode, act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { ToastProvider } from '@peasant-labs/fairtrade/ui'
import App from './App'
import { THEME_STORAGE_KEY } from './theme'

afterEach(() => { localStorage.clear(); vi.restoreAllMocks(); vi.unstubAllGlobals() })

it('applies preference and system changes, retaining a choice when storage fails', async () => {
  localStorage.setItem(THEME_STORAGE_KEY, 'invalid')
  let dark = false
  const listeners = new Set<() => void>()
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  vi.stubGlobal('matchMedia', () => ({
    get matches() { return dark },
    addEventListener: (_: string, listener: () => void) => listeners.add(listener),
    removeEventListener: (_: string, listener: () => void) => listeners.delete(listener),
  }))
  vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('offline')))
  vi.stubGlobal('WebSocket', class { close() {} })
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  const assertTheme = (value: string) => {
    expect(document.documentElement.dataset.theme).toBe(value)
    expect(document.documentElement.style.colorScheme).toBe(value)
  }
  try {
    await act(async () => { root.render(<StrictMode><ToastProvider placement="bottom-right"><App /></ToastProvider></StrictMode>) })
    const label = [...container.querySelectorAll('label')].find((element) => element.textContent === 'Theme')!
    const select = container.querySelector<HTMLSelectElement>(`select[id="${label.htmlFor}"]`)!
    expect(select.value).toBe('system')
    expect(listeners.size).toBe(1)
    assertTheme('light')
    await act(async () => { dark = true; listeners.forEach((listener) => listener()) })
    assertTheme('dark')
    const choose = async (value: string) => act(async () => { select.value = value; select.dispatchEvent(new Event('change', { bubbles: true })) })
    await choose('light')
    assertTheme('light')
    expect(listeners.size).toBe(0)
    await act(async () => { dark = false; listeners.forEach((listener) => listener()) })
    assertTheme('light')
    await choose('dark')
    assertTheme('dark')
    await choose('system')
    assertTheme('light')
    vi.spyOn(localStorage, 'setItem').mockImplementation(() => { throw new Error('denied') })
    await choose('dark')
    expect(select.value).toBe('dark')
    assertTheme('dark')
    expect(container.querySelector('[role="status"]')?.textContent).toMatch(/not saved.*allow site storage/i)
  } finally {
    await act(async () => { root.unmount() })
    container.remove()
  }
  expect(listeners.size).toBe(0)
})
