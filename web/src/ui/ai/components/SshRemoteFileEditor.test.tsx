import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createRoot } from 'react-dom/client'
import { act } from 'react'
import { I18nProvider } from '../../../i18n'
import { EditorKeymapProvider } from '../../editor/EditorKeymapContext'
import { SshRemoteFileEditor } from './SshRemoteFileEditor'

const fileReadMock = vi.fn()
const fileWriteMock = vi.fn()

vi.mock('../../../gen-clients/sshmanager/client', () => ({
  fileRead: (...a: unknown[]) => fileReadMock(...a),
  fileWrite: (...a: unknown[]) => fileWriteMock(...a),
}))

function renderEditor(props: Record<string, unknown> = {}) {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  act(() => {
    root.render(
      <I18nProvider initialLocale="en-US">
        <EditorKeymapProvider>
          <SshRemoteFileEditor sessionId="sess-1" path="/etc/app.conf" {...props} />
        </EditorKeymapProvider>
      </I18nProvider>,
    )
  })
  return { unmount: () => act(() => { root.unmount(); document.body.removeChild(container) }) }
}

beforeEach(() => {
  vi.clearAllMocks()
  fileReadMock.mockResolvedValue({ Content: 'hello world', IsBinary: false })
  fileWriteMock.mockResolvedValue({})
})

describe('SshRemoteFileEditor', () => {
  it('loads and shows the remote path in the toolbar', async () => {
    const { unmount } = renderEditor()
    expect(await screen.findByText('/etc/app.conf')).toBeTruthy()
    unmount()
  })

  it('shows a load-failed message when file_read rejects', async () => {
    fileReadMock.mockRejectedValue(new Error('Connection refused'))
    const { unmount } = renderEditor()
    expect(await screen.findByText(/Failed to load file/)).toBeTruthy()
    unmount()
  })

  it('shows a binary notice when the file is binary', async () => {
    fileReadMock.mockResolvedValue({ Content: '', IsBinary: true })
    const { unmount } = renderEditor()
    expect(await screen.findByText(/binary/i)).toBeTruthy()
    unmount()
  })

  it('saves edits back via file_write on the save button', async () => {
    const user = userEvent.setup()
    const { unmount } = renderEditor()
    await screen.findByText('/etc/app.conf')
    const saveBtn = await screen.findByRole('button', { name: /save/i })
    expect((saveBtn as HTMLButtonElement).disabled).toBe(true)
    const editor = await screen.findByRole('textbox')
    await user.type(editor, '!')
    expect((saveBtn as HTMLButtonElement).disabled).toBe(false)
    await user.click(saveBtn)
    expect(fileWriteMock).toHaveBeenCalledTimes(1)
    const req = fileWriteMock.mock.calls[0]?.[1] as { SessionId: string; Path: string; Content: string }
    expect(req.SessionId).toBe('sess-1')
    expect(req.Path).toBe('/etc/app.conf')
    expect(req.Content).toContain('hello world')
    expect(req.Content).toContain('!')
    unmount()
  })
})
