import React, { useState, useRef, useEffect } from 'react'
import type { FrameAnimation } from '../../model/frame-types'

interface TransitionHostProps {
  animation: FrameAnimation
  version: number
  children: React.ReactNode
}

export const TransitionHost: React.FC<TransitionHostProps> = ({
  animation,
  version,
  children,
}) => {
  if (animation === 'none') return <>{children}</>

  const animClass = animation === 'slide'
    ? 'ai-transition-slide'
    : animation === 'scale'
      ? 'ai-transition-scale'
      : 'ai-transition-fade'

  return (
    <div className={`ai-transition-host ${animClass}`}>
      <TransitionContent key={version}>
        {children}
      </TransitionContent>
    </div>
  )
}

/** Inner component that manages exit/enter on version (key) change */
const TransitionContent: React.FC<{
  children: React.ReactNode
}> = ({ children }) => {
  const [exiting, setExiting] = useState<React.ReactNode | null>(null)
  const [entering, setEntering] = useState<React.ReactNode>(children)
  const prevChildrenRef = useRef(children)

  useEffect(() => {
    if (prevChildrenRef.current !== children) {
      setExiting(prevChildrenRef.current)
      setEntering(children)
      prevChildrenRef.current = children
      const timer = setTimeout(() => setExiting(null), 300)
      return () => clearTimeout(timer)
    }
  }, [children])

  return (
    <>
      {exiting && (
        <div className="ai-transition-exit" aria-hidden>
          {exiting}
        </div>
      )}
      <div className="ai-transition-enter">
        {entering}
      </div>
    </>
  )
}
