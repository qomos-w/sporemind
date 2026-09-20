import fs from 'node:fs'
import path from 'node:path'

const WIKI_DIR = path.resolve(process.cwd(), '.sporecode/wiki')

interface Card {
  file: string
  title: string
  type?: string
  list: string[]
  parent?: string
  body: string
}

function parseYamlBlock(text: string): Record<string, unknown> {
  const result: Record<string, unknown> = {}
  const lines = text.split(/\r?\n/)
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    if (!line || line.trim().startsWith('#')) continue
    const trimmed = line.trim()
    if (trimmed === '') continue
    const [key, ...rest] = trimmed.split(':')
    if (!key || rest.length === 0) continue
    const value = rest.join(':').trim()
    if (value === '') {
      if (key === 'data') {
        const block: Record<string, unknown> = {}
        let j = i + 1
        while (j < lines.length && (lines[j].startsWith('  ') || lines[j].startsWith('\t'))) {
          const l = lines[j].trim()
          if (l.includes(':')) {
            const [k, ...v] = l.split(':')
            block[k.trim()] = v.join(':').trim()
          }
          j++
        }
        i = j - 1
        result[key] = block
        continue
      }
      const collected: string[] = []
      let j = i + 1
      while (j < lines.length && (lines[j].startsWith('- ') || lines[j].startsWith('  -'))) {
        collected.push(lines[j].replace(/^\s*-\s*/, '').trim())
        j++
      }
      if (collected.length > 0) {
        i = j - 1
        result[key] = collected
      }
      continue
    }
    result[key] = value.replace(/^["']|["']$/g, '')
  }
  return result
}

function splitFrontmatter(raw: string): { meta: Record<string, unknown>; body: string } {
  const match = raw.match(/^---\r?\n([\s\S]*?)\r?\n---\r?\n?/)
  if (!match) return { meta: {}, body: raw }
  return { meta: parseYamlBlock(match[1]), body: raw.slice(match[0].length) }
}

function toStringArray(v: unknown): string[] {
  if (Array.isArray(v)) return v.map(s => String(s))
  if (typeof v === 'string') {
    return v.replace(/^\[|\]$/g, '').split(',').map(s => s.trim()).filter(Boolean)
  }
  return []
}

function collectCards(): Card[] {
  const cards: Card[] = []
  for (const file of fs.readdirSync(WIKI_DIR)) {
    if (!file.endsWith('.md')) continue
    const raw = fs.readFileSync(path.join(WIKI_DIR, file), 'utf-8')
    const { meta, body } = splitFrontmatter(raw)
    cards.push({
      file,
      title: file.replace(/\.md$/, ''),
      type: meta.type ? String(meta.type) : undefined,
      list: toStringArray(meta.list),
      parent: meta.parent ? String(meta.parent) : undefined,
      body,
    })
  }
  return cards
}

function extractWikiWords(body: string): string[] {
  const matches = body.match(/\[\[([^\]]+)\]\]/g) || []
  return matches.map(m => m.slice(2, -2).split('|')[0].trim()).filter(Boolean)
}

const cards = collectCards()
const titles = new Set(cards.map(c => c.title))

const issues: { file: string; title: string; field: string; value: string }[] = []

for (const card of cards) {
  if (card.parent && !titles.has(card.parent)) {
    issues.push({ file: card.file, title: card.title, field: 'parent', value: card.parent })
  }
  for (const item of card.list) {
    if (!titles.has(item)) {
      issues.push({ file: card.file, title: card.title, field: 'list', value: item })
    }
  }
  for (const ww of extractWikiWords(card.body)) {
    if (!titles.has(ww)) {
      issues.push({ file: card.file, title: card.title, field: 'wikiword', value: ww })
    }
  }
}

if (issues.length === 0) {
  console.log('No dangling parent/list/wikiword references found.')
} else {
  console.log(`Found ${issues.length} dangling references:\n`)
  for (const issue of issues) {
    console.log(`${issue.file}: ${issue.field}=${issue.value}`)
  }
}
