import React, { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import type { I18nKey, Locale } from './types'
import { defaultLocale, isLocale, parseLocale, supportedLocales } from './types'
import zhCN from './locales/zh-CN.json'
import zhTW from './locales/zh-TW.json'
import enUS from './locales/en-US.json'
import jaJP from './locales/ja-JP.json'
import koKR from './locales/ko-KR.json'
import frFR from './locales/fr-FR.json'
import deDE from './locales/de-DE.json'
import esES from './locales/es-ES.json'
import ptBR from './locales/pt-BR.json'
import ruRU from './locales/ru-RU.json'

const catalogs: Record<Locale, Record<string, string>> = {
  'zh-CN': zhCN,
  'zh-TW': zhTW,
  'en-US': enUS,
  'ja-JP': jaJP,
  'ko-KR': koKR,
  'fr-FR': frFR,
  'de-DE': deDE,
  'es-ES': esES,
  'pt-BR': ptBR,
  'ru-RU': ruRU,
}

export interface I18nContextValue {
  locale: Locale
  supportedLocales: readonly Locale[]
  t: (key: I18nKey, params?: Record<string, string | number>) => string
  setLocale: (locale: Locale) => void
}

export const I18nContext = createContext<I18nContextValue | null>(null)

export interface I18nProviderProps {
  children: React.ReactNode
  initialLocale?: Locale
  onLocaleChange?: (locale: Locale) => void
}

function detectBrowserLocale(): Locale {
  if (typeof navigator === 'undefined') {
    return defaultLocale
  }
  const raw = navigator.language || (navigator as unknown as { browserLanguage?: string }).browserLanguage
  return raw ? parseLocale(raw) : defaultLocale
}

export function resolveInitialLocale(preferred?: string): Locale {
  if (preferred && isLocale(preferred)) {
    return preferred
  }
  return detectBrowserLocale()
}

export function I18nProvider({ children, initialLocale, onLocaleChange }: I18nProviderProps) {
  const [locale, setLocaleState] = useState<Locale>(() => resolveInitialLocale(initialLocale))

  // Mirror the active locale onto <html lang> — the canonical DOM signal the
  // plugin iframe bridge observes (bootstrap payload + sporemind:locale-update),
  // mirroring how applyTheme publishes data-theme. Also correct a11y metadata.
  useEffect(() => {
    document.documentElement.lang = locale
  }, [locale])

  const setLocale = useCallback(
    (next: Locale) => {
      if (!supportedLocales.includes(next)) {
        return
      }
      setLocaleState(next)
      onLocaleChange?.(next)
    },
    [onLocaleChange]
  )

  useEffect(() => {
    if (initialLocale && isLocale(initialLocale) && initialLocale !== locale) {
      setLocaleState(initialLocale)
    }
  }, [initialLocale])

  const t = useCallback(
    (key: I18nKey, params?: Record<string, string | number>) => {
      const catalog = catalogs[locale] ?? catalogs[defaultLocale]
      let message = catalog[key] ?? catalogs[defaultLocale][key] ?? key
      if (params) {
        for (const [name, value] of Object.entries(params)) {
          message = message.replace(new RegExp(`\\{${name}\\}`, 'g'), String(value))
        }
      }
      return message
    },
    [locale]
  )

  const value = useMemo<I18nContextValue>(
    () => ({
      locale,
      supportedLocales,
      t,
      setLocale,
    }),
    [locale, t, setLocale]
  )

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>
}

export function useI18n(): I18nContextValue {
  const ctx = useContext(I18nContext)
  if (!ctx) {
    throw new Error('useI18n must be used inside <I18nProvider>')
  }
  return ctx
}

export function useLocale(): Locale {
  return useI18n().locale
}

export function useSetLocale(): (locale: Locale) => void {
  return useI18n().setLocale
}
