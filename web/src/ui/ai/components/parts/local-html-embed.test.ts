import { describe, it, expect, vi } from 'vitest'
import { materializeHtml, joinRel } from './local-html-embed'

vi.mock('../../../../application/generated-client', () => ({ client: {} }))
vi.mock('../ImageViewer.tsx', () => ({
  base64ToBytes: (b64: string) => {
    const bin = atob(b64)
    const out = new Uint8Array(bin.length)
    for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i) & 0xff
    return out
  },
}))

function fakeIO(files: Record<string, string>): { io: Parameters<typeof materializeHtml>[2]; reads: string[] } {
  const reads: string[] = []
  const content = (p: string) => {
    reads.push(p)
    const c = files[p]
    if (c === undefined) throw new Error('not found: ' + p)
    return c
  }
  return {
    reads,
    io: {
      readText: async p => content(p),
      readBase64: async p => btoa(content(p)),
    },
  }
}

describe('joinRel', () => {
  it('collapses ./ and ../ segments', () => {
    expect(joinRel('build', './a.css')).toBe('build/a.css')
    expect(joinRel('build/sub', '../a.css')).toBe('build/a.css')
    expect(joinRel('build', 'assets/logo.svg')).toBe('build/assets/logo.svg')
  })

  it('strips query and fragment', () => {
    expect(joinRel('build', 'a.css?v=1#x')).toBe('build/a.css')
  })
})

describe('materializeHtml', () => {
  it('rewrites relative <link>/<img>/<script> refs to data: URLs', async () => {
    const html = [
      '<!DOCTYPE html><html><head>',
      '<link rel="stylesheet" href="style.css">',
      '</head><body>',
      '<img src="img/pic.png">',
      '<script src="app.js"></script>',
      '</body></html>',
    ].join('')
    const { io } = fakeIO({ 'style.css': 'body{color:red}', 'img/pic.png': 'PNGDATA', 'app.js': 'console.log(1)' })
    const res = await materializeHtml(html, '', io)
    expect(res.html).toContain('href="data:text/css;base64,')
    expect(res.html).toContain('src="data:image/png;base64,')
    expect(res.html).toContain('src="data:text/javascript;base64,')
    expect(res.html).not.toContain('href="style.css"')
  })

  it('rewrites url() inside <style> blocks and linked css recursively', async () => {
    const html = '<style>.a{background:url(bg.png)}</style><link rel="stylesheet" href="css/main.css">'
    const { io, reads } = fakeIO({
      'bg.png': 'PNG',
      'css/main.css': '.b{background:url(../fonts/x.woff2)}',
      'fonts/x.woff2': 'WOFF',
    })
    const res = await materializeHtml(html, '', io)
    expect(res.html).toMatch(/url\("data:/)
    expect(reads).toContain('css/main.css')
    expect(reads).toContain('fonts/x.woff2')
    expect(reads).toContain('bg.png')
  })

  it('leaves absolute, protocol-relative, data: and root-relative refs untouched', async () => {
    const html = [
      '<link rel="stylesheet" href="https://cdn.example.com/a.css">',
      '<link rel="stylesheet" href="//cdn.example.com/b.css">',
      '<img src="data:image/png;base64,AAAA">',
      '<img src="/static/logo.png">',
      '<script src="app.js"></script>',
    ].join('')
    const { io, reads } = fakeIO({ 'build/app.js': 'x' })
    const res = await materializeHtml(html, 'build', io)
    expect(res.html).toContain('https://cdn.example.com/a.css')
    expect(res.html).toContain('//cdn.example.com/b.css')
    expect(res.html).toContain('data:image/png;base64,AAAA')
    expect(res.html).toContain('/static/logo.png')
    expect(res.html).toContain('data:text/javascript;base64,')
    expect(reads).toEqual(['build/app.js'])
  })

  it('handles missing assets by keeping the original reference', async () => {
    const html = '<link rel="stylesheet" href="missing.css">'
    const { io } = fakeIO({})
    const res = await materializeHtml(html, '', io)
    expect(res.html).toContain('href="missing.css"')
  })

  it('rewrites inline style url() references', async () => {
    const html = '<div style="background:url(assets/logo.svg) center/contain no-repeat"></div>'
    const { io } = fakeIO({ 'assets/logo.svg': '<svg/>' })
    const res = await materializeHtml(html, '', io)
    expect(res.html).toMatch(/url\("data:/)
  })
})
