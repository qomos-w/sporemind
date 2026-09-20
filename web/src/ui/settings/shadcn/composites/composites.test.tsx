import '@testing-library/jest-dom/vitest'
import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { userEvent } from '@testing-library/user-event'

import { SettingRow } from './SettingRow'
import { FeatureCard } from './FeatureCard'
import { SettingsNav, type NavGroup } from './SettingsNav'
import { ThemePreview } from './ThemePreview'

import { Switch } from '../ui/switch'
import { Input } from '../ui/input'

// ---- data-guide-id query locator (mirrors ui-interactor.ts) ----
function q(guideId: string) {
  return document.querySelector(`[data-guide-id="${guideId}"]`)
}

// =============================================================================
// SettingRow
// =============================================================================
describe('SettingRow', () => {
  it('renders horizontal layout: label/description left, control right', () => {
    render(
      <SettingRow
        data-guide-id="row.h"
        label="Enable"
        description="Toggle it"
        control={<Switch data-guide-id="row.h.control" />}
      />,
    )
    const row = q('row.h')
    expect(row).toBeInTheDocument()
    expect(row).toHaveClass('flex-row')
    expect(screen.getByText('Enable')).toBeInTheDocument()
    expect(screen.getByText('Toggle it')).toBeInTheDocument()
    expect(q('row.h.control')).toBeInTheDocument()
  })

  it('renders vertical layout: label/description above, control below', () => {
    render(
      <SettingRow
        data-guide-id="row.v"
        htmlFor="row.v.input"
        label="Name"
        description="Your name"
      >
        <Input id="row.v.input" data-guide-id="row.v.control" />
      </SettingRow>,
    )
    const row = q('row.v')
    expect(row).toBeInTheDocument()
    expect(row).toHaveClass('flex-col')
    // label is associated with the control id
    expect(screen.getByText('Name')).toHaveAttribute('for', 'row.v.input')
    expect(screen.getByText('Your name')).toBeInTheDocument()
    expect(q('row.v.control')).toBeInTheDocument()
  })

  it('passes rest props to the root element', () => {
    render(
      <SettingRow
        label="X"
        control={<Switch />}
        className="custom-row-class"
      />,
    )
    expect(screen.getByText('X').parentElement!.parentElement).toHaveClass('custom-row-class')
  })
})

// =============================================================================
// FeatureCard
// =============================================================================
describe('FeatureCard', () => {
  it('renders header/content/footer structure', () => {
    render(
      <FeatureCard
        icon={<span data-testid="fc.icon">*</span>}
        title="Title"
        description="Desc"
        footer={<span>Footer</span>}
      >
        Body
      </FeatureCard>,
    )
    expect(screen.getByText('Title')).toBeInTheDocument()
    expect(screen.getByText('Desc')).toBeInTheDocument()
    expect(screen.getByText('Body')).toBeInTheDocument()
    expect(screen.getByText('Footer')).toBeInTheDocument()
    expect(screen.getByTestId('fc.icon')).toBeInTheDocument()
  })

  it('passes rest props to the root card', () => {
    render(<FeatureCard title="T" data-guide-id="fc.1" />)
    expect(q('fc.1')).toBeInTheDocument()
  })
})

// =============================================================================
// SettingsNav
// =============================================================================
describe('SettingsNav', () => {
  const groups: NavGroup[] = [
    {
      label: 'GENERAL',
      items: [
        { id: 'general', label: 'General' },
        { id: 'animation', label: 'Animation', icon: <span data-testid="nav.icon">*</span> },
      ],
    },
    {
      label: 'ACCOUNT',
      items: [{ id: 'account', label: 'Account' }],
    },
  ]

  it('renders groups with titles and items', () => {
    render(<SettingsNav groups={groups} activeId="general" onNavigate={() => {}} />)
    expect(screen.getByText('GENERAL')).toBeInTheDocument()
    expect(screen.getByText('ACCOUNT')).toBeInTheDocument()
    expect(screen.getByText('General')).toBeInTheDocument()
    expect(screen.getByText('Account')).toBeInTheDocument()
    expect(screen.getByTestId('nav.icon')).toBeInTheDocument()
  })

  it('sets data-guide-id on each item with / layered format', () => {
    render(<SettingsNav groups={groups} activeId="general" onNavigate={() => {}} />)
    expect(q('settings/category/general')).toBeInTheDocument()
    expect(q('settings/category/animation')).toBeInTheDocument()
    expect(q('settings/category/account')).toBeInTheDocument()
  })

  it('highlights the active item', () => {
    render(<SettingsNav groups={groups} activeId="animation" onNavigate={() => {}} />)
    expect(q('settings/category/animation')).toHaveClass('bg-sidebar-accent')
    expect(q('settings/category/general')).not.toHaveClass('bg-sidebar-accent')
  })

  it('fires onNavigate on item click', async () => {
    const fn = vi.fn()
    render(<SettingsNav groups={groups} activeId="general" onNavigate={fn} />)
    await userEvent.click(q('settings/category/account')!)
    expect(fn).toHaveBeenCalledWith('account')
  })
})

// =============================================================================
// ThemePreview
// =============================================================================
describe('ThemePreview', () => {
  it('renders both mode buttons with data-guide-id on the group', () => {
    render(<ThemePreview value="light" onChange={() => {}} />)
    expect(q('settings/general/theme-mode')).toBeInTheDocument()
    expect(screen.getByText('Light')).toBeInTheDocument()
    expect(screen.getByText('Dark')).toBeInTheDocument()
  })

  it('fires onChange with the clicked mode', async () => {
    const fn = vi.fn()
    render(<ThemePreview value="light" onChange={fn} />)
    await userEvent.click(screen.getByText('Dark'))
    expect(fn).toHaveBeenCalledWith('dark')
    await userEvent.click(screen.getByText('Light'))
    expect(fn).toHaveBeenCalledWith('light')
    expect(fn).toHaveBeenCalledTimes(2)
  })
})