/**
 * Quick-commit prompt builder. Quick commit is a latency- and token-sensitive
 * action: the prompt carries a compact file manifest with +/- counts and
 * hard-capped diff excerpts instead of the full diff (a large change set used
 * to ship 100KB+ to the model).
 */

export interface CommitFile {
  path: string
  /** git status letter, e.g. M / A / D / ? */
  status: string
  /** raw unified diff text ('' when unavailable) */
  diff: string
}

export interface CommitPromptInput {
  files: CommitFile[]
  /** pre-formatted recent commit lines, used as a style/language reference */
  recentCommits: string[]
}

const TOTAL_DIFF_BUDGET = 6000
const PER_FILE_CAP = 1200

/** Generated/lock artifacts: listed in the manifest but never excerpted. */
const GENERATED_RE =
  /(^|\/)(package-lock\.json|pnpm-lock\.yaml|yarn\.lock|poetry\.lock|Cargo\.lock)|\.lock$|\.min\.(js|css)$|\.gen\.go$|\.gen\.part\d*\.go$|(^|\/)(dist|build|node_modules|coverage)\//

export function isGeneratedPath(path: string): boolean {
  return GENERATED_RE.test(path)
}

function addedRemoved(diff: string): string {
  let add = 0
  let del = 0
  for (const line of diff.split('\n')) {
    if (line.startsWith('+++') || line.startsWith('---')) continue
    if (line.startsWith('+')) add++
    else if (line.startsWith('-')) del++
  }
  if (add === 0 && del === 0) return ''
  return ` (+${add} -${del})`
}

function truncateDiff(path: string, diff: string): string {
  if (diff.length <= PER_FILE_CAP) return diff
  const cut = diff.slice(0, PER_FILE_CAP)
  const nl = cut.lastIndexOf('\n')
  return `${nl > 0 ? cut.slice(0, nl) : cut}\n… (${path} truncated)`
}

export function buildCommitPrompt(input: CommitPromptInput): string {
  const manifest = input.files
    .map(f => `  ${f.status} ${f.path}${addedRemoved(f.diff)}`)
    .join('\n')

  const excerpts: string[] = []
  let budget = TOTAL_DIFF_BUDGET
  let omitted = 0
  for (const f of input.files) {
    if (!f.diff || isGeneratedPath(f.path)) continue
    if (budget <= 0) {
      omitted++
      continue
    }
    const excerpt = truncateDiff(f.path, f.diff)
    excerpts.push(`--- ${f.path}\n${excerpt}`)
    budget -= excerpt.length
  }

  const parts: string[] = ['Generate a one-line git commit title for the staged changes.']
  if (input.recentCommits.length > 0) {
    parts.push(`Recent commits (match this style and language):\n${input.recentCommits.join('\n')}`)
  }
  parts.push(`Changed files:\n${manifest}`)
  if (excerpts.length > 0) {
    let section = `Diff excerpts:\n${excerpts.join('\n\n')}`
    if (omitted > 0) section += `\n… (${omitted} file${omitted > 1 ? 's' : ''} not excerpted)`
    parts.push(section)
  }
  parts.push(
    'Rules:\n- Conventional commit format (feat:/fix:/refactor:/docs:/chore:).\n- One line, max 72 characters.\n- Return ONLY the title, no explanation, no markdown.',
  )
  return parts.join('\n\n')
}
