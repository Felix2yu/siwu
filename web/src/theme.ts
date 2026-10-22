export type ThemeMode = 'system' | 'light' | 'dark'

const STORAGE_KEY = 'siwu.theme'

// 读不到或值非法时按跟随系统处理。
export function loadThemeMode(): ThemeMode {
  if (typeof window === 'undefined') return 'system'
  try {
    const v = window.localStorage.getItem(STORAGE_KEY)
    return v === 'light' || v === 'dark' || v === 'system' ? v : 'system'
  } catch {
    return 'system'
  }
}

export function resolveTheme(mode: ThemeMode, systemDark: boolean): 'light' | 'dark' {
  if (mode === 'system') return systemDark ? 'dark' : 'light'
  return mode
}

export function applyTheme(mode: ThemeMode) {
  const systemDark =
    typeof window !== 'undefined' &&
    typeof window.matchMedia === 'function' &&
    window.matchMedia('(prefers-color-scheme: dark)').matches
  const resolved = resolveTheme(mode, systemDark)
  const root = document.documentElement
  root.classList.toggle('dark', resolved === 'dark')
  root.style.colorScheme = resolved
}

export function saveThemeMode(mode: ThemeMode) {
  try {
    window.localStorage.setItem(STORAGE_KEY, mode)
  } catch {
    /* 隐私模式下写不进去也不影响本次切换 */
  }
  applyTheme(mode)
}
