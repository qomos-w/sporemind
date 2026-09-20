import fs from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const scriptDir = path.dirname(fileURLToPath(import.meta.url))
const root = path.resolve(scriptDir, '..')
const schemasDir = path.join(root, 'schemas')
const outBase = path.join(root, 'web', 'src', 'gen-types')

const structRegex = /struct\s+(\w+)\s*\{([\s\S]*?)\}/g

function parseSchemaBaseID(filename) {
  const match = filename.match(/\._(\d+)\.spore$/)
  if (!match) {
    throw new Error(`[gen-schema-ts] schema file ${filename} must declare a base id via the ._N filename suffix`)
  }
  return parseInt(match[1], 10)
}

function mapScalar(type) {
  switch (type) {
    case 'string':
      return 'string'
    case 'bool':
      return 'boolean'
    case 'int':
    case 'int32':
    case 'int64':
    case 'uint':
    case 'uint32':
    case 'uint64':
    case 'long':
    case 'float':
    case 'double':
      return 'number'
    case 'any':
      return 'unknown'
    case 'bytes':
      return 'Uint8Array'
    default:
      return type
  }
}

function mapType(raw) {
  const trimmed = raw.trim()
  const arrayMatch = trimmed.match(/^array<(.+)>$/)
  if (arrayMatch) return `${mapType(arrayMatch[1])}[]`
  const mapMatch = trimmed.match(/^map<(.+),\s*(.+)>$/)
  if (mapMatch) return `Record<${mapType(mapMatch[1])}, ${mapType(mapMatch[2])}>`
  return mapScalar(trimmed)
}

function deriveStem(filename) {
  return filename.replace(/\.spore$/, '').replace(/\.\_\d+$/, '')
}

// splitTopLevel splits str by sep, ignoring separators nested inside angle
// brackets so map<K, V> and array<T> type arguments stay intact. This lets
// inline struct bodies like "{ A: X, optional B: Y }" be parsed alongside
// the multi-line one-field-per-line style.
function splitTopLevel(str, sep = ',') {
  const parts = []
  let depth = 0
  let current = ''
  for (const ch of str) {
    if (ch === '<') depth++
    else if (ch === '>') depth--
    if (ch === sep && depth === 0) {
      parts.push(current)
      current = ''
    } else {
      current += ch
    }
  }
  if (current.trim()) parts.push(current)
  return parts
}

function parseStructs(src, filename, baseId) {
  const structs = []
  let match
  let offset = 0
  // Strip // comments before matching so brace-shaped samples inside comments
  // (e.g. "listen <kind> {}") cannot terminate the non-greedy body match early.
  const code = src.replace(/\/\/[^\n]*/g, '')
  while ((match = structRegex.exec(code)) !== null) {
    const [, name, body] = match
    const fields = body
      .split('\n')
      .map(line => line.trim())
      .filter(line => line && !line.startsWith('//'))
      .flatMap(line => {
        const commentIdx = line.indexOf('//')
        const cleanLine = commentIdx >= 0 ? line.slice(0, commentIdx).trimEnd() : line
        return splitTopLevel(cleanLine)
      })
      .map(part => part.trim())
      .filter(part => part)
      .map(part => {
        const optional = part.startsWith('optional ')
        const cleaned = optional ? part.slice('optional '.length) : part
        const fieldMatch = cleaned.match(/^(\w+)\s*:\s*(.+)$/)
        if (!fieldMatch) throw new Error(`Unsupported field syntax in ${name}: ${part}`)
        return {
          name: fieldMatch[1],
          optional,
          type: mapType(fieldMatch[2]),
        }
      })
    structs.push({
      name,
      schemaId: baseId + offset,
      fields,
    })
    offset++
  }
  return structs
}

function emitField(field) {
  const optionalToken = field.optional ? '?' : ''
  const type = field.optional ? `${field.type} | undefined` : field.type
  return `  ${field.name}${optionalToken}: ${type};`
}

function emitStruct(def) {
  const fields = def.fields.map(emitField).join('\n')
  return `export interface ${def.name} {\n${fields}\n}`
}

const builtInTypeNames = new Set(['Record'])

function collectReferencedNames(typeStr) {
  const names = new Set()
  const re = /\b([A-Z][A-Za-z0-9_]*)\b/g
  let m
  while ((m = re.exec(typeStr)) !== null) {
    const n = m[1]
    if (!builtInTypeNames.has(n)) {
      names.add(n)
    }
  }
  return names
}

function emitImports(structs, allNames, currentStem) {
  const localNames = new Set(structs.map(s => s.name))
  const importsByStem = new Map()
  for (const s of structs) {
    for (const f of s.fields) {
      for (const name of collectReferencedNames(f.type)) {
        if (localNames.has(name)) continue
        const info = allNames.get(name)
        if (!info) continue
        if (!importsByStem.has(info.stem)) {
          importsByStem.set(info.stem, new Set())
        }
        importsByStem.get(info.stem).add(name)
      }
    }
  }
  if (importsByStem.size === 0) return ''
  const lines = []
  for (const [stem, names] of importsByStem) {
    const sorted = Array.from(names).sort()
    lines.push(`import { ${sorted.join(', ')} } from './${stem}';`)
  }
  return lines.join('\n') + '\n\n'
}

async function main() {
  const entries = await fs.readdir(schemasDir)
  const sporeFiles = entries.filter(f => f.endsWith('.spore')).sort()

  // Global conflict detection.
  const names = new Map()
  const ids = new Map()
  const fileStructs = []
  const registry = []

  for (const filename of sporeFiles) {
    const filepath = path.join(schemasDir, filename)
    const src = await fs.readFile(filepath, 'utf8')

    if (!src.includes('@ts-export')) {
      continue
    }

    const structs = parseStructs(src, filename, parseSchemaBaseID(filename))
    if (structs.length === 0) {
      console.warn(`[gen-schema-ts] @ts-export in ${filename} but no structs found`)
      continue
    }

    const stem = deriveStem(filename)
    fileStructs.push({ stem, structs })

    for (const s of structs) {
      if (names.has(s.name)) {
        throw new Error(`[gen-schema-ts] duplicate struct name ${s.name}: ${names.get(s.name).stem} and ${stem}`)
      }
      names.set(s.name, { stem, schemaId: s.schemaId })

      if (s.schemaId !== undefined) {
        if (ids.has(s.schemaId)) {
          throw new Error(`[gen-schema-ts] duplicate schema ID ${s.schemaId}: ${ids.get(s.schemaId).name} and ${s.name}`)
        }
        ids.set(s.schemaId, { name: s.name, stem })
        registry.push({ id: s.schemaId, name: s.name })
      }
    }
  }

  // Wipe and recreate the single output directory.
  await fs.rm(outBase, { recursive: true, force: true })
  await fs.mkdir(outBase, { recursive: true })

  // Emit one file per source .spore.
  for (const { stem, structs } of fileStructs) {
    const imports = emitImports(structs, names, stem)
    const typesContent = `// AUTO-GENERATED - DO NOT EDIT. To regenerate:\n//   make gen-schema-ts\n\n${imports}${structs.map(emitStruct).join('\n\n')}\n`
    await fs.writeFile(path.join(outBase, `${stem}.ts`), typesContent, 'utf8')
  }

  // Emit aggregate namespace files for split schemas (e.g. aigen.part1..3 -> aigen.ts).
  const aggregates = new Map() // namespace -> [part stems]
  for (const { stem } of fileStructs) {
    const partMatch = stem.match(/^(.*)\.part(\d+)$/)
    if (!partMatch) continue
    const ns = partMatch[1]
    if (!aggregates.has(ns)) aggregates.set(ns, [])
    aggregates.get(ns).push(stem)
  }
  for (const [ns, parts] of aggregates) {
    parts.sort((a, b) => {
      const na = parseInt(a.match(/\.part(\d+)$/)[1], 10)
      const nb = parseInt(b.match(/\.part(\d+)$/)[1], 10)
      return na - nb
    })
    const reexports = parts.map(p => `export * from './${p}';`).join('\n')
    const aggContent = `// AUTO-GENERATED - DO NOT EDIT. To regenerate:\n//   make gen-schema-ts\n\n${reexports}\n`
    await fs.writeFile(path.join(outBase, `${ns}.ts`), aggContent, 'utf8')
  }

  // Emit unified registry.
  registry.sort((a, b) => a.id - b.id)
  const idEntries = registry.map(r => `  ${r.name}: ${r.id},`).join('\n')
  const nameEntries = registry.map(r => `  ${r.id}: '${r.name}',`).join('\n')
  const registryContent = `// AUTO-GENERATED - DO NOT EDIT. To regenerate:\n//   make gen-schema-ts\n\nexport const SchemaIDs = {\n${idEntries}\n} as const;\n\nexport type SchemaName = keyof typeof SchemaIDs;\n\nexport const SchemaIDToName: Record<number, SchemaName> = {\n${nameEntries}\n};\n`
  await fs.writeFile(path.join(outBase, 'registry.ts'), registryContent, 'utf8')

  const aggCount = aggregates.size
  console.log(`[gen-schema-ts] wrote ${fileStructs.length} file(s) + ${aggCount} aggregate(s) + registry.ts to ${path.relative(root, outBase)} (${registry.length} schema IDs)`)
}

main().catch((error) => {
  console.error('[gen-schema-ts] failed:', error)
  process.exitCode = 1
})
