import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createRoot } from 'react-dom/client'
import { act } from 'react'
import { I18nProvider } from '../../i18n'
import { SshFileTextEditor, isContentDirty } from './SshFileTextEditor'

// ── Pure function tests ──

describe('isContentDirty', () => {
  it('returns false when original is null (still loading)', () => {
    expect(isContentDirty(null, 'hello')).toBe(false)
  })

  it('returns false when content matches original', () => {
    expect(isContentDirty('hello', 'hello')).toBe(false)
  })

  it('returns true when content differs from original', () => {
    expect(isContentDirty('hello', 'world')).toBe(true)
  })

  it('returns true when content has been modified', () => {
    expect(isContentDirty('original text', 'original text with changes')).toBe(true)
  })
})

// ── Component rendering tests ──

const mockClient = {} as any
const defaultProps = {
  client: mockClient,
  sessionId: 'sess-1',
  path: '/home/user/test.txt',
  size: 1024,
  onClose: vi.fn(),
  onSaved: vi.fn(),
}

const fileReadMock = vi.fn()
const fileWriteMock = vi.fn()

vi.mock('../../gen-clients/sshmanager/client', () => ({
  fileRead: (...a: unknown[]) => fileReadMock(...a),
  fileWrite: (...a: unknown[]) => fileWriteMock(...a),
}))

// Capture the textarea ref warning on mount about missing ResizeObserver.
beforeEach(() => {
  vi.clearAllMocks()
  fileReadMock.mockResolvedValue({ Content: 'hello world', IsBinary: false })
  fileWriteMock.mockResolvedValue({})
})

function renderEditor(props = {}) {
  const merged = { ...defaultProps, ...props }
  // Render via I18nProvider so translations are available.
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  act(() => {
    root.render(
      <I18nProvider initialLocale="en-US">
        <SshFileTextEditor {...merged} />
      </I18nProvider>,
    )
  })
  return { container, root, unmount: () => act(() => { root.unmount(); document.body.removeChild(container) }) }
}

describe('SshFileTextEditor', () => {
  it('shows loading state while fetching content', () => {
    // Leave the promise pending by not resolving.
    fileReadMock.mockImplementation(() => new Promise(() => {}))
    const { unmount } = renderEditor()
    expect(screen.getByRole('status')).toBeTruthy()
    unmount()
  })

  it('shows the editor after loading succeeds', async () => {
    const { unmount } = renderEditor()
    // Wait for the textarea to appear.
    const textarea = await screen.findByRole('textbox', { name: /edit file/i })
    expect(textarea).toBeTruthy()
    expect((textarea as HTMLTextAreaElement).value).toBe('hello world')
    unmount()
  })

  it('shows an error state when loading fails', async () => {
    fileReadMock.mockRejectedValue(new Error('Connection refused'))
    const { unmount } = renderEditor()
    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toContain('Failed to load file')
    unmount()
  })

  it('shows a binary-file message when the response has IsBinary=true', async () => {
    fileReadMock.mockResolvedValue({ Content: '', IsBinary: true })
    const { unmount } = renderEditor()
    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toContain('binary')
    unmount()
  })

  it('calls fileWrite and fires onSaved on save', async () => {
    const user = userEvent.setup()
    const onSaved = vi.fn()
    const { unmount } = renderEditor({ onSaved })
    const textarea = await screen.findByRole('textbox', { name: /edit file/i })
    // Type in the editor to make it dirty.
    await user.clear(textarea)
    await user.type(textarea, 'modified content')
    // Click save.
    const saveBtn = screen.getByRole('button', { name: /save/i })
    await user.click(saveBtn)
    expect(fileWriteMock).toHaveBeenCalledWith(mockClient, {
      SessionId: 'sess-1',
      Path: '/home/user/test.txt',
      Content: 'modified content',
    })
    expect(onSaved).toHaveBeenCalled()
    unmount()
  })
})