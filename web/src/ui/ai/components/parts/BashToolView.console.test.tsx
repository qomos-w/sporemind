import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { BashToolView } from './BashToolView'
import type { ToolFrame } from '../../model/frame-types'

// Vitest serves modules over a virtual URL, so resolve the stylesheet from the
// project root (the test runner's cwd) instead of import.meta.url.
const partsCss = readFileSync(resolve(process.cwd(), 'src/ui/ai/components/parts/parts.css'), 'utf8')

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

function makeBashFrame(overrides: Partial<ToolFrame> = {}): ToolFrame {
  return {
    id: 'f-bash-console',
    type: 'tool',
    status: 'completed',
    toolName: 'bash',
    input: JSON.stringify({ Command: 'make', Args: ['build-desktop'] }),
    output: JSON.stringify({ Stdout: 'ok\n', Stderr: 'warn\n', ExitCode: 0 }),
    version: 1,
    exitCode: 0,
    ...overrides,
  } as unknown as ToolFrame
}

let container: HTMLDivElement
let root: Root

beforeEach(() => {
  container = document.createElement('div')
  document.body.appendChild(container)
  root = createRoot(container)
})

afterEach(async () => {
  await act(async () => { root.unmount() })
  container.remove()
  document.documentElement.removeAttribute('data-theme')
})

describe('run-command (bash) v23 console style', () => {
  it('wraps the command block and output regions in the ai-console panel', async () => {
    await act(async () => {
      root.render(<BashToolView frame={makeBashFrame()} />)
    })

    const console = container.querySelector('.ai-console')
    expect(console).toBeTruthy()

    // `$` prompt + scrollable command line.
    const command = container.querySelector('.ai-console .ai-tool-command-block .ai-tool-code')
    expect(command).toBeTruthy()
    expect(command!.textContent).toContain('make build-desktop')

    // stdout + stderr each render inside the console so the CSS tint applies.
    expect(container.querySelector('.ai-console .ai-tool-output-stdout .ai-tool-code')).toBeTruthy()
    const stderr = container.querySelector('.ai-console .ai-tool-output-stderr .ai-tool-code')
    expect(stderr).toBeTruthy()
    expect(stderr!.textContent).toContain('warn')
  })

  it('keeps the console panel across light and dark themes', async () => {
    for (const theme of ['dark', 'light']) {
      await act(async () => {
        document.documentElement.setAttribute('data-theme', theme)
        root.render(<BashToolView frame={makeBashFrame()} />)
      })
      expect(document.documentElement.getAttribute('data-theme')).toBe(theme)
      expect(container.querySelector('.ai-console')).toBeTruthy()
      expect(container.querySelector('.ai-console .ai-tool-command-block .ai-tool-code')).toBeTruthy()
    }
  })
})

describe('v23 console stylesheet contract', () => {
  it('declares the terminal-card tokens for both themes', () => {
    // Dark ground + muted green/gray text.
    expect(partsCss).toContain('--console-bg: #151a17')
    expect(partsCss).toContain('--console-fg: #c9e7ce')
    expect(partsCss).toContain('--console-dim: #5d6f61')
    // Light ground (rice paper) + ink-green text.
    expect(partsCss).toMatch(/\[data-theme="light"\] \.ai-console \{[\s\S]*?#f4f1e8/)
    // Console chrome: 12px radius, no outer rule, mono type, `$` prompt.
    expect(partsCss).toMatch(/\.ai-console \{[\s\S]*?border-radius: 12px/)
    expect(partsCss).toMatch(/\.ai-console \{[\s\S]*?border: none/)
    expect(partsCss).toMatch(/\.ai-console \{[\s\S]*?font-family: var\(--font-mono\)/)
    expect(partsCss).toContain('content: "$ "')
    // Output is scrollable.
    expect(partsCss).toMatch(/\.ai-console \.ai-tool-code \{[\s\S]*?overflow-y: auto/)
  })
})
