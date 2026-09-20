// Generates web/src/about/third-party-licenses.generated.json from the
// license audit reports under docs/licenses/. Run: npm run generate:notices
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const webDir = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const repoRoot = resolve(webDir, '..')
const outPath = resolve(webDir, 'src/about/third-party-licenses.generated.json')

export interface LicenseEntry {
  name: string
  version: string
  license: string
  homepage: string
  copyright: string
}

export interface LicenseGroup {
  name: string
  entries: LicenseEntry[]
}

export interface ThirdPartyNotices {
  generatedAt: string
  sources: string[]
  groups: LicenseGroup[]
}

function parseTableRows(md: string): string[][] {
  const rows: string[][] = []
  for (const line of md.split(/\r?\n/)) {
    const m = line.match(/^\|(.+)\|\s*$/)
    if (!m) continue
    const cells = m[1]!.split('|').map((c) => c.trim())
    if (cells.every((c) => /^:?-{3,}:?$/.test(c))) continue // separator row
    rows.push(cells)
  }
  return rows
}

function cleanCell(s: string): string {
  return s
    .replace(/\*\*/g, '')
    .replace(/`/g, '')
    .replace(/（[^）]*）$/, '')
    .replace(/\(npm author 字段\)/g, '')
    .trim()
}

function splitLicense(cell: string): string {
  const s = cleanCell(cell)
  const m = s.match(/^(MIT|Apache-2\.0|BSD-3-Clause|BSD-2-Clause|ISC|MPL-2\.0|CC-BY-4\.0|Unlicense)/)
  return m ? (m[1] ?? s) : s
}

function goModules(): LicenseGroup {
  const md = readFileSync(resolve(repoRoot, 'docs/licenses/go-modules.md'), 'utf8')
  const rows = parseTableRows(md).slice(1) // drop header
  const entries: LicenseEntry[] = rows
    .filter((c) => c.length === 5 && c[0] !== '模块' && c[0] !== 'name')
    .map((c) => ({
    name: cleanCell(c[0] ?? ''),
    version: cleanCell(c[1] ?? ''),
    license: splitLicense(c[2] ?? ''),
    homepage: cleanCell(c[3] ?? ''),
    copyright: cleanCell(c[4] ?? ''),
  }))
  return { name: 'Go Modules (direct dependencies)', entries }
}

function webDeps(): LicenseGroup {
  const md = readFileSync(resolve(repoRoot, 'docs/licenses/web-deps.md'), 'utf8')
  const entries: LicenseEntry[] = []
  for (const cells of parseTableRows(md).slice(1)) {
    // npm tables: name | version | license | homepage | 版权行
    if (cells.length >= 5 && /\d+\.\d+/.test(cells[1] ?? '') && cells[0] !== 'name') {
      entries.push({
        name: cleanCell(cells[0] ?? ''),
        version: cleanCell(cells[1] ?? ''),
        license: splitLicense(cells[2] ?? ''),
        homepage: cleanCell(cells[3] ?? ''),
        copyright: cleanCell(cells[4] ?? ''),
      })
    }
  }
  return { name: 'Web npm Dependencies', entries }
}

function assets(): LicenseGroup {
  const md = readFileSync(resolve(repoRoot, 'docs/licenses/assets.md'), 'utf8')
  const entries: LicenseEntry[] = [
    {
      name: 'glassesmirror.ttf (SUSE Mono Thin)',
      version: '2.001',
      license: 'SIL OFL-1.1',
      homepage: 'https://github.com/SUSE/suse-font',
      copyright: 'Copyright 2025 The SUSE Project Authors (designer: Rene Bieder)',
    },
    {
      name: 'atom-icons (iconData.ts, via download-atom-icons.cjs)',
      version: 'n/a',
      license: 'MIT',
      homepage: 'https://github.com/AtomMaterialUI/a-file-icon-vscode',
      copyright:
        'Copyright (c) 2023 Elior "Mallowigi" Boukhobza and Philipp "PKief" Kief; icons Copyright (c) 2017-2019 Elior Boukhobza',
    },
    {
      name: '@iconify-json/material-icon-theme',
      version: '1.2.69',
      license: 'MIT',
      homepage: 'https://github.com/material-extensions/vscode-material-icon-theme',
      copyright: 'Material Extensions',
    },
  ]
  void md
  return { name: 'Bundled Assets (fonts & icons)', entries }
}

// Internal compliance/risk assessment lives in docs/licenses/*.md (repo-only);
// the user-facing About page ships attribution notices only.
const notices: ThirdPartyNotices = {
  generatedAt: new Date().toISOString(),
  sources: ['docs/licenses/go-modules.md', 'docs/licenses/web-deps.md', 'docs/licenses/assets.md'],
  groups: [goModules(), webDeps(), assets()],
}

mkdirSync(dirname(outPath), { recursive: true })
writeFileSync(outPath, JSON.stringify(notices, null, 2) + '\n', 'utf8')

const total = notices.groups.reduce((n, g) => n + g.entries.length, 0)
console.log(`wrote ${outPath}: ${notices.groups.length} groups, ${total} entries`)
if (total === 0) {
  console.error('ERROR: generated notices are empty')
  process.exit(1)
}
