import React from 'react'
import { Plug, Terminal, CheckCircle2, XCircle, Clock } from 'lucide-react'
import type { ToolFrame } from '../../model/frame-types.ts'
import type { SshShellOpenReq, SshShellOpenResp } from '../../../../gen-types/sshmanager'
import type { SshShellRunReq, SshShellRunResp } from '../../../../gen-types/sshmanager'
import { parseJsonObject, unescapeOutput } from './tool-display.ts'
import { ToolBodyFrame, ToolRunning, ToolSummaryRow, ToolSection, ToolCodeBlock } from './ToolViewPrimitives.tsx'
import { AnsiText } from './AnsiRenderer.tsx'

// ── shell_open (ssh_session) ──

export const SshSessionToolView: React.FC<{ frame: ToolFrame }> = ({ frame }) => {
  const input = parseJsonObject(frame.input) as SshShellOpenReq | null
  const output = frame.output ? (parseJsonObject(frame.output) as SshShellOpenResp | null) : null

  const hostId = input?.HostId ?? ''
  const sessionId = output?.SessionId ?? ''
  const connected = output?.Connected ?? false
  const error = output?.Error ?? ''
  const isRunning = frame.status === 'running'

  return (
    <ToolBodyFrame frame={frame}>
      <div className="ai-ssh-session-card">
        <div className={`ai-ssh-session-status ${isRunning ? 'running' : connected ? 'connected' : 'failed'}`}>
          {isRunning ? (
            <>
              <Clock size={14} className="ai-ssh-session-status-icon" />
              <span>Connecting to {hostId}…</span>
            </>
          ) : connected ? (
            <>
              <CheckCircle2 size={14} className="ai-ssh-session-status-icon" />
              <span>Connected</span>
            </>
          ) : (
            <>
              <XCircle size={14} className="ai-ssh-session-status-icon" />
              <span>Failed</span>
            </>
          )}
        </div>
        <div className="ai-ssh-session-details">
          <div className="ai-ssh-session-row">
            <Plug size={12} />
            <span className="ai-ssh-session-label">Host</span>
            <span className="ai-ssh-session-value">{hostId}</span>
          </div>
          {sessionId && (
            <div className="ai-ssh-session-row">
              <Terminal size={12} />
              <span className="ai-ssh-session-label">Session</span>
              <span className="ai-ssh-session-value ai-ssh-session-id">{sessionId}</span>
            </div>
          )}
        </div>
        {error && (
          <div className="ai-ssh-session-error">{error}</div>
        )}
        {isRunning && !output && <ToolRunning />}
      </div>
    </ToolBodyFrame>
  )
}

// ── shell_run (ssh_run) ──

const MAX_OUTPUT_LINES = 8

export const SshRunToolView: React.FC<{ frame: ToolFrame }> = ({ frame }) => {
  const input = parseJsonObject(frame.input) as SshShellRunReq | null
  const output = frame.output ? (parseJsonObject(frame.output) as SshShellRunResp | null) : null
  const isRunning = frame.status === 'running'

  const command = input?.Command ?? ''
  const sessionId = input?.SessionId ?? ''
  const outText = output ? unescapeOutput(output.Output) : ''
  const exitCode = output?.ExitCode
  const timedOut = output?.TimedOut ?? false
  const truncated = output?.Truncated ?? false

  const [expanded, setExpanded] = React.useState(false)
  const lines = outText.split('\n')
  const overLimit = lines.length > MAX_OUTPUT_LINES
  const displayText = expanded || !overLimit
    ? outText
    : lines.slice(0, MAX_OUTPUT_LINES).join('\n')

  const commandFailed = exitCode != null && exitCode !== 0

  return (
    <ToolBodyFrame frame={frame}>
      {command && (
        <ToolSection label="Command">
          <ToolCodeBlock terminal>{command}</ToolCodeBlock>
        </ToolSection>
      )}

      {sessionId && <ToolSummaryRow label="Session" value={sessionId} />}

      {outText && (
        <ToolSection label={isRunning ? 'Output' : `Output (${lines.length} lines)`}>
          <div className="ai-tool-code-wrapper ai-tool-output-stdout">
            <pre className={`ai-tool-code terminal ${isRunning ? 'running-scroll' : ''}`}>
              <AnsiText text={displayText} />
              {isRunning && <span className="ai-terminal-cursor" />}
            </pre>
            {!isRunning && overLimit && !expanded && (
              <button type="button" className="ai-tool-code-expander" onClick={() => setExpanded(true)}>
                … +{lines.length - MAX_OUTPUT_LINES} more lines (click to expand)
              </button>
            )}
            {!isRunning && expanded && (
              <button type="button" className="ai-tool-code-expander" onClick={() => setExpanded(false)}>
                collapse
              </button>
            )}
          </div>
        </ToolSection>
      )}

      {!outText && isRunning && (
        <ToolSection label="Output">
          <ToolRunning />
        </ToolSection>
      )}

      {exitCode != null && (
        <ToolSummaryRow label="Exit" value={`${exitCode}`} />
      )}

      {timedOut && (
        <div className="ai-tool-running">
          Command timed out waiting for sentinel.
        </div>
      )}

      {truncated && (
        <div className="ai-tool-running">
          Output was truncated (head + tail).
        </div>
      )}

      {commandFailed && (
        <div className="ai-tool-command-status failed">
          Command failed with exit code {exitCode}
        </div>
      )}
    </ToolBodyFrame>
  )
}