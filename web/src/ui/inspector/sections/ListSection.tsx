import type { InspectSection, OpenTarget, InspectRef } from '../../../gen-clients/system/types'
import { useInspect } from '../inspectContext'
import { openWorkbenchTarget } from '../../../application/open-service'

export function ListSection({ section }: { section: InspectSection }) {
  const items = section.Items!
  const inspect = useInspect()
  const handleClick = (openTarget?: OpenTarget, ref?: InspectRef) => {
    if (openTarget) {
      openWorkbenchTarget(openTarget)
    } else if (ref) {
      inspect?.(ref)
    }
  }

  return (
    <div className="inspector-section">
      <h4 className="inspector-section-title">{section.Title}</h4>
      <div className="inspector-list-items">
        {items.map((item, i) => (
          <div
            key={i}
            className={`inspector-list-item${item.Ref || item.OpenTarget ? ' inspector-list-item--clickable' : ''}`}
            onClick={() => handleClick(item.OpenTarget, item.Ref)}
          >
            <span className="inspector-list-label">{item.Label}</span>
            {item.Description && (
              <span className="inspector-list-description">{item.Description}</span>
            )}
          </div>
        ))}
      </div>
    </div>
  )
}
