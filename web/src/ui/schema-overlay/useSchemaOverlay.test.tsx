import { act, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import React from 'react'
import { SchemaOverlayProvider, useSchemaOverlay } from './useSchemaOverlay'

// Mock the Modal + i18n + client + theme-persist stack so we can exercise
// just the provider contract (open / close / isOpen, request replacement)
// without spinning up the full app shell.
vi.mock('../components/Modal', () => ({
  Modal: ({ open, title, children }: { open: boolean; title?: React.ReactNode; children?: React.ReactNode }) =>
    open ? <div data-testid="mock-modal">{title}<div data-testid="modal-body">{children}</div></div> : null,
}))

vi.mock('../../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

vi.mock('../../application/generated-client', () => ({
  client: { invoke: vi.fn() },
}))

vi.mock('../../application/theme-persist', () => ({
  savePreference: vi.fn().mockResolvedValue(undefined),
  loadPreference: vi.fn().mockResolvedValue(undefined),
}))

afterEach(() => {
  vi.restoreAllMocks()
})

function Probe({ onReady }: { onReady: (api: ReturnType<typeof useSchemaOverlay>) => void }) {
  const api = useSchemaOverlay()
  React.useEffect(() => {
    onReady(api)
  })
  return null
}

describe('SchemaOverlayProvider', () => {
  it('exposes a no-op controller when absent', () => {
    let captured: ReturnType<typeof useSchemaOverlay> | null = null
    function NakedProbe() {
      captured = useSchemaOverlay()
      return null
    }
    render(<NakedProbe />)
    expect(captured).not.toBeNull()
    expect(captured!.isOpen).toBe(false)
    // No-op should swallow errors rather than throw:
    expect(() => captured!.open({ callableId: 'foo', jsonSchema: {} })).not.toThrow()
    expect(() => captured!.close()).not.toThrow()
  })

  it('mounts a modal when open() is called and hides it on close()', async () => {
    let captured: ReturnType<typeof useSchemaOverlay> | null = null
    render(
      <SchemaOverlayProvider>
        <Probe onReady={(api) => { captured = api }} />
      </SchemaOverlayProvider>,
    )
    expect(captured!.isOpen).toBe(false)
    expect(screen.queryByTestId('mock-modal')).toBeNull()

    act(() => {
      captured!.open({
        callableId: 'toast.action',
        jsonSchema: { type: 'object', properties: { msg: { type: 'string' } } },
        title: 'Action',
      })
    })
    expect(captured!.isOpen).toBe(true)
    expect(screen.getByTestId('mock-modal')).toBeTruthy()
    expect(screen.getByText('Action')).toBeTruthy()

    act(() => {
      captured!.close()
    })
    expect(captured!.isOpen).toBe(false)
    expect(screen.queryByTestId('mock-modal')).toBeNull()
  })

  it('replaces an in-flight request when open() is called again', () => {
    let captured: ReturnType<typeof useSchemaOverlay> | null = null
    render(
      <SchemaOverlayProvider>
        <Probe onReady={(api) => { captured = api }} />
      </SchemaOverlayProvider>,
    )
    act(() => {
      captured!.open({
        callableId: 'toast.action',
        jsonSchema: {},
        title: 'First',
      })
    })
    expect(screen.getByText('First')).toBeTruthy()
    act(() => {
      captured!.open({
        callableId: 'toast.action',
        jsonSchema: {},
        title: 'Second',
      })
    })
    expect(screen.queryByText('First')).toBeNull()
    expect(screen.getByText('Second')).toBeTruthy()
  })
})