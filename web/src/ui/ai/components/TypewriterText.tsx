import React from 'react'

interface TypewriterTextProps {
  text: string
  /** Average ms per character. Default 18. */
  speed?: number
  className?: string
}

/**
 * TypewriterText — progressively reveals text, character by character.
 * When the source text grows (e.g. streaming), it keeps typing the new tail.
 */
export const TypewriterText: React.FC<TypewriterTextProps> = ({
  text,
  speed = 18,
  className,
}) => {
  const [index, setIndex] = React.useState(0)
  const textRef = React.useRef(text)
  const timerRef = React.useRef<number | null>(null)

  React.useEffect(() => {
    // If text was shortened, clamp index back to the new length
    if (text.length < index) {
      setIndex(text.length)
    }
    textRef.current = text

    if (timerRef.current) {
      window.clearTimeout(timerRef.current)
    }

    function typeNext() {
      const currentText = textRef.current
      setIndex(prev => {
        if (prev >= currentText.length) return prev
        const next = prev + 1
        timerRef.current = window.setTimeout(typeNext, speed)
        return next
      })
    }

    if (index < text.length) {
      timerRef.current = window.setTimeout(typeNext, speed)
    }

    return () => {
      if (timerRef.current) window.clearTimeout(timerRef.current)
    }
  }, [text, speed])

  return (
    <span className={className}>
      {text.slice(0, index)}
      {index < text.length && (
        <span className="typewriter-cursor">|</span>
      )}
    </span>
  )
}
