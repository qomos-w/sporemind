import zhCN from './locales/zh-CN.json'

export type Locale =
  | 'zh-CN'
  | 'zh-TW'
  | 'en-US'
  | 'ja-JP'
  | 'ko-KR'
  | 'fr-FR'
  | 'de-DE'
  | 'es-ES'
  | 'pt-BR'
  | 'ru-RU'

export const supportedLocales: Locale[] = [
  'zh-CN',
  'zh-TW',
  'en-US',
  'ja-JP',
  'ko-KR',
  'fr-FR',
  'de-DE',
  'es-ES',
  'pt-BR',
  'ru-RU',
]

export const defaultLocale: Locale = 'en-US'

export const localeLabels: Record<Locale, string> = {
  'zh-CN': '简体中文',
  'zh-TW': '繁體中文',
  'en-US': 'English',
  'ja-JP': '日本語',
  'ko-KR': '한국어',
  'fr-FR': 'Français',
  'de-DE': 'Deutsch',
  'es-ES': 'Español',
  'pt-BR': 'Português',
  'ru-RU': 'Русский',
}

export type I18nKey = keyof typeof zhCN

export function isLocale(value: unknown): value is Locale {
  return typeof value === 'string' && supportedLocales.includes(value as Locale)
}

export function parseLocale(raw: string): Locale {
  const normalized = raw.toLowerCase().replace(/_/g, '-')
  switch (normalized) {
    case 'zh':
    case 'zh-cn':
    case 'zh-hans':
    case 'zh-sg':
      return 'zh-CN'
    case 'en':
    case 'en-us':
    case 'en-gb':
    case 'en-au':
    case 'en-ca':
      return 'en-US'
    case 'zh-tw':
    case 'zh-hant':
    case 'zh-hk':
    case 'zh-mo':
      return 'zh-TW'
    case 'ja':
    case 'ja-jp':
      return 'ja-JP'
    case 'ko':
    case 'ko-kr':
      return 'ko-KR'
    case 'fr':
    case 'fr-fr':
    case 'fr-ca':
      return 'fr-FR'
    case 'de':
    case 'de-de':
    case 'de-at':
    case 'de-ch':
      return 'de-DE'
    case 'es':
    case 'es-es':
    case 'es-mx':
      return 'es-ES'
    case 'pt':
    case 'pt-br':
    case 'pt-pt':
      return 'pt-BR'
    case 'ru':
    case 'ru-ru':
      return 'ru-RU'
    default:
      return defaultLocale
  }
}
