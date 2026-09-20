// Generates pkg/actor/appmanager/icon_catalog.gen.json from the frontend card
// icon catalog. The JSON is embedded by the appmanager actor to serve
// appmanager.icon_names, so agents doing native app / bundle development can
// discover valid icon names. Run via: node --experimental-strip-types
// web/scripts/gen-icon-catalog.ts [--check]
//
//   --check  verify the committed JSON is in sync; exit 1 on drift (CI).

import { writeFileSync, readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { CARD_ICON_CATALOG } from '../src/ui/ai/components/cardIconCatalog.ts'

const here = dirname(fileURLToPath(import.meta.url))
const outPath = join(here, '..', '..', 'pkg', 'actor', 'appmanager', 'icon_catalog.gen.json')

const seen = new Set<string>()
for (const [name] of CARD_ICON_CATALOG) {
  if (seen.has(name)) throw new Error(`duplicate icon name: ${name}`)
  seen.add(name)
}

const entries = CARD_ICON_CATALOG.map(([name, label, keywords, category]) => ({
  Name: name,
  Label: label,
  Keywords: keywords,
  Category: category,
}))

const payload = JSON.stringify(entries, null, 2) + '\n'

if (process.argv.includes('--check')) {
  const current = readFileSync(outPath, 'utf8')
  if (current !== payload) {
    console.error('[gen-icon-catalog] icon_catalog.gen.json is out of sync with cardIconCatalog.ts. Run gen-icon-catalog and commit.')
    process.exit(1)
  }
  console.log(`[gen-icon-catalog] ok (${entries.length} icons)`)
} else {
  writeFileSync(outPath, payload)
  console.log(`[gen-icon-catalog] wrote ${outPath} (${entries.length} icons)`)
}
