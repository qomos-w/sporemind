// EditorStore: tracks open files, dirty state, language detection.
// Class-based store with subscribe/getVersion for useSyncExternalStore.

import * as filesystem from '../../gen-clients/filesystem/client'
import { client } from '../../application/generated-client'

export interface OpenFile {
  projectId: string
  filePath: string
  content: string
  savedContent: string
  language: string
  isBinary: boolean
  size: number
}

type Listener = () => void

function langFromPath(path: string): string {
  const ext = path.split('.').pop()?.toLowerCase() ?? ''
  const map: Record<string, string> = {
    ts: 'typescript', tsx: 'typescript',
    js: 'javascript', jsx: 'javascript', mjs: 'javascript',
    go: 'go',
    py: 'python',
    rs: 'rust',
    json: 'json',
    md: 'markdown',
    html: 'html', htm: 'html',
    css: 'css', scss: 'scss', less: 'less',
    yaml: 'yaml', yml: 'yaml',
    toml: 'toml',
    sh: 'shell', bash: 'shell',
    sql: 'sql',
    xml: 'xml', svg: 'xml',
    c: 'c', h: 'c',
    cpp: 'cpp', cc: 'cpp', cxx: 'cpp', hpp: 'cpp',
    java: 'java',
    kt: 'kotlin',
    swift: 'swift',
    rb: 'ruby',
    php: 'php',
    proto: 'protobuf',
    dockerfile: 'dockerfile',
    lua: 'lua',
    zig: 'zig',
  }
  return map[ext] ?? 'plaintext'
}

const BINARY_EXTENSIONS = new Set([
  'png', 'jpg', 'jpeg', 'gif', 'bmp', 'webp', 'ico', 'tiff', 'tif',
  'mp3', 'mp4', 'wav', 'avi', 'mov', 'mkv',
  'pdf', 'doc', 'docx', 'xls', 'xlsx', 'ppt', 'pptx',
  'zip', 'tar', 'gz', 'bz2', '7z', 'rar',
  'exe', 'dll', 'so', 'dylib',
  'ttf', 'otf', 'woff', 'woff2', 'eot',
  'wasm',
])

const IMAGE_EXTENSIONS = new Set(['png', 'jpg', 'jpeg', 'gif', 'bmp', 'webp', 'svg', 'ico'])

function isBinaryFile(path: string): boolean {
  const ext = path.split('.').pop()?.toLowerCase() ?? ''
  return BINARY_EXTENSIONS.has(ext)
}

export function isImageFile(path: string): boolean {
  const ext = path.split('.').pop()?.toLowerCase() ?? ''
  return IMAGE_EXTENSIONS.has(ext)
}

class EditorStore {
  openFiles = new Map<string, OpenFile>()

  private listeners = new Set<Listener>()
  private _version = 0
  private fileListeners = new Map<string, Set<Listener>>()
  private fileVersions = new Map<string, number>()

  getVersion = () => this._version

  subscribe = (fn: Listener): () => void => {
    this.listeners.add(fn)
    return () => { this.listeners.delete(fn) }
  }

  /** Subscribe to changes for a specific file only. Returns unsubscribe function. */
  subscribeFile(fileKey: string, fn: Listener): () => void {
    let set = this.fileListeners.get(fileKey)
    if (!set) {
      set = new Set()
      this.fileListeners.set(fileKey, set)
    }
    set.add(fn)
    return () => { set?.delete(fn) }
  }

  /** React snapshot for a specific file — only changes when that file's content/structure changes. */
  getFileVersion = (fileKey: string): number => {
    return this.fileVersions.get(fileKey) ?? 0
  }

  private emit() {
    this._version++
    for (const fn of this.listeners) fn()
  }

  /** Notify only listeners for a specific file key. */
  private emitFile(fileKey: string) {
    const ver = (this.fileVersions.get(fileKey) ?? 0) + 1
    this.fileVersions.set(fileKey, ver)
    this.fileListeners.get(fileKey)?.forEach(fn => fn())
  }

  fileKey(projectId: string, filePath: string): string {
    return `${projectId}::${filePath}`
  }

  getFile(projectId: string, filePath: string): OpenFile | undefined {
    return this.openFiles.get(this.fileKey(projectId, filePath))
  }

  async openFile(projectId: string, filePath: string): Promise<void> {
    const key = this.fileKey(projectId, filePath)
    if (this.openFiles.has(key)) return
    if (!filePath) return

    const binary = isBinaryFile(filePath)
    const resp = binary ? null : await filesystem.read(client, { Path: filePath, Offset: 0, Limit: 0 })
    const content = resp?.Content ?? ''
    this.openFiles.set(key, {
      projectId,
      filePath,
      content,
      savedContent: content,
      language: langFromPath(filePath),
      isBinary: binary,
      size: resp?.NumLines ?? 0,
    })
    this.emit()
  }

  setContent(projectId: string, filePath: string, content: string): void {
    const key = this.fileKey(projectId, filePath)
    const f = this.openFiles.get(key)
    if (!f) return
    f.content = content
    // Per-file emit only — other editors don't need to re-render for this file's changes
    this.emitFile(key)
  }

  async saveFile(projectId: string, filePath: string): Promise<void> {
    const key = this.fileKey(projectId, filePath)
    const f = this.openFiles.get(key)
    if (!f) return
    await filesystem.write(client, { Path: filePath, Content: f.content })
    f.savedContent = f.content
    this.emit()
  }

  isDirty(projectId: string, filePath: string): boolean {
    const key = this.fileKey(projectId, filePath)
    const f = this.openFiles.get(key)
    return !!f && f.content !== f.savedContent
  }

  closeFile(projectId: string, filePath: string): void {
    const key = this.fileKey(projectId, filePath)
    this.openFiles.delete(key)
    this.emit()
  }
}

export const editorStore = new EditorStore()
