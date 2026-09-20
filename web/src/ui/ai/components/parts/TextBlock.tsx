import React, { useMemo, useRef, useState, useContext } from 'react'
import { BookmarkPlus, Maximize2, Volume2, Square } from 'lucide-react'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import type { PluggableList } from 'unified'
import type { Frame, TextFrame } from '../../model/frame-types.ts'
import { TimelineStep } from '../timeline'
import { looksLikeCompleteTable, splitMarkdownChunks } from './markdown-chunks.ts'
import { useStreamingMarkdownChunks, isOpenDiagramSource } from './streaming-markdown.ts'
import { makeRehypePlugins, useFileReference, resolveFileReference } from './file-reference.tsx'
import { processWikiWords, wikiUrlTransform } from './wikiword.ts'
import { useSmoothText } from './useSmoothStreamContent.ts'
import { useSmoothStreamContext } from '../../context/SmoothStreamContext'
import { AIShellContext } from '../../context/AIShellContext'
import { MermaidRender } from './mermaid-render.tsx'
import { useI18n } from '../../../../i18n/provider'
import { speak, stop as stopTTS, isSpeaking } from '../../voice-synthesis'
import { isBuiltinCard, getBuiltinTagDisplayName } from '../../../../domain/builtin-cards'
import { highlightedCode } from './diff-highlight'

const MAX_TEXT_CHARS = 4000
// When the truncation slice would land inside a ``` code fence, snap it to a
// fence boundary so the preview never renders a half-open fence (which turns
// the trailing text into a code block, or cuts a mermaid diagram in half).
// - If every fence opened up to `max` is also closed, cut at `max`.
// - If the last fence is still open but closes shortly after `max`, include
//   the whole fence so no fence is split.
// - Otherwise cut just before the unclosed opener, dropping that fence.
const FENCE_GRACE = 800
export function smartSlice(content: string, max: number): string {
  if (content.length <= max) return content
  const fences: number[] = []
  let i = 0
  while (i < max) {
    const j = content.indexOf('```', i)
    if (j === -1 || j >= max) break
    fences.push(j)
    i = j + 3
  }
  if (fences.length === 0 || fences.length % 2 === 0) return content.slice(0, max)
  const lastOpener = fences[fences.length - 1] ?? 0
  const closer = content.indexOf('```', lastOpener + 3)
  if (closer !== -1 && closer + 3 <= max + FENCE_GRACE) return content.slice(0, closer + 3)
  return content.slice(0, lastOpener).replace(/[\s,]+$/, '')
}

function markdownComponents(language: string | undefined, code: string): React.ReactNode {
  if (language === 'mermaid') {
    return <MermaidRender code={code} />
  }
  if (language) {
    const highlighted = highlightedCode(language, code)
    if (highlighted) return <code className={`language-${language} ai-highlighted-code`}>{highlighted}</code>
  }
  return undefined
}

/** Create the wiki link handler closure for a given onOpenCard callback and i18n function. */
function makeWikiLinkHandler(onOpenCard?: (cardId: string) => void, t?: (key: any) => string) {
  return ({ href, children, className, ...props }: React.ComponentProps<'a'>) => {
    if (href?.startsWith('wiki:')) {
      const word = decodeURIComponent(href.slice(5))
      // Resolve builtin card IDs to localized display names
      const displayText = (t && isBuiltinCard(word))
        ? getBuiltinTagDisplayName(word, t)
        : undefined
      return (
        <a
          className="wiki-word-link"
          href="#"
          onClick={(e) => {
            e.preventDefault()
            onOpenCard?.(word)
          }}
        >
          {displayText ?? children}
        </a>
      )
    }
    // File references (rehype-generated anchors) must not open new tabs;
    // they are handled by handleClick via event delegation.
    if (typeof className === 'string' && className.includes('ai-file-ref')) {
      return <a {...props} className={className}>{children}</a>
    }
    return <a href={href} target="_blank" rel="noreferrer">{children}</a>
  }
}

/** Stable code renderer for react-markdown `components.code`. Defined at
 * module scope: an inline closure would get a new identity on every parent
 * render, and react-markdown elements typed by it would be remounted —
 * taking MermaidRender (and its immediate first render) down with them on
 * every conversation-level re-render. */
function MarkdownCode({ className, children, ...props }: React.ComponentProps<'code'> & { children?: React.ReactNode }) {
  const lang = className?.replace('language-', '')
  const codeText = String(children ?? '')
  const rendered = markdownComponents(lang, codeText)
  if (rendered) return rendered
  return <code className={className} {...props}>{children}</code>
}

interface MemoizedMarkdownProps {
  children: string
  rehypePlugins: PluggableList
  components?: Record<string, React.ComponentType>
}

/** Memoized Markdown renderer so finalized chunks never re-parse. */
const MemoizedMarkdown = React.memo(function MemoizedMarkdown({
  children,
  rehypePlugins,
  components,
}: MemoizedMarkdownProps) {
  return (
    <Markdown
      remarkPlugins={[remarkGfm]}
      rehypePlugins={rehypePlugins}
      urlTransform={wikiUrlTransform}
      components={{
        code: MarkdownCode,
        ...(components ?? {}),
      }}
    >
      {children}
    </Markdown>
  )
}, (prev, next) => prev.children === next.children && prev.rehypePlugins === next.rehypePlugins && prev.components === next.components)

interface TextBlockProps {
  frame: TextFrame
  onFrameSelect?: (frame: Frame) => void
  onSaveToCard?: (content: string) => void
}

export const TextBlock: React.FC<TextBlockProps> = ({ frame, onFrameSelect, onSaveToCard }) => {
  const isStreaming = frame.status === 'running'
  const [expanded, setExpanded] = useState(false)
  const [speaking, setSpeaking] = useState(false)
  const fileRefCtx = useFileReference()
  const aiShellCtx = useContext(AIShellContext)
  const { t } = useI18n()
  const { smoothStream } = useSmoothStreamContext()

  const content = frame.content
  const shouldTruncate = content.length > MAX_TEXT_CHARS && !isStreaming && !expanded
  const displayContent = shouldTruncate
    ? smartSlice(content, MAX_TEXT_CHARS)
    : content

  // Smooth reveal: buffer the streaming text and reveal at a rate that tracks
  // arrival, so bursts don't dump whole paragraphs and slow streams don't
  // stutter. When not streaming, returns the full content unchanged.
  const { displayed: smoothedContent, guardRef: smoothGuardRef } = useSmoothText(displayContent, isStreaming && smoothStream)

  // Content is "growing" while the frame runs OR while the smooth reveal is
  // still draining its backlog after the stream ended. Growing content must
  // take the chunked path (unclosed fences stay plain text); the full
  // <Markdown> pass is only safe for settled content — re-parsing a partial
  // mermaid fence per tick re-renders the diagram/error UI nonstop.
  const revealing = smoothedContent.length < displayContent.length
  const growing = isStreaming || revealing

  // Apply wikiword processing (converts [[...]], CamelCase, and `WikiWord` to wiki: links).
  // Streaming path: incremental chunking inside the hook — wikiword + split run
  // only on the active tail each reveal tick, not the full string.
  const chunks = useStreamingMarkdownChunks(smoothedContent, growing)

  // Freeze: a frame that has ever rendered chunked STAYS chunked after growth
  // ends. Switching to the single full <Markdown> pass at settle would remount
  // every finalized chunk — MermaidRender would unmount/remount, flashing the
  // diagram through its empty state and yanking the scroll position once more.
  const hasGrownRef = useRef(false)
  if (growing) hasGrownRef.current = true
  const settledChunks = useMemo(() => {
    if (growing || !hasGrownRef.current) return null
    return splitMarkdownChunks(processWikiWords(displayContent))
      .filter(c => c.content !== '')
      // The splitter leaves the trailing paragraph unfinalized (no closing
      // blank line); once settled it must render as Markdown, not a tail.
      .map((c, i, arr) => (i === arr.length - 1 ? { ...c, finalized: true } : c))
  }, [growing, displayContent])

  const processedContent = useMemo(
    () => (chunks === null && settledChunks === null ? processWikiWords(smoothedContent) : ''),
    [chunks, settledChunks, smoothedContent],
  )

  const rehypePlugins = useMemo(() => makeRehypePlugins({
    projectId: fileRefCtx?.projectId ?? null,
    projectRoot: fileRefCtx?.projectRoot ?? null,
  }), [fileRefCtx?.projectId, fileRefCtx?.projectRoot])

  // Wiki link handler — stable as long as onOpenCard is stable
  const wikiComponents = useMemo(() => ({
    a: makeWikiLinkHandler(aiShellCtx?.onOpenCard, t),
  }), [aiShellCtx?.onOpenCard, t])

  // Streaming: split into finalized chunks (Markdown) + trailing streaming tail (plain text).
  // Completed: single Markdown pass, no splitting overhead — except for frames
  // that grew in place (freeze), which keep the chunked tree for element stability.
  const renderContent = () => {
    const activeChunks = growing ? chunks : settledChunks
    if (!activeChunks) {
      // Settled content (history, never-grew frames): the memoized pass
      // blocks re-parsing — and MermaidRender remounts — on unrelated
      // conversation re-renders.
      return (
        <MemoizedMarkdown rehypePlugins={rehypePlugins} components={wikiComponents}>
          {processedContent}
        </MemoizedMarkdown>
      )
    }
    return (
      <>
        {activeChunks.map((chunk, i) => {
          // A complete table at the streaming tail should be rendered as Markdown
          // immediately, so that subsequent content (e.g. a blockquote) does not
          // keep it stuck in plain-text mode.
          if (chunk.finalized || looksLikeCompleteTable(chunk.content)) {
            return <MemoizedMarkdown key={i} rehypePlugins={rehypePlugins} components={wikiComponents}>{chunk.content}</MemoizedMarkdown>
          }
          // An unclosed diagram fence (mermaid) streams as a fixed-height
          // placeholder: revealing hundreds of source lines would grow the
          // stream on every tick and drag the whole stream down via
          // scroll-follow. The diagram itself mounts once the closing fence
          // arrives.
          if (isOpenDiagramSource(chunk.content)) {
            // Strip the splitter's synthesized trailing newline before counting.
            const lines = chunk.content.replace(/\n+$/, '').split('\n').length - 1
            return (
              <div key={`mstream-${i}`} className="ai-diagram-streaming">
                {t('ai.diagram.streaming')}
                <span className="ai-diagram-streaming-count">{lines}</span>
              </div>
            )
          }
          // The tail renders as one pre-wrap text node (.streaming-tail has
          // white-space: pre-wrap): newlines come free from the string, so a
          // long unfinalized block doesn't rebuild thousands of <br/> nodes
          // every reveal tick. Strip the splitter's synthesized trailing
          // newline so the old line-split rendering (no break after the last
          // line) is preserved exactly.
          return (
            <span key={`streaming-${i}`} className="streaming-tail">
              {chunk.content.replace(/\n+$/, '')}
            </span>
          )
        })}
      </>
    )
  }

  const handleClick = async (e: React.MouseEvent) => {
    const target = e.target as HTMLElement

    // Wiki word span click → open card (legacy: auto-detected spans)
    // Most wikiwords now render as markdown links handled by the `a` component.
    const wikiAuto = target.closest('.wiki-word-auto') as HTMLElement | null
    if (wikiAuto?.dataset.wiki) {
      e.preventDefault()
      aiShellCtx?.onOpenCard?.(wikiAuto.dataset.wiki)
      return
    }

    const fileRef = target.closest('.ai-file-ref') as HTMLElement | null
    if (!fileRef) return
    const filePath = fileRef.dataset.aiFilePath
    if (!filePath) return

    e.preventDefault()
    e.stopPropagation()

    const lineStr = fileRef.dataset.aiFileLine
    const line = lineStr ? parseInt(lineStr, 10) : undefined
    const lineEndStr = fileRef.dataset.aiFileLineEnd
    const lineEnd = lineEndStr ? parseInt(lineEndStr, 10) : undefined

    if (aiShellCtx?.onOpenFile) {
      const ref = {
        raw: filePath,
        path: filePath,
        ext: filePath.split('.').pop()?.toLowerCase() ?? '',
        line,
        lineEnd,
      }
      const resolved = await resolveFileReference(ref, fileRefCtx?.projectRoot ?? null)
      if (!resolved) return
      aiShellCtx.onOpenFile(resolved.filePath, undefined, resolved.line ?? line, resolved.lineEnd ?? lineEnd)
    }
  }

  const toggleSpeak = async () => {
    if (speaking || isSpeaking()) {
      stopTTS()
      setSpeaking(false)
      return
    }
    setSpeaking(true)
    try {
      await speak(content)
    } catch {
      setSpeaking(false)
    }
  }

  return (
    <TimelineStep
      slotId={frame.id}
      status={frame.status}
      icon={null}
      label={null}
      bare
      alwaysVisible={
        <div ref={smoothGuardRef} className={`ai-text-content markdown-content ${isStreaming ? 'streaming' : ''}`} onClick={handleClick}>
          {onSaveToCard && !isStreaming && (
            <button
              type="button"
              className="ai-frame-maximize-btn ai-frame-maximize-float ai-text-save-card-btn"
              title="Save to card"
              aria-label="Save to card"
              onClick={(e) => {
                e.stopPropagation()
                onSaveToCard(content)
              }}
            >
              <BookmarkPlus size={12} />
            </button>
          )}
          {!isStreaming && (
            <button
              type="button"
              className={`ai-frame-maximize-btn ai-frame-maximize-float ai-text-speak-btn${speaking ? ' is-speaking' : ''}`}
              title={speaking ? t('common.stop') : t('settings.voice.readAloud')}
              aria-label={speaking ? t('common.stop') : t('settings.voice.readAloud')}
              onClick={(e) => {
                e.stopPropagation()
                void toggleSpeak()
              }}
            >
              {speaking ? <Square size={11} /> : <Volume2 size={12} />}
            </button>
          )}
          {onFrameSelect && (
            <button
              type="button"
              className="ai-frame-maximize-btn ai-frame-maximize-float"
              title="View full details"
              onClick={() => onFrameSelect(frame)}
            >
              <Maximize2 size={12} />
            </button>
          )}
          {renderContent()}
          {shouldTruncate && (
            <button
              type="button"
              className="ai-text-expand-btn"
              onClick={() => setExpanded(true)}
            >
              Show more ({Math.ceil((content.length - MAX_TEXT_CHARS) / 1000)}K chars remaining)
            </button>
          )}
        </div>
      }
    />
  )
}
