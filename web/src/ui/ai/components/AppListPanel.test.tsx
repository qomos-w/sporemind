import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { AppListPanel } from './AppListPanel'
import type { AppEntry } from '../../../application/app-registry'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

// The picker only cares about the tile markup, not the icon asset resolution.
vi.mock('./appIconResolver', () => ({
  resolveAppIcon: () => null,
}))

function makeApp(overrides: Partial<AppEntry> & Pick<AppEntry, 'id' | 'state'>): AppEntry {
  return {
    runtime: 'spore',
    version: '1.0.0',
    entrypoints: [],
    ...overrides,
  } as AppEntry
}

describe('AppListPanel', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    act(() => root.unmount())
    container.remove()
  })

  it('lists only openable apps and calls onSelect on click', () => {
    const onSelect = vi.fn()
    const running = makeApp({ id: 'notes', name: 'Notes', state: 'running', entrypoints: [{ id: 'main', kind: 'view', title: 'Notes' }] })
    const stopped = makeApp({ id: 'dead', name: 'Dead', state: 'stopped', entrypoints: [{ id: 'main', kind: 'view', title: 'Dead' }] })
    const noView = makeApp({ id: 'headless', name: 'Headless', state: 'running', entrypoints: [{ id: 'svc', kind: 'callable', title: 'Svc' }] })

    act(() => {
      root.render(<AppListPanel apps={[running, stopped, noView]} onSelect={onSelect} />)
    })

    expect(container.querySelector('[data-app-id="notes"]')).not.toBeNull()
    expect(container.querySelector('[data-app-id="dead"]')).toBeNull()
    expect(container.querySelector('[data-app-id="headless"]')).toBeNull()

    act(() => {
      ;(container.querySelector('[data-app-id="notes"]') as HTMLButtonElement).click()
    })
    expect(onSelect).toHaveBeenCalledTimes(1)
    expect(onSelect).toHaveBeenCalledWith(running)
  })

  it('shows the empty state when nothing is openable', () => {
    const stopped = makeApp({ id: 'dead', state: 'stopped', entrypoints: [{ id: 'main', kind: 'view', title: 'Dead' }] })
    act(() => {
      root.render(<AppListPanel apps={[stopped]} onSelect={() => {}} />)
    })
    expect(container.querySelector('[data-testid="right-panel-app-list"]')).toBeNull()
    expect(container.textContent).toContain('rightPanel.appsEmpty')
  })
})
