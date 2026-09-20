import fs from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const scriptDir = path.dirname(fileURLToPath(import.meta.url))
const pkgDir = path.resolve(scriptDir, '..', 'pkg')

const importPathPrefix = 'github.com/qomos-w/sporemind/pkg/domain/gen/'
const genImportLine = '\tgen "github.com/qomos-w/sporemind/pkg/domain/gen"'

async function main() {
  const files = await walk(pkgDir)
  for (const file of files) {
    if (!file.endsWith('.go')) continue
    const src = await fs.readFile(file, 'utf8')
    const result = transform(src)
    if (result.changed) {
      await fs.writeFile(file, result.source, 'utf8')
      console.log(`[migrate-gen-imports] ${path.relative(pkgDir, file)}`)
    }
  }
}

function transform(src) {
  // Normalise CRLF to LF for easier regex handling; write back LF.
  const normalizedSrc = src.replace(/\r\n/g, '\n')
  let changed = false
  let newSrc = normalizedSrc

  // Identify every local alias that points to a domain/gen/* import.
  const aliasToPath = new Map() // alias -> full import path

  const importLineRegex = new RegExp(`^\\s*([a-zA-Z_][a-zA-Z0-9_]*)?\\s*"${importPathPrefix}([^"]+)"`, 'gm')
  let m
  while ((m = importLineRegex.exec(normalizedSrc)) !== null) {
    const alias = m[1] || m[2]
    aliasToPath.set(alias, `${importPathPrefix}${m[2]}`)
  }

  if (aliasToPath.size === 0) {
    return { source: src, changed: false }
  }
  changed = true

  // Replace every qualified identifier that uses one of those aliases.
  for (const alias of aliasToPath.keys()) {
    newSrc = newSrc.replace(new RegExp(`\\b${alias}\\.`, 'g'), 'gen.')
  }

  // Rebuild import block(s).
  newSrc = newSrc.replace(/import\s*\(\r?\n([\s\S]*?)\r?\n\)/g, (match, body) => {
    const lines = body.split('\n')
    const kept = []
    let hasGen = false

    for (const rawLine of lines) {
      const line = rawLine.replace(/\r/g, '')
      const trimmed = line.trim()
      if (trimmed === '' || trimmed.startsWith('//')) {
        kept.push(line)
        continue
      }
      const m = trimmed.match(/^([a-zA-Z_][a-zA-Z0-9_]*)?\s*"([^"]+)"$/)
      if (!m) {
        kept.push(line)
        continue
      }
      const p = m[2]
      if (p.startsWith(importPathPrefix)) {
        // Drop old gen subpackage import.
        continue
      }
      if (p === 'github.com/qomos-w/sporemind/pkg/domain/gen') {
        hasGen = true
      }
      kept.push(line)
    }

    if (!hasGen) {
      kept.push(genImportLine)
    }

    const sorted = kept
      .filter(l => l.trim() !== '')
      .sort((a, b) => {
        const at = a.trim().replace(/^\w+\s+/, '"')
        const bt = b.trim().replace(/^\w+\s+/, '"')
        return at.localeCompare(bt)
      })

    const normalized = sorted.map(l => l.startsWith('\t') ? l : '\t' + l.trim())
    return `import (\n${normalized.join('\n')}\n)`
  })

  // Handle single-line import statements.
  newSrc = newSrc.replace(/import\s+([a-zA-Z_][a-zA-Z0-9_]*)?\s*"([^"]+)"\n/g, (match, alias, p) => {
    if (!p.startsWith(importPathPrefix)) return match
    return `import gen "github.com/qomos-w/sporemind/pkg/domain/gen"\n`
  })

  return { source: newSrc, changed }
}

async function walk(dir) {
  const entries = await fs.readdir(dir, { withFileTypes: true })
  const files = []
  for (const entry of entries) {
    const full = path.join(dir, entry.name)
    if (entry.isDirectory()) {
      files.push(...await walk(full))
    } else {
      files.push(full)
    }
  }
  return files
}

main().catch(err => {
  console.error('[migrate-gen-imports] failed:', err)
  process.exitCode = 1
})
