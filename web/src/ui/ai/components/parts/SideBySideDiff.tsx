import React from 'react'

interface SideBySideLine {
  leftText: string
  leftTone: 'removed' | 'plain' | 'hunk' | 'empty'
  leftNum?: number
  rightText: string
  rightTone: 'added' | 'plain' | 'hunk' | 'empty'
  rightNum?: number
}

function parseDiffWithLineNumbers(diffContent: string): Array<{ text: string; tone: 'added' | 'removed' | 'hunk' | 'plain'; oldNum?: number; newNum?: number }> {
  const lines = diffContent.split('\n')
  const result: Array<{ text: string; tone: 'added' | 'removed' | 'hunk' | 'plain'; oldNum?: number; newNum?: number }> = []
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

function buildSideBySideLines(parsed: ReturnType<typeof parseDiffWithLineNumbers>): SideBySideLine[] {
  const rows: SideBySideLine[] = []

  for (const line of parsed) {
    switch (line.tone) {
      case 'hunk':
        rows.push({
          leftText: line.text,
          leftTone: 'hunk',
          rightText: line.text,
          rightTone: 'hunk',
        })
        break
      case 'removed':
        rows.push({
          leftText: line.text,
          leftTone: 'removed',
          leftNum: line.oldNum,
          rightText: '',
          rightTone: 'empty',
        })
        break
      case 'added':
        rows.push({
          leftText: '',
          leftTone: 'empty',
          rightText: line.text,
          rightTone: 'added',
          rightNum: line.newNum,
        })
        break
      case 'plain':
        rows.push({
          leftText: line.text,
          leftTone: 'plain',
          leftNum: line.oldNum,
          rightText: line.text,
          rightTone: 'plain',
          rightNum: line.newNum,
        })
        break
    }
  }

  return rows
}

function pad(n: number | undefined, width: number): string {
  if (n === undefined) return ' '.repeat(width)
  const s = String(n)
  return s.length >= width ? s : ' '.repeat(width - s.length) + s
}

interface SideBySideDiffProps {
  diffContent: string
}

export const SideBySideDiff: React.FC<SideBySideDiffProps> = ({ diffContent }) => {
  const parsed = parseDiffWithLineNumbers(diffContent)
  const rows = buildSideBySideLines(parsed)

  const maxLeftNum = Math.max(...rows.map(r => r.leftNum ?? 0))
  const maxRightNum = Math.max(...rows.map(r => r.rightNum ?? 0))
  const leftNumWidth = String(maxLeftNum).length
  const rightNumWidth = String(maxRightNum).length

  return (
    <div className="ai-sbs-diff">
      <div className="ai-sbs-diff-header">
        <span className="ai-sbs-diff-header-left">Old</span>
        <span className="ai-sbs-diff-header-right">New</span>
      </div>
      <div className="ai-sbs-diff-body">
        {rows.map((row, i) => {
          if (row.leftTone === 'hunk') {
            return (
              <div key={`hunk-${i}`} className="ai-sbs-diff-hunk">
                {row.leftText}
              </div>
            )
          }

          const leftNumStr = pad(row.leftNum, leftNumWidth)
          const rightNumStr = pad(row.rightNum, rightNumWidth)

          return (
            <div key={`row-${i}`} className="ai-sbs-diff-row">
              <div className={`ai-sbs-diff-cell ${row.leftTone}`}>
                <span className="ai-sbs-diff-linenum">{leftNumStr}</span>
                <span className="ai-sbs-diff-text">{row.leftText || ' '}</span>
              </div>
              <div className={`ai-sbs-diff-cell ${row.rightTone}`}>
                <span className="ai-sbs-diff-linenum">{rightNumStr}</span>
                <span className="ai-sbs-diff-text">{row.rightText || ' '}</span>
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}
