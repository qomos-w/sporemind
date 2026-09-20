import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { createRoot, type Root } from 'react-dom/client'
import { act } from 'react'
import type { Skill } from '../../../domain/skill-types'
import { AIShellContext } from '../context/AIShellContext'

;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

vi.mock('../../../application/generated-client', () => ({ client: {} }))
vi.mock('../../../gen-clients/workspace/client', () => ({
  listProject: vi.fn().mockResolvedValue({ Items: [] }),
}))
vi.mock('../../../i18n', () => ({ useI18n: () => ({ t: (k: string) => k }) }))

// Use the real skill-card-store module so `resolveSkillOverrides` runs for real;
// only `listSkills` / `getSkill` are stubbed with test fixtures.
vi.mock('../../../application/skill-card-store', async () => {
  const actual = await vi.importActual<typeof import('../../../application/skill-card-store')>('../../../application/skill-card-store')
  return {
    ...actual,
    listSkills: vi.fn(),
    getSkill: vi.fn(),
  }
})
import * as skillCards from '../../../application/skill-card-store'
import { ShellSkillSettings } from './ShellSkillSettings'

function skill(partial: Partial<Skill> & Pick<Skill, 'Id' | 'Name'>): Skill {
  return {
    Source: 'user',
    Version: 1,
    ForkOf: '',
    BuiltinKey: '',
    Description: '',
    Tags: [],
    Tools: [],
    Params: [],
    Context: 'inline',
    Permission: '',
    SubAgentType: '',
    Unit: { model: '', provider: '' },
    Template: '',
    Editable: true,
    Deletable: true,
    ...partial,
  } as Skill
}

describe('ShellSkillSettings external (read-only) skill display', () => {
  let container: HTMLDivElement | null = null
  let root: Root | null = null
  const onOpenCardForEdit = vi.fn()

  beforeEach(() => {
    onOpenCardForEdit.mockClear()
    container = document.createElement('div')
    document.body.appendChild(container)
    root = createRoot(container)
  })

  afterEach(() => {
    if (root && container) {
      act(() => {
        root!.unmount()
      })
      container.remove()
    }
    root = null
    container = null
  })

  async function render() {
    await act(async () => {
      root!.render(
        <AIShellContext.Provider value={{ onOpenCardForEdit } as never}>
          <ShellSkillSettings />
        </AIShellContext.Provider>,
      )
    })
    // Flush the async listSkills + listProject fetches.
    await act(async () => {})
    await act(async () => {})
  }

  it('groups external skills under an external section and marks them read-only', async () => {
    vi.mocked(skillCards.listSkills).mockResolvedValue([
      skill({ Id: 'my-skill', Name: 'My Skill', Source: 'user', Editable: true, Deletable: true }),
      skill({
        Id: 'ext-skill:claude:lint',
        Name: 'Lint',
        Source: 'claude',
        Description: 'External linter',
        Editable: false,
        Deletable: false,
      }),
    ])

    await render()

    const text = container!.textContent ?? ''
    // The external skill is shown.
    expect(text).toContain('Lint')
    // Grouped under the external section header.
    expect(text).toContain('external')
    // Read-only indicator surfaces on the non-editable card.
    expect(text).toContain('skill.tag.readOnly')
    // The external count badge appears.
    expect(text).toContain('external 1')
  })

  it('opens the skill card in the right panel instead of expanding inline', async () => {
    vi.mocked(skillCards.listSkills).mockResolvedValue([
      skill({ Id: 'my-skill', Name: 'My Skill', Source: 'user', Editable: true, Deletable: true }),
    ])

    await render()

    const card = container!.querySelector('.agent-config-card')
    expect(card).not.toBeNull()
    await act(async () => {
      card!.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    })
    expect(onOpenCardForEdit).toHaveBeenCalledWith('skill:my-skill')
    // No inline expanded editor remains in the list.
    expect(container!.querySelector('.agent-config-card--expanded')).toBeNull()
  })

  it('does not show a read-only tag for editable project skills', async () => {
    vi.mocked(skillCards.listSkills).mockResolvedValue([
      skill({ Id: 'my-skill', Name: 'My Skill', Source: 'user', Editable: true, Deletable: true }),
    ])

    await render()

    const text = container!.textContent ?? ''
    expect(text).toContain('My Skill')
    expect(text).not.toContain('skill.tag.readOnly')
  })

  it('hides an external skill overridden by a same-name internal skill', async () => {
    vi.mocked(skillCards.listSkills).mockResolvedValue([
      skill({ Id: 'lint', Name: 'Lint', Source: 'user', Description: 'Project lint' }),
      skill({
        Id: 'ext-skill:claude:lint',
        Name: 'Lint',
        Source: 'claude',
        Description: 'External lint',
        Editable: false,
        Deletable: false,
      }),
    ])

    await render()

    const text = container!.textContent ?? ''
    // Only the winning internal skill remains.
    expect(text).toContain('Project lint')
    // The overridden external card is gone.
    expect(text).not.toContain('External lint')
    expect(text).not.toContain('ext-skill:claude:lint')
  })

  it('resolves all three layers at once (project > system > external) in the display list', async () => {
    vi.mocked(skillCards.listSkills).mockResolvedValue([
      skill({
        Id: 'ext-skill:claude:lint',
        Name: 'Lint',
        Source: 'claude',
        Description: 'External lint',
        Editable: false,
        Deletable: false,
      }),
      skill({ Id: 'lint', Name: 'Lint', Source: 'builtin', Description: 'System lint' }),
      skill({ Id: 'lint', Name: 'Lint', Source: 'user', Description: 'Project lint' }),
    ])

    await render()

    const text = container!.textContent ?? ''
    // Project-internal wins over system and external.
    expect(text).toContain('Project lint')
    expect(text).not.toContain('System lint')
    expect(text).not.toContain('External lint')
  })
})
