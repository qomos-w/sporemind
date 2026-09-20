import { describe, it, expect } from 'vitest'
import './agentPromptInspectorRegistration'
import { getInspectorComponent } from './inspectorRegistry'

describe('agentPromptInspectorRegistration', () => {
  it('registers prompt-context component', () => {
    expect(getInspectorComponent('prompt-context')).toBeDefined()
  })

  it('registers compiled-prompt component', () => {
    expect(getInspectorComponent('compiled-prompt')).toBeDefined()
  })

  it('registers turn-history component', () => {
    expect(getInspectorComponent('turn-history')).toBeDefined()
  })

  it('registers compaction-snapshot component', () => {
    expect(getInspectorComponent('compaction-snapshot')).toBeDefined()
  })

  it('registers context-budget component', () => {
    expect(getInspectorComponent('context-budget')).toBeDefined()
  })

  it('registers component-snapshot component', () => {
    expect(getInspectorComponent('component-snapshot')).toBeDefined()
  })
})
