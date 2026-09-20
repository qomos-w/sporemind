import React from 'react'
import type { Element, ElementContent, Root, Text } from 'hast'
import { langFromPath, isLangSupported, highlightCode } from './diff-highlight'

interface DiffBlockProps {
  diffContent: string
  /** Max diff lines to show before truncating. 0 = show all. Default: 30. */
  maxLines?: number
  /** Optional file path used to pick a syntax-highlight language by extension.
   * When omitted or with an unknown extension, lines render as plain text. */
  filePath?: string
}

interface DiffLine {
  text: string
  tone: 'added' | 'removed' | 'hunk' | 'plain'
  oldNum?: number
  newNum?: number
}

function parseDiffWithLineNumbers(diffContent: string): DiffLine[] {
  const lines = diffContent.split('\n')
  const result: DiffLine[] = []
  let oldNum = 0
  let newNum = 0

  for (const line of lines) {
    if (line.startsWith('@@')) {
      const match = line.match(/@@ -(\d+),?(\d*) \+(\d+),?(\d*) @@/)
      if (match) {
        oldNum = parseInt(match[1] ?? '0', 10)
        newNum = parseInt(match[3] ?? '0', 10)
      }
      result.push({ text: line, tone: 'hunk' })
    } else if (line.startsWith('-') && !line.startsWith('---')) {
      result.push({ text: line, tone: 'removed', oldNum })
      oldNum++
    } else if (line.startsWith('+') && !line.startsWith('+++')) {
      result.push({ text: line, tone: 'added', newNum })
      newNum++
    } else {
      result.push({ text: line, tone: 'plain', oldNum, newNum })
      oldNum++
      newNum++
    }
  }
  return result
}

function pad(n: number | undefined, width: number): string {
  if (n === undefined) return ' '.repeat(width)
  const s = String(n)
  return s.length >= width ? s : ' '.repeat(width - s.length) + s
}

const DEFAULT_MAX_LINES = 30

type HastLike = Root | Element | ElementContent

/** Render a lowlight hast tree as nested <span>s carrying hljs-* class names. */
function renderHast(node: HastLike, keyPrefix: string): React.ReactNode {
  if (node.type === 'text') {
    return (node as Text).value
  }
  if (node.type === 'element') {
    const el = node as Element
    const cls = el.properties?.className
    const className = Array.isArray(cls)
      ? cls.join(' ')
      : typeof cls === 'string'
        ? cls
        : undefined
    return (
      <span key={keyPrefix} className={className}>
        {el.children?.map((c, i) => renderHast(c as ElementContent, `${keyPrefix}.${i}`))}
      </span>
    )
  }
  if (node.type === 'root') {
    return (node as Root).children.map((c, i) =>
      renderHast(c as ElementContent, `${keyPrefix}.${i}`),
    )
  }
  return null
}

interface LineParts {
  sign: string
  code: string
  highlight: boolean
}

/** Split a diff line into the +/- sign and the code body, deciding whether the
 * body should be syntax-highlighted (file headers and hunk markers are plain). */
function splitLine(line: DiffLine): LineParts {
  const isFileHeader = line.text.startsWith('---') || line.text.startsWith('+++')
  if (line.tone === 'hunk' || isFileHeader) {
    return { sign: '', code: line.text, highlight: false }
  }
  const first = line.text[0] ?? ''
  const sign = first === '+' || first === '-' ? first : ''
  return { sign, code: sign ? line.text.slice(1) : line.text, highlight: true }
}

function tryHighlight(lang: string, code: string, keyPrefix: string): React.ReactNode {
  try {
    return renderHast(highlightCode(lang, code), keyPrefix)
  } catch {
    return null
  }
}

export const DiffBlock: React.FC<DiffBlockProps> = ({ diffContent, maxLines, filePath }) => {
  const limit = maxLines ?? DEFAULT_MAX_LINES
  const [expanded, setExpanded] = React.useState(false)
  const lang = langFromPath(filePath)
  const useSyntax = isLangSupported(lang)

  const { lineNodes, overLimit, hiddenCount } = React.useMemo(() => {
    const allLines = parseDiffWithLineNumbers(diffContent)
    const over = limit > 0 && allLines.length > limit
    const shown = expanded || !over ? allLines : allLines.slice(0, limit)
    const maxLineNum = shown.reduce(
      (m, l) => Math.max(m, l.oldNum ?? 0, l.newNum ?? 0),
      0,
    )
    const numWidth = String(maxLineNum).length

    const nodes = shown.map((line, index) => {
      const oldStr = pad(line.oldNum, numWidth)
      const newStr = pad(line.newNum, numWidth)
      const nums =
        line.tone === 'hunk' ? ' '.repeat(numWidth * 2 + 2) : `${oldStr} ${newStr} `
      const { sign, code, highlight } = splitLine(line)
      const tokenTree =
        useSyntax && highlight ? tryHighlight(lang!, code, `t${index}`) : null

      return (
        <div key={`${index}-${line.text}`} className={`ai-tool-diff-line ${line.tone}`}>
          <span className="ai-tool-diff-line-num">{nums}</span>
          <span className="ai-tool-diff-line-content">
            {sign && <span className="ai-tool-diff-sign">{sign}</span>}
            {tokenTree != null ? (
              <span className="ai-tool-diff-code">{tokenTree}</span>
            ) : (
              code
            )}
          </span>
        </div>
      )
    })

    return {
      lineNodes: nodes,
      overLimit: over,
      hiddenCount: over ? allLines.length - limit : 0,
    }
  }, [diffContent, limit, expanded, lang, useSyntax])

  return (
    <div className="ai-tool-diff-wrapper">
      <pre className="ai-tool-diff" aria-label="Unified diff output">
        {lineNodes}
      </pre>
      {overLimit && (
        <button
          type="button"
          className="ai-tool-code-expander"
          onClick={() => setExpanded(!expanded)}
        >
          {expanded
            ? 'collapse'
            : `... +${hiddenCount} more lines (click to expand)`}
        </button>
      )}
    </div>
  )
}
