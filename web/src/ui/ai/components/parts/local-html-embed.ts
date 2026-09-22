import * as filesystemClient from '../../../../gen-clients/filesystem/client'
import * as projectClient from '../../../../gen-clients/project/client'
import { client } from '../../../../application/generated-client'
import { base64ToBytes } from '../ImageViewer.tsx'

/**
 * Local HTML preview materializer.
 *
 * file:// URLs cannot be iframed from an http(s)/wails page (Chromium blocks
 * cross-scheme loads), so previews render from a blob URL. But a blob URL has
 * no path, which breaks every RELATIVE subresource reference in the page
 * (stylesheets, scripts, images resolve against the blob root and 404).
 * This module rewrites those references to their own blob URLs by reading the
 * files through the filesystem actor, producing a self-contained HTML string.
 */

const MIME_BY_EXT: Record<string, string> = {
  css: 'text/css',
  js: 'text/javascript',
  mjs: 'text/javascript',
  json: 'application/json',
  png: 'image/png', jpg: 'image/jpeg', jpeg: 'image/jpeg',
  gif: 'image/gif', webp: 'image/webp', svg: 'image/svg+xml',
  ico: 'image/x-icon', bmp: 'image/bmp', avif: 'image/avif',
  woff: 'font/woff', woff2: 'font/woff2', ttf: 'font/ttf', otf: 'font/otf',
  mp4: 'video/mp4', webm: 'video/webm', mp3: 'audio/mpeg', wav: 'audio/wav',
  ogg: 'audio/ogg',
}

function extOf(path: string): string {
  const clean = path.split('#')[0]?.split('?')[0] ?? ''
  return clean.split('.').pop()?.toLowerCase() ?? ''
}

function mimeOf(path: string): string {
  return MIME_BY_EXT[extOf(path)] ?? 'application/octet-stream'
}

/** Textual assets whose content can be read with filesystem.read. */
function isTextAsset(path: string): boolean {
  return ['css', 'js', 'mjs', 'json', 'svg'].includes(extOf(path))
}

function isAbsoluteRef(u: string): boolean {
  return !u || /^[a-z][a-z0-9+.-]*:/i.test(u) || u.startsWith('//') || u.startsWith('#') || u.startsWith('/')
}

/** Join a base directory with a relative ref, collapsing ./ and ../ . */
export function joinRel(dir: string, ref: string): string {
  const clean = ref.split('#')[0]?.split('?')[0] ?? ''
  const stack: string[] = []
  for (const part of (dir ? dir.split('/') : [])) {
    if (part && part !== '.') stack.push(part)
  }
  for (const part of clean.split('/')) {
    if (!part || part === '.') continue
    if (part === '..') stack.pop()
    else stack.push(part)
  }
  return stack.join('/')
}

/** Relative href/src/poster references in resource-bearing tags. */
const ATTR_RE = /(<(?:link|script|img|source|video|audio|track)\b[^>]*?\b(?:href|src|poster|data-src)\s*=\s*)(["'])([^"']+)\2/gi
/** url(...) references inside <style> blocks and inline style attributes. */
const CSS_URL_RE = /url\(\s*(['"]?)([^'")]+)\1\s*\)/gi

export interface HtmlAssetIO {
  readText(path: string): Promise<string>
  readBase64(path: string): Promise<string>
}

/** Asset-URL cache shared across one materialization run. */
interface MaterializeCtx {
  io: HtmlAssetIO
  cache: Map<string, Promise<string | null>>
}

/**
 * data: URLs instead of blob: URLs — the preview iframe is sandboxed (opaque
 * origin), and blob: subresources can only be fetched by same-origin
 * contexts, so blob refs would silently fail and render unstyled. data:
 * URLs carry no origin restriction.
 */
function bytesToDataUrl(bytes: Uint8Array, mime: string): string {
  let bin = ''
  const chunk = 0x8000
  for (let i = 0; i < bytes.length; i += chunk) {
    bin += String.fromCharCode(...bytes.subarray(i, i + chunk))
  }
  return `data:${mime};base64,${btoa(bin)}`
}

function textToDataUrl(text: string, mime: string): string {
  return bytesToDataUrl(new TextEncoder().encode(text), mime)
}

async function assetDataUrl(ctx: MaterializeCtx, path: string): Promise<string | null> {
  const hit = ctx.cache.get(path)
  if (hit) return hit
  const p = (async () => {
    if (isTextAsset(path)) {
      let text = await ctx.io.readText(path)
      if (extOf(path) === 'css' || extOf(path) === 'svg') {
        text = await rewriteCssUrls(ctx, text, dirOf(path))
      }
      return textToDataUrl(text, mimeOf(path))
    }
    const b64 = await ctx.io.readBase64(path)
    return bytesToDataUrl(base64ToBytes(b64), mimeOf(path))
  })().catch(() => null)
  ctx.cache.set(path, p)
  return p
}

function dirOf(path: string): string {
  const idx = path.lastIndexOf('/')
  return idx < 0 ? '' : path.slice(0, idx)
}

async function rewriteCssUrls(ctx: MaterializeCtx, css: string, dir: string): Promise<string> {
  return replaceAsync(css, CSS_URL_RE, async (m, _quote: string, ref: string) => {
    if (isAbsoluteRef(ref)) return m
    const url = await assetDataUrl(ctx, joinRel(dir, ref))
    return url ? `url("${url}")` : m
  })
}

async function replaceAsync(
  input: string,
  re: RegExp,
  fn: (match: string, ...groups: string[]) => Promise<string>,
): Promise<string> {
  const tasks: Array<{ start: number; end: number; p: Promise<string> }> = []
  input.replace(re, (m, ...rest) => {
    const offset = rest[rest.length - 2] as number
    tasks.push({ start: offset, end: offset + m.length, p: fn(m, ...(rest.slice(0, -2) as string[])) })
    return m
  })
  if (!tasks.length) return input
  const resolved = await Promise.all(tasks.map(t => t.p))
  let out = ''
  let pos = 0
  for (let i = 0; i < tasks.length; i++) {
    const t = tasks[i]
    const r = resolved[i]
    if (t === undefined || r === undefined) continue
    out += input.slice(pos, t.start) + r
    pos = t.end
  }
  return out + input.slice(pos)
}

/**
 * Rewrite all relative subresource references in `html` (which lives in
 * `dir`) to data: URLs, producing a self-contained HTML string that renders
 * correctly inside the sandboxed preview iframe.
 */
export async function materializeHtml(
  html: string,
  dir: string,
  io: HtmlAssetIO,
): Promise<{ html: string }> {
  const ctx: MaterializeCtx = { io, cache: new Map() }

  // <style> blocks carry url() refs against the page dir.
  const styleBlockRe = /(<style\b[^>]*>)([\s\S]*?)(<\/style>)/gi
  const styleBlocks: Array<{ start: number; end: number; p: Promise<string> }> = []
  html.replace(styleBlockRe, (m, open: string, body: string, close: string, offset: number) => {
    styleBlocks.push({
      start: offset,
      end: offset + m.length,
      p: rewriteCssUrls(ctx, body, dir).then(b => open + b + close),
    })
    return m
  })
  let out = html
  if (styleBlocks.length) {
    const resolved = await Promise.all(styleBlocks.map(t => t.p))
    let pos = 0
    let parts = ''
    for (let i = 0; i < styleBlocks.length; i++) {
      const t = styleBlocks[i]
      const r = resolved[i]
      if (t === undefined || r === undefined) continue
      parts += out.slice(pos, t.start) + r
      pos = t.end
    }
    out = parts + out.slice(pos)
  }

  // Tag attributes (href/src/poster/data-src).
  out = await replaceAsync(out, ATTR_RE, async (m, prefix: string, quote: string, ref: string) => {
    if (isAbsoluteRef(ref)) return m
    const url = await assetDataUrl(ctx, joinRel(dir, ref))
    return url ? `${prefix}${quote}${url}${quote}` : m
  })

  // Inline style="... url(...)" attributes.
  out = await replaceAsync(out, /(style\s*=\s*)(["'])([^"']*url\([^)]*\)[^"']*)\2/gi,
    async (m, prefix: string, quote: string, body: string) => {
      const rewritten = await rewriteCssUrls(ctx, body, dir)
      return rewritten === body ? m : `${prefix}${quote}${rewritten}${quote}`
    })

  return { html: out }
}

/** IO bound to the generated filesystem client. */
export const fsAssetIO: HtmlAssetIO = {
  readText: async path => (await filesystemClient.read(client, { Path: path })).Content ?? '',
  readBase64: async path => (await filesystemClient.readBase64(client, { Path: path })).Content ?? '',
}

/**
 * Project-aware asset IO: sibling files of a project-scoped preview must be
 * read through the owning project actor (same routing as the viewer's own
 * reads); without a project the global filesystem actor serves the paths.
 */
export function assetIOFor(projectId?: string): HtmlAssetIO {
  if (!projectId) return fsAssetIO
  return {
    readText: async path => (await projectClient.read(client, { Path: path }, { target: projectId })).Content ?? '',
    readBase64: async path => (await projectClient.readBase64(client, { Path: path }, { target: projectId })).Content ?? '',
  }
}
