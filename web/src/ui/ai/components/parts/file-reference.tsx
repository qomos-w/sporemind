import React, { createContext, useContext, useMemo } from 'react'
import type { Element, ElementContent, Parent, Root } from 'hast'
import { visitParents } from 'unist-util-visit-parents'
import type { PluggableList } from 'unified'
import * as filesystem from '../../../../gen-clients/filesystem/client'
import { client } from '../../../../application/generated-client'

/** 代码/配置/文本源文件后缀名白名单。 */
const CODE_FILE_EXTENSIONS = new Set([
  'ts', 'tsx',
  'js', 'jsx', 'mjs',
  'go',
  'py',
  'rs',
  'json',
  'html', 'htm',
  'css', 'scss', 'less',
  'yaml', 'yml',
  'toml',
  'sh', 'bash',
  'sql',
  'xml', 'svg',
  'c', 'h',
  'cpp', 'cc', 'cxx', 'hpp',
  'java',
  'kt',
  'swift',
  'rb',
  'php',
  'proto',
  'dockerfile',
  'lua',
  'zig',
  'md', 'mdx',
  'txt',
  'ini', 'cfg', 'conf',
  'env',
  'properties',
])

/** 常见无扩展名 dotfile 白名单。 */
const KNOWN_DOTFILES = new Set([
  '.gitignore',
  '.gitattributes',
  '.gitmodules',
  '.gitkeep',
  '.editorconfig',
  '.eslintrc',
  '.prettierrc',
  '.babelrc',
  '.npmrc',
  '.yarnrc',
  '.nvmrc',
  '.dockerignore',
  '.prettierignore',
  '.eslintignore',
  '.babelignore',
  '.stylelintrc',
  '.lintstagedrc',
  '.cursorrules',
  '.clang-format',
  '.clang-tidy',
  '.node-version',
  '.python-version',
  '.ruby-version',
  '.env.local',
  '.env.development',
  '.env.production',
  '.env.test',
  '.env.example',
])

export function isCodeFileExt(ext: string): boolean {
  return CODE_FILE_EXTENSIONS.has(ext.toLowerCase())
}

export function isKnownDotfile(name: string): boolean {
  return KNOWN_DOTFILES.has(name.toLowerCase())
}

export interface FileReference {
  raw: string
  path: string
  ext: string
  line?: number
  lineEnd?: number
}

/**
 * 从一段文本里解析可能的文件引用。
 * 支持：`path/to/file.go:549`、`file.go`、`./file.go`、Windows 风格分隔符，
 * 以及 `.gitignore`、`.editorconfig` 等常见无扩展名 dotfile。
 */
export function parseFileReference(text: string): FileReference | null {
  const trimmed = text.trim().replace(/^[\s"'([{`]+|[\s"')\]}``]+$/g, '')

  const dotMatch = trimmed.match(/^(?<path>(?:[\w.\/\\-]+\/)?\.[A-Za-z0-9][\w.-]*)(?::(?<line>\d+)(?:-(?<lineEnd>\d+))?)?$/)
  if (dotMatch) {
    const path = dotMatch.groups?.path ?? ''
    const lineStr = dotMatch.groups?.line
    const lineEndStr = dotMatch.groups?.lineEnd
    const normalizedPath = path.replace(/\\/g, '/')
    const name = normalizedPath.split('/').pop() ?? ''
    if (path && isKnownDotfile(name)) {
      return {
        raw: trimmed,
        path: normalizedPath,
        ext: name.toLowerCase(),
        ...parseLineRange(lineStr, lineEndStr),
      }
    }
  }

  const match = trimmed.match(/^(?<path>[\w.\/\\-]+?\w\.([A-Za-z0-9]+))(?::(?<line>\d+)(?:-(?<lineEnd>\d+))?)?$/)
  if (match) {
    const path = match.groups?.path ?? ''
    const ext = match[2] ?? ''
    const lineStr = match.groups?.line
    const lineEndStr = match.groups?.lineEnd
    if (!path || !isCodeFileExt(ext)) return null

    return {
      raw: trimmed,
      path: path.replace(/\\/g, '/'),
      ext: ext.toLowerCase(),
      ...parseLineRange(lineStr, lineEndStr),
    }
  }

  return null
}

function parseLineRange(lineStr: string | undefined, lineEndStr: string | undefined): { line?: number; lineEnd?: number } {
  if (!lineStr) return {}
  const line = parseInt(lineStr, 10)
  if (!lineEndStr) return { line }
  const lineEnd = parseInt(lineEndStr, 10)
  return lineEnd > line ? { line, lineEnd } : { line }
}

interface FileReferenceContextValue {
  projectId: string | null
  projectRoot: string | null
}

const FileReferenceContext = createContext<FileReferenceContextValue | null>(null)

export const FileReferenceProvider: React.FC<{
  projectId: string | null
  projectRoot: string | null
  children: React.ReactNode
}> = ({ projectId, projectRoot, children }) => {
  const value = useMemo(() => ({ projectId, projectRoot }), [projectId, projectRoot])
  return <FileReferenceContext.Provider value={value}>{children}</FileReferenceContext.Provider>
}

export function useFileReference(): FileReferenceContextValue | null {
  return useContext(FileReferenceContext)
}

export interface RehypeFileReferenceOptions {
  projectId: string | null
  projectRoot: string | null
}

function isTextNode(node: ElementContent): node is { type: 'text'; value: string } {
  return node.type === 'text'
}

function isElementParent(parent: Parent | undefined): parent is Parent & { children: ElementContent[] } {
  return parent !== undefined && Array.isArray((parent as Parent).children)
}

/**
 * rehype 插件：在标准 Markdown 渲染为 HAST 后，把匹配代码文件引用的 inline code
 * 标记为可点击文件引用。普通代码块与非文件引用 inline code 保持原样。
 */
export function rehypeFileReferences(options: RehypeFileReferenceOptions) {
  return function transformer(tree: Root) {
    const { projectId, projectRoot } = options
    if (!projectRoot || !projectId) return

    visitParents(tree, 'element', (node, ancestors) => {
      if (node.tagName !== 'code') return

      // 跳过代码块（<pre><code>...</code></pre>），只处理 inline code。
      const parent = ancestors[ancestors.length - 1]
      if (parent && 'tagName' in parent && parent.tagName === 'pre') return

      if (!node.children.every(isTextNode)) return
      const text = node.children.map(n => n.value).join('')
      const ref = parseFileReference(text)
      if (!ref) return

      const anchor: Element = {
        type: 'element',
        tagName: 'a',
        properties: {
          className: ['ai-file-ref'],
          href: '#',
          'data-ai-file-raw': ref.raw,
          'data-ai-file-path': ref.path,
          ...(ref.line != null ? { 'data-ai-file-line': String(ref.line) } : {}),
          ...(ref.lineEnd != null ? { 'data-ai-file-line-end': String(ref.lineEnd) } : {}),
          'data-ai-project-id': projectId,
        },
        children: [node],
      }

      if (!isElementParent(parent)) return
      const index = parent.children.indexOf(node)
      if (index === -1) return
      parent.children[index] = anchor
    })
  }
}

export function makeRehypePlugins(options: RehypeFileReferenceOptions): PluggableList {
  return [[rehypeFileReferences, options]]
}

interface ResolvedFile {
  filePath: string
  line?: number
  lineEnd?: number
}

const resolveCache = new Map<string, ResolvedFile | null>()

function cacheKey(projectRoot: string | null, ref: FileReference): string {
  return `${projectRoot ?? ''}::${ref.path}`
}

export function joinRoot(root: string | null, rel: string): string {
  const normalizedRel = rel.replace(/\\/g, '/').replace(/^\//, '')
  if (!root) return normalizedRel
  const normalizedRoot = root.replace(/\\/g, '/').replace(/\/$/, '')
  return `${normalizedRoot}/${normalizedRel}`
}

async function searchByPattern(projectRoot: string | null, pattern: string, exclude?: string): Promise<string[]> {
  try {
    const resp = await filesystem.glob(client, {
      Path: projectRoot ?? '',
      Pattern: pattern,
      Maxdepth: 30,
      ...(exclude ? { Exclude: exclude } : {}),
    })
    return resp.Files ?? []
  } catch {
    return []
  }
}

export async function resolveFileReference(
  ref: FileReference,
  projectRoot: string | null,
): Promise<ResolvedFile | null> {
  const key = cacheKey(projectRoot, ref)
  const cached = resolveCache.get(key)
  if (cached !== undefined) return cached

  const hasDir = ref.path.includes('/')
  let matches: string[] = []

  if (hasDir) {
    // 先按相对路径精确匹配。
    matches = await searchByPattern(projectRoot, ref.path)
  }

  if (matches.length === 0) {
    // 退回到按文件名搜索。排除常见重型目录，避免全树遍历。
    const parts = ref.path.split('/')
    const baseName = parts[parts.length - 1]
    if (baseName) {
      matches = await searchByPattern(
        projectRoot,
        `**/${baseName}`,
        'node_modules/**,.git/**,vendor/**,dist/**,build/**,.next/**,out/**,target/**,__pycache__/**',
      )
    }
  }

  if (matches.length === 0) {
    resolveCache.set(key, null)
    return null
  }

  // 多个匹配时优先最短的（尽量顶层），再按字母序稳定。
  matches.sort((a, b) => {
    const depthDiff = a.split('/').length - b.split('/').length
    if (depthDiff !== 0) return depthDiff
    return a.localeCompare(b)
  })

  const result: ResolvedFile = {
    filePath: joinRoot(projectRoot, matches[0]!),
    line: ref.line,
    lineEnd: ref.lineEnd,
  }
  resolveCache.set(key, result)
  return result
}
