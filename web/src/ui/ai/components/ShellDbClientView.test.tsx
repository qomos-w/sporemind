import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import type { ReactElement } from 'react'
import { ShellDbClientView } from './ShellDbClientView'
import { I18nProvider } from '../../../i18n/provider'

// Mock the gospore client + generated dbclient client so the component runs
// without a real backend.
vi.mock('../../../application/generated-client', () => ({
  client: { invoke: vi.fn() },
}))

vi.mock('../../../gen-clients/dbclient/client', () => ({
  tree: vi.fn(),
  read: vi.fn(),
  query: vi.fn(),
  dialTest: vi.fn(),
  close: vi.fn(),
  describe: vi.fn(),
}))

import * as dbclientApi from '../../../gen-clients/dbclient/client'

const PROPS = {
  profileId: 'p1',
  profileName: 'Local MySQL',
  backend: 'mysql',
}

const renderView = (ui: ReactElement) => render(<I18nProvider initialLocale="en-US">{ui}</I18nProvider>)

const DB_NODES = { Nodes: [{ Path: 'mydb', Kind: 'database', Label: 'mydb' }], HasMore: false }
const TABLE_NODES = { Nodes: [{ Path: 'mydb.users', Kind: 'table', Label: 'users' }], HasMore: false }

describe('ShellDbClientView', () => {
  beforeEach(() => {
    vi.mocked(dbclientApi.tree).mockReset()
    vi.mocked(dbclientApi.read).mockReset()
    vi.mocked(dbclientApi.query).mockReset()
    vi.mocked(dbclientApi.dialTest).mockReset().mockResolvedValue({ Ok: true, LatencyMs: 5, ServerVersion: '8.0.36' })
    vi.mocked(dbclientApi.describe).mockReset()
  })

  it('renders the header with profile name and backend badge', () => {
    vi.mocked(dbclientApi.tree).mockResolvedValue({ Nodes: [], HasMore: false })
    renderView(<ShellDbClientView {...PROPS} />)
    expect(screen.getByText('Local MySQL')).toBeTruthy()
    expect(screen.getByText('mysql')).toBeTruthy()
  })

  it('dials and loads the tree root when the tab becomes active', async () => {
    vi.mocked(dbclientApi.tree).mockResolvedValue({ Nodes: [], HasMore: false })
    renderView(<ShellDbClientView {...PROPS} isActive />)
    await waitFor(() => expect(dbclientApi.dialTest).toHaveBeenCalledWith(expect.anything(), { ProfileId: 'p1' }))
    await waitFor(() => expect(dbclientApi.tree).toHaveBeenCalledWith(expect.anything(), expect.objectContaining({ ProfileId: 'p1', Path: '' })))
  })

  it('does not dial while inactive', () => {
    vi.mocked(dbclientApi.tree).mockResolvedValue({ Nodes: [], HasMore: false })
    renderView(<ShellDbClientView {...PROPS} isActive={false} />)
    expect(dbclientApi.dialTest).not.toHaveBeenCalled()
    expect(dbclientApi.tree).not.toHaveBeenCalled()
  })

  it('expand db → select table → reads data with limit and renders rows', async () => {
    vi.mocked(dbclientApi.tree)
      .mockResolvedValueOnce(DB_NODES)
      .mockResolvedValueOnce(TABLE_NODES)
    vi.mocked(dbclientApi.read).mockResolvedValue({ Columns: ['id', 'name'], Rows: [['1', 'ada'], ['2', 'bob']], Truncated: false })
    vi.mocked(dbclientApi.describe).mockResolvedValue({ Columns: [], Indexes: [] })

    renderView(<ShellDbClientView {...PROPS} isActive />)
    await waitFor(() => screen.getByText('mydb'))
    fireEvent.click(screen.getByText('mydb'))
    await waitFor(() => expect(dbclientApi.tree).toHaveBeenCalledWith(expect.anything(), expect.objectContaining({ Path: 'mydb' })))
    fireEvent.click(screen.getByText('users'))
    await waitFor(() => expect(dbclientApi.read).toHaveBeenCalledWith(expect.anything(), expect.objectContaining({ ProfileId: 'p1', Path: 'mydb.users', Limit: 200 })))
    await waitFor(() => expect(screen.getByText('ada')).toBeTruthy())
    expect(screen.getByText(/2 rows/)).toBeTruthy()
  })

  it('table selection triggers describe and the structure tab renders columns + indexes', async () => {
    vi.mocked(dbclientApi.tree)
      .mockResolvedValueOnce(DB_NODES)
      .mockResolvedValueOnce(TABLE_NODES)
    vi.mocked(dbclientApi.read).mockResolvedValue({ Columns: [], Rows: [], Truncated: false })
    vi.mocked(dbclientApi.describe).mockResolvedValue({
      Columns: [{ Name: 'id', DataType: 'bigint', Nullable: false, Key: 'PRI' }],
      Indexes: [{ Name: 'PRIMARY', Columns: 'id', Unique: true }],
    })

    renderView(<ShellDbClientView {...PROPS} isActive />)
    await waitFor(() => screen.getByText('mydb'))
    fireEvent.click(screen.getByText('mydb'))
    await waitFor(() => screen.getByText('users'))
    fireEvent.click(screen.getByText('users'))
    await waitFor(() => expect(dbclientApi.describe).toHaveBeenCalledWith(expect.anything(), expect.objectContaining({ ProfileId: 'p1', Path: 'mydb.users' })))

    fireEvent.click(screen.getByRole('button', { name: 'Structure' }))
    await waitFor(() => expect(screen.getByText('bigint')).toBeTruthy())
    expect(screen.getByText('PRIMARY')).toBeTruthy()
  })

  it('runs the query tab through dbclient.query with sql mode (Ctrl+Enter path)', async () => {
    vi.mocked(dbclientApi.tree).mockResolvedValue({ Nodes: [], HasMore: false })
    vi.mocked(dbclientApi.query).mockResolvedValue({ Columns: ['n'], Rows: [['7']], Truncated: true, Message: 'rows clipped' })

    renderView(<ShellDbClientView {...PROPS} isActive />)
    fireEvent.click(screen.getByRole('button', { name: 'Query' }))
    const area = screen.getByRole('textbox', { name: 'Query' })
    fireEvent.change(area, { target: { value: 'SELECT 7' } })
    fireEvent.keyDown(area, { key: 'Enter', ctrlKey: true })
    await waitFor(() => expect(dbclientApi.query).toHaveBeenCalledWith(expect.anything(), expect.objectContaining({ ProfileId: 'p1', Text: 'SELECT 7', Mode: 'sql' })))
    await waitFor(() => expect(screen.getByText('7')).toBeTruthy())
    expect(screen.getByText(/truncated/i)).toBeTruthy()
    expect(screen.getByText('rows clipped')).toBeTruthy()
  })

  it('mongo backend: structure tab disabled, query mode json, describe never called', async () => {
    vi.mocked(dbclientApi.tree).mockResolvedValue({ Nodes: [], HasMore: false })
    vi.mocked(dbclientApi.query).mockResolvedValue({ Columns: [], Rows: [], Truncated: false })

    renderView(<ShellDbClientView {...PROPS} backend="mongo" isActive />)
    expect((screen.getByRole('button', { name: 'Structure' }) as HTMLButtonElement).disabled).toBe(true)
    fireEvent.click(screen.getByRole('button', { name: 'Query' }))
    fireEvent.change(screen.getByRole('textbox', { name: 'Query' }), { target: { value: '{"collection":"x","filter":{}}' } })
    fireEvent.click(screen.getByRole('button', { name: /^Run$/i }))
    await waitFor(() => expect(dbclientApi.query).toHaveBeenCalledWith(expect.anything(), expect.objectContaining({ Mode: 'json' })))
    expect(dbclientApi.describe).not.toHaveBeenCalled()
  })

  it('shows a load-more control when the tree root page has more', async () => {
    vi.mocked(dbclientApi.tree).mockResolvedValue({ Nodes: DB_NODES.Nodes, Cursor: '42', HasMore: true })
    renderView(<ShellDbClientView {...PROPS} isActive />)
    const more = await screen.findByText(/load more/i)
    fireEvent.click(more)
    await waitFor(() => expect(dbclientApi.tree).toHaveBeenCalledWith(expect.anything(), expect.objectContaining({ Cursor: '42' })))
  })
})
