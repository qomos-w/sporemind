import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import { AIStepText } from './AIStepText'

const hoisted = vi.hoisted(() => ({
  t: vi.fn((key: string) => key),
}))

vi.mock('../../../../i18n', () => ({
  useI18n: () => ({ t: hoisted.t }),
}))

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

describe('AIStepText', () => {
  let container: HTMLDivElement
  let root: Root

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    vi.clearAllMocks()
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
  })

  const renderText = async (text: string, className?: string) => {
    await act(async () => {
      root.render(<AIStepText className={className}>{text}</AIStepText>)
    })
  }

  it('renders plain text unchanged', async () => {
    await renderText('plain text content')
    expect(container.textContent).toContain('plain text content')
  })

  it('renders markdown formatting', async () => {
    await renderText('**bold** and *italic*')
    const strong = container.querySelector('strong')
    const em = container.querySelector('em')
    expect(strong).not.toBeNull()
    expect(strong!.textContent).toBe('bold')
    expect(em).not.toBeNull()
    expect(em!.textContent).toBe('italic')
  })

  it('renders wikiword links for [[CardID]] syntax', async () => {
    await renderText('See [[CardID]] for details.')
    const link = container.querySelector('a.wiki-word-link')
    expect(link).not.toBeNull()
    expect(link!.textContent).toBe('CardID')
  })

  it('renders auto-detected CamelCase wikiword links', async () => {
    await renderText('Open ProjectSummary to continue.')
    const link = container.querySelector('a.wiki-word-link')
    expect(link).not.toBeNull()
    expect(link!.textContent).toBe('ProjectSummary')
  })

  it('applies custom className to the wrapper', async () => {
    await renderText('content', 'custom-class')
    const wrapper = container.querySelector('.ai-step-text.custom-class')
    expect(wrapper).not.toBeNull()
  })
})
