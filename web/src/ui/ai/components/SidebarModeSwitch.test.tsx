import { describe, it, expect, vi, beforeEach, afterEach, type Mock } from 'vitest'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { I18nProvider } from '../../../i18n'
import { SidebarModeSwitch, type LauncherMode } from './SidebarModeSwitch'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

describe('SidebarModeSwitch', () => {
  let container: HTMLDivElement
  let root: Root
  let onChange: Mock

  const setup = async (value: LauncherMode = 'code') => {
    onChange = vi.fn()
    await act(async () => {
      root = createRoot(container)
      root.render(
        <I18nProvider initialLocale="en-US">
          <SidebarModeSwitch value={value} onChange={onChange} />
        </I18nProvider>,
      )
    })
  }

  const rerender = async (value: LauncherMode) => {
    await act(async () => {
      root.render(
        <I18nProvider initialLocale="en-US">
          <SidebarModeSwitch value={value} onChange={onChange} />
        </I18nProvider>,
      )
    })
  }

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
  })

  afterEach(() => {
    act(() => root?.unmount())
    if (container.parentNode) document.body.removeChild(container)
    vi.restoreAllMocks()
  })

  const segments = () => Array.from(container.querySelectorAll<HTMLButtonElement>('.ai-sidebar-mode-switch-segment'))
  const segmentByLabel = (label: string) => segments().find(s => s.textContent?.includes(label))!

  it('renders a tablist labeled Launcher mode with three segments', async () => {
    await setup('code')
    const group = container.querySelector('.ai-sidebar-mode-switch')!
    expect(group.getAttribute('role')).toBe('tablist')
    expect(group.getAttribute('aria-label')).toBe('Launcher mode')
    expect(segments()).toHaveLength(3)
  })

  it('marks the active segment with aria-selected and the active class', async () => {
    await setup('code')
    expect(segmentByLabel('Build').classList.contains('active')).toBe(true)
    expect(segmentByLabel('Build').getAttribute('aria-selected')).toBe('true')
    expect(segmentByLabel('App').classList.contains('active')).toBe(false)
    await rerender('app')
    expect(segmentByLabel('App').classList.contains('active')).toBe(true)
    expect(segmentByLabel('Build').classList.contains('active')).toBe(false)
  })

  it('clicking a segment switches to that mode directly', async () => {
    await setup('code')
    act(() => { segmentByLabel('App').click() })
    expect(onChange).toHaveBeenCalledWith('app')
    await rerender('app')
    act(() => { segmentByLabel('Build').click() })
    expect(onChange).toHaveBeenCalledWith('code')
    act(() => { segmentByLabel('Workbench').click() })
    expect(onChange).toHaveBeenCalledWith('workbench')
  })

  it('renders every segment with icon + label', async () => {
    await setup('code')
    expect(segmentByLabel('Build').querySelector('svg.lucide-code-xml')).toBeTruthy()
    expect(segmentByLabel('App').querySelector('svg.lucide-rocket')).toBeTruthy()
    expect(segmentByLabel('Workbench').querySelector('svg.lucide-layout-dashboard')).toBeTruthy()
  })

  it('is styled after theme-switch: soft pill track, elevated active pill', async () => {
    await setup('code')
    const css = readFileSync(join(process.cwd(), 'src/ui/ai/components/SidebarModeSwitch.css'), 'utf8')
    const track = css.match(/\.ai\-sidebar\-mode\-switch\s*\{[^}]*\}/)?.[0] ?? ''
    expect(track).toMatch(/background:\s*var\(--bg\-hover\)/)
    expect(track).toMatch(/border-radius:\s*9999px/)
    const active = css.match(/\.ai\-sidebar\-mode\-switch\-segment\.active\s*\{[^}]*\}/)?.[0] ?? ''
    expect(active).toMatch(/background:\s*var\(--bg\-elevated\)/)
    expect(active).toMatch(/color:\s*var\(--text\-primary\)/)
  })

  it('does not depend on the browser overlay (no popup)', async () => {
    await setup('code')
    expect(onChange).not.toHaveBeenCalled()
  })
})
