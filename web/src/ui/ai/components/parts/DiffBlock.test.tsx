import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { DiffBlock } from './DiffBlock'

;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

// A minimal Go diff with added/removed lines and a hunk header.
const goDiff = [
  '--- a/main.go',
  '+++ b/main.go',
  '@@ -1,1 +1,1 @@',
  '-func old() string { return "x" }',
  '+func main() string { return "y" }',
].join('\n')

describe('DiffBlock', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  const render = async (props: Parameters<typeof DiffBlock>[0]): Promise<void> => {
    await act(async () => {
      root.render(<DiffBlock {...props} />)
    })
  }

  it('applies syntax highlighting when a known filePath is provided', async () => {
    await render({ diffContent: goDiff, filePath: 'main.go' })
    expect(container.querySelector('.hljs-keyword')).not.toBeNull()
    expect(container.querySelector('.hljs-string')).not.toBeNull()
  })

  it('renders plain text (no hljs spans) for unknown extensions', async () => {
    await render({ diffContent: goDiff, filePath: 'notes.unknownext' })
    expect(container.querySelector('.hljs-keyword')).toBeNull()
    // line text is still present as plain text
    expect(container.textContent).toContain('func main()')
  })

  it('renders plain text when no filePath is given', async () => {
    await render({ diffContent: goDiff })
    expect(container.querySelector('.hljs-keyword')).toBeNull()
  })

  it('keeps add/remove line background classes alongside highlighting', async () => {
    await render({ diffContent: goDiff, filePath: 'main.go' })
    const added = container.querySelector('.ai-tool-diff-line.added')
    const removed = container.querySelector('.ai-tool-diff-line.removed')
    expect(added).not.toBeNull()
    expect(removed).not.toBeNull()
    // highlighted code lives inside a .ai-tool-diff-code span on those lines
    expect(added!.querySelector('.ai-tool-diff-code .hljs-keyword')).not.toBeNull()
  })

  it('keeps the +/- sign marker outside the highlighted code span', async () => {
    await render({ diffContent: goDiff, filePath: 'main.go' })
    const signs = container.querySelectorAll('.ai-tool-diff-sign')
    expect(signs.length).toBeGreaterThanOrEqual(2)
    expect(['+', '-']).toContain(signs[0]!.textContent)
  })

  it('leaves hunk header and file headers unhighlighted', async () => {
    await render({ diffContent: goDiff, filePath: 'main.go' })
    const hunk = container.querySelector('.ai-tool-diff-line.hunk')
    expect(hunk).not.toBeNull()
    expect(hunk!.querySelector('.ai-tool-diff-code')).toBeNull()
  })
})
