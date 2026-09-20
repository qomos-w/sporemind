import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { CooldownProviderOption } from './CooldownProviderOption'
import type { ProviderOption } from './AIComposer'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const t = vi.fn((key: string, params?: Record<string, string | number>) => {
  if (params) return `${key}:${JSON.stringify(params)}`
  return key
})

function baseOption(overrides: Partial<ProviderOption> = {}): ProviderOption {
  return {
    id: 'prov1::gpt-4',
    label: 'gpt-4',
    subtitle: 'prov1',
    unit: { provider: 'prov1', model: 'gpt-4' },
    ...overrides,
  }
}

describe('CooldownProviderOption', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    t.mockClear()
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  it('renders a healthy option as clickable with no overlay', () => {
    const onSelect = vi.fn()
    act(() => {
      root.render(
        <CooldownProviderOption
          option={baseOption()}
          active={false}
          nowMs={1000_000}
          onSelect={onSelect}
          t={t}
        />,
      )
    })
    const btn = container.querySelector('button')!
    expect(btn).toBeTruthy()
    expect(btn.getAttribute('aria-disabled')).toBeNull()
    expect(btn.className).not.toContain('is-cooling')
    expect(btn.className).not.toContain('is-disabled')
    expect(container.querySelector('.ai-composer-cooldown-overlay')).toBeNull()
    expect(container.querySelector('.ai-composer-disabled-overlay')).toBeNull()

    act(() => { btn.click() })
    expect(onSelect).toHaveBeenCalledTimes(1)
  })

  it('renders a cooling_down option with progress overlay and countdown', () => {
    const now = 1_000_000
    const onSelect = vi.fn()
    // deadline is 60s from now
    const option = baseOption({
      healthState: 'cooling_down',
      cooldownUntil: now / 1000 + 60,
    })
    act(() => {
      root.render(
        <CooldownProviderOption
          option={option}
          active={false}
          nowMs={now}
          onSelect={onSelect}
          t={t}
        />,
      )
    })
    const btn = container.querySelector('button')!
    expect(btn.className).toContain('is-cooling')
    expect(btn.getAttribute('aria-disabled')).toBeNull()

    const overlay = container.querySelector('.ai-composer-cooldown-overlay')! as HTMLElement
    expect(overlay).toBeTruthy()
    // On first render, remaining = total, so overlay width should be ~100%
    expect(parseFloat(overlay.style.width)).toBeGreaterThan(99)

    const countdown = container.querySelector('.ai-composer-cooldown-countdown')!
    expect(countdown.textContent).toBe('01:00')

    // Cooldown is advisory for manual selection.
    act(() => { btn.click() })
    expect(onSelect).toHaveBeenCalledTimes(1)

    // aria-describedby should expose the cooldown and schedule reason.
    const descId = btn.getAttribute('aria-describedby')!
    expect(descId).toBeTruthy()
    const desc = document.getElementById(descId)!
    expect(desc.textContent).toContain('health.coolingDown')
  })

  it('surfaces the daily disable-window reason while cooling down', () => {
    const option = baseOption({
      healthState: 'cooling_down',
      healthReason: 'disable_window',
      cooldownUntil: 1_060,
    })
    act(() => {
      root.render(
        <CooldownProviderOption
          option={option}
          active={false}
          nowMs={1_000_000}
          onSelect={vi.fn()}
          t={t}
        />,
      )
    })

    const btn = container.querySelector('button')!
    const descId = btn.getAttribute('aria-describedby')!
    expect(document.getElementById(descId)?.textContent).toContain('health.reason.disable_window')
  })

  it('shrinks overlay width as cooldown elapses', () => {
    // First render at t=0 establishes total = 60s
    const startTime = 1_000_000
    const deadline = startTime / 1000 + 60
    const option = baseOption({
      healthState: 'cooling_down',
      cooldownUntil: deadline,
    })

    act(() => {
      root.render(
        <CooldownProviderOption
          option={option}
          active={false}
          nowMs={startTime}
          onSelect={vi.fn()}
          t={t}
        />,
      )
    })

    let overlay = container.querySelector('.ai-composer-cooldown-overlay')! as HTMLElement
    expect(parseFloat(overlay.style.width)).toBeCloseTo(100, 0)

    // 30s later: half remaining
    act(() => {
      root.render(
        <CooldownProviderOption
          option={option}
          active={false}
          nowMs={startTime + 30_000}
          onSelect={vi.fn()}
          t={t}
        />,
      )
    })
    overlay = container.querySelector('.ai-composer-cooldown-overlay')! as HTMLElement
    expect(parseFloat(overlay.style.width)).toBeCloseTo(50, 0)

    // Countdown should show ~30s
    const countdown = container.querySelector('.ai-composer-cooldown-countdown')!
    expect(countdown.textContent).toBe('00:30')
  })

  it('renders a disabled option with static mask and reason, no countdown', () => {
    const onSelect = vi.fn()
    const option = baseOption({
      healthState: 'disabled',
      healthReason: 'quota_exhausted',
      recoveryMode: 'manual_or_balance_refresh',
    })
    act(() => {
      root.render(
        <CooldownProviderOption
          option={option}
          active={false}
          nowMs={1_000_000}
          onSelect={onSelect}
          t={t}
        />,
      )
    })
    const btn = container.querySelector('button')!
    expect(btn.className).toContain('is-disabled')
    expect(btn.getAttribute('aria-disabled')).toBeNull()

    // Static mask, not progress overlay
    expect(container.querySelector('.ai-composer-disabled-overlay')).toBeTruthy()
    expect(container.querySelector('.ai-composer-cooldown-overlay')).toBeNull()

    // Reason label shown, no countdown
    expect(container.querySelector('.ai-composer-disabled-reason')).toBeTruthy()
    expect(container.querySelector('.ai-composer-cooldown-countdown')).toBeNull()

    // Long-term health is advisory for manual selection.
    act(() => { btn.click() })
    expect(onSelect).toHaveBeenCalledTimes(1)

    // aria description includes reason and recovery
    const descId = btn.getAttribute('aria-describedby')!
    const desc = document.getElementById(descId)!
    expect(desc.textContent).toContain('health.reason.quota_exhausted')
    expect(desc.textContent).toContain('health.recovery.manual_or_balance_refresh')
  })

  it('shows active check when selected and healthy', () => {
    act(() => {
      root.render(
        <CooldownProviderOption
          option={baseOption()}
          active={true}
          nowMs={1_000_000}
          onSelect={vi.fn()}
          t={t}
        />,
      )
    })
    const btn = container.querySelector('button')!
    expect(btn.className).toContain('active')
    expect(btn.querySelector('svg')).toBeTruthy()
  })
})
