import React from 'react'
import type { ToolFrame } from '../../model/frame-types.ts'
import { parseJsonObject, parseMCPToolName } from './tool-display.ts'
import { ToolBodyFrame, ToolCodeBlock, ToolSection, ToolSummaryRow, ToolRunning } from './ToolViewPrimitives.tsx'

interface McpToolViewProps {
  frame: ToolFrame
}

const MAX_OUTPUT_CHARS = 50000

function maybeTruncateOutput(output: string): string {
  if (output.length <= MAX_OUTPUT_CHARS) return output
  return output.slice(0, MAX_OUTPUT_CHARS) +
    `\n\n[Output truncated: ${output.length - MAX_OUTPUT_CHARS} additional characters hidden]`
}

/**
 * View for MCP tool calls ("mcp.<server>.<tool>"). Shows the parsed server /
 * tool identity, the raw input arguments as JSON, and the tool result text.
 */
export const McpToolView: React.FC<McpToolViewProps> = ({ frame }) => {
  const { tool } = parseMCPToolName(frame.toolName)
  const isRunning = frame.status === 'running'
  const parsedInput = parseJsonObject(frame.input)
  const output = frame.output ? maybeTruncateOutput(frame.output) : undefined

  return (
    <ToolBodyFrame frame={frame}>
      {frame.callableId && <ToolSummaryRow label="Method" value={frame.callableId} />}

      <ToolSection label="Arguments">
        <ToolCodeBlock>{parsedInput ? JSON.stringify(parsedInput, null, 2) : frame.input || '{}'}</ToolCodeBlock>
      </ToolSection>

      {isRunning && (
        <ToolRunning label={<>Running {tool ?? frame.toolName}…</>} />
      )}

      {output && !isRunning && (
        <ToolSection label={frame.status === 'error' ? 'Error' : 'Result'}>
          <ToolCodeBlock error={frame.status === 'error'}>{output}</ToolCodeBlock>
        </ToolSection>
      )}
    </ToolBodyFrame>
  )
}
