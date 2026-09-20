import { describe, it, expect } from 'vitest'
import { stepsToEnvelopes, mergeStepEnvelopes, dedupeEnvelopes, parseSenderMeta } from './steps-to-envelopes'
import type { Step } from '../../../gen-types/aigen'
import type { LocalInteractionResponse, PlanFrame, GoalSubmitFrame, Frame, ErrorFrame } from '../model/frame-types'

describe('parseSenderMeta', () => {
  it('parses agent/user sender metas into peerSender identity', () => {
    expect(parseSenderMeta('agent|Coder#0001|Bob the Builder')).toEqual({ kind: 'agent', id: 'Coder#0001', name: 'Bob the Builder' })
    expect(parseSenderMeta('agent||Bob the Builder')).toEqual({ kind: 'agent', id: '', name: 'Bob the Builder' })
    expect(parseSenderMeta('user|alice|Alice')).toEqual({ kind: 'user', id: 'alice', name: 'Alice' })
  })

  it('rejects bare markers and unrelated metas', () => {
    expect(parseSenderMeta('agent')).toBeUndefined()
    expect(parseSenderMeta('user')).toBeUndefined()
    expect(parseSenderMeta('agent||')).toBeUndefined()
    expect(parseSenderMeta('goal')).toBeUndefined()
    expect(parseSenderMeta('workflow')).toBeUndefined()
    expect(parseSenderMeta(undefined)).toBeUndefined()
  })
})

describe('stepsToEnvelopes sender meta', () => {
  const mkStep = (meta?: string): Step[] => [{
    Id: 'u1', Role: 'user', Type: 'text',
    Content: [{ Type: 'text', Text: 'hi' }],
    Meta: meta,
    Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1',
  }]

  it('keeps peerSender for agent peers and distinct-id human injects', () => {
    const agent = stepsToEnvelopes(mkStep('agent|Coder#0001|Bob the Builder'))
    expect(agent[0]!.peerSender).toEqual({ kind: 'agent', id: 'Coder#0001', name: 'Bob the Builder' })
    const human = stepsToEnvelopes(mkStep('user|alice|Alice'))
    expect(human[0]!.peerSender).toEqual({ kind: 'user', id: 'alice', name: 'Alice' })
  })

  it('drops the legacy self-stamp (user|<subject>|<subject>) — own messages carry no avatar', () => {
    const self = stepsToEnvelopes(mkStep('user|admin|admin'))
    expect(self[0]!.peerSender).toBeUndefined()
  })

  it('operator own messages (bare user meta) carry no peerSender', () => {
    const own = stepsToEnvelopes(mkStep('user'))
    expect(own[0]!.peerSender).toBeUndefined()
  })
})

describe('stepsToEnvelopes', () => {
  it('converts a user text step to a user envelope', () => {
    const steps: Step[] = [{
      Id: 'u1', Role: 'user', Type: 'text',
      Content: [{ Type: 'text', Text: 'Hello' }],
      Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1',
    }]
    const result = stepsToEnvelopes(steps)
    expect(result).toHaveLength(1)
    expect(result[0]).toMatchObject({
      id: 'u1',
      role: 'user',
      userContent: 'Hello',
      completed: true,
    })
    expect(result[0]!.frames[0]!).toMatchObject({
      type: 'text', status: 'completed', content: 'Hello',
    })
  })

  it('derives systemOrigin=goal from Meta="goal"', () => {
    const steps: Step[] = [{
      Id: 'u1', Role: 'user', Type: 'text',
      Content: [{ Type: 'text', Text: "We'll continue working toward the active goal. (turn 3 of 10)" }],
      Meta: 'goal',
      Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1',
    }]
    const result = stepsToEnvelopes(steps)
    expect(result).toHaveLength(1)
    expect(result[0]).toMatchObject({ role: 'user', systemOrigin: 'goal' })
  })

  it('derives systemOrigin=workflow from Meta="workflow"', () => {
    const steps: Step[] = [{
      Id: 'u1', Role: 'user', Type: 'text',
      Content: [{ Type: 'text', Text: 'Map [[m1]] has 3 frontier tickets ready:' }],
      Meta: 'workflow',
      Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1',
    }]
    const result = stepsToEnvelopes(steps)
    expect(result).toHaveLength(1)
    expect(result[0]).toMatchObject({ role: 'user', systemOrigin: 'workflow' })
  })

  it('keeps systemOrigin undefined for Meta="user" and Meta="user|<id>|<name>"', () => {
    const steps: Step[] = [
      {
        Id: 'u1', Role: 'user', Type: 'text',
        Content: [{ Type: 'text', Text: 'plain human message' }],
        Meta: 'user',
        Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1',
      },
      {
        Id: 'u2', Role: 'user', Type: 'text',
        Content: [{ Type: 'text', Text: 'named human message' }],
        Meta: 'user|alice|Alice',
        Closed: true, Timestamp: '2024-01-01T00:00:01Z', TurnId: 't2',
      },
      {
        Id: 'u3', Role: 'user', Type: 'text',
        Content: [{ Type: 'text', Text: 'no meta message' }],
        Closed: true, Timestamp: '2024-01-01T00:00:02Z', TurnId: 't3',
      },
    ]
    const result = stepsToEnvelopes(steps)
    expect(result).toHaveLength(3)
    for (const env of result) {
      expect(env.systemOrigin).toBeUndefined()
    }
  })

  it('converts a pending user_inject step to an assistant envelope with an inject frame', () => {
    const steps: Step[] = [{
      Id: 'ps-1', Role: 'assistant', Type: 'user_inject',
      Content: [{ Type: 'text', Text: 'queued message' }],
      Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1',
    }]
    const result = stepsToEnvelopes(steps)
    expect(result).toHaveLength(1)
    expect(result[0]).toMatchObject({
      id: 't1',
      role: 'assistant',
      completed: true,
    })
    expect(result[0]!.frames[0]).toMatchObject({
      type: 'user_inject',
      status: 'completed',
      items: [{ id: 'ps-1', text: 'queued message' }],
    })
  })

  it('converts an assistant text step to an assistant envelope', () => {
    const steps: Step[] = [{
      Id: 'a1', Role: 'assistant', Type: 'text',
      Content: [{ Type: 'text', Text: 'Hi there' }],
      Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1',
    }]
    const result = stepsToEnvelopes(steps)
    expect(result).toHaveLength(1)
    expect(result[0]).toMatchObject({
      role: 'assistant',
      completed: true,
      metadata: { turnId: 't1' },
    })
    expect(result[0]!.frames[0]!).toMatchObject({
      type: 'text', status: 'completed', content: 'Hi there',
    })
  })

  it('keeps turn_start as a placeholder assistant envelope', () => {
    const steps: Step[] = [{
      Id: 'turn-start-1', Role: 'assistant', Type: 'turn_start',
      Content: [],
      Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1',
    }]
    const result = stepsToEnvelopes(steps)
    expect(result).toHaveLength(1)
    expect(result[0]).toMatchObject({
      role: 'assistant',
      completed: false,
      metadata: { turnId: 't1' },
    })
    expect(result[0]!.frames[0]).toMatchObject({
      type: 'text', status: 'running', content: '',
    })
  })

  it('marks running assistant step as not completed', () => {
    const steps: Step[] = [{
      Id: 'a1', Role: 'assistant', Type: 'text',
      Content: [{ Type: 'text', Text: 'Partial' }],
      Closed: false, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1',
    }]
    const result = stepsToEnvelopes(steps)
    expect(result[0]!.completed).toBe(false)
    expect(result[0]!.frames[0]!.status).toBe('running')
  })

  it('merges consecutive assistant steps into one envelope', () => {
    const steps: Step[] = [
      {
        Id: 'u1', Role: 'user', Type: 'text',
        Content: [{ Type: 'text', Text: 'List files' }],
        Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1',
      },
      {
        Id: 'llm-1', Role: 'assistant', Type: 'text',
        Content: [{ Type: 'text', Text: 'Let me check' }],
        Closed: true, Timestamp: '2024-01-01T00:00:01Z', TurnId: 't1',
      },
      {
        Id: 'tool-1', Role: 'assistant', Type: 'tool_call',
        Content: [
          { Type: 'tool_use', ToolName: 'project.list_json', Input: '{"path":"/"}' },
          { Type: 'tool_result', Text: 'bin etc home' },
        ],
        Closed: true, Timestamp: '2024-01-01T00:00:02Z', TurnId: 't1',
      },
      {
        Id: 'llm-2', Role: 'assistant', Type: 'text',
        Content: [{ Type: 'text', Text: 'Here are the files' }],
        Closed: true, Timestamp: '2024-01-01T00:00:03Z', TurnId: 't1',
      },
    ]
    const result = stepsToEnvelopes(steps)
    expect(result).toHaveLength(2) // 1 user + 1 assistant

    const asst = result.find(e => e.role === 'assistant')
    expect(asst).toBeDefined()
    expect(asst!.frames).toHaveLength(3)
    expect(asst!.frames[0]).toMatchObject({ type: 'text', content: 'Let me check' })
    expect(asst!.frames[1]).toMatchObject({ type: 'tool', toolName: 'project.list' })
    expect(asst!.frames[2]).toMatchObject({ type: 'text', content: 'Here are the files' })
    expect(asst!.completed).toBe(true)
  })

  it('preserves chronological order across turns', () => {
    const steps: Step[] = [
      {
        Id: 'u1', Role: 'user', Type: 'text',
        Content: [{ Type: 'text', Text: 'Q1' }],
        Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1',
      },
      {
        Id: 'a1', Role: 'assistant', Type: 'text',
        Content: [{ Type: 'text', Text: 'A1' }],
        Closed: true, Timestamp: '2024-01-01T00:00:01Z', TurnId: 't1',
      },
      {
        Id: 'u2', Role: 'user', Type: 'text',
        Content: [{ Type: 'text', Text: 'Q2' }],
        Closed: true, Timestamp: '2024-01-01T00:00:02Z', TurnId: 't2',
      },
      {
        Id: 'a2', Role: 'assistant', Type: 'text',
        Content: [{ Type: 'text', Text: 'A2' }],
        Closed: true, Timestamp: '2024-01-01T00:00:03Z', TurnId: 't2',
      },
    ]
    const result = stepsToEnvelopes(steps)
    expect(result).toHaveLength(4)
    expect(result.map(e => e.role)).toEqual(['user', 'assistant', 'user', 'assistant'])
  })

  it('handles reasoning step', () => {
    const steps: Step[] = [{
      Id: 'r1', Role: 'assistant', Type: 'reasoning',
      Content: [],
      ReasoningContent: 'Analyzing...',
      Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1',
    }]
    const result = stepsToEnvelopes(steps)
    expect(result[0]!.frames[0]!).toMatchObject({
      type: 'reasoning', content: 'Analyzing...',
    })
  })

  it('handles empty content gracefully', () => {
    const steps: Step[] = [{
      Id: 'a1', Role: 'assistant', Type: 'text',
      Content: [],
      Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1',
    }]
    const result = stepsToEnvelopes(steps)
    expect(result[0]!.frames[0]!).toMatchObject({
      type: 'text', content: '',
    })
  })

  it('handles steps without TurnId, preserving order', () => {
    const steps: Step[] = [
      {
        Id: 'u1', Role: 'user', Type: 'text',
        Content: [{ Type: 'text', Text: 'Hello' }],
        Closed: true, Timestamp: '2024-01-01T00:00:00Z',
      },
      {
        Id: 'a1', Role: 'assistant', Type: 'text',
        Content: [{ Type: 'text', Text: 'Hi' }],
        Closed: false, Timestamp: '2024-01-01T00:00:01Z',
      },
    ]
    const result = stepsToEnvelopes(steps)
    expect(result).toHaveLength(2)
    expect(result[0]!.role).toBe('user')
    expect(result[1]!.role).toBe('assistant')
    expect(result[1]!.metadata?.turnId).toBe('a1') // falls back to step Id
  })
  it('carries Step.Seq through to user and assistant envelopes', () => {
    const steps: Step[] = [
      {
        Id: 'u1', Role: 'user', Type: 'text',
        Content: [{ Type: 'text', Text: 'Hello' }],
        Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1', Seq: 10,
      },
      {
        Id: 'a1', Role: 'assistant', Type: 'text',
        Content: [{ Type: 'text', Text: 'Hi' }],
        Closed: true, Timestamp: '2024-01-01T00:00:01Z', TurnId: 't1', Seq: 11,
      },
    ]
    const result = stepsToEnvelopes(steps)
    expect(result[0]!.seq).toBe(10)
    expect(result[1]!.seq).toBe(11)
  })

  it('sorts out-of-order steps by Seq before grouping', () => {
    const steps: Step[] = [
      {
        Id: 'a1', Role: 'assistant', Type: 'text',
        Content: [{ Type: 'text', Text: 'A1' }],
        Closed: true, Timestamp: '2024-01-01T00:00:04Z', TurnId: 't1', Seq: 3,
      },
      {
        Id: 'u2', Role: 'user', Type: 'text',
        Content: [{ Type: 'text', Text: 'Q2' }],
        Closed: true, Timestamp: '2024-01-01T00:00:02Z', TurnId: 't2', Seq: 4,
      },
      {
        Id: 'u1', Role: 'user', Type: 'text',
        Content: [{ Type: 'text', Text: 'Q1' }],
        Closed: true, Timestamp: '2024-01-01T00:00:01Z', TurnId: 't1', Seq: 1,
      },
      {
        Id: 'a2', Role: 'assistant', Type: 'text',
        Content: [{ Type: 'text', Text: 'A2' }],
        Closed: true, Timestamp: '2024-01-01T00:00:05Z', TurnId: 't2', Seq: 5,
      },
      {
        Id: 'tool-1', Role: 'assistant', Type: 'tool_call',
        Content: [
          { Type: 'tool_use', ToolName: 'project.list_json', Input: '{}' },
          { Type: 'tool_result', Text: 'bin etc' },
        ],
        Closed: true, Timestamp: '2024-01-01T00:00:03Z', TurnId: 't1', Seq: 2,
      },
    ]
    const result = stepsToEnvelopes(steps)
    expect(result.map(e => e.role)).toEqual(['user', 'assistant', 'user', 'assistant'])
    const asst1 = result.find(e => e.role === 'assistant' && e.metadata?.turnId === 't1')
    expect(asst1?.frames).toHaveLength(2)
    expect(asst1?.frames[0]).toMatchObject({ type: 'tool' })
    expect(asst1?.frames[1]).toMatchObject({ type: 'text', content: 'A1' })
  })

  it('renders ask_user tool_call as a single ask_user frame', () => {
    const askInput = JSON.stringify({
      questions: [{ header: 'File', question: 'Which file?', options: ['a.ts', 'b.ts'], multiSelect: false }],
    })
    const steps: Step[] = [{
      Id: 'tool-ask', Role: 'assistant', Type: 'tool_call',
      Content: [{ Type: 'tool_use', ToolName: 'ask_user', ToolUseId: 'ask-req-1', Input: askInput }],
      Closed: false, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1', Seq: 1,
    }]
    const result = stepsToEnvelopes(steps)
    expect(result).toHaveLength(1)
    const frame = result[0]!.frames[0] as import('../model/frame-types').AskUserQuestionFrame
    expect(frame.type).toBe('ask_user')
    expect(frame.requestId).toBe('ask-req-1')
    expect(frame.questions).toHaveLength(1)
    expect(frame.questions[0]).toMatchObject({ header: 'File', question: 'Which file?' })
  })

  it('preserves the recommended flag on ask_user options', () => {
    const askInput = JSON.stringify({
      questions: [{
        header: 'File', question: 'Which file?', multiSelect: false,
        options: [
          { label: 'a.ts', recommended: true },
          { label: 'b.ts' },
        ],
      }],
    })
    const steps: Step[] = [{
      Id: 'tool-ask', Role: 'assistant', Type: 'tool_call',
      Content: [{ Type: 'tool_use', ToolName: 'ask_user', ToolUseId: 'ask-req-1', Input: askInput }],
      Closed: false, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1', Seq: 1,
    }]
    const result = stepsToEnvelopes(steps)
    const frame = result[0]!.frames[0] as import('../model/frame-types').AskUserQuestionFrame
    expect(frame.questions[0]!.options).toEqual([
      { label: 'a.ts', description: undefined, recommended: true },
      { label: 'b.ts', description: undefined, recommended: undefined },
    ])
  })

  it('parses permission step payload as {toolCalls, reason} object from emitPermissionEvent', () => {
    // The step reducer stringifies the backend interaction Task payload:
    // emitPermissionEvent sends {"toolCalls":[...],"reason":"..."}. The
    // permission frame must expose the toolCalls list and the reason, or
    // RegistrationPermissionOverlay never opens.
    const steps: Step[] = [{
      Id: 'perm1', Role: 'assistant', Type: 'permission',
      Content: [{
        Type: 'text',
        Text: JSON.stringify({
          toolCalls: [{ id: 'c1', callableId: 'appmanager.register_project', input: '{"AppDir":"pkg/demo"}' }],
          reason: 'app registration requires explicit user authorization',
        }),
      }],
      Closed: false, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1', RequestId: 'req1',
    }]
    const result = stepsToEnvelopes(steps)
    expect(result).toHaveLength(1)
    const frame: any = result[0]!.frames[0]
    expect(frame.type).toBe('permission_request')
    expect(frame.requestId).toBe('req1')
    expect(frame.allowed).toBeUndefined()
    expect(frame.toolCalls).toEqual([
      { id: 'c1', callableId: 'appmanager.register_project', input: '{"AppDir":"pkg/demo"}' },
    ])
    expect(frame.reason).toBe('app registration requires explicit user authorization')
  })

  it('parses legacy bare-array permission step payload', () => {
    const steps: Step[] = [{
      Id: 'perm2', Role: 'assistant', Type: 'permission',
      Content: [{
        Type: 'text',
        Text: JSON.stringify([{ id: 'c2', callableId: 'project.shell_exec', input: '{}' }]),
      }],
      Closed: false, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1', RequestId: 'req2',
    }]
    const result = stepsToEnvelopes(steps)
    const frame: any = result[0]!.frames[0]
    expect(frame.toolCalls).toEqual([
      { id: 'c2', callableId: 'project.shell_exec', input: '{}' },
    ])
  })

  it('renders ask_user interaction step from backend-wrapped task payload', () => {
    const askInput = JSON.stringify({
      questions: [{ header: 'File', question: 'Which file?', options: ['a.ts', 'b.ts'], multiSelect: false }],
    })
    const steps: Step[] = [{
      Id: 't1-ask-ask-req-1', Role: 'assistant', Type: 'ask_user',
      Content: [{ Type: 'text', Text: JSON.stringify({ questions: askInput }) }],
      Closed: false, Timestamp: '2024-01-01T00:00:01Z', TurnId: 't1', Seq: 2,
      InteractionStatus: 'pending',
    }]
    const result = stepsToEnvelopes(steps)
    expect(result).toHaveLength(1)
    const frame = result[0]!.frames[0] as import('../model/frame-types').AskUserQuestionFrame
    expect(frame.type).toBe('ask_user')
    expect(frame.questions).toHaveLength(1)
    expect(frame.questions[0]).toMatchObject({ header: 'File', question: 'Which file?' })
  })

  it('recovers answered ask_user from a resolved step (live-path appended answer)', () => {
    // applyStepEvent appends json.Marshal({"answer": answersJSON}) on
    // step.interaction_resolved — a reloaded timeline must not render the
    // questions as still pending.
    const askInput = JSON.stringify({
      questions: [{ header: 'File', question: 'Which file?', options: ['a.ts', 'b.ts'], multiSelect: false }],
    })
    const steps: Step[] = [{
      Id: 't1-ask-ask-req-1', Role: 'assistant', Type: 'ask_user',
      Content: [
        { Type: 'text', Text: JSON.stringify({ questions: askInput }) },
        { Type: 'text', Text: JSON.stringify({ answer: JSON.stringify({ 0: 'a.ts' }) }) },
      ],
      Closed: true, Timestamp: '2024-01-01T00:00:01Z', TurnId: 't1', Seq: 2,
      InteractionStatus: 'resolved', RequestId: 'ask-req-1',
    }]
    const frame = stepsToEnvelopes(steps)[0]!.frames[0] as import('../model/frame-types').AskUserQuestionFrame
    expect(frame.answers).toEqual({ 0: 'a.ts' })
    expect(frame.status).toBe('completed')
  })

  it('recovers answered ask_user from a resolved step (restart-path replaced content)', () => {
    // resolvePendingInteractionStep replaces content with the raw answersJSON.
    const steps: Step[] = [{
      Id: 't1-ask-r1', Role: 'assistant', Type: 'ask_user',
      Content: [{ Type: 'text', Text: JSON.stringify({ 1: 'yes' }) }],
      Closed: true, Timestamp: '2024-01-01T00:00:01Z', TurnId: 't1', Seq: 2,
      InteractionStatus: 'resolved', RequestId: 'r1',
    }]
    const frame = stepsToEnvelopes(steps)[0]!.frames[0] as import('../model/frame-types').AskUserQuestionFrame
    expect(frame.answers).toEqual({ 1: 'yes' })
  })

  it('recovers the permission decision from a resolved step (live-path appended answer)', () => {
    const steps: Step[] = [{
      Id: 'perm1', Role: 'assistant', Type: 'permission',
      Content: [
        { Type: 'text', Text: JSON.stringify({ toolCalls: [{ id: 'c1', callableId: 'appmanager.register_project' }], reason: 'registration' }) },
        { Type: 'text', Text: JSON.stringify({ allowed: true, answer: JSON.stringify({ allowed: true }) }) },
      ],
      Closed: true, Timestamp: '2024-01-01T00:00:02Z', TurnId: 't1', RequestId: 'req1',
      InteractionStatus: 'resolved',
    }]
    const frame: any = stepsToEnvelopes(steps)[0]!.frames[0]
    expect(frame.allowed).toBe(true)
    expect(frame.status).toBe('completed')
  })

  it('recovers the permission decision from a resolved step (restart-path replaced content)', () => {
    const steps: Step[] = [{
      Id: 'perm2', Role: 'assistant', Type: 'permission',
      Content: [{ Type: 'text', Text: JSON.stringify({ allowed: false, allowInProject: false }) }],
      Closed: true, Timestamp: '2024-01-01T00:00:02Z', TurnId: 't1', RequestId: 'req2',
      InteractionStatus: 'resolved',
    }]
    const frame: any = stepsToEnvelopes(steps)[0]!.frames[0]
    expect(frame.allowed).toBe(false)
  })

  it('leaves an unresolved permission step pending', () => {
    const steps: Step[] = [{
      Id: 'perm3', Role: 'assistant', Type: 'permission',
      Content: [{ Type: 'text', Text: JSON.stringify({ toolCalls: [{ id: 'c1', callableId: 'shell.exec' }], reason: 'r' }) }],
      Closed: false, Timestamp: '2024-01-01T00:00:02Z', TurnId: 't1', RequestId: 'req3',
      InteractionStatus: 'pending',
    }]
    const frame: any = stepsToEnvelopes(steps)[0]!.frames[0]
    expect(frame.allowed).toBeUndefined()
    expect(frame.status).toBe('running')
  })

  it('deduplicates ask_user frames when tool_call and interaction steps coexist', () => {
    const askInput = JSON.stringify({
      questions: [{ header: 'File', question: 'Which file?', options: ['a.ts', 'b.ts'], multiSelect: false }],
    })
    const steps: Step[] = [
      {
        Id: 'tool-ask', Role: 'assistant', Type: 'tool_call',
        Content: [{ Type: 'tool_use', ToolName: 'ask_user', ToolUseId: 'ask-req-1', Input: askInput }],
        Closed: false, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1', Seq: 1,
      },
      {
        Id: 't1-ask-ask-req-1', Role: 'assistant', Type: 'ask_user',
        Content: [{ Type: 'text', Text: JSON.stringify({ questions: askInput }) }],
        Closed: false, Timestamp: '2024-01-01T00:00:01Z', TurnId: 't1', Seq: 2,
        InteractionStatus: 'pending',
      },
    ]
    const result = stepsToEnvelopes(steps)
    expect(result).toHaveLength(1)
    expect(result[0]!.frames).toHaveLength(1)
    const frame = result[0]!.frames[0] as import('../model/frame-types').AskUserQuestionFrame
    expect(frame.type).toBe('ask_user')
    // Keep the tool_call frame (first occurrence) so requestId matches ToolUseId.
    expect(frame.requestId).toBe('ask-req-1')
  })

  it('renders preview-style ask_user step with direct questions array', () => {
    const questions = [{ header: 'File', question: 'Which file?', options: ['a.ts'], multiSelect: false }]
    const steps: Step[] = [{
      Id: 'ask-1', Role: 'assistant', Type: 'ask_user',
      Content: [{ Type: 'text', Text: JSON.stringify(questions) }],
      Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1', Seq: 1,
    }]
    const result = stepsToEnvelopes(steps)
    expect(result).toHaveLength(1)
    const frame = result[0]!.frames[0] as import('../model/frame-types').AskUserQuestionFrame
    expect(frame.type).toBe('ask_user')
    expect(frame.questions).toHaveLength(1)
    expect(frame.questions[0]).toMatchObject({ header: 'File', question: 'Which file?' })
  })

  it('falls back to timestamp sorting for legacy steps without Seq', () => {
    const steps: Step[] = [
      {
        Id: 'a1', Role: 'assistant', Type: 'text',
        Content: [{ Type: 'text', Text: 'A1' }],
        Closed: true, Timestamp: '2024-01-01T00:00:02Z', TurnId: 't1',
      },
      {
        Id: 'u1', Role: 'user', Type: 'text',
        Content: [{ Type: 'text', Text: 'Q1' }],
        Closed: true, Timestamp: '2024-01-01T00:00:01Z', TurnId: 't1',
      },
    ]
    const result = stepsToEnvelopes(steps)
    expect(result.map(e => e.role)).toEqual(['user', 'assistant'])
  })

  it('renders a legacy system skill-mount step as a skill_use frame inside its assistant turn', () => {
    const steps: Step[] = [
      {
        Id: 'u1', Role: 'user', Type: 'text',
        Content: [{ Type: 'text', Text: '/read-logs analyze backend' }],
        Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1',
        Seq: 1,
      },
      {
        Id: 'sk1', Role: 'system', Type: 'text',
        Content: [{ Type: 'text', Text: '<!-- loaded skill: read-logs -->\nskill body here' }],
        Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 'turn-2',
        Seq: 2,
      },
      {
        Id: 'a1', Role: 'assistant', Type: 'text',
        Content: [{ Type: 'text', Text: 'OK' }],
        Closed: true, Timestamp: '2024-01-01T00:00:01Z', TurnId: 'turn-2',
        Seq: 3,
      },
    ]
    const result = stepsToEnvelopes(steps)
    expect(result.map(e => e.role)).toEqual(['user', 'assistant'])
    const asst = result[1]!
    const skillFrame = asst.frames.find(f => f.type === 'skill_use')
    expect(skillFrame).toMatchObject({
      type: 'skill_use',
      skillName: 'read-logs',
      status: 'completed',
    })
    // The body is intentionally not surfaced.
    expect((skillFrame as any).skillBody).toBeUndefined()
    // Assistant envelope has both skill_use and the text frame.
    expect(asst.frames.some(f => f.type === 'text')).toBe(true)
  })

  it('renders an agent_skill_use tool_call as a compact skill_use frame without body', () => {
    const steps: Step[] = [
      {
        Id: 'u1', Role: 'user', Type: 'text',
        Content: [{ Type: 'text', Text: '/plan-module refactor auth' }],
        Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1', Seq: 1,
      },
      {
        Id: 'sk1', Role: 'assistant', Type: 'tool_call',
        Content: [
          { Type: 'tool_use', ToolName: 'agent_skill_use', ToolUseId: 'tu-1', Input: JSON.stringify({ SkillId: 'plan-module' }) },
          { Type: 'tool_result', ToolUseId: 'tu-1', Text: 'the full skill body content...' },
        ],
        Closed: true, Timestamp: '2024-01-01T00:00:01Z', TurnId: 'turn-2', Seq: 2,
      },
      {
        Id: 'a1', Role: 'assistant', Type: 'text',
        Content: [{ Type: 'text', Text: 'OK' }],
        Closed: true, Timestamp: '2024-01-01T00:00:02Z', TurnId: 'turn-2', Seq: 3,
      },
    ]
    const result = stepsToEnvelopes(steps)
    expect(result.map(e => e.role)).toEqual(['user', 'assistant'])
    const asst = result[1]!
    const skillFrame = asst.frames.find(f => f.type === 'skill_use') as import('../model/frame-types').SkillUseFrame
    expect(skillFrame).toBeDefined()
    expect(skillFrame.skillName).toBe('plan-module')
    expect(skillFrame.status).toBe('completed')
    expect(skillFrame.isError).toBe(false)
    // The body is intentionally not surfaced on the frame.
    expect((skillFrame as any).skillBody).toBeUndefined()
  })

  it('merges streamed plan text into the plan approval frame', () => {
    const planText = '# Plan\n\n1. Do thing\n2. Do other thing'
    const steps: Step[] = [
      {
        Id: 'text-1', Role: 'assistant', Type: 'text',
        Content: [{ Type: 'text', Text: planText }],
        Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1', Seq: 1,
      },
      {
        Id: 't1-plan-req-1', Role: 'assistant', Type: 'plan_approval',
        Content: [{ Type: 'text', Text: JSON.stringify({ plan: planText, editable: true, tasks: [] }) }],
        Closed: false, Timestamp: '2024-01-01T00:00:01Z', TurnId: 't1', Seq: 2,
        InteractionStatus: 'pending',
        RequestId: 'req-1',
      },
    ]
    const result = stepsToEnvelopes(steps)
    expect(result).toHaveLength(1)
    const asst = result[0]!
    expect(asst.frames).toHaveLength(1)
    expect(asst.frames[0]).toMatchObject({
      type: 'plan',
      content: planText,
      requestId: 'req-1',
      approvalStatus: 'pending',
    })
  })

  it('invalidates step cache when plan approval answer is keyed by requestId', () => {
    const planText = '# Plan'
    const planStep: Step = {
      Id: 't1-plan-req-1', Role: 'assistant', Type: 'plan_approval',
      Content: [{ Type: 'text', Text: JSON.stringify({ plan: planText, editable: true, tasks: [] }) }],
      Closed: false, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1', Seq: 1,
      InteractionStatus: 'pending',
      RequestId: 'req-1',
    }
    const localResponses = new Map<string, LocalInteractionResponse>()
    const stepCache = new Map<string, { sig: string; frames: Frame[] }>()

    // First render: pending.
    let result = stepsToEnvelopes([planStep], localResponses, undefined, stepCache)
    let frame = result[0]!.frames[0] as PlanFrame
    expect(frame.approvalStatus).toBe('pending')

    // User approves; response keyed by requestId (not step.Id).
    localResponses.set('req-1', { kind: 'plan_approval_answered', decision: 'approve' })

    // Re-render with the same step object and cache must reflect approved state.
    result = stepsToEnvelopes([planStep], localResponses, undefined, stepCache)
    frame = result[0]!.frames[0] as PlanFrame
    expect(frame.approvalStatus).toBe('approved')
  })

  it('marks pending plan as cancelled when turn ends (step closed without resolution)', () => {
    const planText = '# Plan'
    const steps: Step[] = [
      {
        Id: 't1-plan-req-1', Role: 'assistant', Type: 'plan_approval',
        Content: [{ Type: 'text', Text: JSON.stringify({ plan: planText, editable: true, tasks: [] }) }],
        // Turn cancelled mid-approval: _closeOpenStepsInTurn marks Closed=true,
        // but InteractionStatus stays 'pending' (no answer ever arrived).
        Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1', Seq: 1,
        InteractionStatus: 'pending',
        RequestId: 'req-1',
      },
    ]
    const result = stepsToEnvelopes(steps)
    expect(result).toHaveLength(1)
    const frame = result[0]!.frames[0] as PlanFrame
    expect(frame.approvalStatus).toBe('cancelled')
  })

  it('renders rejected plan from interaction_resolved payload without local response', () => {
    const planText = '# Plan'
    const steps: Step[] = [
      {
        Id: 't1-plan-req-1', Role: 'assistant', Type: 'plan_approval',
        Content: [
          { Type: 'text', Text: JSON.stringify({ plan: planText, editable: true, tasks: [] }) },
          { Type: 'text', Text: JSON.stringify({ decision: 'reject', answer: '{"decision":"reject"}' }) },
        ],
        Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1', Seq: 1,
        InteractionStatus: 'resolved',
        RequestId: 'req-1',
      },
    ]
    const result = stepsToEnvelopes(steps)
    expect(result).toHaveLength(1)
    const frame = result[0]!.frames[0] as PlanFrame
    expect(frame.approvalStatus).toBe('rejected')
  })

  it('renders a completed goal_review step as a goal_review frame with achieved verdict', () => {
    const verdict = JSON.stringify({
      condition: 'refactor the auth module',
      achieved: true,
      reason: 'model signaled [GOAL_ACHIEVED]',
      turnCount: 3,
      maxTurns: 20,
      aborted: false,
    })
    const steps: Step[] = [{
      Id: 'turn-1-goal-review', Role: 'assistant', Type: 'goal_review',
      Content: [
        { Type: 'text', Text: 'Reviewing goal: refactor the auth module' },
        { Type: 'text', Text: verdict },
      ],
      Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1', Seq: 1,
    }]
    const result = stepsToEnvelopes(steps)
    expect(result).toHaveLength(1)
    const frame = result[0]!.frames[0] as import('../model/frame-types').GoalReviewFrame
    expect(frame.type).toBe('goal_review')
    expect(frame.status).toBe('completed')
    expect(frame.condition).toBe('refactor the auth module')
    expect(frame.achieved).toBe(true)
    expect(frame.reason).toBe('model signaled [GOAL_ACHIEVED]')
    expect(frame.turnCount).toBe(3)
    expect(frame.maxTurns).toBe(20)
    expect(frame.aborted).toBe(false)
  })

  it('renders a completed goal_review step with aborted verdict', () => {
    const verdict = JSON.stringify({
      condition: 'refactor the auth module',
      achieved: false,
      reason: 'still missing tests',
      turnCount: 20,
      maxTurns: 20,
      aborted: true,
    })
    const steps: Step[] = [{
      Id: 'turn-2-goal-review', Role: 'assistant', Type: 'goal_review',
      Content: [
        { Type: 'text', Text: 'Reviewing goal: refactor the auth module' },
        { Type: 'text', Text: verdict },
      ],
      Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't2', Seq: 2,
    }]
    const result = stepsToEnvelopes(steps)
    expect(result).toHaveLength(1)
    const frame = result[0]!.frames[0] as import('../model/frame-types').GoalReviewFrame
    expect(frame.type).toBe('goal_review')
    expect(frame.achieved).toBe(false)
    expect(frame.reason).toBe('still missing tests')
    expect(frame.turnCount).toBe(20)
    expect(frame.maxTurns).toBe(20)
    expect(frame.aborted).toBe(true)
  })

  it('renders a running goal_review step with progress active work', () => {
    const steps: Step[] = [{
      Id: 'turn-4-goal-review', Role: 'assistant', Type: 'goal_review',
      Content: [{ Type: 'text', Text: 'Reviewing goal: refactor the auth module' }],
      Closed: false, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't4', Seq: 4,
      Progress: JSON.stringify({ phase: 'reviewing', activeWork: [{ Type: 'git_diff', Target: 'src/auth.go' }] }),
    }]
    const result = stepsToEnvelopes(steps)
    expect(result).toHaveLength(1)
    const frame = result[0]!.frames[0] as import('../model/frame-types').GoalReviewFrame
    expect(frame.type).toBe('goal_review')
    expect(frame.status).toBe('running')
    expect(frame.progress?.phase).toBe('reviewing')
    expect(frame.progress?.activeWork).toEqual([{ Type: 'git_diff', Target: 'src/auth.go' }])
  })

  it('renders a running goal_review step without verdict', () => {
    const steps: Step[] = [{
      Id: 'turn-3-goal-review', Role: 'assistant', Type: 'goal_review',
      Content: [{ Type: 'text', Text: 'Reviewing goal: refactor the auth module' }],
      Closed: false, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't3', Seq: 3,
    }]
    const result = stepsToEnvelopes(steps)
    expect(result).toHaveLength(1)
    const frame = result[0]!.frames[0] as import('../model/frame-types').GoalReviewFrame
    expect(frame.type).toBe('goal_review')
    expect(frame.status).toBe('running')
    expect(frame.condition).toBe('refactor the auth module')
    expect(frame.achieved).toBeUndefined()
    expect(frame.reason).toBeUndefined()
  })

  it('renders a pending goal_submit step as an interactive goal_submit frame', () => {
    const steps: Step[] = [{
      Id: 't1-goal-submit-req-1', Role: 'assistant', Type: 'goal_submit',
      Content: [{ Type: 'text', Text: JSON.stringify({ condition: 'fix the bug', interpretedGoal: 'Null-check the parser entry' }) }],
      Closed: false, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1', Seq: 1,
      InteractionStatus: 'pending',
      RequestId: 'req-1',
    }]
    const result = stepsToEnvelopes(steps)
    expect(result).toHaveLength(1)
    const frame = result[0]!.frames[0] as GoalSubmitFrame
    expect(frame.type).toBe('goal_submit')
    expect(frame.condition).toBe('fix the bug')
    expect(frame.interpretedGoal).toBe('Null-check the parser entry')
    expect(frame.requestId).toBe('req-1')
    expect(frame.approvalStatus).toBe('pending')
    expect(frame.status).not.toBe('completed')
  })

  it('flips goal_submit to approved optimistically from local response keyed by requestId', () => {
    const goalStep: Step = {
      Id: 't1-goal-submit-req-1', Role: 'assistant', Type: 'goal_submit',
      Content: [{ Type: 'text', Text: JSON.stringify({ condition: 'fix the bug', interpretedGoal: 'Null-check the parser entry' }) }],
      Closed: false, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1', Seq: 1,
      InteractionStatus: 'pending',
      RequestId: 'req-1',
    }
    const localResponses = new Map<string, LocalInteractionResponse>()
    const stepCache = new Map<string, { sig: string; frames: Frame[] }>()

    let result = stepsToEnvelopes([goalStep], localResponses, undefined, stepCache)
    let frame = result[0]!.frames[0] as GoalSubmitFrame
    expect(frame.approvalStatus).toBe('pending')

    localResponses.set('req-1', { kind: 'goal_submit_answered', decision: 'approve' })
    result = stepsToEnvelopes([goalStep], localResponses, undefined, stepCache)
    frame = result[0]!.frames[0] as GoalSubmitFrame
    expect(frame.approvalStatus).toBe('approved')
    expect(frame.status).toBe('completed')

    localResponses.set('req-1', { kind: 'goal_submit_answered', decision: 'reject' })
    result = stepsToEnvelopes([goalStep], localResponses, undefined, stepCache)
    frame = result[0]!.frames[0] as GoalSubmitFrame
    expect(frame.approvalStatus).toBe('rejected')
  })

  it('renders rejected goal_submit from interaction_resolved payload without local response', () => {
    const steps: Step[] = [{
      Id: 't1-goal-submit-req-1', Role: 'assistant', Type: 'goal_submit',
      Content: [
        { Type: 'text', Text: JSON.stringify({ condition: 'fix the bug', interpretedGoal: 'Null-check the parser entry' }) },
        { Type: 'text', Text: JSON.stringify({ decision: 'reject', answer: '{"decision":"reject"}' }) },
      ],
      Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1', Seq: 1,
      InteractionStatus: 'resolved',
      RequestId: 'req-1',
    }]
    const result = stepsToEnvelopes(steps)
    expect(result).toHaveLength(1)
    const frame = result[0]!.frames[0] as GoalSubmitFrame
    expect(frame.approvalStatus).toBe('rejected')
    expect(frame.status).toBe('completed')
  })

  it('marks pending goal_submit as cancelled when the step closes without resolution', () => {
    const steps: Step[] = [{
      Id: 't1-goal-submit-req-1', Role: 'assistant', Type: 'goal_submit',
      Content: [{ Type: 'text', Text: JSON.stringify({ condition: 'fix the bug', interpretedGoal: 'Null-check the parser entry' }) }],
      Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1', Seq: 1,
      InteractionStatus: 'pending',
      RequestId: 'req-1',
    }]
    const result = stepsToEnvelopes(steps)
    expect(result).toHaveLength(1)
    const frame = result[0]!.frames[0] as GoalSubmitFrame
    expect(frame.approvalStatus).toBe('cancelled')
  })

  it('parses goal_submit payload even when it is not the first text block', () => {
    const steps: Step[] = [{
      Id: 't1-goal-submit-req-1', Role: 'assistant', Type: 'goal_submit',
      Content: [
        { Type: 'tool_result', Text: 'Goal confirmed. Proceed with execution toward the confirmed goal.' },
        { Type: 'text', Text: JSON.stringify({ condition: 'fix the bug', interpretedGoal: 'Null-check the parser entry' }) },
        { Type: 'text', Text: JSON.stringify({ decision: 'approve', answer: '{"decision":"approve"}' }) },
      ],
      Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1', Seq: 1,
      InteractionStatus: 'resolved',
      RequestId: 'req-1',
    }]
    const result = stepsToEnvelopes(steps)
    expect(result).toHaveLength(1)
    const frame = result[0]!.frames[0] as GoalSubmitFrame
    expect(frame.condition).toBe('fix the bug')
    expect(frame.interpretedGoal).toBe('Null-check the parser entry')
    expect(frame.approvalStatus).toBe('approved')
  })

  it('renders computeruse.screenshot tool_call as an image frame', () => {
    // The step records the LLM-facing tool name: the bare name 'screenshot'
    // (toolregistry.go maps callable IDs to bare names unless overridden).
    const steps: Step[] = [{
      Id: 'tool-ss', Role: 'assistant', Type: 'tool_call',
      Content: [
        { Type: 'tool_use', ToolName: 'screenshot', ToolUseId: 'tu-ss', Input: '{}' },
        { Type: 'tool_result', ToolUseId: 'tu-ss', Text: JSON.stringify({ ImageBytes: 'aGVsbG8=', Format: 'jpeg', Width: 100, Height: 50 }) },
      ],
      Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1', Seq: 1,
    }]
    const result = stepsToEnvelopes(steps)
    expect(result).toHaveLength(1)
    expect(result[0]!.frames[0]).toMatchObject({
      type: 'image',
      status: 'completed',
      url: 'data:image/jpeg;base64,aGVsbG8=',
    })
  })

  it('renders a screenshot observation step as an image ai-step in the assistant envelope', () => {
    // The engine persists the screenshot observation as a user_inject step
    // (Meta 'screenshot'): processing flows as a user message, but the UI
    // renders the image inside the ASSISTANT turn envelope — never as a user
    // bubble and never as recognition text (that lives on the wire only).
    const steps: Step[] = [{
      Id: 'u-obs', Role: 'assistant', Type: 'user_inject', Meta: 'screenshot',
      Content: [
        { Type: 'text', Text: '[computeruse.screenshot 2560x1440 jpeg]' },
        { Type: 'image', ImageUrl: 'data:image/jpeg;base64,aGVsbG8=', MimeType: 'image/jpeg' },
      ],
      Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1', Seq: 2,
    }]
    const result = stepsToEnvelopes(steps)
    expect(result).toHaveLength(1)
    expect(result[0]!.role).toBe('assistant')
    expect(result[0]!.frames[0]).toMatchObject({
      type: 'image',
      status: 'completed',
      url: 'data:image/jpeg;base64,aGVsbG8=',
      alt: '[computeruse.screenshot 2560x1440 jpeg]',
    })
  })

  it('falls back to a generic tool frame when screenshot has no image bytes', () => {
    const steps: Step[] = [{
      Id: 'tool-ss', Role: 'assistant', Type: 'tool_call',
      Content: [
        { Type: 'tool_use', ToolName: 'screenshot', ToolUseId: 'tu-ss', Input: '{}' },
        { Type: 'tool_result', ToolUseId: 'tu-ss', Text: JSON.stringify({ Unchanged: true, Message: 'no change' }) },
      ],
      Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1', Seq: 1,
    }]
    const result = stepsToEnvelopes(steps)
    expect(result[0]!.frames[0]).toMatchObject({
      type: 'tool',
      toolName: 'computeruse.screenshot',
    })
  })

  it('error override on a tool_call step carries the tool name', () => {
    const steps: Step[] = [{
      Id: 'tool-err', Role: 'assistant', Type: 'tool_call',
      Content: [
        { Type: 'tool_use', ToolName: 'project.list', ToolUseId: 'tu-err', Input: '{"path":"/"}' },
      ],
      Error: 'permission denied',
      Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1', Seq: 1,
    }]
    const result = stepsToEnvelopes(steps)
    expect(result[0]!.frames[0]).toMatchObject({
      type: 'error',
      status: 'error',
      message: 'permission denied',
      toolName: 'project.list',
    } as ErrorFrame)
  })

  it('error override infers the tool name from input when ToolName is generic', () => {
    const steps: Step[] = [{
      Id: 'tool-err', Role: 'assistant', Type: 'tool_call',
      Content: [
        { Type: 'tool_use', ToolName: 'tool_call', ToolUseId: 'tu-err', Input: '{"command":"ls -la"}' },
      ],
      Error: 'exit code 1',
      Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1', Seq: 1,
    }]
    const result = stepsToEnvelopes(steps)
    expect(result[0]!.frames[0]).toMatchObject({
      type: 'error',
      message: 'exit code 1',
      toolName: 'project.shell_exec',
    } as ErrorFrame)
  })

  it('error override on a tool_call step without a tool_use block stays unnamed', () => {
    const steps: Step[] = [{
      Id: 'tool-err', Role: 'assistant', Type: 'tool_call',
      Content: [],
      Error: 'dispatch failed',
      Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1', Seq: 1,
    }]
    const result = stepsToEnvelopes(steps)
    expect(result[0]!.frames[0]).toMatchObject({
      type: 'error',
      message: 'dispatch failed',
      toolName: undefined,
    } as ErrorFrame)
  })

  it('error override on an llm_call step carries no tool name', () => {
    const steps: Step[] = [{
      Id: 'llm-err', Role: 'assistant', Type: 'llm_call',
      Content: [{ Type: 'text', Text: 'partial' }],
      Error: 'LLM timeout',
      Closed: true, Timestamp: '2024-01-01T00:00:00Z', TurnId: 't1', Seq: 1,
    }]
    const result = stepsToEnvelopes(steps)
    expect(result[0]!.frames[0]).toMatchObject({
      type: 'error',
      message: 'LLM timeout',
      toolName: undefined,
    } as ErrorFrame)
  })
})

describe('mergeStepEnvelopes', () => {
  it('replaces assistant envelopes for turns with step data', () => {
    const existing = [
      { id: 'u1', role: 'user' as const, frames: [], timestamp: '' },
      { id: 'a1', role: 'assistant' as const, frames: [{ id: 'f1', type: 'text' as const, status: 'completed' as const, content: 'old' }], timestamp: '', metadata: { turnId: 't1' } },
    ]
    const stepEnvs = [
      { id: 't1', role: 'assistant' as const, frames: [{ id: 'f2', type: 'text' as const, status: 'completed' as const, content: 'new' }], timestamp: '', metadata: { turnId: 't1' } },
    ]
    const result = mergeStepEnvelopes(existing, stepEnvs)
    expect(result).toHaveLength(2)
    const asst = result.find(e => e.role === 'assistant')
    expect((asst!.frames[0] as import('../model/frame-types').TextFrame).content).toBe('new')
  })

  it('preserves user envelopes', () => {
    const existing = [
      { id: 'u1', role: 'user' as const, frames: [], timestamp: '' },
    ]
    const stepEnvs = [
      { id: 't1', role: 'assistant' as const, frames: [], timestamp: '', metadata: { turnId: 't1' } },
    ]
    const result = mergeStepEnvelopes(existing, stepEnvs)
    expect(result).toHaveLength(2)
    expect(result.find(e => e.role === 'user')).toBeDefined()
  })
})

describe('dedupeEnvelopes', () => {
  it('keeps order when no duplicates', () => {
    const envelopes = [
      { id: 'u1', role: 'user' as const, frames: [], timestamp: '1' },
      { id: 'a1', role: 'assistant' as const, frames: [], timestamp: '2' },
      { id: 'u2', role: 'user' as const, frames: [], timestamp: '3' },
    ]
    const result = dedupeEnvelopes(envelopes)
    expect(result.map(e => e.id)).toEqual(['u1', 'a1', 'u2'])
  })

  it('keeps first occurrence position, uses freshest data', () => {
    const envelopes = [
      { id: 'a1', role: 'assistant' as const, frames: [{ id: 'f1', type: 'text' as const, status: 'completed' as const, content: 'old' }], timestamp: '1' },
      { id: 'u1', role: 'user' as const, frames: [], timestamp: '2' },
      { id: 'a1', role: 'assistant' as const, frames: [{ id: 'f2', type: 'text' as const, status: 'completed' as const, content: 'new' }], timestamp: '3' },
    ]
    const result = dedupeEnvelopes(envelopes)
    expect(result).toHaveLength(2)
    expect(result.map(e => e.id)).toEqual(['a1', 'u1'])
    const asst = result.find(e => e.id === 'a1')
    expect((asst!.frames[0] as import('../model/frame-types').TextFrame).content).toBe('new')
  })

  it('does not let a live paused envelope inherit completed history state', () => {
    const history = {
      id: 'turn-1', role: 'assistant' as const, frames: [], timestamp: '1',
      completed: true, metadata: { turnId: 'turn-1', turnState: 'completed' as const },
    }
    const paused = {
      id: 'turn-1', role: 'assistant' as const, frames: [], timestamp: '2',
      completed: false, metadata: { turnId: 'turn-1', turnState: 'paused' as const },
    }
    const result = dedupeEnvelopes([history, paused])
    expect(result[0]!.completed).toBe(false)
    expect(result[0]!.metadata?.turnState).toBe('paused')
  })

  it('preserves causal order when history and step envelopes overlap', () => {
    const envelopes = [
      { id: 'u1', role: 'user' as const, frames: [], timestamp: '1' },
      { id: 'a1', role: 'assistant' as const, frames: [], timestamp: '2' },
      { id: 'u2', role: 'user' as const, frames: [], timestamp: '3' },
      { id: 'a2', role: 'assistant' as const, frames: [], timestamp: '4' },
      { id: 'u1', role: 'user' as const, frames: [], timestamp: '1' },
      { id: 'a1', role: 'assistant' as const, frames: [], timestamp: '2' },
    ]
    const result = dedupeEnvelopes(envelopes)
    expect(result.map(e => e.id)).toEqual(['u1', 'a1', 'u2', 'a2'])
  })

  it('preserves fileChanges from existing when incoming lacks them', () => {
    const history = {
      id: 'turn-1', role: 'assistant' as const, frames: [{ id: 'f1', type: 'text' as const, status: 'completed' as const, content: 'hello' }], timestamp: '1',
      fileChanges: [{ filename: 'a.ts', filepath: 'a.ts', icon: 'ts' as const, additions: 1, deletions: 0, diffContent: '' }],
    }
    const stepDerived = {
      id: 'turn-1', role: 'assistant' as const, frames: [{ id: 'f2', type: 'text' as const, status: 'completed' as const, content: 'hello' }], timestamp: '2',
    }
    const result = dedupeEnvelopes([history, stepDerived])
    expect(result[0]!.fileChanges).toBeDefined()
    expect(result[0]!.fileChanges!).toHaveLength(1)
    expect(result[0]!.fileChanges![0]!.filename).toBe('a.ts')
  })

  it('preserves tasks from existing when incoming lacks them', () => {
    const history = {
      id: 'turn-1', role: 'assistant' as const, frames: [{ id: 'f1', type: 'text' as const, status: 'completed' as const, content: 'hello' }], timestamp: '1',
      tasks: [{ id: 'task-1', subject: 'Do something', status: 'completed' as const }],
    }
    const stepDerived = {
      id: 'turn-1', role: 'assistant' as const, frames: [{ id: 'f2', type: 'text' as const, status: 'completed' as const, content: 'hello' }], timestamp: '2',
    }
    const result = dedupeEnvelopes([history, stepDerived])
    expect(result[0]!.tasks).toBeDefined()
    expect(result[0]!.tasks!).toHaveLength(1)
    expect(result[0]!.tasks![0]!.subject).toBe('Do something')
  })
})

describe('compaction frame status', () => {
  const compactionFrame = (status: string, extra: Record<string, unknown> = {}) => JSON.stringify({
    type: 'compaction',
    id: 'frame-1',
    status,
    trigger: 'user',
    beforeTokens: 1000,
    afterTokens: 500,
    contextWindowSize: 200000,
    rounds: [],
    ...extra,
  })
  const mkStep = (blocks: Array<{ Type: string; Text: string }>, closed: boolean): Step[] => [{
    Id: 'c1', Role: 'assistant', Type: 'text',
    Content: blocks,
    Closed: closed, Timestamp: '2024-01-01T00:00:00Z', TurnId: 'tc',
  }]

  it('stays running for a running block while the step is open', () => {
    const result = stepsToEnvelopes(mkStep([{ Type: 'compaction', Text: compactionFrame('running') }], false))
    const frame = result[0]!.frames[0]
    expect(frame?.type).toBe('compaction')
    expect(frame?.status).toBe('running')
  })

  it('flips to completed from the final block status before step.closed arrives', () => {
    const result = stepsToEnvelopes(mkStep([{ Type: 'compaction', Text: compactionFrame('completed', { afterTokens: 4200, afterLayout: {} }) }], false))
    expect(result[0]!.frames[0]?.status).toBe('completed')
  })

  it('surfaces the error from the failed compaction frame', () => {
    const result = stepsToEnvelopes(mkStep([{ Type: 'compaction', Text: compactionFrame('error', { error: 'round 1 summarize failed: boom' }) }], true))
    expect(result[0]!.frames[0]?.status).toBe('error')
  })
})
