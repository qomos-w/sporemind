import { describe, it, expect } from 'vitest'
import { describeCron, filterTimersByStatus, searchTimers, relativeTime, cronToDraft, draftToCron, resolveScheduleType, resolveAgentStateAction, resolveTaskMode, firstBodyLine, truncatePreview, agentActionsOf, agentRefId, findAgentByRef, scheduleDraftIsDirty, resolveScheduledScope } from './scheduledTasks'
import type { WikiTimerListItem } from '../../../gen-types/project.wiki.part1'

describe('describeCron', () => {
  it('classifies daily at 8:00', () => {
    expect(describeCron('0 8 * * *')).toEqual({ kind: 'everyDay', time: '8:00' })
  })

  it('classifies weekdays 9:00', () => {
    expect(describeCron('0 9 * * 1-5')).toEqual({ kind: 'weekdays', time: '9:00' })
    expect(describeCron('0 9 * * 1,2,3,4,5')).toEqual({ kind: 'weekdays', time: '9:00' })
  })

  it('classifies weekly Monday 16:00', () => {
    expect(describeCron('0 16 * * 1')).toEqual({ kind: 'weekly', time: '16:00', dow: 1 })
  })

  it('classifies Sunday as dow 7', () => {
    expect(describeCron('30 9 * * 0')).toEqual({ kind: 'weekly', time: '9:30', dow: 7 })
    expect(describeCron('30 9 * * 7')).toEqual({ kind: 'weekly', time: '9:30', dow: 7 })
  })

  it('classifies monthly on the 1st', () => {
    expect(describeCron('0 0 1 * *')).toEqual({ kind: 'monthly', time: '0:00', dom: 1 })
    expect(describeCron('30 18 15 * *')).toEqual({ kind: 'monthly', time: '18:30', dom: 15 })
  })

  it('pads minutes but not hours', () => {
    expect(describeCron('5 9 * * *').time).toBe('9:05')
    expect(describeCron('0 9 * * *').time).toBe('9:00')
  })

  it('falls back to custom for ranges/lists/step expressions', () => {
    expect(describeCron('*/5 * * * *')).toEqual({ kind: 'custom', time: '' })
    expect(describeCron('0 8,18 * * *')).toEqual({ kind: 'custom', time: '' })
    // Multi day-of-week keeps a clean time but is still custom (not a single weekday).
    expect(describeCron('0 9 * * 1,3,5')).toEqual({ kind: 'custom', time: '9:00' })
  })

  it('falls back to custom for malformed input', () => {
    expect(describeCron('')).toEqual({ kind: 'custom', time: '' })
    expect(describeCron('not a cron')).toEqual({ kind: 'custom', time: '' })
    expect(describeCron('0 8 * *')).toEqual({ kind: 'custom', time: '' })
  })
})

const mk = (id: string, enabled: boolean): WikiTimerListItem => ({
  Id: id, NextFireAt: '', LastRunAt: '', LastStatus: '', Enabled: enabled, CurrentInstance: '',
})

describe('filterTimersByStatus', () => {
  const timers = [mk('a', true), mk('b', false), mk('c', true)]
  it('returns all for all', () => {
    expect(filterTimersByStatus(timers, 'all')).toHaveLength(3)
  })
  it('returns only enabled', () => {
    expect(filterTimersByStatus(timers, 'enabled').map(t => t.Id)).toEqual(['a', 'c'])
  })
  it('returns only paused', () => {
    expect(filterTimersByStatus(timers, 'paused').map(t => t.Id)).toEqual(['b'])
  })
})

describe('searchTimers', () => {
  const items = [
    { timer: mk('id-1', true), title: '每日简报' },
    { timer: mk('id-2', false), title: '每周回顾' },
  ]
  it('matches by title', () => {
    expect(searchTimers(items, '简报')).toHaveLength(1)
  })
  it('matches by id', () => {
    expect(searchTimers(items, 'id-2')).toHaveLength(1)
  })
  it('empty query returns all', () => {
    expect(searchTimers(items, '')).toHaveLength(2)
  })
  it('case-insensitive', () => {
    expect(searchTimers(items, 'ID-1')).toHaveLength(1)
  })
})

describe('relativeTime', () => {
  const now = new Date('2026-08-22T12:00:00Z')
  it('empty iso returns empty', () => {
    expect(relativeTime('', now)).toBe('')
  })
  it('future returns empty', () => {
    expect(relativeTime('2026-08-22T13:00:00Z', now)).toBe('')
  })
  it('returns minutes for under an hour', () => {
    expect(relativeTime('2026-08-22T11:32:00Z', now)).toBe('28')
  })
  it('returns hours for under a day', () => {
    expect(relativeTime('2026-08-22T05:00:00Z', now)).toBe('7 小时')
  })
  it('returns days for over a day', () => {
    expect(relativeTime('2026-08-20T05:00:00Z', now)).toBe('2 天')
  })
})

describe('resolveScheduleType', () => {
  it('converges every scheduler card to the unified task type', () => {
    expect(resolveScheduleType('task')).toBe('task')
    expect(resolveScheduleType('workflow')).toBe('task')
    expect(resolveScheduleType('prompt')).toBe('task')
    expect(resolveScheduleType('agent_action')).toBe('task')
    expect(resolveScheduleType(undefined)).toBe('task')
    expect(resolveScheduleType('')).toBe('task')
  })
})

describe('resolveAgentStateAction', () => {
  it('recognizes pause and resume', () => {
    expect(resolveAgentStateAction('pause')).toBe('pause')
    expect(resolveAgentStateAction('resume')).toBe('resume')
  })

  it('falls back to pause for legacy agent_action schedule type', () => {
    expect(resolveAgentStateAction(undefined, 'agent_action')).toBe('pause')
  })

  it('returns undefined for ordinary tasks', () => {
    expect(resolveAgentStateAction(undefined, undefined)).toBeUndefined()
    expect(resolveAgentStateAction(undefined, 'task')).toBeUndefined()
    expect(resolveAgentStateAction('', 'task')).toBeUndefined()
    expect(resolveAgentStateAction('other')).toBeUndefined()
  })
})

describe('agentActionsOf', () => {
  it('parses the unified agent_actions JSON list (canonical target key)', () => {
    expect(agentActionsOf({ agent_actions: '[{"action":"pause","target":"agent:coder"}]' }))
      .toEqual([{ action: 'pause', targetAgent: 'agent:coder' }])
    expect(agentActionsOf({ agent_actions: '[{"action":"resume","target":"agent:my-coder"},{"action":"pause","target":"agent:dreamer"}]' }))
      .toEqual([
        { action: 'resume', targetAgent: 'agent:my-coder' },
        { action: 'pause', targetAgent: 'agent:dreamer' },
      ])
  })

  it('accepts legacy target_agent keys in the JSON list', () => {
    expect(agentActionsOf({ agent_actions: '[{"action":"pause","target_agent":"agent:coder"}]' }))
      .toEqual([{ action: 'pause', targetAgent: 'agent:coder' }])
  })

  it('falls back to legacy single-action fields', () => {
    expect(agentActionsOf({ agent_action: 'resume', target_agent: 'agent:x' }))
      .toEqual([{ action: 'resume', targetAgent: 'agent:x' }])
    expect(agentActionsOf({ schedule_type: 'agent_action', target_agent: 'agent:x' }))
      .toEqual([{ action: 'pause', targetAgent: 'agent:x' }])
  })

  it('returns an empty array when no actions are configured', () => {
    expect(agentActionsOf({})).toEqual([])
    expect(agentActionsOf({ schedule_type: 'task' })).toEqual([])
    expect(agentActionsOf({ agent_actions: '' })).toEqual([])
    expect(agentActionsOf({ agent_actions: 'not-json' })).toEqual([])
  })

  it('skips malformed entries inside a JSON list', () => {
    expect(agentActionsOf({ agent_actions: '[{"action":"pause","target_agent":"agent:a"},{"bad":true}]' }))
      .toEqual([{ action: 'pause', targetAgent: 'agent:a' }])
  })

  it('accepts a pre-parsed array (not a JSON string)', () => {
    expect(agentActionsOf({ agent_actions: [{ action: 'pause', target: 'agent:a' }] }))
      .toEqual([{ action: 'pause', targetAgent: 'agent:a' }])
  })
})

describe('agentRefId / findAgentByRef null-safety', () => {
  it('agentRefId returns empty string for null/undefined', () => {
    expect(agentRefId(null)).toBe('')
    expect(agentRefId(undefined)).toBe('')
    expect(agentRefId('')).toBe('')
    expect(agentRefId('agent:coder')).toBe('coder')
    expect(agentRefId('raw-id')).toBe('raw-id')
  })

  it('findAgentByRef returns undefined for null/undefined ref', () => {
    expect(findAgentByRef([], null)).toBeUndefined()
    expect(findAgentByRef([], undefined)).toBeUndefined()
  })
})

describe('resolveTaskMode', () => {
  it('prefers an explicit template mode', () => {
    expect(resolveTaskMode('template', 'tpl::x')).toBe('template')
    expect(resolveTaskMode('template', undefined)).toBe('template')
  })

  it('derives template mode from a bound template id', () => {
    expect(resolveTaskMode(undefined, 'tpl::x')).toBe('template')
    expect(resolveTaskMode('', 'tpl::x')).toBe('template')
  })

  it('defaults to prompt mode when no template is bound', () => {
    expect(resolveTaskMode(undefined, undefined)).toBe('prompt')
    expect(resolveTaskMode('', '')).toBe('prompt')
    expect(resolveTaskMode('prompt', undefined)).toBe('prompt')
  })
})

describe('prompt preview helpers', () => {
  it('firstBodyLine returns the first non-empty trimmed line', () => {
    expect(firstBodyLine('')).toBe('')
    expect(firstBodyLine('\n  \nFirst line\nSecond')).toBe('First line')
    expect(firstBodyLine('Only')).toBe('Only')
  })

  it('truncatePreview collapses whitespace and truncates with ellipsis', () => {
    expect(truncatePreview('  a\n  b  c ', 200)).toBe('a b c')
    expect(truncatePreview('x'.repeat(10), 5)).toBe('xxxxx…')
    expect(truncatePreview('short', 20)).toBe('short')
  })
})

describe('schedule editor round-trip', () => {
  it('parses cron into a padded-time draft and back', () => {
    const draft = cronToDraft('5 9 * * *', '')
    expect(draft.kind).toBe('everyDay')
    expect(draft.time).toBe('09:05')
    expect(draftToCron(draft)).toBe('5 9 * * *')
  })

  it('parses cron into a draft without bind-mode fields', () => {
    const draft = cronToDraft('5 9 * * *', '')
    expect(draft).not.toHaveProperty('bindMode')
    expect(draft).not.toHaveProperty('boundAgent')
    expect(draftToCron(draft)).toBe('5 9 * * *')
  })

  it('maps Sunday between CronSummary(7) and cron(0) numbering', () => {
    const draft = cronToDraft('30 9 * * 0', '')
    expect(draft.kind).toBe('weekly')
    expect(draft.dow).toBe(0)
    expect(draftToCron(draft)).toBe('30 9 * * 0')
  })

  it('round-trips weekly/monthly/weekdays', () => {
    for (const cron of ['0 16 * * 1', '0 9 * * 1-5', '30 18 15 * *']) {
      expect(draftToCron(cronToDraft(cron, ''))).toBe(cron)
    }
  })

  it('keeps custom cron verbatim and rejects malformed input', () => {
    expect(draftToCron({ kind: 'custom', time: '', dow: 0, dom: 1, custom: '*/5 9 * * *' })).toBe('*/5 9 * * *')
    expect(draftToCron({ kind: 'custom', time: '', dow: 0, dom: 1, custom: 'not a cron' })).toBe('')
    expect(draftToCron({ kind: 'everyDay', time: '25:00', dow: 0, dom: 1, custom: '' })).toBe('')
    expect(draftToCron({ kind: 'monthly', time: '09:00', dow: 0, dom: 32, custom: '' })).toBe('')
  })

  it('falls back to 09:00 when the stored cron has no clean time', () => {
    const draft = cronToDraft('*/5 * * * *', '')
    expect(draft.kind).toBe('custom')
    expect(draft.time).toBe('09:00')
    expect(draft.custom).toBe('*/5 * * * *')
  })

  it('scheduleDraftIsDirty: an untouched draft is never dirty', () => {
    for (const cron of ['0 9 * * *', '30 10 * * 5', '0 9 * * 1-5', '15 8 3 * *', '*/5 * * * *']) {
      expect(scheduleDraftIsDirty(cronToDraft(cron, ''), cron)).toBe(false)
    }
    // A fresh (unscheduled) card compares against the seeded default.
    expect(scheduleDraftIsDirty(cronToDraft('', ''), '')).toBe(false)
  })

  it('scheduleDraftIsDirty: detects real schedule changes', () => {
    expect(scheduleDraftIsDirty({ ...cronToDraft('0 9 * * *', ''), time: '10:30' }, '0 9 * * *')).toBe(true)
    expect(scheduleDraftIsDirty({ ...cronToDraft('0 9 * * *', ''), kind: 'weekdays' }, '0 9 * * *')).toBe(true)
    expect(scheduleDraftIsDirty({ ...cronToDraft('*/5 * * * *', ''), custom: '*/10 * * * *' }, '*/5 * * * *')).toBe(true)
    // Reverting the only change hides the save button again.
    const draft = cronToDraft('0 9 * * *', '')
    expect(scheduleDraftIsDirty({ ...draft, time: '10:00' }, '0 9 * * *')).toBe(true)
    expect(scheduleDraftIsDirty({ ...draft, time: '09:00' }, '0 9 * * *')).toBe(false)
  })

  it('defaults an unscheduled card to every day at 09:00', () => {
    const draft = cronToDraft('', '')
    expect(draft.kind).toBe('everyDay')
    expect(draft.time).toBe('09:00')
    expect(draftToCron(draft)).toBe('0 9 * * *')
  })
})

describe('cron/expression coexistence (#1)', () => {
  it('seeds kind + custom text from expression || cron', () => {
    const draft = cronToDraft('0 8 * * *', '*/5 * * * *')
    // Kind derives from the effective (expression) schedule, and the custom
    // text matches it instead of the raw cron.
    expect(draft.kind).toBe('custom')
    expect(draft.custom).toBe('*/5 * * * *')
    // Rebuilding the untouched draft reproduces the expression exactly.
    expect(draftToCron(draft)).toBe('*/5 * * * *')
  })

  it('does not report a phantom modification when both fields are present', () => {
    const draft = cronToDraft('0 8 * * *', '*/5 * * * *')
    expect(scheduleDraftIsDirty(draft, '0 8 * * *', '*/5 * * * *')).toBe(false)
  })

  it('detects a real change against the effective expression baseline', () => {
    const draft = cronToDraft('0 8 * * *', '*/5 * * * *')
    expect(scheduleDraftIsDirty({ ...draft, custom: '*/10 * * * *' }, '0 8 * * *', '*/5 * * * *')).toBe(true)
    // Comparing against the stale raw cron would have flipped this to dirty.
    expect(scheduleDraftIsDirty(draft, '0 8 * * *')).toBe(true)
  })

  it('prefers expression when only it is present', () => {
    const draft = cronToDraft('', '0 16 * * 1')
    expect(draft.kind).toBe('weekly')
    expect(draft.dow).toBe(1)
    expect(draft.custom).toBe('0 16 * * 1')
    expect(draftToCron(draft)).toBe('0 16 * * 1')
  })
})

describe('resolveScheduledScope', () => {
  it('defaults to the active project for an empty value', () => {
    expect(resolveScheduledScope('', 'proj-1')).toEqual({ kind: 'active' })
    expect(resolveScheduledScope(undefined, 'proj-1')).toEqual({ kind: 'active' })
    expect(resolveScheduledScope('', undefined)).toEqual({ kind: 'active' })
  })

  it('resolves the global scope', () => {
    expect(resolveScheduledScope('global', 'proj-1')).toEqual({ kind: 'global' })
  })

  it('pins any other non-empty value as a foreign project id', () => {
    expect(resolveScheduledScope('proj-2', 'proj-1')).toEqual({ kind: 'project', projectId: 'proj-2' })
    // Unknown ids stay pinned here; the view drops stale pins separately.
    expect(resolveScheduledScope('deleted-project', 'proj-1')).toEqual({ kind: 'project', projectId: 'deleted-project' })
  })

  it('collapses a pin on the active project back to the active scope', () => {
    expect(resolveScheduledScope('proj-1', 'proj-1')).toEqual({ kind: 'active' })
  })
})
