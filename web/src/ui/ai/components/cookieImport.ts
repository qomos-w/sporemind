import type { BrowserCookieEntry } from '../../../gen-types/browser.cookie'

export interface ParsedCookieFile {
  result: Record<string, BrowserCookieEntry[]>
  /** Entries dropped as invalid; -1 = unrecognized file format. */
  skipped: number
}

// Recognizes three cookie file formats:
//  1. sporemind export: `{ "<domain>": [ { Name, Value, ... } ] }`
//  2. EditThisCookie JSON: `[ { domain, name, value, expirationDate, ... } ]`
//  3. Netscape cookies.txt: tab-separated `domain  flag  path  secure  expiry  name  value`
export function parseCookieFile(raw: string): ParsedCookieFile {
  const trimmed = raw.trim()
  if (trimmed.startsWith('{') || trimmed.startsWith('[')) {
    try {
      const data: unknown = JSON.parse(trimmed)
      if (Array.isArray(data)) return parseEditThisCookie(data)
      if (data && typeof data === 'object') return parseSporemindMap(data as Record<string, unknown>)
      return { result: {}, skipped: -1 }
    } catch {
      // fall through to Netscape
    }
  }
  return parseNetscape(trimmed)
}

// Lenient like the original inline parser: drop invalid entries, keep going.
function parseSporemindMap(data: Record<string, unknown>): ParsedCookieFile {
  const result: Record<string, BrowserCookieEntry[]> = {}
  let skipped = 0
  for (const [domain, entries] of Object.entries(data)) {
    if (!domain) { skipped++; continue }
    if (!Array.isArray(entries)) { skipped++; continue }
    const filtered: BrowserCookieEntry[] = []
    for (const rawEntry of entries) {
      if (!rawEntry || typeof rawEntry !== 'object' || Array.isArray(rawEntry)) { skipped++; continue }
      const e = rawEntry as Record<string, unknown>
      if (typeof e.Name !== 'string' || typeof e.Value !== 'string') { skipped++; continue }
      filtered.push({
        Name: e.Name,
        Value: e.Value,
        Domain: typeof e.Domain === 'string' ? e.Domain : domain,
        Path: typeof e.Path === 'string' ? e.Path : '/',
        Expires: typeof e.Expires === 'number' ? e.Expires : 0,
        HttpOnly: typeof e.HttpOnly === 'boolean' ? e.HttpOnly : false,
        Secure: typeof e.Secure === 'boolean' ? e.Secure : false,
        SameSite: typeof e.SameSite === 'string' ? e.SameSite : '',
      })
    }
    if (filtered.length > 0) result[domain] = filtered
  }
  return { result, skipped }
}

function parseEditThisCookie(data: unknown[]): ParsedCookieFile {
  const result: Record<string, BrowserCookieEntry[]> = {}
  let skipped = 0
  for (const rawEntry of data) {
    if (!rawEntry || typeof rawEntry !== 'object' || Array.isArray(rawEntry)) { skipped++; continue }
    const e = rawEntry as Record<string, unknown>
    if (typeof e.name !== 'string' || !e.name || typeof e.value !== 'string' || typeof e.domain !== 'string' || !e.domain) {
      skipped++
      continue
    }
    let domain = e.domain
    if (e.hostOnly === true && domain.startsWith('.')) domain = domain.slice(1)
    const exp = typeof e.expirationDate === 'number' && Number.isFinite(e.expirationDate) ? Math.floor(e.expirationDate) : 0
    const entry: BrowserCookieEntry = {
      Name: e.name,
      Value: e.value,
      Domain: domain,
      Path: typeof e.path === 'string' && e.path ? e.path : '/',
      Expires: exp,
      HttpOnly: e.httpOnly === true,
      Secure: e.secure === true,
      SameSite: editThisCookieSameSite(e.sameSite),
    }
    ;(result[domain] ??= []).push(entry)
  }
  return { result, skipped }
}

function editThisCookieSameSite(v: unknown): string {
  if (typeof v !== 'string') return ''
  switch (v) {
    case 'no_restriction':
    case 'none':
      return 'None'
    case 'lax':
      return 'Lax'
    case 'strict':
      return 'Strict'
    default:
      return ''
  }
}

function parseNetscape(raw: string): ParsedCookieFile {
  const result: Record<string, BrowserCookieEntry[]> = {}
  let skipped = 0
  let seen = false
  for (const rawLine of raw.split(/\r?\n/)) {
    const line = rawLine.trim()
    if (!line) continue
    let httpOnly = false
    let body = line
    if (body.startsWith('#')) {
      if (!body.startsWith('#HttpOnly_')) continue // plain comment
      httpOnly = true
      body = body.slice('#HttpOnly_'.length)
    }
    const parts = body.split('\t')
    if (parts.length < 2) continue // not tab-separated: not a cookie line
    if (parts.length < 7) { skipped++; continue }
    const [domain, , path, secure, expiry, name, ...rest] = parts
    if (!domain || !name) { skipped++; continue }
    seen = true
    const exp = expiry ? Number.parseFloat(expiry) : NaN
    const entry: BrowserCookieEntry = {
      Name: name,
      Value: rest.join('\t'),
      Domain: domain,
      Path: path || '/',
      Expires: Number.isFinite(exp) && exp > 0 ? Math.floor(exp) : 0,
      HttpOnly: httpOnly,
      Secure: (secure ?? '').toUpperCase() === 'TRUE',
      SameSite: '',
    }
    ;(result[domain] ??= []).push(entry)
  }
  if (!seen && skipped === 0) return { result: {}, skipped: -1 }
  return { result, skipped }
}
