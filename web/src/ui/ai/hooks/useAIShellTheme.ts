import { useCallback, useEffect, useState } from 'react'
import { applyTheme, defaultTheme, type ThemeState } from '@qomos/sporemind-theme'
import { loadTheme, persistTheme } from '../../../application/theme-persist'

interface UseAIShellThemeOptions {
  embedded: boolean
  theme?: ThemeState
  onThemeChange?: (theme: ThemeState) => void
  scope?: string
}

interface UseAIShellThemeResult {
  theme: ThemeState
  handleThemeChange: (next: ThemeState) => void
}

export function useAIShellTheme({
  embedded,
  theme: externalTheme,
  onThemeChange,
  scope = 'ai-shell',
}: UseAIShellThemeOptions): UseAIShellThemeResult {
  const [internalTheme, setInternalTheme] = useState<ThemeState>(defaultTheme)

  useEffect(() => {
    if (externalTheme) return
    let cancelled = false
    loadTheme(scope)
      .then(next => {
        if (!cancelled) setInternalTheme(next)
      })
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [externalTheme, scope])

  const theme = externalTheme ?? internalTheme

  useEffect(() => {
    if (!embedded && !externalTheme) {
      applyTheme(theme)
    }
  }, [embedded, externalTheme, theme])

  const handleThemeChange = useCallback((next: ThemeState) => {
    if (onThemeChange) {
      onThemeChange(next)
    } else {
      setInternalTheme(next)
      applyTheme(next)
    }
    void persistTheme(scope, next)
  }, [onThemeChange, scope])

  return {
    theme,
    handleThemeChange,
  }
}
