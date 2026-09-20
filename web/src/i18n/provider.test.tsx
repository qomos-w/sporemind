import { describe, it, expect, afterEach } from 'vitest'
import { render } from '@testing-library/react'
import { I18nProvider, useI18n } from './provider'
import type { Locale } from './types'

function LocaleProbe({ onLocale }: { onLocale?: (l: Locale) => void }) {
  const { locale } = useI18n()
  onLocale?.(locale)
  return null
}

describe('I18nProvider locale mirroring', () => {
  afterEach(() => {
    document.documentElement.removeAttribute('lang')
  })

  it('mirrors the active locale onto <html lang> and follows changes', () => {
    const { rerender } = render(
      <I18nProvider initialLocale="zh-CN">
        <LocaleProbe />
      </I18nProvider>,
    )
    expect(document.documentElement.lang).toBe('zh-CN')

    rerender(
      <I18nProvider initialLocale="ja-JP">
        <LocaleProbe />
      </I18nProvider>,
    )
    expect(document.documentElement.lang).toBe('ja-JP')
  })

  it('mirrors the detected browser locale when no initialLocale is given', () => {
    render(
      <I18nProvider>
        <LocaleProbe />
      </I18nProvider>,
    )
    expect(document.documentElement.lang).not.toBe('')
  })
})
