import React from 'react'

interface TypeWriterProps {
  text: string
  isStreaming?: boolean
}

export const TypeWriter: React.FC<TypeWriterProps> = ({ text }) => {
  return (
    <span>
      {text}
    </span>
  )
}
