import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { ShellEnvSettings } from './ShellEnvSettings'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

const hoisted = vi.hoisted(() => ({
  t: vi.fn((key: string) => key),
  shellEnvProbe: vi.fn(),
  shellPrefSave: vi.fn(),
}))

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: hoisted.t }),
}))

vi.mock('../../../application/generated-client', () => ({
  client: {},
}))

vi.mock('../../../gen-clients/workspace/client', () => ({
  shellEnvProbe: hoisted.shellEnvProbe,
  shellPrefSave: hoisted.shellPrefSave,
}))

const WINDOWS_PROBE = {
  Current: { Kind: 'gitbash', Executable: 'C:\\Program Files\\Git\\bin\\bash.exe', Available: true },
  Candidates: [
    { Kind: 'powershell5', Executable: 'C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe', Available: true },
    { Kind: 'powershell7', Executable: 'C:\\Program Files\\PowerShell\\7\\pwsh.exe', Available: true },
    { Kind: 'bash', Executable: 'C:\\Program Files\\Git\\bin\\bash.exe', Available: true },
    { Kind: 'gitbash', Executable: 'C:\\Program Files\\Git\\bin\\bash.exe', Available: true },
  ],
  Platform: 'windows',
}

describe('ShellEnvSettings', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    hoisted.shellEnvProbe.mockResolvedValue(WINDOWS_PROBE)
    hoisted.shellPrefSave.mockResolvedValue({
      Kind: 'powershell7',
      Current: { Kind: 'powershell7', Executable: 'C:\\Program Files\\PowerShell\\7\\pwsh.exe', Available: true },
    })
  })

  it('renders all four shell candidates on windows', async () => {
    await act(async () => {
      root.render(<ShellEnvSettings />)
    })
    // Wait for the probe to resolve and re-render.
    await act(async () => {
      await new Promise(r => setTimeout(r, 0))
    })
    const html = container.innerHTML
    expect(html).toContain('powershell5')
    expect(html).toContain('powershell7')
    expect(html).toContain('bash')
    expect(html).toContain('gitbash')
    expect(hoisted.shellEnvProbe).toHaveBeenCalled()
  })

  it('calls shellPrefSave when a candidate is selected', async () => {
    await act(async () => {
      root.render(<ShellEnvSettings />)
    })
    await act(async () => {
      await new Promise(r => setTimeout(r, 0))
    })
    const options = container.querySelectorAll('[role="radio"]')
    // options[0] = auto, [1] = powershell5, [2] = powershell7, [3] = bash, [4] = gitbash
    const ps7Option = options[2] as HTMLButtonElement
    await act(async () => {
      ps7Option.click()
      await new Promise(r => setTimeout(r, 0))
    })
    expect(hoisted.shellPrefSave).toHaveBeenCalledWith(expect.anything(), { RequestId: '', Kind: 'powershell7' })
  })
})
