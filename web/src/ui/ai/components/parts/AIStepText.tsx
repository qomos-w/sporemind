import React, { useContext, useMemo } from 'react'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { makeRehypePlugins, useFileReference, resolveFileReference } from './file-reference.tsx'
import { processWikiWords, wikiUrlTransform } from './wikiword.ts'
import { useI18n } from '../../../../i18n'
import { AIShellContext } from '../../context/AIShellContext'
import { MermaidRender } from './mermaid-render.tsx'
import { isBuiltinCard, getBuiltinTagDisplayName } from '../../../../domain/builtin-cards'
import { highlightedCode } from './diff-highlight'

function markdownCodeComponent(language: string | undefined, code: string): React.ReactNode {
  if (language === 'mermaid') {
    return <MermaidRender code={code} />
  }
  if (language) {
    const highlighted = highlightedCode(language, code)
    if (highlighted) return <code className={`language-${language} ai-highlighted-code`}>{highlighted}</code>
  }
  return undefined
}

function makeWikiLinkHandler(onOpenCard?: (cardId: string) => void, t?: (key: any) => string) {
  return ({ href, children, className, ...props }: React.ComponentProps<'a'>) => {
    if (href?.startsWith('wiki:')) {
      const word = decodeURIComponent(href.slice(5))
      const displayText = t && isBuiltinCard(word) ? getBuiltinTagDisplayName(word, t) : undefined
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

    if (typeof className === 'string' && className.includes('ai-file-ref')) {
      return <a {...props} className={className}>{children}</a>
    }

    return <a href={href} target="_blank" rel="noreferrer">{children}</a>
  }
}

interface AIStepTextProps {
  children: string
  className?: string
  onClick?: (e: React.MouseEvent) => void
}

/**
 * Renders a step-level text block with full markdown and wikiword support.
 *
 * This is the shared renderer for assistant text inside the timeline; using it
 * for goal_submit text ensures the same markdown parsing, wikiword link handling,
 * and file-reference behavior as regular text frames.
 */
export const AIStepText: React.FC<AIStepTextProps> = ({ children, className, onClick }) => {
  const fileRefCtx = useFileReference()
  const aiShellCtx = useContext(AIShellContext)
  const { t } = useI18n()

  const processedContent = useMemo(() => processWikiWords(children), [children])

  const rehypePlugins = useMemo(
    () => makeRehypePlugins({
      projectId: fileRefCtx?.projectId ?? null,
      projectRoot: fileRefCtx?.projectRoot ?? null,
    }),
    [fileRefCtx?.projectId, fileRefCtx?.projectRoot],
  )

  const components = useMemo(
    () => ({
      a: makeWikiLinkHandler(aiShellCtx?.onOpenCard, t),
      code({ className: codeClassName, children: codeChildren, ...props }: React.ComponentProps<'code'>) {
        const lang = codeClassName?.replace('language-', '')
        const codeText = String(codeChildren ?? '')
        const rendered = markdownCodeComponent(lang, codeText)
        if (rendered) return rendered
        return <code className={codeClassName} {...props}>{codeChildren}</code>
      },
    }),
    [aiShellCtx?.onOpenCard, t],
  )

  const handleClick = async (e: React.MouseEvent) => {
    onClick?.(e)

    const target = e.target as HTMLElement

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

  return (
    <div className={`ai-step-text markdown-content ${className ?? ''}`} onClick={handleClick}>
      <Markdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={rehypePlugins}
        urlTransform={wikiUrlTransform}
        components={components}
      >
        {processedContent}
      </Markdown>
    </div>
  )
}
