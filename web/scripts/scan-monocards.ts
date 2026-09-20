import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { parseMonoCard, validateMonoCard, normalizeMonoCardType, type MonoCard } from '../src/domain/mono-types'

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)

const WIKI_DIR = process.env.WIKI_DIR
  ? path.resolve(process.env.WIKI_DIR)
  : path.resolve(__dirname, '../../.sporecode/wiki')

interface Issue {
  filePath: string
  id: string
  errors: string[]
  suggestions: string[]
}

function walk(dir: string): string[] {
  const results: string[] = []
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name)
    if (entry.isDirectory()) {
      if (entry.name === '.migration-backup') continue
      results.push(...walk(full))
    } else if (entry.isFile() && entry.name.endsWith('.md')) {
      results.push(full)
    }
  }
  return results
}

function suggest(card: MonoCard, error: string): string {
  if (error.startsWith('invalid type')) {
    const normalized = normalizeMonoCardType(card.type)
    if (normalized !== (card.type ?? '').trim().toLowerCase()) {
      return `将 type 改为 "${normalized}"（ legacy 类型已归一化）`
    }
    return 'type 不在规范集合中，需手动指定为 agent/prompt/skill/knowledge/concept/callable/capability_module/scheduler/task/wiki/workflow/bundle 之一'
  }
  if (error.startsWith('forbidden tag')) {
    const match = error.match(/"([^"]+)"/)
    const tag = match ? match[1] : ''
    return `移除 tag "${tag}"；系统保留 tag 不可用于用户卡片`
  }
  if (error.startsWith('parent')) {
    if (card.parent?.startsWith('__builtin_')) {
      return `parent 指向系统卡片 "${card.parent}"，需改为普通卡片 id 或清空`
    }
    return `将 "${card.parent}" 加入 tags，或清空 parent，或把第一个 tag 改为其父节点`
  }
  return '需人工判断'
}

function scan(): { total: number; missingFrontmatter: number; issues: Issue[] } {
  if (!fs.existsSync(WIKI_DIR)) {
    console.error(`Wiki directory not found: ${WIKI_DIR}`)
    process.exit(2)
  }

  const files = walk(WIKI_DIR)
  const issues: Issue[] = []
  let missingFrontmatter = 0

  for (const filePath of files) {
    const raw = fs.readFileSync(filePath, 'utf-8')
    const id = path.relative(WIKI_DIR, filePath).replace(/\.md$/, '')
    const card = parseMonoCard(id, raw)
    if (!card) {
      missingFrontmatter++
      issues.push({
        filePath,
        id,
        errors: ['missing frontmatter'],
        suggestions: ['补充 frontmatter（至少包含 title 与 type）'],
      })
      continue
    }

    const errors = validateMonoCard(card)
    if (errors.length > 0) {
      issues.push({
        filePath,
        id,
        errors,
        suggestions: errors.map(e => suggest(card, e)),
      })
    }
  }

  return { total: files.length, missingFrontmatter, issues }
}

function main() {
  const { total, missingFrontmatter, issues } = scan()
  const invalidCount = issues.length

  console.log(`Wiki dir: ${WIKI_DIR}`)
  console.log(`Total .md files: ${total}`)
  console.log(`Missing frontmatter: ${missingFrontmatter}`)
  console.log(`Invalid cards: ${invalidCount}`)
  console.log('')

  if (invalidCount === 0) {
    console.log('All cards are valid under the new monocard rules.')
    return
  }

  // Group by error kind for summary.
  const counts: Record<string, number> = {}
  for (const issue of issues) {
    for (const err of issue.errors) {
      const kind = err.split('"')[0]!.trim()
      counts[kind] = (counts[kind] ?? 0) + 1
    }
  }
  console.log('Issue breakdown:')
  for (const [kind, count] of Object.entries(counts).sort((a, b) => b[1] - a[1])) {
    console.log(`  ${kind}: ${count}`)
  }
  console.log('')

  console.log('Invalid cards:')
  for (const { filePath, id, errors, suggestions } of issues) {
    console.log(`\n${id}`)
    console.log(`  file: ${path.relative(process.cwd(), filePath)}`)
    for (let i = 0; i < errors.length; i++) {
      console.log(`  - ${errors[i]}`)
      console.log(`    suggestion: ${suggestions[i]}`)
    }
  }

  process.exit(1)
}

main()
