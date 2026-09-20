import { describe, expect, it } from 'vitest'
import { agentAvatarLabel } from './agent-avatar'

describe('agentAvatarLabel', () => {
  it('uses display name initials first', () => {
    expect(agentAvatarLabel('Build Agent', 'coder')).toBe('BA')
  })

  it('uses agent kind initials when display name is absent', () => {
    expect(agentAvatarLabel('', 'architect')).toBe('AR')
  })

  it('uses the generic fallback when both are absent', () => {
    expect(agentAvatarLabel(undefined, undefined)).toBe('AG')
  })
})
