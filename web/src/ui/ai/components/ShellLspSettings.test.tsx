import '@testing-library/jest-dom/vitest'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import * as lspClient from '../../../gen-clients/lsp/client'
import type { LspInstallProgressEvent } from '../../../gen-types/lsp'
import { ShellLspSettings } from './ShellLspSettings'

vi.mock('../../../application/generated-client', () => ({
  client: { invoke: vi.fn() },
}))

const { lspStatus, lspInstall, onLspInstallProgress, emitInstallProgress } = vi.hoisted(() => {
  let handler: ((ev: LspInstallProgressEvent) => void) | null = null
  return {
    lspStatus: vi.fn(),
    lspInstall: vi.fn(),
    onLspInstallProgress: vi.fn((_client, h) => {
      handler = h
      return () => { handler = null }
    }),
    emitInstallProgress: async (ev: LspInstallProgressEvent) => {
      await act(async () => {
        if (handler) handler(ev)
      })
    },
  }
})

vi.mock('./lsp-install-client', () => ({
  lspStatus,
  lspInstall,
  onLspInstallProgress,
}))

// t() returns the key verbatim so assertions match on stable i18n keys.
vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: (key: string, _opts?: Record<string, unknown>) => key }),
}))

const ALL_ENABLED = {
  Languages: [
    { Language: 'go', Enabled: true },
    { Language: 'typescript', Enabled: true },
    { Language: 'javascript', Enabled: true },
  ],
}

const ALL_DISABLED = {
  Languages: [
    { Language: 'go', Enabled: false },
    { Language: 'typescript', Enabled: false },
    { Language: 'javascript', Enabled: false },
  ],
}

const NO_INSTALLS = { Languages: [] }

const ALL_INSTALLED = {
  Languages: [
    { Language: 'go', Installed: true, Version: '0.15.3', DownloadSource: 'managed' },
    { Language: 'typescript', Installed: true, Version: '6.0.0', DownloadSource: 'managed' },
    { Language: 'javascript', Installed: true, Version: '6.0.0', DownloadSource: 'managed' },
    { Language: 'python', Installed: true, Version: '1.1.413', DownloadSource: 'managed' },
    { Language: 'rust', Installed: true, Version: '2026-08-17.4', DownloadSource: 'managed' },
    { Language: 'cpp', Installed: true, Version: '22.1.6', DownloadSource: 'managed' },
    { Language: 'css', Installed: true, Version: '4.10.0', DownloadSource: 'managed' },
    { Language: 'html', Installed: true, Version: '4.10.0', DownloadSource: 'managed' },
    { Language: 'json', Installed: true, Version: '4.10.0', DownloadSource: 'managed' },
    { Language: 'bash', Installed: true, Version: '5.6.0', DownloadSource: 'managed' },
  ],
}

const INSTALLED_GO = {
  Languages: [
    { Language: 'go', Installed: true, Version: '0.15.3', DownloadSource: 'managed' },
  ],
}

const NOT_INSTALLED_GO = {
  Languages: [
    { Language: 'go', Installed: false },
  ],
}

describe('ShellLspSettings', () => {
  const origRAF = globalThis.requestAnimationFrame
  const origCAF = globalThis.cancelAnimationFrame

  beforeEach(() => {
    globalThis.requestAnimationFrame = (cb: FrameRequestCallback) => setTimeout(cb, 0) as unknown as number
    globalThis.cancelAnimationFrame = (id: number) => clearTimeout(id)
    document.body.innerHTML = ''
    lspStatus.mockClear()
    lspInstall.mockClear()
    onLspInstallProgress.mockClear()
    lspStatus.mockResolvedValue(NO_INSTALLS)
    lspInstall.mockResolvedValue({ Started: true })
  })

  afterEach(() => {
    globalThis.requestAnimationFrame = origRAF
    globalThis.cancelAnimationFrame = origCAF
    vi.restoreAllMocks()
    document.body.innerHTML = ''
  })

  it('loads and reflects per-language enabled state', async () => {
    vi.spyOn(lspClient, 'stateGet').mockResolvedValue(ALL_ENABLED)
    render(<ShellLspSettings />)

    for (const lang of ['go', 'typescript', 'javascript']) {
      const key = `settings.lsp.lang.${lang}`
      await waitFor(() => {
        const sw = screen.getByRole('switch', { name: key })
        expect(sw).toHaveAttribute('data-checked', '')
      })
    }
  })

  it('calls stateSave on toggle with the correct language and updates the switch', async () => {
    vi.spyOn(lspClient, 'stateGet').mockResolvedValue(ALL_ENABLED)
    const stateSave = vi.spyOn(lspClient, 'stateSave').mockResolvedValue({ Languages: [] })
    render(<ShellLspSettings />)

    const goKey = 'settings.lsp.lang.go'
    await waitFor(() => {
      expect(screen.getByRole('switch', { name: goKey })).toHaveAttribute('data-checked', '')
    })

    const sw = screen.getByRole('switch', { name: goKey })
    fireEvent.click(sw)

    await waitFor(() => {
      expect(stateSave).toHaveBeenCalledWith(expect.anything(), { Language: 'go', Enabled: false })
    })
    await waitFor(() => {
      expect(sw).not.toHaveAttribute('data-checked')
    })
  })

  it('reverts the switch on save failure', async () => {
    vi.spyOn(lspClient, 'stateGet').mockResolvedValue(ALL_ENABLED)
    vi.spyOn(lspClient, 'stateSave').mockRejectedValue(new Error('network down'))
    render(<ShellLspSettings />)

    const goKey = 'settings.lsp.lang.go'
    await waitFor(() => {
      expect(screen.getByRole('switch', { name: goKey })).toHaveAttribute('data-checked', '')
    })

    fireEvent.click(screen.getByRole('switch', { name: goKey }))

    await waitFor(() => {
      expect(screen.getByRole('switch', { name: goKey })).toHaveAttribute('data-checked', '')
    })
    expect(screen.getByText(/network down/)).toBeInTheDocument()
  })

  it('disables the Clear Cache button when all languages are off', async () => {
    vi.spyOn(lspClient, 'stateGet').mockResolvedValue(ALL_DISABLED)
    render(<ShellLspSettings />)

    for (const lang of ['go', 'typescript', 'javascript']) {
      const key = `settings.lsp.lang.${lang}`
      await waitFor(() => {
        expect(screen.getByRole('switch', { name: key })).not.toHaveAttribute('data-checked')
      })
    }

    const clearBtn = screen.getByRole('button', { name: 'settings.lsp.clear' })
    expect(clearBtn).toBeDisabled()
  })

  it('shows not-installed badge and install button when language server is not installed', async () => {
    vi.spyOn(lspClient, 'stateGet').mockResolvedValue(ALL_ENABLED)
    lspStatus.mockResolvedValue(NOT_INSTALLED_GO)
    render(<ShellLspSettings />)

    await waitFor(() => {
      expect(screen.getAllByText('settings.lsp.status.notInstalled').length).toBeGreaterThan(0)
    })

    const installBtns = screen.getAllByRole('button', { name: /settings\.lsp\.install/ })
    expect(installBtns.length).toBeGreaterThan(0)
    expect(installBtns[0]).toBeEnabled()
  })

  it('does not flash install button while install states are loading', async () => {
    vi.spyOn(lspClient, 'stateGet').mockResolvedValue(ALL_ENABLED)
    // Never-resolving lspStatus keeps the component in the loading state.
    lspStatus.mockReturnValue(new Promise(() => {}))
    render(<ShellLspSettings />)

    // The rows render (state loaded) but stay neutral: checking badge, no
    // install button, no "not installed" flash.
    await waitFor(() => {
      expect(screen.getAllByText('settings.lsp.status.checking').length).toBeGreaterThan(0)
    })
    expect(screen.queryByRole('button', { name: /settings\.lsp\.install/ })).toBeNull()
    expect(screen.queryByText('settings.lsp.status.notInstalled')).toBeNull()
  })

  it('shows installed version badge and hides install button when language server is installed', async () => {
    vi.spyOn(lspClient, 'stateGet').mockResolvedValue(ALL_ENABLED)
    lspStatus.mockResolvedValue(ALL_INSTALLED)
    render(<ShellLspSettings />)

    await waitFor(() => {
      expect(screen.getAllByText('settings.lsp.status.installed').length).toBe(10)
    })

    expect(screen.queryAllByRole('button', { name: /settings\.lsp\.install/ }).length).toBe(0)
  })

  it('clicking install triggers lspInstall and shows progress percentage', async () => {
    vi.spyOn(lspClient, 'stateGet').mockResolvedValue(ALL_ENABLED)
    lspStatus.mockResolvedValue(NOT_INSTALLED_GO)
    render(<ShellLspSettings />)

    await waitFor(() => {
      expect(screen.getAllByText('settings.lsp.status.notInstalled').length).toBeGreaterThan(0)
    })

    const installBtns = screen.getAllByRole('button', { name: /settings\.lsp\.install/ })
    fireEvent.click(installBtns[0]!)

    // Go is builtin (always installed, no button), so the first install
    // button belongs to typescript.
    await waitFor(() => {
      expect(lspInstall).toHaveBeenCalledWith(expect.anything(), { Language: 'typescript' })
    })

    await emitInstallProgress({ Language: 'typescript', State: 'running', Percent: 42 })

    await waitFor(() => {
      expect(screen.getAllByText('settings.lsp.installing').length).toBeGreaterThan(0)
    })
    expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '42')
  })

  it('install done event refreshes status and badge flips to installed', async () => {
    vi.spyOn(lspClient, 'stateGet').mockResolvedValue(ALL_ENABLED)
    lspStatus
      .mockResolvedValueOnce(NOT_INSTALLED_GO)
      .mockResolvedValueOnce(INSTALLED_GO)
    render(<ShellLspSettings />)

    await waitFor(() => {
      expect(screen.getAllByText('settings.lsp.status.notInstalled').length).toBeGreaterThan(0)
    })

    fireEvent.click(screen.getAllByRole('button', { name: /settings\.lsp\.install/ })[0]!)

    await emitInstallProgress({ Language: 'go', State: 'done', Percent: 100 })

    await waitFor(() => {
      expect(screen.getByText('settings.lsp.status.installed')).toBeInTheDocument()
    })
    expect(lspStatus).toHaveBeenCalledTimes(2)
  })

  it('install failure displays failed badge and clears installing state', async () => {
    vi.spyOn(lspClient, 'stateGet').mockResolvedValue(ALL_ENABLED)
    lspStatus.mockResolvedValue(NOT_INSTALLED_GO)
    render(<ShellLspSettings />)

    await waitFor(() => {
      expect(screen.getAllByText('settings.lsp.status.notInstalled').length).toBeGreaterThan(0)
    })

    // Go is builtin (no button); the first install button belongs to typescript.
    fireEvent.click(screen.getAllByRole('button', { name: /settings\.lsp\.install/ })[0]!)

    await emitInstallProgress({ Language: 'typescript', State: 'failed', Percent: 0, Error: 'network down' })

    await waitFor(() => {
      expect(screen.getByText('settings.lsp.status.installFailed')).toBeInTheDocument()
    })
    expect(screen.getAllByRole('button', { name: /settings\.lsp\.install/ }).length).toBeGreaterThan(0)
  })
})