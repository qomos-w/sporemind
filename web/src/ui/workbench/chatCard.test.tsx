import { describe, it, expect, vi, afterEach } from 'vitest'
import type { ReactNode } from 'react'
import { createRoot } from 'react-dom/client'
import { act } from 'react'

import { I18nProvider } from '../../i18n'
import { ChatCardPanel, chatActorId, chatCardId, chatStatusText, createChatCardDescriptor, createChatCardDescriptors, formatRelativeTime } from './chatCard'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

function renderPanel(node: ReactNode) {
  const container = document.createElement('div')
  document.body.appendChild(container)
  const root = createRoot(container)
  act(() => {
    root.render(<I18nProvider initialLocale="en-US">{node}</I18nProvider>)
  })
  return { root, container }
}

afterEach(() => {
  document.body.innerHTML = ''
})

describe('chatCardId / chatActorId', () => {
  it('round-trips an actor id through the chat card id', () => {
    expect(chatCardId('actor-1')).toBe('agent:actor-1')
    expect(chatActorId('agent:actor-1')).toBe('actor-1')
    expect(chatActorId('app:com.example')).toBe('')
  })
})

describe('chatStatusText', () => {
  it('joins the recent turn status and the session title', () => {
    expect(chatStatusText({ name: 'Ada', status: 'running', sessionTitle: 'Fix the build' })).toBe(
      'running · Fix the build',
    )
  })

  it('omits the title when it equals the agent name and falls back to empty', () => {
    expect(chatStatusText({ name: 'Ada', status: 'idle', sessionTitle: 'Ada' })).toBe('idle')
    expect(chatStatusText({ name: 'Ada' })).toBe('')
  })
})

describe('formatRelativeTime', () => {
  const now = Date.parse('2026-09-14T12:00:00Z')
  it('formats minutes / hours / days and blanks sub-minute / invalid input', () => {
    expect(formatRelativeTime('2026-09-14T11:30:00Z', now)).toBe('30m')
    expect(formatRelativeTime('2026-09-14T09:00:00Z', now)).toBe('3h')
    expect(formatRelativeTime('2026-09-13T12:00:00Z', now)).toBe('1d')
    expect(formatRelativeTime('2026-09-14T11:59:30Z', now)).toBe('')
    expect(formatRelativeTime('', now)).toBe('')
    expect(formatRelativeTime('not-a-date', now)).toBe('')
  })
})

describe('createChatCardDescriptor', () => {
  it('builds a chat-kind descriptor with a status summary', () => {
    const descriptor = createChatCardDescriptor({ actorId: 'actor-1', title: 'Ada', status: 'running' })
    expect(descriptor.id).toBe('agent:actor-1')
    expect(descriptor.kind).toBe('chat')
    expect(descriptor.title).toBe('Ada')
    expect(descriptor.compactMeta.statusText).toBe('running')
  })

  it('never renders a body compact, mounts the panel when expanded', () => {
    const descriptor = createChatCardDescriptor({ actorId: 'actor-1', title: 'Ada' })
    expect(descriptor.render(false)).toBeNull()
    const element = descriptor.render(true) as { type: unknown; props: Record<string, unknown> }
    expect(element.type).toBe(ChatCardPanel)
    expect(element.props).toMatchObject({ actorId: 'actor-1', title: 'Ada' })
  })

  it('routes the open entry to the injected handler', () => {
    const onOpenConversation = vi.fn()
    const { container } = renderPanel(
      <ChatCardPanel
        actorId="actor-1"
        projectId="proj-1"
        title="Ada"
        status="running"
        sessionTitle="Fix the build"
        lastActivity={new Date().toISOString()}
        onOpenConversation={onOpenConversation}
      />,
    )
    expect(container.querySelector('.wb-chat-status')?.textContent).toBe('running')
    expect(container.querySelector('.wb-chat-session')?.textContent).toBe('Fix the build')

    act(() => {
      container.querySelector<HTMLButtonElement>('.wb-chat-open')!.click()
    })
    expect(onOpenConversation).toHaveBeenCalledWith({ actorId: 'actor-1', projectId: 'proj-1' })
  })

  it('defaults the jump to the sporemind:open-agent-chat shell contract', () => {
    const handler = vi.fn()
    window.addEventListener('sporemind:open-agent-chat', handler)
    const { container } = renderPanel(
      <ChatCardPanel actorId="actor-1" projectId="proj-1" title="Ada" />,
    )
    act(() => {
      container.querySelector<HTMLButtonElement>('.wb-chat-open')!.click()
    })
    window.removeEventListener('sporemind:open-agent-chat', handler)
    expect(handler).toHaveBeenCalledTimes(1)
    const detail = (handler.mock.calls[0]![0] as CustomEvent).detail
    expect(detail).toEqual({ projectId: 'proj-1', agentActorId: 'actor-1' })
  })
})

describe('createChatCardDescriptors', () => {
  const chatState = (id: string, title = 'Agent') => ({
    Id: id,
    Kind: 'chat',
    Title: title,
    Icon: '',
    Score: 5,
    Slot: 'side',
    Pinned: false,
  })

  it('maps projected chat cards joined with the agent session summary', () => {
    const sessions = new Map([
      ['actor-1', { name: 'Ada', projectId: 'proj-1', status: 'running', title: 'Fix the build' }],
    ])
    const descriptors = createChatCardDescriptors(
      [chatState('agent:actor-1'), chatState('agent:actor-2'), { ...chatState('app:x'), Kind: 'app' }] as never,
      sessions,
    )
    expect(descriptors.map(d => d.id)).toEqual(['agent:actor-1', 'agent:actor-2'])

    expect(descriptors[0]!.title).toBe('Ada')
    expect(descriptors[0]!.compactMeta.statusText).toBe('running · Fix the build')

    // Unknown session falls back to the actor's projected title.
    expect(descriptors[1]!.title).toBe('Agent')
  })
})
