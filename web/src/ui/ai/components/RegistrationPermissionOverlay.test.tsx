import { describe, it, expect, vi } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { RegistrationPermissionOverlay } from './RegistrationPermissionOverlay'
import { InteractionDispatchContext } from '../hooks/useStepInteraction'
import type { PermissionRequestFrame, TurnEnvelope } from '../model/frame-types'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

function makeEnvelope(frames: TurnEnvelope['frames']): TurnEnvelope {
  return { id: 'env-a', role: 'assistant', frames, timestamp: new Date().toISOString(), completed: false }
}

function makePermissionFrame(overrides: Partial<PermissionRequestFrame> = {}): PermissionRequestFrame {
  return {
    id: 'perm-1',
    type: 'permission_request',
    status: 'running',
    toolCalls: [{ id: 'c1', callableId: 'appmanager.register_project', input: '{"AppDir":"plugins/admin-tools","ProjectId":"p1"}' }],
    reason: 'app registration requires explicit user authorization',
    requestId: 'req-perm',
    ...overrides,
  }
}

function render(envelopes: TurnEnvelope[], dispatch: (event: any) => Promise<void>) {
  const container = document.createElement('div')
  document.body.appendChild(container)
  let root: Root
  act(() => {
    root = createRoot(container)
    root.render(
      <InteractionDispatchContext.Provider value={dispatch}>
        <RegistrationPermissionOverlay envelopes={envelopes} />
      </InteractionDispatchContext.Provider>
    )
  })
  return { container, unmount: () => { root!.unmount(); container.remove() } }
}

function findBtn(container: HTMLElement, label: string): HTMLButtonElement {
  const btn = Array.from(container.querySelectorAll('button')).find(b => b.textContent === label)
  if (!btn) throw new Error(`button ${label} not found`)
  return btn as HTMLButtonElement
}

describe('RegistrationPermissionOverlay', () => {
  it('renders Android-style permission rows and dispatches confirm', () => {
    const dispatch = vi.fn().mockResolvedValue(undefined)
    const { container, unmount } = render([makeEnvelope([makePermissionFrame()])], dispatch)

    expect(container.querySelector('.reg-perm-overlay')).toBeTruthy()
    // action card: icon + localized title + payload summary (AppDir)
    const card = container.querySelector('.reg-perm-overlay-card')!
    expect(card).toBeTruthy()
    expect(card.querySelector('.reg-perm-overlay-action-icon')).toBeTruthy()
    expect(card.textContent).toContain('ai.overlay.registration.perm.register_project')
    expect(card.textContent).toContain('plugins/admin-tools')
    // raw payload stays collapsed under Details
    expect(container.querySelector('.reg-perm-overlay-details')!.textContent).toContain('appmanager.register_project')
    // backend reason is not surfaced raw
    expect(container.textContent).not.toContain('app registration requires explicit user authorization')
    // nothing attached (older host / unresolved) → warning note, confirm usable
    expect(container.textContent).toContain('ai.overlay.registration.hostPermissions.unavailable')
    expect(findBtn(container, 'common.confirm').disabled).toBe(false)

    act(() => { findBtn(container, 'common.confirm').click() })
    expect(dispatch).toHaveBeenCalledWith({ kind: 'ai.permission_answered', requestId: 'req-perm', allowed: true, allowInProject: undefined })
    unmount()
  })

  it('dispatches deny on cancel', () => {
    const dispatch = vi.fn().mockResolvedValue(undefined)
    const { container, unmount } = render([makeEnvelope([makePermissionFrame()])], dispatch)

    act(() => { findBtn(container, 'common.cancel').click() })
    expect(dispatch).toHaveBeenCalledWith({ kind: 'ai.permission_answered', requestId: 'req-perm', allowed: false, allowInProject: undefined })
    unmount()
  })

  it('stays hidden for non-registration permissions and answered frames', () => {
    const dispatch = vi.fn().mockResolvedValue(undefined)
    const nonRegistration = render([makeEnvelope([makePermissionFrame({
      toolCalls: [{ id: 'c1', callableId: 'project.git_push' }],
    })])], dispatch)
    expect(nonRegistration.container.querySelector('.reg-perm-overlay')).toBeNull()
    nonRegistration.unmount()

    const answered = render([makeEnvelope([makePermissionFrame({ allowed: true, status: 'completed' })])], dispatch)
    expect(answered.container.querySelector('.reg-perm-overlay')).toBeNull()
    answered.unmount()
    expect(dispatch).not.toHaveBeenCalled()
  })

  it('shows engine-attached manifest host permissions immediately, risk-ordered', () => {
    const dispatch = vi.fn().mockResolvedValue(undefined)
    const frame = makePermissionFrame({
      toolCalls: [{
        id: 'c1',
        callableId: 'appmanager.register',
        input: '{"Manifest":{"Id":"com.example.demo"},"EntryModule":"main"}',
        appId: 'com.example.demo',
        appName: 'Demo',
        permissions: ['fs.read', 'shell.exec', 'made.up.cap'],
      }],
    })
    const { container, unmount } = render([makeEnvelope([frame])], dispatch)

    // Synchronous derivation: no loading state, confirm usable right away.
    expect(container.textContent).not.toContain('ai.overlay.registration.hostPermissions.loading')
    expect(findBtn(container, 'common.confirm').disabled).toBe(false)

    // risk-ordered chips — unknown capability sorts first (treated as high),
    // then high, then low; app headline + micro-label shown
    const cardEl = container.querySelector('.reg-perm-overlay-card')!
    expect(cardEl.textContent).toContain('Demo')
    expect(cardEl.textContent).toContain('ai.overlay.registration.hostPermissions')
    const items = Array.from(cardEl.querySelectorAll('.reg-perm-overlay-chip'))
    expect(items.map(el => el.className)).toEqual([
      'reg-perm-overlay-chip risk-unknown', // made.up.cap (raw id shown)
      'reg-perm-overlay-chip risk-high', // shell.exec
      'reg-perm-overlay-chip risk-low', // fs.read
    ])
    expect(items[0]!.textContent).toContain('made.up.cap')
    expect(items[1]!.textContent).toContain('capability.shell.exec.title')
    // risk word lives in the chip tooltip, not the row text
    expect(items[1]!.getAttribute('title')).toContain('capability.risk.high')
    expect(items[1]!.getAttribute('title')).toContain('capability.shell.exec.description')

    act(() => { findBtn(container, 'common.confirm').click() })
    expect(dispatch).toHaveBeenCalledWith({ kind: 'ai.permission_answered', requestId: 'req-perm', allowed: true, allowInProject: undefined })
    unmount()
  })

  it('appends the backend permissionNote to the unavailable warning', () => {
    const dispatch = vi.fn().mockResolvedValue(undefined)
    const frame = makePermissionFrame({
      toolCalls: [{ id: 'c1', callableId: 'appmanager.register_project', input: '{"AppDir":"."}', permissionNote: 'appmanager: read manifest: not found' }],
    })
    const { container, unmount } = render([makeEnvelope([frame])], dispatch)
    const note = container.querySelector('.reg-perm-overlay-note.warn')!
    expect(note).toBeTruthy()
    expect(note.textContent).toContain('ai.overlay.registration.hostPermissions.unavailable')
    expect(note.textContent).toContain('appmanager: read manifest: not found')
    unmount()
  })
})
