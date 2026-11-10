// @vitest-environment happy-dom
import { StrictMode, act, useSyncExternalStore } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { createThemeController } from './theme'

afterEach(() => { vi.unstubAllGlobals(); localStorage.clear() })

it('holds exactly one system listener in StrictMode and cleans up on unmount', async () => {
  const listeners = new Set<EventListenerOrEventListenerObject>()
  vi.stubGlobal('matchMedia', () => ({
    matches: false,
    addEventListener: (_: string, listener: EventListenerOrEventListenerObject) => listeners.add(listener),
    removeEventListener: (_: string, listener: EventListenerOrEventListenerObject) => listeners.delete(listener),
  }))
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  const controller = createThemeController()
  function Consumer() {
    const snapshot = useSyncExternalStore(controller.subscribe, controller.getSnapshot)
    return <span>{snapshot.resolvedTheme}</span>
  }
  const container = document.createElement('div')
  const root = createRoot(container)
  try {
    await act(async () => root.render(<StrictMode><Consumer /></StrictMode>))
    expect(container.textContent).toBe('light')
    expect(listeners.size).toBe(1)
  } finally {
    await act(async () => root.unmount())
  }
  expect(listeners.size).toBe(0)
})
