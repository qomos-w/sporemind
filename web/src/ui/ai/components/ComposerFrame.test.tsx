import { describe, it, expect, vi } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { ComposerFrame } from './ComposerFrame'
import type { TurnEnvelope } from '../model/frame-types'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

// ── Helpers ──

function makeUserEnvelope(content: string, id = 'env-u'): TurnEnvelope {
  return { id, role: 'user', userContent: content, frames: [], timestamp: new Date().toISOString() }
}

function renderFrame(props: Partial<Parameters<typeof ComposerFrame>[0]> & { showDrawer: boolean; envelopes: TurnEnvelope[] }) {
  const container = document.createElement('div')
  document.body.appendChild(container)
  let root: Root
  act(() => {
    root = createRoot(container)
    root.render(
      <ComposerFrame
        showDrawer={props.showDrawer}
        envelopes={props.envelopes}
        isStreaming={props.isStreaming}
        onFrameSelect={props.onFrameSelect}
        aboveBarSlot={props.aboveBarSlot}
      >
        <div className="mock-composer">Mock Composer</div>
      </ComposerFrame>
    )
  })
  return { container, unmount: () => { root!.unmount(); container.remove() } }
}

// ── Tests ──

describe('ComposerFrame', () => {
  it('renders composer only when showDrawer=false', () => {
    const { container, unmount } = renderFrame({ showDrawer: false, envelopes: [] })
    expect(container.querySelector('.composer-frame')).toBeTruthy()
    expect(container.querySelector('.mock-composer')).toBeTruthy()
    expect(container.querySelector('.message-drawer')).toBeNull()
    unmount()
  })

  it('renders no-drawer class when showDrawer=false', () => {
    const { container, unmount } = renderFrame({ showDrawer: false, envelopes: [] })
    expect(container.querySelector('.composer-frame.no-drawer')).toBeTruthy()
    unmount()
  })

  it('renders drawer card when showDrawer=true and envelopes exist', () => {
    const envs = [makeUserEnvelope('Hello')]
    const { container, unmount } = renderFrame({ showDrawer: true, envelopes: envs })
    expect(container.querySelector('.composer-frame.has-drawer')).toBeTruthy()
    expect(container.querySelector('.composer-frame-drawer-card')).toBeTruthy()
    expect(container.querySelector('.composer-frame-drawer-content')).toBeTruthy()
    expect(container.querySelector('.composer-frame-composer-card')).toBeTruthy()
    expect(container.querySelector('.mock-composer')).toBeTruthy()
    unmount()
  })

  it('shows empty drawer when showDrawer=true but no envelopes', () => {
    const { container, unmount } = renderFrame({ showDrawer: true, envelopes: [] })
    expect(container.querySelector('.composer-frame.has-drawer')).toBeTruthy()
    expect(container.querySelector('.composer-frame-drawer-card')).toBeTruthy()
    expect(container.querySelector('.message-drawer-peek')?.textContent).toContain('No messages')
    unmount()
  })

  it('composer card comes after drawer content in DOM (higher stacking)', () => {
    const envs = [makeUserEnvelope('Hello')]
    const { container, unmount } = renderFrame({ showDrawer: true, envelopes: envs })
    const card = container.querySelector('.composer-frame-drawer-card')!
    const children = Array.from(card.children)
    const contentIdx = children.findIndex(c => c.classList.contains('composer-frame-drawer-content'))
    const composerIdx = children.findIndex(c => c.classList.contains('composer-frame-composer-card'))
    expect(contentIdx).toBeGreaterThanOrEqual(0)
    expect(composerIdx).toBeGreaterThanOrEqual(0)
    expect(composerIdx).toBeGreaterThan(contentIdx)
    unmount()
  })

  it('renders aboveBarSlot above drawer card', () => {
    const envs = [makeUserEnvelope('Hello')]
    const { container, unmount } = renderFrame({
      showDrawer: true,
      envelopes: envs,
      aboveBarSlot: <div className="test-above-bar">Projects</div>,
    })
    const frame = container.querySelector('.composer-frame')!
    const children = Array.from(frame.children)
    const aboveIdx = children.findIndex(c => (c as HTMLElement).className?.includes('composer-frame-above-bar'))
    const drawerIdx = children.findIndex(c => (c as HTMLElement).className?.includes('composer-frame-drawer-card'))
    expect(aboveIdx).toBeGreaterThanOrEqual(0)
    expect(drawerIdx).toBeGreaterThanOrEqual(0)
    expect(aboveIdx).toBeLessThan(drawerIdx)
    expect(container.querySelector('.test-above-bar')).toBeTruthy()
    unmount()
  })

  it('expands drawer when peek is clicked', () => {
    const envs = [makeUserEnvelope('Hello')]
    const { container, unmount } = renderFrame({ showDrawer: true, envelopes: envs })
    const peek = container.querySelector('.message-drawer-peek') as HTMLElement
    act(() => { peek.click() })
    expect(container.querySelector('.message-drawer.expanded')).toBeTruthy()
    expect(container.querySelector('.compact-message-stream')).toBeTruthy()
    unmount()
  })

  it('collapse button only renders in drawer mode', () => {
    const envs = [makeUserEnvelope('Hello')]
    const { container: noDrawer, unmount: u1 } = renderFrame({ showDrawer: false, envelopes: envs })
    expect(noDrawer.querySelector('.composer-frame-collapse-btn')).toBeNull()
    u1()

    const { container: withDrawer, unmount: u2 } = renderFrame({ showDrawer: true, envelopes: envs })
    expect(withDrawer.querySelector('.composer-frame-collapse-btn')).toBeTruthy()
    u2()
  })

  it('collapse button collapses an expanded drawer first, then the composer', () => {
    const envs = [makeUserEnvelope('Hello')]
    const { container, unmount } = renderFrame({ showDrawer: true, envelopes: envs })
    const btn = container.querySelector('.composer-frame-collapse-btn') as HTMLElement

    // Expand drawer, then click collapse: drawer collapses, composer stays
    act(() => { (container.querySelector('.message-drawer-peek') as HTMLElement).click() })
    expect(container.querySelector('.message-drawer.expanded')).toBeTruthy()
    act(() => { btn.click() })
    expect(container.querySelector('.message-drawer.collapsed')).toBeTruthy()
    expect(container.querySelector('.composer-frame-drawer-card.composer-collapsed')).toBeNull()

    // Second click: composer hides, only the drawer remains
    act(() => { btn.click() })
    expect(container.querySelector('.composer-frame-drawer-card.composer-collapsed')).toBeTruthy()
    unmount()
  })

  it('peek click restores collapsed composer first, then expands drawer', () => {
    const envs = [makeUserEnvelope('Hello')]
    const { container, unmount } = renderFrame({ showDrawer: true, envelopes: envs })
    const btn = container.querySelector('.composer-frame-collapse-btn') as HTMLElement

    // Collapse the composer (drawer already collapsed)
    act(() => { btn.click() })
    expect(container.querySelector('.composer-frame-drawer-card.composer-collapsed')).toBeTruthy()

    // First peek click: restore composer, drawer stays collapsed
    const peek = container.querySelector('.message-drawer-peek') as HTMLElement
    act(() => { peek.click() })
    expect(container.querySelector('.composer-frame-drawer-card.composer-collapsed')).toBeNull()
    expect(container.querySelector('.message-drawer.collapsed')).toBeTruthy()

    // Second peek click: expand drawer
    act(() => { peek.click() })
    expect(container.querySelector('.message-drawer.expanded')).toBeTruthy()
    unmount()
  })

  it('auto-collapses when new message arrives while expanded', () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    let root: Root

    const envs1 = [makeUserEnvelope('Hello')]
    act(() => {
      root = createRoot(container)
      root.render(
        <ComposerFrame showDrawer envelopes={envs1}>
          <div className="mock-composer" />
        </ComposerFrame>
      )
    })

    // Expand drawer
    const peek = container.querySelector('.message-drawer-peek') as HTMLElement
    act(() => { peek.click() })
    expect(container.querySelector('.message-drawer.expanded')).toBeTruthy()

    // Add new message
    const envs2 = [makeUserEnvelope('Hello'), makeUserEnvelope('World', 'env-2')]
    act(() => {
      root!.render(
        <ComposerFrame showDrawer envelopes={envs2} isStreaming={false}>
          <div className="mock-composer" />
        </ComposerFrame>
      )
    })

    expect(container.querySelector('.message-drawer.collapsed')).toBeTruthy()
    root!.unmount()
    container.remove()
  })

  it('keeps drawer expanded when switching conversations (agent switch)', () => {
    const container = document.createElement('div')
    document.body.appendChild(container)
    let root: Root

    const envsA = [makeUserEnvelope('Hello')]
    act(() => {
      root = createRoot(container)
      root.render(
        <ComposerFrame showDrawer envelopes={envsA} conversationId="conv-a">
          <div className="mock-composer" />
        </ComposerFrame>
      )
    })

    // Expand drawer
    const peek = container.querySelector('.message-drawer-peek') as HTMLElement
    act(() => { peek.click() })
    expect(container.querySelector('.message-drawer.expanded')).toBeTruthy()

    // Switch agent: envelopes clear first, then the new history loads
    act(() => {
      root!.render(
        <ComposerFrame showDrawer envelopes={[]} conversationId="conv-b">
          <div className="mock-composer" />
        </ComposerFrame>
      )
    })
    const envsB = [makeUserEnvelope('A', 'env-a'), makeUserEnvelope('B', 'env-b'), makeUserEnvelope('C', 'env-c')]
    act(() => {
      root!.render(
        <ComposerFrame showDrawer envelopes={envsB} conversationId="conv-b">
          <div className="mock-composer" />
        </ComposerFrame>
      )
    })

    expect(container.querySelector('.message-drawer.expanded')).toBeTruthy()
    root!.unmount()
    container.remove()
  })

  it('frame box is transparent to pointer events; cards re-enable them', () => {
    const css = readFileSync(resolve(__dirname, 'ComposerFrame.css'), 'utf-8')
    const frameRule = /\.composer-frame\s*\{[^}]*\}/.exec(css)?.[0] ?? ''
    expect(frameRule).toContain('pointer-events: none')
    const childRule = /\.composer-frame\s*>\s*\*\s*\{[^}]*\}/.exec(css)?.[0] ?? ''
    expect(childRule).toContain('pointer-events: auto')
  })
})
