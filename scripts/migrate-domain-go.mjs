import fs from 'node:fs/promises'

const file = process.argv[2]
if (!file) {
  console.error('usage: node migrate-domain-go.mjs <path>')
  process.exit(1)
}

let src = await fs.readFile(file, 'utf8')

// Replace import block.
src = src.replace(
  /import\s*\(\r?\n[\s\S]*?\r?\n\)/,
  `import (\n\t"fmt"\n\n\tgen "github.com/qomos-w/sporemind/pkg/domain/gen"\n)`
)

// Replace only type alias lines: type X = pkg.Type -> type X = gen.Type
src = src.replace(/^type\s+(\w+)\s*=\s+([a-zA-Z_][a-zA-Z0-9_]*)\.(\w+)\s*$/gm, 'type $1 = gen.$3')

await fs.writeFile(file, src, 'utf8')
console.log('[migrate-domain-go] done')
