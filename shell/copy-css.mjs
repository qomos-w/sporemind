// Post-tsc step: copy *.css alongside the emitted .js files so relative
// `import './Foo.css'` lines in dist/components/*.js resolve.
import { readdirSync, copyFileSync, mkdirSync, existsSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const here = dirname(fileURLToPath(import.meta.url))
const srcDir = join(here, 'src', 'components')
const dstDir = join(here, 'dist', 'components')

if (!existsSync(dstDir)) mkdirSync(dstDir, { recursive: true })

let copied = 0
for (const name of readdirSync(srcDir)) {
  if (!name.endsWith('.css')) continue
  copyFileSync(join(srcDir, name), join(dstDir, name))
  copied++
}
console.log(`shell: copied ${copied} css file(s) to dist/components/`)
