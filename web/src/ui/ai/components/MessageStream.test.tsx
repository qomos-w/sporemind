import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import MessageStream, { sameEnvelopeRender } from './MessageStream'
import { AIShellContext } from '../context/AIShellContext'
import type { TurnEnvelope, SessionGoal } from '../model/frame-types'

;(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('../../../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

vi.mock('../../../i18n/provider', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

const { agentSnapshotMock } = vi.hoisted(() => ({ agentSnapshotMock: vi.fn() }))

vi.mock('../hooks/agentInfoStore', () => ({
  useAgentInfoList: () => agentSnapshotMock(),
}))

function makeUserEnvelope(images: { url: string; alt?: string }[]): TurnEnvelope {
  return {
    id: 'env-u',
    role: 'user',
    userContent: 'check this',
    userImages: images,
    frames: [],
    timestamp: new Date().toISOString(),
  }
}

function makeSystemEnvelope(origin: 'goal' | 'workflow', text: string, goal?: SessionGoal): TurnEnvelope {
  return {
    id: `env-${origin}`,
    role: 'user',
    userContent: text,
    frames: [],
    timestamp: new Date().toISOString(),
    systemOrigin: origin,
    goal,
  }
}

describe('MessageStream user images', () => {
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

  const renderStream = async (envelope: TurnEnvelope, contextValue: unknown) => {
    await act(async () => {
      root.render(
        <AIShellContext.Provider value={contextValue as any}>
          <MessageStream
            envelopes={[envelope]}
            streamRef={{ current: null }}
            contentRef={{ current: null }}
            slots={new Map()}
          />
        </AIShellContext.Provider>,
      )
    })
  }

  it('clicking a user bubble image opens it in the right-panel viewer', async () => {
    const onOpenImage = vi.fn()
    await renderStream(
      makeUserEnvelope([{ url: 'data:image/png;base64,AAA', alt: 'screenshot' }]),
      { onOpenImage },
    )

    const img = container.querySelector('.ai-user-image') as HTMLImageElement
    expect(img).not.toBeNull()
    expect(img.getAttribute('src')).toBe('data:image/png;base64,AAA')

    await act(async () => {
      img.click()
    })
    expect(onOpenImage).toHaveBeenCalledTimes(1)
    expect(onOpenImage).toHaveBeenCalledWith('data:image/png;base64,AAA', 'screenshot')
  })

  it('clicking a user image is a no-op without a shell provider', async () => {
    await renderStream(makeUserEnvelope([{ url: 'data:image/png;base64,BBB' }]), null)

    const img = container.querySelector('.ai-user-image') as HTMLImageElement
    expect(img).not.toBeNull()
    await act(async () => {
      img.click()
    })
  })
})

describe('MessageStream system continuation', () => {
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

  const renderStream = async (envelope: TurnEnvelope) => {
    await act(async () => {
      root.render(
        <AIShellContext.Provider value={null}>
          <MessageStream
            envelopes={[envelope]}
            streamRef={{ current: null }}
            contentRef={{ current: null }}
            slots={new Map()}
          />
        </AIShellContext.Provider>,
      )
    })
  }

  it('renders a goal continuation as a collapsed system bubble, not a user bubble', async () => {
    await renderStream(makeSystemEnvelope('goal', "We'll continue working toward the active goal. (turn 3 of 10)"))

    const bubble = container.querySelector('.ai-system-continuation-goal')
    expect(bubble).not.toBeNull()
    expect(container.querySelector('.ai-user-bubble')).toBeNull()
    // Goal continuation shows only the label; no summary text after it.
    expect(container.querySelector('.ai-system-continuation-summary')).toBeNull()
    // Collapsed by default: full original text hidden.
    expect(container.querySelector('.ai-system-continuation-body')).toBeNull()
  })

  it('shows the goal name on a goal continuation when the envelope carries the session goal', async () => {
    const goal: SessionGoal = { Condition: '构建登录页', InterpretedGoal: '实现带会话保持的登录页面', MaxTurns: 10, TurnCount: 4 }
    await renderStream(makeSystemEnvelope('goal', "We'll continue working toward the active goal. (turn 4 of 10)", goal))

    const bubble = container.querySelector('.ai-system-continuation-goal')
    expect(bubble).not.toBeNull()
    expect(container.querySelector('.ai-system-continuation-summary')!.textContent).toBe('实现带会话保持的登录页面')
  })

  it('falls back to Condition when InterpretedGoal is absent on a goal continuation', async () => {
    const goal: SessionGoal = { Condition: '构建登录页', MaxTurns: 10, TurnCount: 5 }
    await renderStream(makeSystemEnvelope('goal', "We'll continue working toward the active goal. (turn 5 of 10)", goal))

    expect(container.querySelector('.ai-system-continuation-summary')!.textContent).toBe('构建登录页')
  })

  it('renders a workflow continuation with extracted frontier summary', async () => {
    await renderStream(makeSystemEnvelope('workflow', 'Map [[m1]] has 3 frontier tickets ready:\n\n- [[c1]]\n\nCheck: ...'))

    const bubble = container.querySelector('.ai-system-continuation-workflow')
    expect(bubble).not.toBeNull()
    expect(container.querySelector('.ai-user-bubble')).toBeNull()
    expect(container.querySelector('.ai-system-continuation-summary')!.textContent).toBe('3 frontier tickets ready')
  })

  it('expands to show the full original text on click', async () => {
    const fullText = "We'll continue: 2 to review, 1 unresponsive."
    await renderStream(makeSystemEnvelope('workflow', fullText))

    expect(container.querySelector('.ai-system-continuation-body')).toBeNull()

    await act(async () => {
      (container.querySelector('.ai-system-continuation-row') as HTMLElement)!.click()
    })

    const body = container.querySelector('.ai-system-continuation-body')
    expect(body).not.toBeNull()
    expect(body!.textContent).toContain(fullText)
  })

  it('keeps plain user envelopes on the original user bubble path', async () => {
    await renderStream(makeUserEnvelope([]))

    expect(container.querySelector('.ai-user-bubble')).not.toBeNull()
    expect(container.querySelector('.ai-system-continuation')).toBeNull()
  })

  it('keeps isInject envelopes on the original TimelineStep path', async () => {
    await renderStream({
      id: 'env-inject',
      role: 'user',
      userContent: 'injected message',
      frames: [],
      timestamp: new Date().toISOString(),
      isInject: true,
    })

    expect(container.querySelector('.ai-user-bubble')).toBeNull()
    expect(container.querySelector('.ai-system-continuation')).toBeNull()
    expect(container.querySelector('.ai-step-label')!.textContent).toBe('ai.turn.injected')
  })
})

describe('MessageStream peer sender avatars', () => {
  let container: HTMLDivElement
  let root: Root

  const emptySnapshot = { items: [], byActorId: new Map(), byId: new Map() }

  beforeEach(() => {
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
    agentSnapshotMock.mockReturnValue(emptySnapshot)
  })

  afterEach(async () => {
    await act(async () => {
      root.unmount()
    })
    container.remove()
    agentSnapshotMock.mockReset()
  })

  const renderStream = async (envelope: TurnEnvelope) => {
    await act(async () => {
      root.render(
        <AIShellContext.Provider value={null}>
          <MessageStream
            envelopes={[envelope]}
            streamRef={{ current: null }}
            contentRef={{ current: null }}
            slots={new Map()}
          />
        </AIShellContext.Provider>,
      )
    })
  }

  it('renders no avatar on the operator own user bubble', async () => {
    await renderStream(makeUserEnvelope([]))

    expect(container.querySelector('.ai-user-turn > .sender-avatar')).toBeNull()
    expect(container.querySelector('.sender-avatar')).toBeNull()
    expect(container.querySelector('.ai-user-bubble')).not.toBeNull()
  })

  it('renders a peer avatar left of an injected message, labelled with the sender name', async () => {
    await renderStream({
      id: 'env-inject-peer',
      role: 'user',
      userContent: 'peer says hi',
      frames: [],
      timestamp: new Date().toISOString(),
      isInject: true,
      peerSender: { kind: 'agent', id: 'Coder#0001', name: 'Bob the Builder' },
    })

    const row = container.querySelector('.ai-inject-peer-row')
    expect(row).not.toBeNull()
    const avatar = row!.querySelector('.sender-avatar') as HTMLElement
    expect(avatar).not.toBeNull()
    expect(avatar.classList.contains('sender-avatar-jump')).toBe(false)
    expect(container.querySelector('.ai-step-label')!.textContent).toBe('Bob the Builder')
  })

  it('clicking a resolvable agent sender avatar dispatches open-agent-chat', async () => {
    const info = {
      Id: 'Coder#0001', ActorId: 'actor-1', DisplayName: 'Bob the Builder', AgentKind: 'Coder',
      ProjectId: 'proj-1', Status: 'idle',
    }
    agentSnapshotMock.mockReturnValue({
      items: [info],
      byActorId: new Map([['actor-1', info]]),
      byId: new Map([['Coder#0001', info]]),
    })

    await renderStream({
      id: 'env-inject-peer',
      role: 'user',
      userContent: 'peer says hi',
      frames: [],
      timestamp: new Date().toISOString(),
      isInject: true,
      peerSender: { kind: 'agent', id: 'Coder#0001', name: 'Bob the Builder' },
    })

    const events: { projectId: string; agentActorId: string }[] = []
    const handler = (e: Event) => {
      events.push((e as CustomEvent<{ projectId: string; agentActorId: string }>).detail)
    }
    window.addEventListener('sporemind:open-agent-chat', handler)

    const avatar = container.querySelector('.ai-inject-peer-row .sender-avatar-jump') as HTMLElement
    expect(avatar).not.toBeNull()
    await act(async () => {
      avatar.click()
    })

    window.removeEventListener('sporemind:open-agent-chat', handler)
    expect(events).toHaveLength(1)
    expect(events[0]).toEqual({ projectId: 'proj-1', agentActorId: 'actor-1' })
  })

  it('renders a human-inject sender avatar above a non-inject user bubble', async () => {
    await renderStream({
      id: 'env-user-peer',
      role: 'user',
      userContent: 'from alice',
      frames: [],
      timestamp: new Date().toISOString(),
      peerSender: { kind: 'user', id: 'alice', name: 'Alice' },
    })

    const avatar = container.querySelector('.ai-user-turn > .sender-avatar') as HTMLElement
    expect(avatar).not.toBeNull()
    expect(avatar.getAttribute('title')).toBe('Alice')
    expect(avatar.classList.contains('sender-avatar-jump')).toBe(false)
  })
})

describe('sameEnvelopeRender (memo comparator)', () => {
  const frameA = { id: 'f1', type: 'text', status: 'completed', content: 'hello' }
  const frameB = { id: 'f2', type: 'text', status: 'completed', content: 'world' }

  function makeEnvelope(overrides: Record<string, unknown> = {}): TurnEnvelope {
    return {
      id: 'env-a',
      role: 'assistant',
      frames: [frameA, frameB],
      timestamp: '2026-09-04T00:00:00Z',
      completed: true,
      metadata: { turnId: 't1', turnState: 'completed', usage: { inputTokens: 1, outputTokens: 2, totalTokens: 3 } },
      ...overrides,
    } as TurnEnvelope
  }

  it('treats a rebuilt envelope with identical frame refs and scalars as unchanged', () => {
    // The projection rebuilds every envelope object on each notify; frame
    // elements stay referentially stable for closed turns. The comparator
    // must return true here or React.memo is defeated (full-timeline
    // re-render per streaming flush).
    const a = makeEnvelope()
    const b = makeEnvelope({
      metadata: { turnId: 't1', turnState: 'completed', usage: { inputTokens: 1, outputTokens: 2, totalTokens: 3 } },
    })
    expect(b).not.toBe(a)
    expect(b.frames).not.toBe(a.frames)
    expect(sameEnvelopeRender(a, b)).toBe(true)
  })

  it('detects a replaced frame object', () => {
    const a = makeEnvelope()
    const b = makeEnvelope({ frames: [frameA, { ...frameB, content: 'changed' }] })
    expect(sameEnvelopeRender(a, b)).toBe(false)
  })

  it('detects an appended frame', () => {
    const a = makeEnvelope()
    const b = makeEnvelope({ frames: [frameA, frameB, { id: 'f3', type: 'text', status: 'completed', content: '!' }] })
    expect(sameEnvelopeRender(a, b)).toBe(false)
  })

  it('detects metadata changes that drive rendering', () => {
    const a = makeEnvelope()
    expect(sameEnvelopeRender(a, makeEnvelope({ metadata: { turnId: 't1', turnState: 'failed' } }))).toBe(false)
    expect(sameEnvelopeRender(a, makeEnvelope({ metadata: { turnId: 't1', turnState: 'completed', usage: { inputTokens: 1, outputTokens: 2, totalTokens: 9 } } }))).toBe(false)
    expect(sameEnvelopeRender(a, makeEnvelope({ metadata: { turnId: 't1', turnState: 'completed', retryCount: 2 } }))).toBe(false)
    expect(sameEnvelopeRender(a, makeEnvelope({ completed: false }))).toBe(false)
  })
})
