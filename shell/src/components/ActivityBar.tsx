import React from 'react'
import { Settings } from 'lucide-react'
import './ActivityBar.css'

export interface ActivityBarEntry {
  id: string
  icon: React.ReactNode
  label: string
  /** Whether this panel is the active tab in its zone (for pinned) or currently shown (for floating) */
  visible: boolean
  /** Panel's dock zone — determines top vs bottom icon placement */
  dockZone?: string | null
}

interface ActivityBarProps {
  items: ActivityBarEntry[]
  onToggle: (id: string) => void
  position?: 'left' | 'right'
  showSettings?: boolean
}

// Zones for each section
const TOP_ZONES = new Set(['left-top', 'right-top'])
const MIDDLE_ZONES = new Set(['left-bottom', 'right-bottom'])
const BOTTOM_ZONES = new Set(['bottom-left', 'bottom-right'])

export const ActivityBar: React.FC<ActivityBarProps> = ({
  items,
  onToggle,
  position = 'left',
  showSettings = false,
}) => {
  const topItems = items.filter(item => item.dockZone && TOP_ZONES.has(item.dockZone))
  const middleItems = items.filter(item => item.dockZone && MIDDLE_ZONES.has(item.dockZone))
  const bottomItems = items.filter(item => item.dockZone && BOTTOM_ZONES.has(item.dockZone))

  const renderBtn = (item: ActivityBarEntry) => (
    <button
      key={item.id}
      className={`ab-btn ${item.visible ? 'active' : ''}`}
      onClick={() => onToggle(item.id)}
      title={item.label}
    >
      {item.icon}
    </button>
  )

  return (
    <div className={`activity-bar ${position}`}>
      {/* Upper area: top items + middle items (fills remaining space) */}
      <div className="ab-upper">
        <div className="ab-top">
          {topItems.map(renderBtn)}
        </div>
        <div className="ab-middle">
          {middleItems.reverse().map(renderBtn)}
        </div>
      </div>

      {/* Lower area: bottom-zone icons */}
      <div className="ab-lower">
        {bottomItems.map(renderBtn)}
        {showSettings && (
          <button className="ab-btn" title="Settings" disabled>
            <Settings size={16} />
          </button>
        )}
      </div>
    </div>
  )
}
