#!/usr/bin/env node
// Publish a desktop test build to the Cloudflare R2 release bucket.
//
// Layout (see wiki card "Cloudflare R2 二进制分发改造"):
//   releases/{channel}/{public_version}/sporemind-{public_version}-{channel}.exe
//   releases/{channel}/{public_version}/meta.json
//   releases/{channel}/latest.json   (channel pointer, overwritten each publish)
//
// Usage:
//   node scripts/publish-release.mjs [--bin build/bin/sporemind.exe]
//                                    [--channel beta] [--notes "first beta"]
// PUBLIC_VERSION defaults to the root PUBLIC_VERSION file.

import { createHash } from 'node:crypto'
import { execFileSync } from 'node:child_process'
import { readFileSync, writeFileSync, statSync } from 'node:fs'
import { resolve, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const BUCKET = 'sporemind-downloads'
const R2_PUBLIC_BASE = 'https://pub-0bbae7fdac9547609439416396df20e4.r2.dev'

function arg(name, fallback) {
  const i = process.argv.indexOf(`--${name}`)
  return i >= 0 && process.argv[i + 1] && !process.argv[i + 1].startsWith('--')
    ? process.argv[i + 1]
    : fallback
}

const publicVersion = (arg('public-version') || readFileSync(resolve(ROOT, 'PUBLIC_VERSION'), 'utf8')).trim()
const channel = arg('channel', 'beta')
const binPath = resolve(ROOT, arg('bin', 'build/bin/sporemind.exe'))
const notes = arg('notes', '')
const buildSeq = readFileSync(resolve(ROOT, 'version'), 'utf8').trim()
const commit = execFileSync('git', ['rev-parse', '--short', 'HEAD'], { cwd: ROOT }).toString().trim()
const buildTime = new Date().toISOString().replace(/\.\d+Z$/, 'Z')

const size = statSync(binPath).size
const buf = readFileSync(binPath)
const md5 = createHash('md5').update(buf).digest('hex')
const sha256 = createHash('sha256').update(buf).digest('hex')

const fileName = `sporemind-${publicVersion}-${channel}.exe`
const prefix = `releases/${channel}/${publicVersion}`
const objectKey = `${prefix}/${fileName}`
const url = `${R2_PUBLIC_BASE}/${objectKey}`

const meta = {
  public_version: publicVersion,
  channel,
  build_seq: buildSeq,
  platform: 'windows-x64',
  file: fileName,
  url,
  md5,
  sha256,
  size,
  git_commit: commit,
  build_time: buildTime,
  notes,
}

const metaPath = resolve(ROOT, 'tmp', `meta-${publicVersion}-${channel}.json`)
const latestPath = resolve(ROOT, 'tmp', `latest-${channel}.json`)
writeFileSync(metaPath, JSON.stringify(meta, null, 2) + '\n')
writeFileSync(latestPath, JSON.stringify(meta, null, 2) + '\n')

console.log(`[publish] ${fileName} (${(size / 1048576).toFixed(1)} MiB)`)
console.log(`[publish] md5    ${md5}`)
console.log(`[publish] sha256 ${sha256}`)
console.log(`[publish] commit ${commit}  seq ${buildSeq}`)

function wrangler(...args) {
  execFileSync('npx', ['wrangler', ...args], {
    cwd: ROOT,
    shell: true,
    stdio: ['ignore', 'inherit', 'inherit'],
  })
}

wrangler('r2', 'object', 'put', `${BUCKET}/${objectKey}`,
  '--file', binPath, '--content-type', 'application/octet-stream', '--remote')
wrangler('r2', 'object', 'put', `${BUCKET}/${prefix}/meta.json`,
  '--file', metaPath, '--content-type', 'application/json', '--remote')
wrangler('r2', 'object', 'put', `${BUCKET}/releases/${channel}/latest.json`,
  '--file', latestPath, '--content-type', 'application/json', '--remote')

console.log(`[publish] uploaded ${objectKey}`)
console.log(`[publish] latest   ${R2_PUBLIC_BASE}/releases/${channel}/latest.json`)
