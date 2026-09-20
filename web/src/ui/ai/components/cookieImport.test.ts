import { describe, it, expect } from 'vitest'
import { parseCookieFile } from './cookieImport'
import type { BrowserCookieEntry } from '../../../gen-types/browser.cookie'

const first = (r: Record<string, BrowserCookieEntry[]>, domain: string): BrowserCookieEntry | undefined => r[domain]?.[0]

describe('parseCookieFile', () => {
  it('parses sporemind domain-grouped JSON leniently', () => {
    const raw = JSON.stringify({
      'example.com': [
        { Name: 'a', Value: '1', Domain: 'example.com', Path: '/', Expires: 1893456000, HttpOnly: true, Secure: true, SameSite: 'Lax' },
        { Name: 'b', Value: '2' },
        { Name: 3, Value: 'x' },
      ],
      'other.com': 'not-an-array',
    })
    const { result, skipped } = parseCookieFile(raw)
    expect(skipped).toBe(2)
    expect(result['example.com']?.length).toBe(2)
    expect(first(result, 'example.com')).toEqual({
      Name: 'a', Value: '1', Domain: 'example.com', Path: '/', Expires: 1893456000, HttpOnly: true, Secure: true, SameSite: 'Lax',
    })
    expect(result['example.com']?.[1]).toMatchObject({ Name: 'b', Domain: 'example.com', Path: '/', Expires: 0 })
    expect(result['other.com']).toBeUndefined()
  })

  it('parses EditThisCookie JSON arrays', () => {
    const raw = JSON.stringify([
      { domain: '.example.com', name: 'sid', value: 'x', expirationDate: 1893456000.9, path: '/app', secure: true, httpOnly: true, sameSite: 'no_restriction' },
      { domain: '.example.com', hostOnly: true, name: 'h', value: 'v', sameSite: 'strict' },
      { domain: 'ok.com', name: 'l', value: 'w', sameSite: 'lax', session: true },
      { name: 'no-domain', value: 'x' },
      'garbage',
    ])
    const { result, skipped } = parseCookieFile(raw)
    expect(skipped).toBe(2)
    expect(first(result, '.example.com')).toEqual({
      Name: 'sid', Value: 'x', Domain: '.example.com', Path: '/app', Expires: 1893456000, HttpOnly: true, Secure: true, SameSite: 'None',
    })
    expect(result['example.com']?.length).toBe(1)
    expect(result['example.com']?.[0]).toMatchObject({ Name: 'h', SameSite: 'Strict', Expires: 0 })
    expect(result['ok.com']?.[0]).toMatchObject({ Name: 'l', SameSite: 'Lax' })
  })

  it('parses Netscape cookies.txt with comments and #HttpOnly_ prefix', () => {
    const raw = [
      '# Netscape HTTP Cookie File',
      '# This is a comment',
      '.example.com\tTRUE\t/\tTRUE\t1893456000\tsid\tabc',
      '#HttpOnly_example.com\tTRUE\t/app\tFALSE\t0\thtok\tdef',
      'malformed line without tabs',
      'too\tfew\tfields',
      '',
    ].join('\n')
    const { result, skipped } = parseCookieFile(raw)
    expect(skipped).toBe(1)
    expect(first(result, '.example.com')).toEqual({
      Name: 'sid', Value: 'abc', Domain: '.example.com', Path: '/', Expires: 1893456000, HttpOnly: false, Secure: true, SameSite: '',
    })
    expect(first(result, 'example.com')).toEqual({
      Name: 'htok', Value: 'def', Domain: 'example.com', Path: '/app', Expires: 0, HttpOnly: true, Secure: false, SameSite: '',
    })
  })

  it('returns skipped -1 for unrecognized formats', () => {
    expect(parseCookieFile('hello world').skipped).toBe(-1)
    expect(parseCookieFile('').skipped).toBe(-1)
    expect(parseCookieFile('# only comments\n# no cookies').skipped).toBe(-1)
    expect(parseCookieFile('42').skipped).toBe(-1)
    expect(parseCookieFile('null').skipped).toBe(-1)
  })
})
