import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import type { ReactElement } from 'react'
import { DbManagerSettings } from './DbManagerSettings'
import { I18nProvider } from '../../../i18n/provider'

vi.mock('../../../application/generated-client', () => ({
  client: { invoke: vi.fn() },
}))

vi.mock('../../../gen-clients/dbmanager/client', () => ({
  profileList: vi.fn(),
  profileGet: vi.fn(),
  profileSave: vi.fn(),
  profileRemove: vi.fn(),
}))

vi.mock('../../../gen-clients/sshmanager/client', () => ({
  hostList: vi.fn(),
}))

import * as dbApi from '../../../gen-clients/dbmanager/client'
import * as sshApi from '../../../gen-clients/sshmanager/client'
import type { DbProfileView } from '../../../gen-types/dbmanager'

const renderView = (ui: ReactElement) => render(<I18nProvider initialLocale="en-US">{ui}</I18nProvider>)

function profile(p: Partial<DbProfileView> & Pick<DbProfileView, 'Id' | 'Name' | 'Backend'>): DbProfileView {
  return { HasPassword: false, HasSecret: false, HasToken: false, ...p }
}

const PROFILES = {
  Items: [
    profile({ Id: 'p1', Name: 'local-pg', Backend: 'postgres', Endpoint: '127.0.0.1:5432', HasPassword: true }),
    profile({ Id: 'p2', Name: 'cache', Backend: 'redis' }),
  ],
}

describe('DbManagerSettings', () => {
  beforeEach(() => {
    vi.mocked(dbApi.profileList).mockReset().mockResolvedValue(PROFILES)
    vi.mocked(dbApi.profileGet).mockReset()
    vi.mocked(dbApi.profileSave).mockReset().mockResolvedValue({
      Profile: profile({ Id: 'p1', Name: 'local-postgres', Backend: 'postgres' }),
    })
    vi.mocked(dbApi.profileRemove).mockReset().mockResolvedValue({})
    vi.mocked(sshApi.hostList).mockReset().mockResolvedValue({ Items: [], Groups: [] })
  })

  it('lists profiles from dbmanager', async () => {
    renderView(<DbManagerSettings />)
    await waitFor(() => expect(screen.getByText('local-pg')).toBeTruthy())
    expect(screen.getByText('cache')).toBeTruthy()
  })

  it('opens the edit modal in the shared Modal chrome and saves trimmed fields', async () => {
    vi.mocked(dbApi.profileGet).mockResolvedValue({
      Profile: profile({ Id: 'p1', Name: 'local-pg', Backend: 'postgres', Endpoint: '127.0.0.1:5432' }),
    })
    renderView(<DbManagerSettings />)
    await waitFor(() => expect(screen.getByText('local-pg')).toBeTruthy())

    fireEvent.click(screen.getAllByTitle('Edit').at(0)!)
    const dialog = await waitFor(() => screen.getByRole('dialog'))
    expect(screen.getByText('Edit connection')).toBeTruthy()
    // The shadcn Select trigger shows the backend label of the loaded profile.
    expect(dialog.textContent).toContain('PostgreSQL')

    fireEvent.change(screen.getByDisplayValue('local-pg'), { target: { value: '  local-postgres  ' } })
    fireEvent.click(screen.getByRole('button', { name: /Save/i }))

    await waitFor(() =>
      expect(dbApi.profileSave).toHaveBeenCalledWith(
        expect.anything(),
        expect.objectContaining({ Id: 'p1', Name: 'local-postgres', Backend: 'postgres' }),
      ),
    )
  })

  it('confirms delete through the modal and calls profileRemove', async () => {
    renderView(<DbManagerSettings />)
    await waitFor(() => expect(screen.getByText('local-pg')).toBeTruthy())

    fireEvent.click(screen.getAllByTitle('Delete').at(0)!)
    await waitFor(() => screen.getByRole('dialog'))
    // Rows are sorted by name, so "cache" (p2) is the first delete target.
    expect(screen.getByText('Delete connection "cache"? This cannot be undone.')).toBeTruthy()

    // Footer confirm button is the only "Delete" button inside the dialog.
    fireEvent.click(screen.getAllByRole('button', { name: /Delete/i }).at(-1)!)
    await waitFor(() => expect(dbApi.profileRemove).toHaveBeenCalledWith(expect.anything(), { Id: 'p2' }))
  })
})
