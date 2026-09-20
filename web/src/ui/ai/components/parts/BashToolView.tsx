import React from 'react'
import { Terminal } from 'lucide-react'
import type { ToolFrame } from '../../model/frame-types.ts'
import type { ShellBashReq, ShellBashResp, ShellExecReq } from '../../../../gen-types/shell'
import { parseJsonObject, unescapeOutput } from './tool-display.ts'
import { ToolBodyFrame, ToolCodeBlock, ToolSection, ToolSummaryRow, renderDiffSection } from './ToolViewPrimitives.tsx'
import { AnsiText } from './AnsiRenderer.tsx'

interface BashToolViewProps {
  frame: ToolFrame
}

function parseBashOutput(output: string): ShellBashResp | null {
  const parsed = parseJsonObject(output) as ShellBashResp | null
  if (!parsed) return null
  if (typeof parsed.Stdout !== 'string' && typeof parsed.Stderr !== 'string') return null
  return parsed
}

const MAX_OUTPUT_LINES = 5

interface AnsiTerminalOutputProps {
  text: string
  isRunning: boolean
  tone?: 'stdout' | 'stderr'
}

function AnsiTerminalOutputInner({ text, isRunning, tone = 'stdout' }: AnsiTerminalOutputProps) {
  const [expanded, setExpanded] = React.useState(false)
  const [stickToBottom, setStickToBottom] = React.useState(true)
  const outputRef = React.useRef<HTMLPreElement>(null)

  const handleScroll = React.useCallback(() => {
    const el = outputRef.current
    if (!el) return
    const distanceFromBottom = el.scrollHeight - el.scrollTop - el.clientHeight
    setStickToBottom(distanceFromBottom < 24)
  }, [])

  React.useEffect(() => {
    if (!isRunning || !stickToBottom) return
    const el = outputRef.current
    if (!el) return
    // stickToBottom (kept in sync by scroll events) is the sole gate: follow
    // the tail whenever the user hasn't scrolled away. Coalesce per-chunk
    // scrolls into one rAF frame to avoid a forced synchronous layout on
    // every streamed chunk.
    const raf = requestAnimationFrame(() => {
      const target = outputRef.current
      if (target) target.scrollTop = target.scrollHeight
    })
    return () => cancelAnimationFrame(raf)
  }, [text, isRunning, stickToBottom])

  React.useEffect(() => {
    if (!isRunning) setStickToBottom(true)
  }, [isRunning])

  // Treat a trailing newline as a line terminator so the tail slice never
  // counts a phantom empty line.
  const lines = text.endsWith('\n') ? text.slice(0, -1).split('\n') : text.split('\n')
  const total = lines.length
  const overLimit = total > MAX_OUTPUT_LINES
  // While the command is running, keep the complete live buffer in the
  // scrollable terminal so the user can follow the latest output. Once
  // finished, keep the tail (most recent lines) visible and fold the earlier
  // output away above.
  const displayText = isRunning || expanded || !overLimit
    ? text
    : lines.slice(-MAX_OUTPUT_LINES).join('\n')

  return (
    <div className={`ai-tool-code-wrapper ai-tool-output-${tone}`}>
      {!isRunning && overLimit && !expanded && (
        <button type="button" className="ai-tool-code-expander" onClick={() => setExpanded(true)}>
          … +{total - MAX_OUTPUT_LINES} earlier lines (click to expand)
        </button>
      )}
      <pre
        ref={outputRef}
        onScroll={handleScroll}
        className={`ai-tool-code terminal ${isRunning ? 'running-scroll' : ''}`}
      >
        <AnsiText text={displayText} />
        {isRunning && <span className="ai-terminal-cursor" />}
      </pre>
      {isRunning && !stickToBottom && (
        <button type="button" className="ai-tool-code-expander" onClick={() => {
          const el = outputRef.current
          if (el) el.scrollTop = el.scrollHeight
          setStickToBottom(true)
        }}>
          Jump to latest output
        </button>
      )}
      {!isRunning && expanded && (
        <button type="button" className="ai-tool-code-expander" onClick={() => setExpanded(false)}>
          collapse
        </button>
      )}
    </div>
  )
}

const AnsiTerminalOutput = React.memo(AnsiTerminalOutputInner)

export const BashToolView: React.FC<BashToolViewProps> = React.memo(({ frame }) => {
  const parsed = parseJsonObject(frame.input) as (ShellBashReq | ShellExecReq) | null
  const command = parsed?.Command
  const cwd = parsed?.Dir
  const commandArgs = parsed && 'Args' in parsed && Array.isArray(parsed.Args) ? parsed.Args : []
  const displayCommand = command
    ? [command, ...commandArgs].map(part => /[\s"']/u.test(part) ? JSON.stringify(part) : part).join(' ')
    : ''
  const isRunning = frame.status === 'running'

  const [commandExpanded, setCommandExpanded] = React.useState(false)
  const commandLines = displayCommand.split('\n')
  const commandIsLong = displayCommand.length > 180 || commandLines.length > 3
  const visibleCommand = commandExpanded || !commandIsLong
    ? displayCommand
    : commandLines.slice(0, 3).join('\n')

  const bashResult = frame.output ? parseBashOutput(frame.output) : null

  // Prefer runningOutput (live-streamed, untruncated) over frame.output
  // (which carries the truncated exit chunk). On history replay runningOutput
  // is absent and frame.output is the only source — that path is fine because
  // history never emitted deltas.
  const running = frame.runningOutput
  const useRunning = running && (running.stdout !== '' || running.stderr !== '')

  const stdout = useRunning ? running!.stdout : (bashResult ? unescapeOutput(bashResult.Stdout) : '')
  const stderr = useRunning ? running!.stderr : (bashResult ? unescapeOutput(bashResult.Stderr) : '')
  const hasStdout = stdout !== ''
  const hasStderr = stderr !== ''
  const hasOutput = hasStdout || hasStderr

  const stdoutLines = hasStdout ? stdout.split('\n').length : 0
  const stderrLines = hasStderr ? stderr.split('\n').length : 0

  const exitCode = bashResult?.ExitCode
  const truncated = bashResult?.Truncated
  const commandFailed = exitCode != null && exitCode !== 0
  const runLabel = isRunning ? 'Running' : commandFailed ? 'Failed' : 'Completed'

  return (
    <ToolBodyFrame frame={frame}>
      <div className="ai-console">
        {commandFailed && (
          <div className="ai-tool-command-status failed">
            Command {runLabel.toLowerCase()} with exit code {exitCode}
          </div>
        )}
        {displayCommand && (
          <ToolSection label="Command">
            <div className="ai-tool-command-block">
              <ToolCodeBlock terminal>{visibleCommand}</ToolCodeBlock>
              {commandIsLong && (
                <button
                  type="button"
                  className="ai-tool-code-expander"
                  onClick={() => setCommandExpanded(value => !value)}
                >
                  {commandExpanded ? 'Collapse command' : 'Show full command'}
                </button>
              )}
            </div>
          </ToolSection>
        )}

        {cwd && <ToolSummaryRow label="CWD" value={cwd} />}

        {hasStdout && (
          <ToolSection label={isRunning ? 'Stdout' : `Stdout (${stdoutLines} lines)`}>
            <AnsiTerminalOutput text={stdout} isRunning={isRunning} />
          </ToolSection>
        )}

        {hasStderr && (
          <ToolSection
            label={
              <>
                <Terminal size={14} className="ai-tool-stderr-icon" />
                {isRunning ? 'Stderr' : `Stderr (${stderrLines} lines)`}
              </>
            }
          >
            <AnsiTerminalOutput text={stderr} isRunning={isRunning} tone="stderr" />
          </ToolSection>
        )}

        {!hasOutput && isRunning && (
          <ToolSection label="Output">
            <pre className="ai-tool-code terminal">
              <span className="ai-terminal-cursor" />
            </pre>
          </ToolSection>
        )}

        {exitCode != null && exitCode !== 0 && (
          <ToolSummaryRow label="Exit" value={`${exitCode}`} />
        )}

        {truncated && (
          <div className="ai-tool-running">
            Output was truncated by the server.
          </div>
        )}

        {renderDiffSection(frame)}
      </div>
    </ToolBodyFrame>
  )
})
