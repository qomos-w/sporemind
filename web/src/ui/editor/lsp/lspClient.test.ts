import { describe, it, expect, vi } from 'vitest'
import type { GosporeClient } from '@qomos/gospore-client'
import { LspClient, pathToUri, uriToPath, type Location } from './lspClient'

/**
 * Build a fake GosporeClient whose `invoke` dispatches to per-callable
 * handlers. Each handler returns the raw LSP value that gets serialized into
 * `LspJsonResp.Json`; callables without a handler resolve to `{ Json: '' }`
 * (mirroring the empty responses the backend returns for notifications).
 */
function createMockClient(handlers: Record<string, (req: unknown) => unknown> = {}) {
  const invoke = vi.fn(async (callable: string, req: unknown) => {
    const handler = handlers[callable]
    if (!handler) return { Json: '' }
    const result = await handler(req)
    return {
      Json: result === undefined ? '' : typeof result === 'string' ? result : JSON.stringify(result),
    }
  })
  return { invoke } as unknown as GosporeClient & { invoke: typeof invoke }
}

type MockClient = ReturnType<typeof createMockClient>

function callFor(gospore: MockClient, callable: string) {
  return gospore.invoke.mock.calls.find((c) => c[0] === callable)
}

describe('pathToUri', () => {
  it('preserves an existing file URI', () => {
    expect(pathToUri('file:///foo/bar')).toBe('file:///foo/bar')
  })

  it('converts a unix absolute path', () => {
    expect(pathToUri('/foo/bar')).toBe('file:///foo/bar')
  })

  it('converts a windows backslash path', () => {
    expect(pathToUri('C:\\foo\\bar')).toBe('file:///C:/foo/bar')
  })

  it('converts a windows slash path', () => {
    expect(pathToUri('D:/foo/bar')).toBe('file:///D:/foo/bar')
  })

  it('percent-encodes spaces', () => {
    expect(pathToUri('/foo bar/baz')).toBe('file:///foo%20bar/baz')
  })

  it('converts a relative path', () => {
    expect(pathToUri('src/foo')).toBe('file:///src/foo')
  })
})

describe('LspClient', () => {
  it('initializes the gateway LSP server once on connect', async () => {
    const gospore = createMockClient({ 'lsp.initialize': () => ({ capabilities: {} }) })
    const client = new LspClient({ client: gospore, rootUri: 'file:///project' })

    // Concurrent connects share the in-flight initialization.
    await Promise.all([client.connect(), client.connect()])
    // A later connect is a no-op once initialized.
    await client.connect()

    expect(gospore.invoke).toHaveBeenCalledTimes(1)
    expect(gospore.invoke).toHaveBeenCalledWith(
      'lsp.initialize',
      { RootUri: 'file:///project', Language: 'go' },
      expect.any(Object),
    )
  })

  it('sends didOpen with version 1, languageId and engine language', async () => {
    const gospore = createMockClient()
    const client = new LspClient({ client: gospore, rootUri: 'file:///project' })

    await client.didOpen('/project/main.ts', 'let x = 1')

    const call = callFor(gospore, 'lsp.did_open')!
    expect(call[1]).toEqual({
      Uri: 'file:///project/main.ts',
      LanguageId: 'typescript',
      Text: 'let x = 1',
      Version: 1,
      RootUri: 'file:///project',
      Language: 'typescript',
    })
  })

  it('increments version on didChange and resets on didClose', async () => {
    const gospore = createMockClient()
    const client = new LspClient({ client: gospore, rootUri: 'file:///project' })

    await client.didOpen('/project/main.ts', 'let x = 1')
    await client.didChange('/project/main.ts', 'let x = 2')

    const change = callFor(gospore, 'lsp.did_change')!
    expect(change[1]).toEqual({
      Uri: 'file:///project/main.ts',
      Version: 2,
      Changes: JSON.stringify([{ text: 'let x = 2' }]),
      RootUri: 'file:///project',
      Language: 'typescript',
    })

    await client.didClose('/project/main.ts')
    const close = callFor(gospore, 'lsp.did_close')!
    expect(close[1]).toEqual({ Uri: 'file:///project/main.ts', RootUri: 'file:///project', Language: 'typescript' })

    await client.didOpen('/project/main.ts', 'let y = 1')
    const opens = gospore.invoke.mock.calls.filter((c) => c[0] === 'lsp.did_open')
    expect(opens.at(-1)![1]).toMatchObject({ Version: 1, Text: 'let y = 1' })
  })

  it.each([
    ['/project/main.ts', 'typescript'],
    ['/project/main.tsx', 'typescript'],
    ['/project/main.js', 'typescript'],
    ['/project/main.jsx', 'typescript'],
    ['/project/main.mts', 'typescript'],
    ['/project/main.cts', 'typescript'],
  ])('maps %s to the typescript engine language', async (file, expected) => {
    const gospore = createMockClient()
    const client = new LspClient({ client: gospore, rootUri: 'file:///project' })

    await client.didOpen(file, 'code')

    const call = callFor(gospore, 'lsp.did_open')!
    expect((call[1] as { Language: string }).Language).toBe(expected)
  })

  it('keeps the LSP languageId specific while routing a js file to the typescript engine', async () => {
    const gospore = createMockClient()
    const client = new LspClient({ client: gospore, rootUri: 'file:///project' })

    await client.didOpen('/project/main.js', 'let x = 1')

    const call = callFor(gospore, 'lsp.did_open')!
    expect(call[1]).toMatchObject({
      LanguageId: 'javascript',
      Language: 'typescript',
    })
  })

  it('maps a .go file to the go engine language', async () => {
    const gospore = createMockClient()
    const client = new LspClient({ client: gospore, rootUri: 'file:///project' })

    await client.didOpen('/project/main.go', 'package main')

    const call = callFor(gospore, 'lsp.did_open')!
    expect((call[1] as { Language: string }).Language).toBe('go')
  })

  it('requests definition and normalizes a single Location', async () => {
    const location: Location = {
      uri: 'file:///C:/project/other.ts',
      range: { start: { line: 0, character: 0 }, end: { line: 0, character: 5 } },
    }
    const gospore = createMockClient({ 'lsp.definition': () => location })
    const client = new LspClient({ client: gospore, rootUri: 'file:///project' })

    const result = await client.definition('C:\\project\\main.ts', 3, 5)

    const call = callFor(gospore, 'lsp.definition')!
    expect(call[1]).toEqual({
      Uri: 'file:///C:/project/main.ts',
      Line: 3,
      Character: 5,
      RootUri: 'file:///project',
      Language: 'typescript',
    })
    expect(result).toEqual(location)
  })

  it('normalizes a LocationLink result', async () => {
    const gospore = createMockClient({
      'lsp.definition': () => ({
        targetUri: 'file:///project/other.ts',
        targetRange: { start: { line: 1, character: 0 }, end: { line: 1, character: 5 } },
        targetSelectionRange: { start: { line: 1, character: 2 }, end: { line: 1, character: 5 } },
      }),
    })
    const client = new LspClient({ client: gospore, rootUri: 'file:///project' })

    const result = await client.definition('/project/main.ts', 0, 0)

    expect(result?.uri).toBe('file:///project/other.ts')
    expect(result?.range).toEqual({ start: { line: 1, character: 2 }, end: { line: 1, character: 5 } })
  })

  it('returns null when definition returns null', async () => {
    const gospore = createMockClient({ 'lsp.definition': () => null })
    const client = new LspClient({ client: gospore, rootUri: 'file:///project' })

    const result = await client.definition('/project/main.ts', 0, 0)
    expect(result).toBeNull()
  })

  it('returns null when definition returns an empty array', async () => {
    const gospore = createMockClient({ 'lsp.definition': () => [] })
    const client = new LspClient({ client: gospore, rootUri: 'file:///project' })

    const result = await client.definition('/project/main.ts', 0, 0)
    expect(result).toBeNull()
  })

  it('requests references and normalizes a list of Locations', async () => {
    const refs: Location[] = [
      { uri: 'file:///project/a.ts', range: { start: { line: 0, character: 0 }, end: { line: 0, character: 5 } } },
      { uri: 'file:///project/b.ts', range: { start: { line: 3, character: 2 }, end: { line: 3, character: 8 } } },
    ]
    const gospore = createMockClient({ 'lsp.references': () => refs })
    const client = new LspClient({ client: gospore, rootUri: 'file:///project' })

    const result = await client.references('C:\\project\\main.ts', 3, 5)

    const call = callFor(gospore, 'lsp.references')!
    expect(call[1]).toEqual({
      Uri: 'file:///C:/project/main.ts',
      Line: 3,
      Character: 5,
      IncludeDeclaration: false,
      RootUri: 'file:///project',
      Language: 'typescript',
    })
    expect(result).toEqual(refs)
  })

  it('passes includeDeclaration when requested', async () => {
    const gospore = createMockClient({ 'lsp.references': () => [] })
    const client = new LspClient({ client: gospore, rootUri: 'file:///project' })

    await client.references('/project/main.ts', 1, 2, true)

    const call = callFor(gospore, 'lsp.references')!
    expect(call[1]).toMatchObject({ IncludeDeclaration: true })
  })

  it('filters out null and LocationLink entries from references', async () => {
    const gospore = createMockClient({
      'lsp.references': () => [
        { uri: 'file:///project/a.ts', range: { start: { line: 0, character: 0 }, end: { line: 0, character: 5 } } },
        null,
        {
          targetUri: 'file:///project/b.ts',
          targetRange: { start: { line: 1, character: 0 }, end: { line: 1, character: 5 } },
          targetSelectionRange: { start: { line: 1, character: 2 }, end: { line: 1, character: 5 } },
        },
        'junk',
      ],
    })
    const client = new LspClient({ client: gospore, rootUri: 'file:///project' })

    const result = await client.references('/project/main.ts', 0, 0)

    expect(result).toHaveLength(2)
    expect(result[0]!.uri).toBe('file:///project/a.ts')
    // LocationLink is normalized to a Location using the target URIs.
    expect(result[1]).toEqual({
      uri: 'file:///project/b.ts',
      range: { start: { line: 1, character: 2 }, end: { line: 1, character: 5 } },
    })
  })

  it('returns an empty array when references returns an empty array', async () => {
    const gospore = createMockClient({ 'lsp.references': () => [] })
    const client = new LspClient({ client: gospore, rootUri: 'file:///project' })

    const result = await client.references('/project/main.ts', 0, 0)
    expect(result).toEqual([])
  })

  it('propagates gateway errors from references', async () => {
    const gospore = createMockClient({
      'lsp.initialize': () => ({ capabilities: {} }),
      'lsp.references': () => {
        throw new Error('lsp.references: gopls error')
      },
    })
    const client = new LspClient({ client: gospore, rootUri: 'file:///project' })

    await expect(client.references('/project/main.ts', 0, 0)).rejects.toThrow('gopls error')
  })

  it('rejects connect when initialize fails and allows retry', async () => {
    const gospore = createMockClient()
    gospore.invoke.mockRejectedValueOnce(new Error('lsp.initialize: refused'))
    const client = new LspClient({ client: gospore, rootUri: 'file:///project' })

    await expect(client.connect()).rejects.toThrow('refused')
    await expect(client.connect()).resolves.toBeUndefined()
    expect(gospore.invoke).toHaveBeenCalledTimes(2)
  })

  it('propagates gateway errors from definition', async () => {
    const gospore = createMockClient({
      'lsp.initialize': () => ({ capabilities: {} }),
      'lsp.definition': () => {
        throw new Error('lsp.definition: gopls error')
      },
    })
    const client = new LspClient({ client: gospore, rootUri: 'file:///project' })

    await expect(client.definition('/project/main.ts', 0, 0)).rejects.toThrow('gopls error')
  })

  it('disconnect resets per-document state and re-initializes on next use', async () => {
    const gospore = createMockClient({ 'lsp.initialize': () => ({ capabilities: {} }) })
    const client = new LspClient({ client: gospore, rootUri: 'file:///project' })

    await client.didOpen('/project/main.ts', 'let x = 1')
    client.disconnect()
    await client.didOpen('/project/main.ts', 'let y = 1')

    const inits = gospore.invoke.mock.calls.filter((c) => c[0] === 'lsp.initialize')
    expect(inits).toHaveLength(2)
    const opens = gospore.invoke.mock.calls.filter((c) => c[0] === 'lsp.did_open')
    expect(opens.at(-1)![1]).toMatchObject({ Version: 1, Text: 'let y = 1' })
  })

  it('warms up a document with the correct uri, rootUri and language', async () => {
    const gospore = createMockClient()
    const client = new LspClient({ client: gospore, rootUri: 'file:///project' })

    await client.warmUp('/project/main.ts')

    const call = callFor(gospore, 'lsp.warm_up')!
    expect(call[1]).toEqual({
      Uri: 'file:///project/main.ts',
      RootUri: 'file:///project',
      Language: 'typescript',
    })
  })

  it('didOpen fires a best-effort warm-up after open', async () => {
    const gospore = createMockClient()
    const client = new LspClient({ client: gospore, rootUri: 'file:///project' })

    await client.didOpen('/project/main.ts', 'let x = 1')
    // The warm-up is fire-and-forget; give the microtask queue a tick.
    await new Promise((resolve) => setTimeout(resolve, 0))

    const call = callFor(gospore, 'lsp.warm_up')
    expect(call).toBeTruthy()
  })
})

describe('uriToPath', () => {
  it('returns the input for non-file URIs', () => {
    expect(uriToPath('https://example.com/foo')).toBe('https://example.com/foo')
  })

  it('converts a unix absolute file URI', () => {
    expect(uriToPath('file:///foo/bar')).toBe('/foo/bar')
  })

  it('converts a windows file URI with drive letter', () => {
    expect(uriToPath('file:///C:/foo/bar')).toBe('C:/foo/bar')
  })

  it('decodes percent-encoded characters', () => {
    expect(uriToPath('file:///foo%20bar')).toBe('/foo bar')
  })

  it('roundtrips with pathToUri for unix absolute', () => {
    const original = '/project/main.ts'
    expect(uriToPath(pathToUri(original))).toBe(original)
  })

  it('roundtrips with pathToUri for windows paths', () => {
    const original = 'C:/project/main.ts'
    expect(uriToPath(pathToUri(original))).toBe(original)
  })
})
