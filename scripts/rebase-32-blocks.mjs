import fs from 'node:fs/promises'
import path from 'node:path'

const root = process.cwd()
const schemasDir = path.resolve(root, 'schemas')
const BLOCK_SIZE = 32

// Reserved ID blocks per functional module. Base IDs are fixed; each namespace
// gets `size` consecutive slots, split into 32-struct part files as needed.
// Keep this table sorted by base and non-overlapping.
const BLOCKS = [
  { stem: 'agent', base: 300, size: 128 },
  { stem: 'agent.chat', base: 428, size: 32 },
  { stem: 'agent.message', base: 460, size: 32 },
  { stem: 'aigen', base: 492, size: 256 },
  { stem: 'computeruse', base: 748, size: 128 },
  { stem: 'browser', base: 876, size: 64 },
  { stem: 'filesystem', base: 940, size: 64 },
  { stem: 'frp', base: 1004, size: 32 },
  { stem: 'inspect', base: 1036, size: 32 },
  { stem: 'observation', base: 1068, size: 32 },
  { stem: 'oracle', base: 1100, size: 64 },
  { stem: 'project', base: 1164, size: 128 },
  { stem: 'project.graph', base: 1292, size: 64 },
  { stem: 'project.kanban', base: 1356, size: 32 },
  { stem: 'prompt', base: 1388, size: 64 },
  { stem: 'provider', base: 1452, size: 64 },
  { stem: 'shell', base: 1516, size: 32 },
  { stem: 'skill', base: 1548, size: 64 },
  { stem: 'sshmanager', base: 1612, size: 64 },
  { stem: 'user', base: 1676, size: 64 },
  { stem: 'voice', base: 1740, size: 32 },
  { stem: 'workspace', base: 1772, size: 256 },
  { stem: 'events', base: 2028, size: 32 },
  { stem: 'desktop', base: 2060, size: 32 },
]

function parseStem(filename) {
  return filename.replace(/\.spore$/, '').replace(/\.\_\d+$/, '')
}

function parseNamespace(stem) {
  return stem.replace(/\.part\d+$/, '')
}

function parsePartNumber(stem) {
  const match = stem.match(/\.part(\d+)$/)
  return match ? parseInt(match[1], 10) : 1
}

function parseStructBlocks(src) {
  // Strip legacy explicit @schema(N) annotations from struct declarations.
  src = src.replace(/struct\s+@schema\(\d+\)\s+/g, 'struct ')
  const lines = src.split('\n')
  let i = 0
  const headerLines = []

  while (i < lines.length) {
    const line = lines[i]
    // Support both `struct Name {` and legacy `struct @schema(N) Name {`.
    if (/^\s*struct(\s+@schema\(\d+\))?\s+\w+\s*\{/.test(line)) {
      break
    }
    headerLines.push(line)
    i++
  }
  const header = headerLines.join('\n').trimEnd()
  const headerEnd = i

  const blocks = []
  while (i < lines.length) {
    const line = lines[i]
    const match = line.match(/^(\s*)struct(?:\s+@schema\(\d+\))?\s+(\w+)\s*\{/)
    if (!match) {
      i++
      continue
    }

    const commentLines = []
    let j = i - 1
    while (j >= headerEnd) {
      const prev = lines[j]
      if (/^\s*$/.test(prev) || /^\s*\/\//.test(prev)) {
        commentLines.unshift(prev)
        j--
      } else {
        break
      }
    }

    let braceDepth = 0
    let k = i
    let structEnd = i
    while (k < lines.length) {
      const ln = lines[k]
      for (const ch of ln) {
        if (ch === '{') braceDepth++
        else if (ch === '}') braceDepth--
      }
      if (braceDepth === 0) {
        structEnd = k
        break
      }
      k++
    }

    const blockLines = [...commentLines, ...lines.slice(i, structEnd + 1)]
    blocks.push(blockLines.join('\n'))
    i = structEnd + 1
  }

  return { header, blocks }
}

async function main() {
  const entries = await fs.readdir(schemasDir)
  const sporeFiles = entries.filter(f => f.endsWith('.spore'))

  const blockByStem = new Map(BLOCKS.map(b => [b.stem, b]))
  const reserved = new Map()
  for (const b of BLOCKS) {
    reserved.set(b.stem, { base: b.base, end: b.base + b.size })
  }

  // Group existing files by namespace and order by part number.
  const groups = new Map()
  for (const filename of sporeFiles) {
    const stem = parseStem(filename)
    const ns = parseNamespace(stem)
    const part = parsePartNumber(stem)
    if (!groups.has(ns)) groups.set(ns, [])
    groups.get(ns).push({ filename, stem, part })
  }
  for (const files of groups.values()) {
    files.sort((a, b) => a.part - b.part)
  }

  // Validate every namespace has a reserved block.
  for (const ns of groups.keys()) {
    if (!reserved.has(ns)) {
      throw new Error(`[rebase-32] no reserved block for namespace ${ns}`)
    }
  }

  // Merge structs per namespace and build write plan.
  const plan = []
  for (const [ns, files] of groups) {
    const { base, end } = reserved.get(ns)
    const blocks = []
    let header = ''

    for (const { filename } of files) {
      const filepath = path.join(schemasDir, filename)
      const src = await fs.readFile(filepath, 'utf8')
      const parsed = parseStructBlocks(src)
      if (header === '') header = parsed.header
      blocks.push(...parsed.blocks)
    }

    const chunkCount = Math.ceil(blocks.length / BLOCK_SIZE)
    const neededEnd = base + chunkCount * BLOCK_SIZE
    if (neededEnd > end) {
      throw new Error(
        `[rebase-32] namespace ${ns} needs ${blocks.length} structs (${neededEnd - base} slots) but block is only ${end - base}`
      )
    }

    const filePlan = []
    for (let c = 0; c < chunkCount; c++) {
      const chunkStem = chunkCount === 1 ? ns : `${ns}.part${c + 1}`
      const chunkBase = base + c * BLOCK_SIZE
      const chunkBlocks = blocks.slice(c * BLOCK_SIZE, (c + 1) * BLOCK_SIZE)
      filePlan.push({ stem: chunkStem, base: chunkBase, blocks: chunkBlocks })
    }
    plan.push({ ns, oldFiles: files.map(f => f.filename), header, filePlan })
  }

  // Write new files and remove old ones.
  for (const { ns, oldFiles, header, filePlan } of plan) {
    const written = new Set()
    for (const { stem, base: chunkBase, blocks } of filePlan) {
      const newFilename = `${stem}._${chunkBase}.spore`
      const newPath = path.join(schemasDir, newFilename)
      const body = blocks.join('\n\n')
      const content = `${header}\n\n${body}\n`
      await fs.writeFile(newPath, content, 'utf8')
      written.add(newFilename)
      console.log(`[rebase-32] wrote ${newFilename} (${blocks.length} structs)`)
    }

    for (const oldFilename of oldFiles) {
      if (written.has(oldFilename)) continue
      const oldPath = path.join(schemasDir, oldFilename)
      await fs.unlink(oldPath)
      console.log(`[rebase-32] removed ${oldFilename}`)
    }
  }

  const totalFiles = plan.reduce((n, p) => n + p.filePlan.length, 0)
  console.log(`[rebase-32] reassigned ${totalFiles} file(s) across ${plan.length} namespace(s)`)
}

main().catch(err => {
  console.error('[rebase-32] failed:', err)
  process.exitCode = 1
})
