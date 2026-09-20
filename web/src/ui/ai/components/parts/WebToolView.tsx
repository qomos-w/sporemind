import React from 'react'
import type { ToolFrame } from '../../model/frame-types.ts'
import { parseJsonObject, firstString, normalizePath } from './tool-display.ts'
import { ToolBodyFrame, ToolCodeBlock, ToolSection, ToolSummaryRow, ToolRunning } from './ToolViewPrimitives.tsx'

interface WebToolViewProps {
  frame: ToolFrame
}

interface FetchOutput {
  url?: string
  code?: number
  codeText?: string
  bytes?: number
  result?: string
  durationMs?: number
}

interface SearchResult {
  title?: string
  url?: string
  snippet?: string
}

interface SearchOutput {
  query?: string
  results?: SearchResult[]
  durationMs?: number
}

function parseFetchOutput(text: string): FetchOutput | undefined {
  const r = parseJsonObject(text)
  if (!r) return undefined
  return {
    url: typeof r.url === 'string' ? r.url : undefined,
    code: typeof r.code === 'number' ? r.code : undefined,
    codeText: typeof r.codeText === 'string' ? r.codeText : undefined,
    bytes: typeof r.bytes === 'number' ? r.bytes : undefined,
    result: typeof r.result === 'string' ? r.result : undefined,
    durationMs: typeof r.durationMs === 'number' ? r.durationMs : undefined,
  }
}

function parseSearchOutput(text: string): SearchOutput | undefined {
  const r = parseJsonObject(text)
  if (!r) return undefined
  const results: SearchResult[] = []
  if (Array.isArray(r.results)) {
    for (const item of r.results) {
      if (item && typeof item === 'object') {
        const ri = item as Record<string, unknown>
        results.push({
          title: typeof ri.title === 'string' ? ri.title : undefined,
          url: typeof ri.url === 'string' ? ri.url : undefined,
          snippet: typeof ri.snippet === 'string' ? ri.snippet : undefined,
        })
      }
    }
  }
  return {
    query: typeof r.query === 'string' ? r.query : undefined,
    results: results.length > 0 ? results : undefined,
    durationMs: typeof r.durationMs === 'number' ? r.durationMs : undefined,
  }
}

const FetchView: React.FC<{ frame: ToolFrame; out: FetchOutput }> = ({ frame, out }) => {
  const parsed = parseJsonObject(frame.input)
  const url = normalizePath(out.url ?? firstString(parsed ?? {}, ['url']))
  const prompt = parsed ? firstString(parsed, ['prompt']) : undefined
  const code = out.code
  const isRunning = frame.status === 'running'

  return (
    <ToolBodyFrame frame={frame}>
      {url && <ToolSummaryRow label="URL" value={url} />}
      {code != null && (
        <ToolSummaryRow
          label="Status"
          value={`${code} ${out.codeText ?? ''}`.trim()}
        />
      )}
      {prompt && <ToolSummaryRow label="Prompt" value={prompt} />}
      {out.durationMs != null && (
        <ToolSummaryRow label="Time" value={`${out.durationMs}ms`} />
      )}
      {out.result != null ? (
        <ToolSection label="Content">
          <ToolCodeBlock>{out.result}</ToolCodeBlock>
        </ToolSection>
      ) : (
        <ToolSection label="Input">
          <ToolCodeBlock>{frame.input}</ToolCodeBlock>
        </ToolSection>
      )}
      {isRunning && <ToolRunning label="Fetching…" />}
    </ToolBodyFrame>
  )
}

const SearchView: React.FC<{ frame: ToolFrame; out: SearchOutput }> = ({ frame, out }) => {
  const parsed = parseJsonObject(frame.input)
  const query = out.query ?? firstString(parsed ?? {}, ['query'])
  const isRunning = frame.status === 'running'

  return (
    <ToolBodyFrame frame={frame}>
      {query && <ToolSummaryRow label="Query" value={query} />}
      {out.results != null && (
        <ToolSummaryRow label="Results" value={`${out.results.length} found`} />
      )}
      {out.durationMs != null && (
        <ToolSummaryRow label="Time" value={`${out.durationMs}ms`} />
      )}

      {out.results && out.results.length > 0 ? (
        <ToolSection label="Results">
          <div className="ai-web-search-results">
            {out.results.map((r, i) => (
              <div key={i} className="ai-web-search-result">
                <div className="ai-web-search-title">
                  {r.url ? (
                    <a href={r.url} target="_blank" rel="noopener noreferrer">{r.title ?? r.url}</a>
                  ) : (
                    r.title ?? 'Untitled'
                  )}
                </div>
                {r.snippet && (
                  <div className="ai-web-search-snippet">
                    {r.snippet.length > 280 ? r.snippet.slice(0, 280) + '…' : r.snippet}
                  </div>
                )}
              </div>
            ))}
          </div>
        </ToolSection>
      ) : (
        <ToolSection label="Input">
          <ToolCodeBlock>{frame.input}</ToolCodeBlock>
        </ToolSection>
      )}

      {isRunning && <ToolRunning label="Searching…" />}
    </ToolBodyFrame>
  )
}

export const WebToolView: React.FC<WebToolViewProps> = ({ frame }) => {
  const toolName = frame.toolName.toLowerCase()
  const isSearch = toolName.includes('search')

  if (isSearch) {
    const out = frame.output ? parseSearchOutput(frame.output) : undefined
    return <SearchView frame={frame} out={out ?? {}} />
  }

  const out = frame.output ? parseFetchOutput(frame.output) : undefined
  return <FetchView frame={frame} out={out ?? {}} />
}
