import React from 'react'

interface TimelineProps {
  children: React.ReactNode
}

export const Timeline: React.FC<TimelineProps> = ({ children }) => (
  <div className="ai-timeline">{children}</div>
)
