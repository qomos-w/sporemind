import type { GosporeClient } from '@qomos/gospore-client'
import * as gateway from '../../../gen-clients/lsp/client'
import { subscribeService } from '../../../gen-clients/gospore.events/client'

export interface Position {
  line: number
  character: number
}

export interface Range {
  start: Position
  end: Position
}

export interface Location {
  uri: string
  range: Range
}

export interface LspDiagnostic {
  range: Range
  severity?: number // 1=Error 2=Warning 3=Information 4=Hint
  code?: string
  source?: string
  message: string
}

export type DiagnosticsHandler = (uri: string, diagnostics: LspDiagnostic[]) => void

interface LocationLink {
  originSelectionRange?: Range
  targetUri: string
  targetRange: Range
  targetSelectionRange?: Range
}

function isLocation(value: unknown): value is Location {
  return (
    !!value &&
    typeof value === 'object' &&
    'uri' in value &&
    typeof (value as Location).uri === 'string' &&
    'range' in value &&
    !!(value as Location).range
  )
}

function isLocationLink(value: unknown): value is LocationLink {
  return !!value && typeof value === 'object' && 'targetUri' in value && typeof (value as LocationLink).targetUri === 'string'
}

function normalizeDefinitionResult(result: unknown): Location | null {
  if (result == null) return null
  if (Array.isArray(result)) {
    if (result.length === 0) return null
    return normalizeSingle(result[0] as unknown)
  }
  return normalizeSingle(result as unknown)
}

function normalizeSingle(value: unknown): Location | null {
  if (isLocation(value)) return value
  if (isLocationLink(value)) {
    return {
      uri: value.targetUri,
      range: value.targetSelectionRange ?? value.targetRange,
    }
  }
  return null
}

function normalizeReferencesResult(result: unknown): Location[] {
  if (result == null || !Array.isArray(result)) return []
  const locations: Location[] = []
  for (const item of result) {
    const location = normalizeSingle(item)
    if (location) locations.push(location)
  }
  return locations
}

export function pathToUri(path: string): string {
  // Already a URI with a scheme.
  if (/^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//.test(path)) {
    return path
  }

  const normalized = path.replace(/\\/g, '/')

  const winMatch = normalized.match(/^([a-zA-Z]):(\/.*)?$/)
  if (winMatch) {
    // gopls's URIFromPath upper-cases the drive letter; match that so didOpen/
    // query URIs compare equal to package-file URIs inside gopls.
    const drive = winMatch[1]!.toUpperCase()
    const rest = winMatch[2] ?? ''
    return `file:///${drive}:${rest.split('/').map(encodeURIComponent).join('/')}`
  }

  if (normalized.startsWith('/')) {
    return `file://${normalized.split('/').map(encodeURIComponent).join('/')}`
  }

  return `file:///${normalized.split('/').map(encodeURIComponent).join('/')}`
}

export function uriToPath(uri: string): string {
  if (!uri.startsWith('file://')) return uri

  let path = uri.slice('file://'.length).replace(/^\/+/, '')
  path = decodeURIComponent(path)

  if (/^[a-zA-Z]:[\\/]/.test(path)) {
    return path.replace(/\\/g, '/')
  }
  return '/' + path
}

function extensionOf(uri: string): string {
  const match = uri.match(/\.([^.]+)$/)
  return match?.[1]?.toLowerCase() ?? ''
}

function languageIdFromUri(uri: string): string {
  const ext = extensionOf(uri)
  switch (ext) {
    case 'ts':
    case 'tsx':
    case 'mts':
    case 'cts':
      return 'typescript'
    case 'js':
    case 'jsx':
    case 'mjs':
    case 'cjs':
      return 'javascript'
    case 'go':
      return 'go'
    case 'py':
    case 'pyw':
      return 'python'
    case 'rs':
      return 'rust'
    case 'java':
      return 'java'
    case 'c':
    case 'cc':
    case 'cxx':
    case 'cpp':
    case 'h':
    case 'hpp':
      return 'cpp'
    case 'md':
    case 'markdown':
      return 'markdown'
    case 'json':
      return 'json'
    case 'html':
    case 'htm':
      return 'html'
    case 'css':
    case 'scss':
    case 'sass':
      return 'css'
    case 'sql':
      return 'sql'
    case 'xml':
    case 'svg':
      return 'xml'
    case 'yaml':
    case 'yml':
      return 'yaml'
    default:
      return ext || 'plaintext'
  }
}

/**
 * Engine language sent in the `Language` field of every lsp.* request. All
 * TypeScript/JavaScript variants (.ts, .tsx, .js, .jsx, .mts, .cts) map to
 * the `typescript` engine so the backend routes them to the TypeScript
 * language server; every other file falls back to its standard LSP languageId.
 */
function lspLanguageFromUri(uri: string): string {
  const ext = extensionOf(uri)
  switch (ext) {
    case 'ts':
    case 'tsx':
    case 'js':
    case 'jsx':
    case 'mts':
    case 'cts':
      return 'typescript'
    default:
      return languageIdFromUri(uri)
  }
}

/**
 * Deserialize the `Json` field of an {@link LspJsonResp} gateway response into
 * a raw LSP value. Returns `null` for empty or unparseable payloads so callers
 * can feed it straight into the existing normalization helpers.
 */
function parseJsonResp(json: string): unknown {
  if (!json) return null
  try {
    return JSON.parse(json)
  } catch (err) {
    console.error('[lsp] failed to parse gateway JSON response:', json, err)
    return null
  }
}

export interface LspClientOptions {
  /** Shared gateway client used to invoke `lsp.*` callables. */
  client: GosporeClient
  /** Workspace root URI passed to `lsp.initialize`. */
  rootUri?: string | null
}

/**
 * Thin client over the `lsp` gateway callables (lsp.definition, lsp.references,
 * lsp.did_open, lsp.did_change, lsp.did_close, lsp.warm_up, lsp.initialize). It
 * owns no
 * WebSocket — every request flows through the shared {@link GosporeClient} and
 * the response `LspJsonResp.Json` is deserialized back into the local LSP
 * types.
 *
 * Every request carries a `Language` field derived from the document URI via
 * {@link lspLanguageFromUri} so the backend can select the correct LSP engine
 * (gopls for Go, typescript-language-server for TypeScript/JavaScript, etc.).
 *
 * Diagnostics computed by the LSP engine are delivered as `lsp.diagnostics`
 * events; register a handler via {@link onDiagnostics} to receive them.
 */
export class LspClient {
  private readonly gospore: GosporeClient
  private readonly rootUri: string | null
  private versions = new Map<string, number>()
  private readyByLanguage = new Map<string, Promise<void>>()

  private diagHandlers = new Set<DiagnosticsHandler>()
  private diagActive = false

  constructor(options: LspClientOptions) {
    this.gospore = options.client
    this.rootUri = options.rootUri ?? null
  }

  /**
   * Ensure the LSP engine for the given language is initialized for the
   * configured workspace root. Idempotent per language: concurrent calls share
   * the in-flight initialization and later calls are no-ops once it has
   * completed. A failed initialization resets so the next call can retry.
   * When called without a language, it defaults to "go" for backward
   * compatibility.
   */
  async connect(language?: string): Promise<void> {
    return this.ensureInitialized(language ?? 'go')
  }

  private async ensureInitialized(language: string): Promise<void> {
    let p = this.readyByLanguage.get(language)
    if (p) return p
    p = this.initialize(language).catch((err) => {
      this.readyByLanguage.delete(language)
      throw err
    })
    this.readyByLanguage.set(language, p)
    return p
  }

  private async initialize(language: string): Promise<void> {
    await gateway.initialize(this.gospore, { RootUri: this.rootUri ?? '', Language: language })
  }

  async definition(uri: string, line: number, char: number): Promise<Location | null> {
    const docUri = pathToUri(uri)
    await this.ensureInitialized(lspLanguageFromUri(docUri))
    const resp = await gateway.definition(this.gospore, {
      Uri: docUri,
      Line: line,
      Character: char,
      RootUri: this.rootUri ?? '',
      Language: lspLanguageFromUri(docUri),
    })
    return normalizeDefinitionResult(parseJsonResp(resp.Json))
  }

  async references(uri: string, line: number, char: number, includeDeclaration = false): Promise<Location[]> {
    const docUri = pathToUri(uri)
    await this.ensureInitialized(lspLanguageFromUri(docUri))
    const resp = await gateway.references(this.gospore, {
      Uri: docUri,
      Line: line,
      Character: char,
      IncludeDeclaration: includeDeclaration,
      RootUri: this.rootUri ?? '',
      Language: lspLanguageFromUri(docUri),
    })
    return normalizeReferencesResult(parseJsonResp(resp.Json))
  }

  async didOpen(uri: string, content: string): Promise<void> {
    const docUri = pathToUri(uri)
    await this.ensureInitialized(lspLanguageFromUri(docUri))
    this.versions.set(docUri, 1)
    await gateway.didOpen(this.gospore, {
      Uri: docUri,
      LanguageId: languageIdFromUri(docUri),
      Text: content,
      Version: 1,
      RootUri: this.rootUri ?? '',
      Language: lspLanguageFromUri(docUri),
    })
    void this.warmUp(uri).catch(() => {
      // Warm-up is best-effort; failures must not crash didOpen.
    })
  }

  async didChange(uri: string, content: string): Promise<void> {
    const docUri = pathToUri(uri)
    await this.ensureInitialized(lspLanguageFromUri(docUri))
    const version = (this.versions.get(docUri) ?? 0) + 1
    this.versions.set(docUri, version)
    await gateway.didChange(this.gospore, {
      Uri: docUri,
      Version: version,
      Changes: JSON.stringify([{ text: content }]),
      RootUri: this.rootUri ?? '',
      Language: lspLanguageFromUri(docUri),
    })
  }

  async didClose(uri: string): Promise<void> {
    const docUri = pathToUri(uri)
    await this.ensureInitialized(lspLanguageFromUri(docUri))
    this.versions.delete(docUri)
    await gateway.didClose(this.gospore, {
      Uri: docUri,
      RootUri: this.rootUri ?? '',
      Language: lspLanguageFromUri(docUri),
    })
  }

  async warmUp(uri: string): Promise<void> {
    const docUri = pathToUri(uri)
    await this.ensureInitialized(lspLanguageFromUri(docUri))
    await gateway.warmUp(this.gospore, {
      Uri: docUri,
      RootUri: this.rootUri ?? '',
      Language: lspLanguageFromUri(docUri),
    })
  }

  /**
   * Register a handler that receives diagnostics for any document. The
   * subscription starts on the first registration and stops on the last
   * unregistration. Returns an unsubscribe function.
   */
  onDiagnostics(handler: DiagnosticsHandler): () => void {
    this.diagHandlers.add(handler)
    if (!this.diagActive) {
      this.diagActive = true
      void this.runDiagnosticsSubscription()
    }
    return () => {
      this.diagHandlers.delete(handler)
      if (this.diagHandlers.size === 0) {
        this.diagActive = false
      }
    }
  }

  private async runDiagnosticsSubscription(): Promise<void> {
    try {
      for await (const ev of subscribeService(this.gospore, { serviceName: 'lsp', kind: 'lsp.diagnostics' })) {
        if (!this.diagActive) break
        const event = ev as { Uri: string; Version: number; Diagnostics: string }
        let diags: LspDiagnostic[] = []
        try {
          diags = JSON.parse(event.Diagnostics || '[]') as LspDiagnostic[]
        } catch {
          // Ignore unparseable payloads.
        }
        for (const h of this.diagHandlers) {
          h(event.Uri, diags)
        }
      }
    } catch (err) {
      if (this.diagActive) {
        console.error('[lsp] diagnostics subscription error:', err)
      }
    } finally {
      this.diagActive = false
    }
  }

  /**
   * Reset per-document client state. The underlying {@link GosporeClient} and
   * the shared LSP server are owned elsewhere, so no transport is closed and
   * `lsp.shutdown` is not invoked — tearing down the gateway session would
   * kill LSP for every other consumer.
   */
  disconnect(): void {
    this.readyByLanguage.clear()
    this.versions.clear()
    this.diagActive = false
    this.diagHandlers.clear()
  }
}
