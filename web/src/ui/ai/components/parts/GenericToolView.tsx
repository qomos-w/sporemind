import React from 'react'
import type { ToolFrame } from '../../model/frame-types.ts'
import { ToolBodyFrame, ToolCodeBlock, ToolSection, ToolSummaryRow, ToolRunning, renderDiffSection } from './ToolViewPrimitives.tsx'

interface GenericToolViewProps {
  frame: ToolFrame
}

/**
 * Fallback view for unrecognized tool calls.
 * Shows raw input and output as-is.
 */
const MAX_OUTPUT_CHARS = 50000

function maybeTruncateOutput(output: string): string {
  if (output.length <= MAX_OUTPUT_CHARS) return output
  return output.slice(0, MAX_OUTPUT_CHARS) +
    `\n\n[Output truncated: ${output.length - MAX_OUTPUT_CHARS} additional characters hidden]`
}

export const GenericToolView: React.FC<GenericToolViewProps> = ({ frame }) => {
  const isRunning = frame.status === 'running'
  const output = frame.output ? maybeTruncateOutput(frame.output) : undefined

  return (
    <ToolBodyFrame frame={frame}>
      {(frame.callableId || frame.targetService) && (
        <ToolSection label="Callable">
          {frame.targetService && (
            <ToolSummaryRow label="Target" value={frame.targetService} />
          )}
          {frame.callableId && (
            <ToolSummaryRow label="Method" value={frame.callableId} />
          )}
        </ToolSection>
      )}

      <ToolSection label="Input">
        <ToolCodeBlock>{frame.input}</ToolCodeBlock>
      </ToolSection>

      {isRunning && (
        <ToolRunning label={<>Running {frame.toolName}…</>} />
      )}

      {output && !isRunning && (
        <ToolSection label={frame.status === 'error' ? 'Error' : 'Output'}>
          <ToolCodeBlock error={frame.status === 'error'}>{output}</ToolCodeBlock>
        </ToolSection>
      )}

      {renderDiffSection(frame)}
    </ToolBodyFrame>
  )
}
