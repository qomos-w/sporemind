import fs from 'node:fs'
import path from 'node:path'
import { execSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import {
  parseMonoCard,
  normalizeMonoCardType,
  isCanonicalMonoCardType,
  type MonoCard,
} from '../src/domain/mono-types'

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)

const WIKI_DIR = process.env.WIKI_DIR
  ? path.resolve(process.env.WIKI_DIR)
  : path.resolve(__dirname, '../../.sporecode/wiki')

const DRY_RUN = !process.argv.includes('--apply')
const BACKUP_DIR = path.join(WIKI_DIR, '.migration-backup')

interface MigrationResult {
  filePath: string
  id: string
  changed: boolean
  changes: string[]
  newRaw?: string
  manual?: string[]
}

function walk(dir: string): string[] {
  const results: string[] = []
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name)
    if (entry.isDirectory()) {
      if (full === BACKUP_DIR) continue
      results.push(...walk(full))
    } else if (entry.isFile() && entry.name.endsWith('.md')) {
      results.push(full)
    }
  }
  return results
}

function detectLineEnding(raw: string): string {
  return raw.includes('\r\n') ? '\r\n' : '\n'
}

function fixMalformedClosing(raw: string): string | null {
  const pattern = /^(---\r?\n[\s\S]*?[^\r\n])---(\r?\n|$)/m
  if (!pattern.test(raw)) return null
  const eol = detectLineEnding(raw)
  return raw.replace(pattern, `$1${eol}---$2`)
}

function isBuiltinTag(tag: string): boolean {
  return tag === 'builtin' || tag.startsWith('__builtin_')
}

function formatInlineArray(items: string[]): string {
  if (items.length === 0) return '[]'
  return `[${items.join(', ')}]`
}

function updateFrontmatterKey(raw: string, key: string, value: string | null): string {
  const pattern = new RegExp(`^([ \\t]*${key}:[ \\t]*)([^\\r\\n]*)(\\r?\\n)`, 'm')
  if (value === null) {
    return raw.replace(pattern, '')
  }
  if (pattern.test(raw)) {
    return raw.replace(pattern, `$1${value}$3`)
  }
  const eol = detectLineEnding(raw)
  const titlePattern = /^([ \t]*title:[^\r\n]*)(\r?\n)/m
  const line = `${key}: ${value}${eol}`
  if (titlePattern.test(raw)) {
    return raw.replace(titlePattern, `$1${eol}${line}`)
  }
  const openingPattern = /^(---\r?\n)/m
  return raw.replace(openingPattern, `$1${line}`)
}

function updateTagsLine(raw: string, oldTags: string[], newTags: string[]): string {
  if (JSON.stringify(oldTags) === JSON.stringify(newTags)) return raw
  const pattern = /^([ \t]*tags:[ \t]*)(\[[^\r\n]*\])(\r?\n)/m
  if (pattern.test(raw)) {
    return raw.replace(pattern, `$1${formatInlineArray(newTags)}$3`)
  }
  return updateFrontmatterKey(raw, 'tags', formatInlineArray(newTags))
}

function removeDataType(raw: string): string {
  return raw.replace(/^  type:[^\r\n]*(\r?\n?)/m, '')
}

function quickValidate(card: MonoCard): { isValid: boolean; errors: string[] } {
  const errors: string[] = []
  if (!isCanonicalMonoCardType(card.type)) {
    errors.push(`invalid type "${card.type ?? ''}"`)
  }
  if (!card.id.startsWith('__builtin_') && !card.id.includes(':')) {
    for (const tag of card.tags) {
      if (isBuiltinTag(tag)) errors.push(`forbidden tag "${tag}"`)
    }
    if (card.parent && card.parent !== card.id && !card.tags.includes(card.parent)) {
      errors.push(`parent "${card.parent}" must be one of the card's tags`)
    }
  }
  return { isValid: errors.length === 0, errors }
}

function collectGitRenameMap(wikiDir: string): Map<string, string> {
  const map = new Map<string, string>()
  try {
    const stdout = execSync(
      'git -c core.quotepath=false log --all --pretty=format: --name-status --find-renames=50 --diff-filter=R -- "*.md"',
      { cwd: wikiDir, encoding: 'utf-8' }
    )
    for (const line of stdout.split('\n')) {
      const parts = line.split('\t')
      if (parts.length !== 3 || !parts[0].startsWith('R')) continue
      const oldPath = parts[1]
      const newPath = parts[2]
      if (!oldPath.startsWith('.sporecode/wiki/') || !newPath.startsWith('.sporecode/wiki/')) continue
      const oldId = oldPath.replace(/^\.sporecode\/wiki\//, '').replace(/\.md$/, '')
      const newId = newPath.replace(/^\.sporecode\/wiki\//, '').replace(/\.md$/, '')
      if (oldId && newId && oldId !== newId) {
        map.set(oldId, newId)
      }
    }
  } catch {
    // Not a git repo or no history; proceed without rename map.
  }
  return map
}

const RENAME_MAP = collectGitRenameMap(WIKI_DIR)

function rewriteRenamedReferences(raw: string, renameMap: Map<string, string>): { raw: string; changed: boolean } {
  if (renameMap.size === 0) return { raw, changed: false }
  let changed = false

  // parent: single value
  const parentPattern = /^([ \t]*parent:[ \t]*)([^\r\n]*)(\r?\n)/m
  const parentMatch = raw.match(parentPattern)
  if (parentMatch) {
    const parentValue = parentMatch[2].trim()
    const replacement = renameMap.get(parentValue)
    if (replacement) {
      raw = raw.replace(parentPattern, `$1${replacement}$3`)
      changed = true
    }
  }

  // tags / list: inline arrays
  for (const key of ['tags', 'list']) {
    const arrayPattern = new RegExp(`^([ \\t]*${key}:[ \\t]*)\\[[^\\r\\n]*\\](\\r?\\n)`, 'm')
    const match = raw.match(arrayPattern)
    if (!match) continue
    const fullLine = match[0]
    const prefix = match[1]
    const suffix = match[2]
    const items = fullLine
      .slice(prefix.length, fullLine.length - suffix.length)
      .replace(/^\[/, '')
      .replace(/\]$/, '')
      .split(',')
      .map(s => s.trim())
      .filter(Boolean)
    const newItems = items.map(item => renameMap.get(item) ?? item)
    if (newItems.some((item, i) => item !== items[i])) {
      raw = raw.replace(arrayPattern, `${prefix}${formatInlineArray(newItems)}${suffix}`)
      changed = true
    }
  }

  // wikiword links in body
  for (const [oldId, newId] of renameMap.entries()) {
    const wikiPattern = new RegExp(`\\[\\[${escapeRegExp(oldId)}(\\|[^\\]]*)?\\]\\]`, 'g')
    if (wikiPattern.test(raw)) {
      raw = raw.replace(wikiPattern, `[[${newId}$1]]`)
      changed = true
    }
  }

  return { raw, changed }
}

function cleanupDanglingReferences(raw: string, titles: Set<string>): { raw: string; changed: boolean } {
  let changed = false

  // parent: remove if it points to a non-existent, non-builtin card
  const parentPattern = /^([ \t]*parent:[ \t]*)([^\r\n]*)(\r?\n)/m
  const parentMatch = raw.match(parentPattern)
  if (parentMatch) {
    const parentValue = parentMatch[2].trim()
    if (parentValue && !titles.has(parentValue) && !parentValue.startsWith('__builtin_')) {
      raw = raw.replace(parentPattern, '')
      changed = true
    }
  }

  return { raw, changed }
}

function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

function migrateFile(filePath: string, titles: Set<string>): MigrationResult {
  const id = path.relative(WIKI_DIR, filePath).replace(/\.md$/, '')
  const originalRaw = fs.readFileSync(filePath, 'utf-8')
  let raw = originalRaw
  const changes: string[] = []

  // 0. Rewrite parent/tags/list/wikiword references that still point to pre-rename IDs.
  const rewritten = rewriteRenamedReferences(raw, RENAME_MAP)
  if (rewritten.changed) {
    raw = rewritten.raw
    changes.push('rewrote renamed ID references to current titles')
  }

  // 0.5. Remove parent references that point to cards which no longer exist.
  const cleaned = cleanupDanglingReferences(raw, titles)
  if (cleaned.changed) {
    raw = cleaned.raw
    changes.push('removed dangling parent reference')
  }

  // 1. Fix malformed frontmatter closing.
  const fixed = fixMalformedClosing(raw)
  if (fixed) {
    raw = fixed
    changes.push('fixed malformed frontmatter closing (---)')
  }

  // 1.5. Rename frontmatter title: → id:, strip builtin_title:.
  const rawAfterIdFix = (() => {
    let r = raw
    const hadBuiltin = /^[ \t]*builtin_title:[^\r\n]*(\r?\n?)/m.test(r)
    if (hadBuiltin) {
      r = r.replace(/^[ \t]*builtin_title:[^\r\n]*(\r?\n?)/m, '')
    }
    const hasId = /^[ \t]*id:[ \t]*/m.test(r)
    const hasTitle = /^[ \t]*title:[ \t]*/m.test(r)
    if (hasTitle) {
      if (hasId) {
        r = r.replace(/^[ \t]*title:[^\r\n]*\r?\n?/m, '')
        changes.push('removed duplicate title: (id: already present)')
      } else {
        r = r.replace(/^([ \t]*)title(:[ \t]*[^\r\n]*)/m, '$1id$2')
        changes.push('renamed frontmatter title: → id:')
      }
    }
    if (hadBuiltin) changes.push('removed builtin_title frontmatter field')
    return r
  })()
  raw = rawAfterIdFix

  let card = parseMonoCard(id, raw)
  if (!card) {
    return { filePath, id, changed: false, changes, manual: ['missing or unparseable frontmatter'] }
  }

  // 2. Infer / normalize top-level type.
  let desiredType = normalizeMonoCardType(card.type)
  if (desiredType === 'wiki' && card.data && typeof card.data.type === 'string') {
    const dataType = normalizeMonoCardType(card.data.type)
    if (dataType !== 'wiki') {
      desiredType = dataType
      changes.push(`set type "${desiredType}" from data.type "${card.data.type}"`)
      raw = removeDataType(raw)
    }
  }
  if (desiredType === 'wiki' && id.startsWith('skill$')) {
    desiredType = 'skill'
    changes.push('set type "skill" from skill$ id prefix')
  }
  if (desiredType === 'wiki' && card.tags.includes('plan') && (!card.type || card.type === '')) {
    desiredType = 'task'
    changes.push('set type "task" because card is tagged "plan"')
  }
  if (desiredType !== normalizeMonoCardType(card.type)) {
    raw = updateFrontmatterKey(raw, 'type', desiredType)
    card = parseMonoCard(id, raw)!
  }

  // 3. Remove forbidden tags; infer skill type if needed.
  const forbidden = card.tags.filter(isBuiltinTag)
  if (forbidden.length > 0) {
    const oldTags = [...card.tags]
    const newTags = card.tags.filter(t => !isBuiltinTag(t))
    raw = updateTagsLine(raw, oldTags, newTags)
    changes.push(`removed forbidden tags: ${forbidden.join(', ')}`)
    card = parseMonoCard(id, raw)!
    if (forbidden.includes('__builtin_skill__') && card.type !== 'skill') {
      raw = updateFrontmatterKey(raw, 'type', 'skill')
      changes.push('set type "skill" because __builtin_skill__ tag was removed')
      card = parseMonoCard(id, raw)!
    }
  }

  // 4. Remove builtin/system parents, except for task status nodes which stay
  // under __builtin_task__.
  const taskStatusBuiltins = new Set([
    '__builtin_backlog__',
    '__builtin_todo__',
    '__builtin_doing__',
    '__builtin_done__',
    '__builtin_blocked__',
    '__builtin_cancelled__',
  ])
  const keepBuiltinParent = taskStatusBuiltins.has(id) && card.parent === '__builtin_task__'
  if (card.parent && !keepBuiltinParent && (card.parent.startsWith('__builtin_') || card.parent === 'builtin')) {
    raw = updateFrontmatterKey(raw, 'parent', null)
    changes.push(`removed builtin parent "${card.parent}"`)
    card = parseMonoCard(id, raw)!
  }

  // 5. Ensure parent is in tags (only for non-builtin cards).
  if (!card.id.startsWith('__builtin_') && card.parent && card.parent !== card.id && !card.tags.includes(card.parent)) {
    const oldTags = [...card.tags]
    const newTags = [card.parent, ...card.tags]
    raw = updateTagsLine(raw, oldTags, newTags)
    changes.push(`added parent "${card.parent}" to tags`)
    card = parseMonoCard(id, raw)!
  }

  const { isValid, errors } = quickValidate(card)
  const changed = raw !== originalRaw

  return {
    filePath,
    id,
    changed,
    changes,
    newRaw: changed ? raw : undefined,
    manual: isValid ? [] : errors,
  }
}

function main() {
  if (!fs.existsSync(WIKI_DIR)) {
    console.error(`Wiki directory not found: ${WIKI_DIR}`)
    process.exit(2)
  }

  const files = walk(WIKI_DIR)
  const titles = new Set(files.map(filePath => path.relative(WIKI_DIR, filePath).replace(/\.md$/, '')))
  const results: MigrationResult[] = []
  for (const filePath of files) {
    results.push(migrateFile(filePath, titles))
  }

  const changed = results.filter(r => r.changed)
  const manual = results.filter(r => r.manual.length > 0)
  const autoFixed = changed.filter(r => r.manual.length === 0)
  const stillInvalid = manual

  console.log(`Mode: ${DRY_RUN ? 'DRY-RUN' : 'APPLY'}`)
  console.log(`Wiki dir: ${WIKI_DIR}`)
  console.log(`Total .md files: ${files.length}`)
  console.log(`Cards changed: ${changed.length}`)
  console.log(`  - automatically valid after migration: ${autoFixed.length}`)
  console.log(`  - still need manual review: ${stillInvalid.length}`)
  console.log('')

  if (changed.length === 0) {
    console.log('No migrations needed.')
    return
  }

  console.log('=== Auto-fixable changes ===')
  for (const r of autoFixed) {
    console.log(`\n${r.id}`)
    console.log(`  file: ${path.relative(process.cwd(), r.filePath)}`)
    for (const c of r.changes) console.log(`  - ${c}`)
  }

  if (stillInvalid.length > 0) {
    console.log('\n=== Needs manual review ===')
    for (const r of stillInvalid) {
      console.log(`\n${r.id}`)
      console.log(`  file: ${path.relative(process.cwd(), r.filePath)}`)
      for (const c of r.changes) console.log(`  migrated: ${c}`)
      for (const m of r.manual) console.log(`  manual: ${m}`)
    }
  }

  if (DRY_RUN) {
    console.log('\n=== DRY-RUN complete ===')
    console.log(`Run with --apply to write ${autoFixed.length} auto-fixable changes to disk.`)
    console.log(`Backups will be written to: ${path.relative(process.cwd(), BACKUP_DIR)}`)
    return
  }

  if (!fs.existsSync(BACKUP_DIR)) {
    fs.mkdirSync(BACKUP_DIR, { recursive: true })
  }
  for (const r of autoFixed) {
    if (!r.newRaw) continue
    const rel = path.relative(WIKI_DIR, r.filePath)
    const backupPath = path.join(BACKUP_DIR, rel)
    fs.mkdirSync(path.dirname(backupPath), { recursive: true })
    fs.copyFileSync(r.filePath, backupPath)
    fs.writeFileSync(r.filePath, r.newRaw, 'utf-8')
  }
  console.log(`\nApplied ${autoFixed.length} migrations. Backups in ${BACKUP_DIR}`)
}

main()
