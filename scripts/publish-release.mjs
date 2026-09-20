#!/usr/bin/env node
// Publish a desktop build as a GitHub Release (repo qomos-w/sporemind).
//
// With GH_TOKEN/GITHUB_TOKEN set (repo write scope):
//   GitHub release tag v{public_version}, assets:
//     sporemind-{public_version}-{channel}.exe
//     meta.json
//   R2 keeps only small pointer files (Pages Functions and the desktop updater
//   read these — keeps GitHub API rate limits out of the request path):
//     releases/{channel}/{public_version}/meta.json
//     releases/{channel}/latest.json   (channel pointer, overwritten each publish)
//   meta.url points at the GitHub asset download URL.
//
// Without a token the script falls back to legacy R2 exe hosting (exe uploaded
// to R2, meta.url points at R2) so publishing never blocks on credentials.
//
// Usage:
//   node scripts/publish-release.mjs [--bin build/bin/sporemind.exe]
//                                    [--channel beta] [--notes "first beta"]
//                                    [--public-version 0.3.1] [--repo qomos-w/sporemind]
//                                    [--create-tag] [--dry-run]
// PUBLIC_VERSION defaults to the root PUBLIC_VERSION file.
// Tag policy: the release tag is v{public_version} and must point at the commit
// being published (enforced). --create-tag tags HEAD when the tag doesn't exist.

import { createHash } from 'node:crypto'
import { execFileSync } from 'node:child_process'
import { readFileSync, writeFileSync, statSync, existsSync } from 'node:fs'
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
function hasFlag(name) {
  return process.argv.includes(`--${name}`)
}

const dryRun = hasFlag('dry-run')
const publicVersion = (arg('public-version') || readFileSync(resolve(ROOT, 'PUBLIC_VERSION'), 'utf8')).trim()
const channel = arg('channel', 'beta')
const repo = arg('repo', 'qomos-w/sporemind')
const binPath = resolve(ROOT, arg('bin', 'build/bin/sporemind.exe'))
const notes = arg('notes', '')
const buildSeq = readFileSync(resolve(ROOT, 'version'), 'utf8').trim()
const commit = execFileSync('git', ['rev-parse', '--short', 'HEAD'], { cwd: ROOT }).toString().trim()
const buildTime = new Date().toISOString().replace(/\.d+Z$/, 'Z')
const token = process.env.GH_TOKEN || process.env.GITHUB_TOKEN

const tag = `v${publicVersion}`
const fileName = `sporemind-${publicVersion}-${channel}.exe`

function git(args) {
  return execFileSync('git', args, { cwd: ROOT }).toString().trim()
}

// --- tag consistency: tag must exist and point at HEAD -----------------------
const tagCommit = (() => {
  try {
    return git(['rev-parse', '-q', '--verify', `refs/tags/${tag}^{commit}`])
  } catch {
    return null
  }
})()
const headCommit = git(['rev-parse', 'HEAD'])
if (tagCommit === null) {
  if (!hasFlag('create-tag')) {
    console.error(`[publish] ERROR: tag ${tag} not found. Re-run with --create-tag (tags HEAD) or create it yourself.`)
    process.exit(1)
  }
  if (!dryRun) {
    git(['tag', '-a', tag, '-m', `sporemind ${publicVersion} (${channel})`])
    console.log(`[publish] created tag ${tag} at ${commit}`)
  }
} else if (tagCommit !== headCommit) {
  console.error(`[publish] ERROR: tag ${tag} points at ${tagCommit.slice(0, 7)} but HEAD is ${commit}. Build from the tagged commit or move the tag.`)
  process.exit(1)
}

// --- find the git remote that hosts {repo} ------------------------------------
const slug = repo.toLowerCase()
const remote = git(['remote']).split('\n').map((r) => r.trim()).filter(Boolean)
  .find((r) => git(['remote', 'get-url', r]).toLowerCase().includes(slug))
if (!remote) {
  console.error(`[publish] ERROR: no git remote points at ${repo}. Push a remote first.`)
  process.exit(1)
}

// --- optional Authenticode signing (Windows) -----------------------------------
// Config comes from env only — no certificate identity lives in this repo:
//   SIGN_CERT_SHA1  code-signing certificate thumbprint (CurrentUser\My store)
//   SIGN_TSA_URL    RFC 3161 timestamp server (default: DigiCert)
// Uses PowerShell Set-AuthenticodeSignature (no Windows SDK / signtool needed).
// Works with cloud KSPs such as Certum SimplySign Desktop. Signing is skipped
// with a warning when the cert is not configured; a failed signature aborts.
if (!dryRun && process.platform === 'win32') {
  const signCert = process.env.SIGN_CERT_SHA1
  if (signCert) {
    const tsa = process.env.SIGN_TSA_URL || 'http://timestamp.digicert.com'
    const winPath = binPath.replace(/\//g, '\\')
    const ps1 = [
      `$ErrorActionPreference = 'Stop'`,
      `$c = Get-ChildItem Cert:\\CurrentUser\\My\\${signCert}`,
      `$s = Set-AuthenticodeSignature -FilePath '${winPath}' -Certificate $c -TimestampServer '${tsa}' -HashAlgorithm SHA256`,
      `Write-Output "SIGN_STATUS: $($s.Status)"`,
      `if ($s.Status -ne 'Valid') { exit 2 }`,
    ].join('\n')
    const psPath = resolve(ROOT, 'tmp', 'sign-release.ps1')
    writeFileSync(psPath, ps1)
    console.log(`[publish] signing ${fileName} (tsa ${tsa})`)
    try {
      execFileSync('powershell', ['-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', psPath], { stdio: 'inherit' })
      console.log('[publish] signed')
    } catch {
      console.error('[publish] ERROR: Authenticode signature not valid — aborting.')
      process.exit(1)
    }
  } else {
    console.warn('[publish] WARNING: Authenticode signing skipped — set SIGN_CERT_SHA1 to sign.')
  }
}

// --- artifact facts ------------------------------------------------------------
const size = statSync(binPath).size
const buf = readFileSync(binPath)
const md5 = createHash('md5').update(buf).digest('hex')
const sha256 = createHash('sha256').update(buf).digest('hex')

const prefix = `releases/${channel}/${publicVersion}`
const objectKey = `${prefix}/${fileName}`
const url = token
  ? `https://github.com/${repo}/releases/download/${tag}/${fileName}`
  : `${R2_PUBLIC_BASE}/${objectKey}`

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
console.log(`[publish] commit ${commit}  seq ${buildSeq}  tag ${tag}`)
console.log(`[publish] url    ${url}`)

if (dryRun) {
  console.log('[publish] dry run: stopping before any upload')
  process.exit(0)
}

// --- push the tag ---------------------------------------------------------------
git(['push', remote, `refs/tags/${tag}`])
console.log(`[publish] pushed tag ${tag} to remote '${remote}'`)

function wrangler(...args) {
  execFileSync('npx', ['wrangler', ...args], {
    cwd: ROOT,
    shell: true,
    stdio: ['ignore', 'inherit', 'inherit'],
  })
}

if (token) {
  const GH = 'https://api.github.com'
  const UPLOADS = 'https://uploads.github.com'
  const commonHeaders = {
    Authorization: `Bearer ${token}`,
    Accept: 'application/vnd.github+json',
    'X-GitHub-Api-Version': '2022-11-28',
    'User-Agent': 'sporemind-publish',
  }
  async function ghJson(method, path, body) {
    const res = await fetch(`${GH}${path}`, {
      method,
      headers: { ...commonHeaders, 'content-type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
    const data = await res.json().catch(() => ({}))
    if (!res.ok) throw new Error(`${method} ${path} -> ${res.status} ${JSON.stringify(data).slice(0, 300)}`)
    return data
  }

  // Reuse the release if it already exists for this tag (republish), else create.
  let release = await ghJson('GET', `/repos/${repo}/releases/tags/${tag}`).catch(() => null)
  if (!release) {
    release = await ghJson('POST', `/repos/${repo}/releases`, {
      tag_name: tag,
      name: `sporemind ${publicVersion} (${channel})`,
      body: [
        notes || `sporemind ${publicVersion} — ${channel} channel`,
        '',
        `- file: \`${fileName}\``,
        `- md5: \`${md5}\``,
        `- sha256: \`${sha256}\``,
        `- size: ${size}`,
        `- commit: ${commit}`,
      ].join('\n'),
      prerelease: channel !== 'stable',
    })
    console.log(`[publish] created release ${release.html_url}`)
  } else {
    console.log(`[publish] reusing release ${release.html_url}`)
  }

  async function uploadAsset(name, file, contentType) {
    const existing = release.assets.find((a) => a.name === name)
    if (existing) {
      await fetch(`${GH}/repos/${repo}/releases/assets/${existing.id}`, {
        method: 'DELETE',
        headers: commonHeaders,
      })
    }
    const res = await fetch(`${UPLOADS}/repos/${repo}/releases/${release.id}/assets?name=${encodeURIComponent(name)}`, {
      method: 'POST',
      headers: { ...commonHeaders, 'content-type': contentType },
      body: readFileSync(file),
    })
    const data = await res.json().catch(() => ({}))
    if (!res.ok) throw new Error(`upload ${name} -> ${res.status} ${JSON.stringify(data).slice(0, 300)}`)
    console.log(`[publish] asset ${data.name} -> ${data.browser_download_url}`)
  }

  await uploadAsset(fileName, binPath, 'application/octet-stream')
  await uploadAsset('meta.json', metaPath, 'application/json')
} else {
  console.warn('[publish] WARNING: no GH_TOKEN/GITHUB_TOKEN in env — hosting the exe on R2 instead of GitHub Releases.')
  wrangler('r2', 'object', 'put', `${BUCKET}/${objectKey}`,
    '--file', binPath, '--content-type', 'application/octet-stream', '--remote')
}

// --- R2 pointer files (consumed by Pages Functions / desktop updater) ----------
wrangler('r2', 'object', 'put', `${BUCKET}/${prefix}/meta.json`,
  '--file', metaPath, '--content-type', 'application/json', '--remote')
wrangler('r2', 'object', 'put', `${BUCKET}/releases/${channel}/latest.json`,
  '--file', latestPath, '--content-type', 'application/json', '--remote')

console.log(`[publish] latest   ${R2_PUBLIC_BASE}/releases/${channel}/latest.json`)
console.log('[publish] done')
