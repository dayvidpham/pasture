export type ThemePreference = 'light' | 'dark' | 'system'
export type ResolvedTheme = 'light' | 'dark'
export type ThemePersistence = 'available' | 'unavailable'
export const THEME_STORAGE_KEY = 'pasture.theme.preference'
export interface ThemeSnapshot { preference: ThemePreference; resolvedTheme: ResolvedTheme; persistence: ThemePersistence }
export function parseThemePreference(value: string | null): ThemePreference {
  return value === 'light' || value === 'dark' || value === 'system' ? value : 'system'
}
function mediaQuery(): MediaQueryList | null {
  try { return typeof window !== 'undefined' && typeof window.matchMedia === 'function' ? window.matchMedia('(prefers-color-scheme: dark)') : null }
  catch { return null }
}
export function resolveTheme(preference: ThemePreference): ResolvedTheme {
  if (preference !== 'system') return preference
  const media = mediaQuery()
  return media ? (media.matches ? 'dark' : 'light') : 'dark'
}
export function createThemeController() {
  let preference: ThemePreference = 'system'
  let persistence: ThemePersistence = 'available'
  if (typeof window !== 'undefined') {
    try { preference = parseThemePreference(window.localStorage.getItem(THEME_STORAGE_KEY)) }
    catch { persistence = 'unavailable' }
  }
  let snapshot: ThemeSnapshot = { preference, resolvedTheme: resolveTheme(preference), persistence }
  const listeners = new Set<() => void>()
  let media: MediaQueryList | null = null
  const update = () => {
    const resolvedTheme = resolveTheme(preference)
    if (snapshot.preference === preference && snapshot.resolvedTheme === resolvedTheme && snapshot.persistence === persistence) return
    snapshot = { preference, resolvedTheme, persistence }
    listeners.forEach(listener => listener())
  }
  const detach = () => { if (media) media.removeEventListener('change', update); media = null }
  const attach = () => {
    detach()
    if (listeners.size && preference === 'system') {
      media = mediaQuery()
      try { media?.addEventListener('change', update) } catch { media = null }
    }
    update()
  }
  return {
    getSnapshot: () => snapshot,
    subscribe(listener: () => void) {
      listeners.add(listener)
      if (listeners.size === 1) attach()
      return () => { listeners.delete(listener); if (!listeners.size) detach() }
    },
    setPreference(next: ThemePreference) {
      preference = next
      try {
        if (typeof window === 'undefined') throw new Error('Storage unavailable')
        window.localStorage.setItem(THEME_STORAGE_KEY, next)
        persistence = 'available'
      } catch { persistence = 'unavailable' }
      attach()
    },
  }
}
