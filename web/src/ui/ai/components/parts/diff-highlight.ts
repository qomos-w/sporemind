import React from 'react'
import { createLowlight } from 'lowlight'
import type { Element, ElementContent, Root, Text } from 'hast'
import go from 'highlight.js/lib/languages/go'
import typescript from 'highlight.js/lib/languages/typescript'
import javascript from 'highlight.js/lib/languages/javascript'
import python from 'highlight.js/lib/languages/python'
import css from 'highlight.js/lib/languages/css'
import json from 'highlight.js/lib/languages/json'
import bash from 'highlight.js/lib/languages/bash'
import xml from 'highlight.js/lib/languages/xml'
import sql from 'highlight.js/lib/languages/sql'
import yaml from 'highlight.js/lib/languages/yaml'
import markdown from 'highlight.js/lib/languages/markdown'
import rust from 'highlight.js/lib/languages/rust'
import java from 'highlight.js/lib/languages/java'
import cpp from 'highlight.js/lib/languages/cpp'
import ruby from 'highlight.js/lib/languages/ruby'
import php from 'highlight.js/lib/languages/php'
import { sporeHighlightJs } from '../../../editor/sporeLanguage'

// Stateless tokenizer configured once, mirroring the module-scoped
// `projectHighlight` style in CodeMirrorViewer. Not business state.
const lowlight = createLowlight()
lowlight.register({
  go, typescript, javascript, python, css, json, bash, xml, sql, yaml, markdown,
  rust, java, cpp, ruby, php,
})
lowlight.register('spore', sporeHighlightJs)

/**
 * Map a file path to a registered lowlight language id, or null when the
 * extension is unknown (caller should render plain text in that case).
 */
export function langFromPath(filePath: string | undefined | null): string | null {
  if (!filePath) return null
  const slash = filePath.replace(/\\/g, '/')
  const base = slash.slice(slash.lastIndexOf('/') + 1)
  const dot = base.lastIndexOf('.')
  const ext = dot >= 0 ? base.slice(dot + 1).toLowerCase() : ''
  switch (ext) {
    case 'go':
      return 'go'
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
    case 'py':
    case 'pyw':
      return 'python'
    case 'css':
    case 'scss':
    case 'sass':
    case 'less':
      return 'css'
    case 'json':
    case 'jsonc':
      return 'json'
    case 'sh':
    case 'bash':
    case 'zsh':
    case 'ksh':
      return 'bash'
    case 'xml':
    case 'html':
    case 'htm':
    case 'xhtml':
    case 'svg':
    case 'vue':
    case 'svelte':
      return 'xml'
    case 'sql':
      return 'sql'
    case 'yml':
    case 'yaml':
      return 'yaml'
    case 'md':
    case 'markdown':
    case 'mdx':
      return 'markdown'
    case 'rs':
      return 'rust'
    case 'java':
    case 'kt':
    case 'kts':
      return 'java'
    case 'cpp':
    case 'cc':
    case 'cxx':
    case 'c':
    case 'h':
    case 'hpp':
    case 'hh':
      return 'cpp'
    case 'rb':
    case 'erb':
      return 'ruby'
    case 'php':
    case 'phtml':
      return 'php'
    case 'spore':
      return 'spore'
    default:
      return null
  }
}

const languageAliases: Record<string, string> = {
  c: 'cpp',
  cjs: 'javascript',
  cplusplus: 'cpp',
  cs: 'cpp',
  html: 'xml',
  java: 'java',
  js: 'javascript',
  jsonc: 'json',
  jsx: 'javascript',
  kt: 'java',
  md: 'markdown',
  mjs: 'javascript',
  py: 'python',
  rb: 'ruby',
  rs: 'rust',
  scss: 'css',
  shell: 'bash',
  sh: 'bash',
  sporescript: 'spore',
  ts: 'typescript',
  tsx: 'typescript',
  yml: 'yaml',
  zsh: 'bash',
}

export function normalizeLanguage(lang: string | null | undefined): string | null {
  if (!lang) return null
  const normalized = lang.toLowerCase()
  return languageAliases[normalized] ?? normalized
}

export function isLangSupported(lang: string | null | undefined): boolean {
  const normalized = normalizeLanguage(lang)
  return !!normalized && lowlight.registered(normalized)
}

/** Tokenize `code` with the given language. Throws if the language is unknown. */
export function highlightCode(lang: string, code: string): Root {
  return lowlight.highlight(normalizeLanguage(lang) ?? lang, code)
}

function renderHast(node: Root | Element | ElementContent, keyPrefix: string): React.ReactNode {
  if (node.type === 'text') return (node as Text).value
  if (node.type === 'element') {
    const element = node as Element
    const classes = element.properties?.className
    const className = Array.isArray(classes)
      ? classes.join(' ')
      : typeof classes === 'string'
        ? classes
        : undefined
    return React.createElement(
      'span',
      { key: keyPrefix, className },
      element.children?.map((child, index) => renderHast(child, `${keyPrefix}.${index}`)),
    )
  }
  if (node.type === 'root') {
    return node.children
      .filter((child): child is ElementContent => child.type === 'element' || child.type === 'text')
      .map((child, index) => renderHast(child, `${keyPrefix}.${index}`))
  }
  return null
}

export function highlightedCode(lang: string, code: string): React.ReactNode | null {
  if (!isLangSupported(lang)) return null
  try {
    return renderHast(highlightCode(lang, code), 'code')
  } catch {
    return null
  }
}
