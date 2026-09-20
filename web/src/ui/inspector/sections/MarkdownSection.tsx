import type { InspectSection } from '../../../gen-clients/system/types'

export function MarkdownSection({ section }: { section: InspectSection }) {
  return (
    <div className="inspector-section">
      <h4 className="inspector-section-title">{section.Title}</h4>
      <div className="inspector-markdown-body">{section.Body!}</div>
    </div>
  )
}
