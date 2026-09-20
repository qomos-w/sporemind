import React, { createContext, useContext, useRef, useCallback, useState } from 'react'

export interface FileClipboardEntry {
  paths: string[]
  isCut: boolean
}

interface FileClipboardContextValue {
  clipboard: React.RefObject<FileClipboardEntry | null>
  cutSourcePath: React.RefObject<string | null>
  setClipboard: (entry: FileClipboardEntry | null) => void
  setCutSource: (path: string | null) => void
  /** Bumped whenever clipboard changes so consumers re-render. */
  clipboardVersion: number
}

const FileClipboardContext = createContext<FileClipboardContextValue | null>(null)

export function FileClipboardProvider({ children }: { children: React.ReactNode }) {
  const clipboard = useRef<FileClipboardEntry | null>(null)
  const cutSourcePath = useRef<string | null>(null)
  const [clipboardVersion, setClipboardVersion] = useState(0)

  const setClipboard = useCallback((entry: FileClipboardEntry | null) => {
    clipboard.current = entry
    setClipboardVersion(v => v + 1)
  }, [])

  const setCutSource = useCallback((path: string | null) => {
    cutSourcePath.current = path
  }, [])

  const value: FileClipboardContextValue = {
    clipboard,
    cutSourcePath,
    setClipboard,
    setCutSource,
    clipboardVersion,
  }

  return React.createElement(FileClipboardContext.Provider, { value }, children)
}

export function useFileClipboard(): FileClipboardContextValue {
  const ctx = useContext(FileClipboardContext)
  if (!ctx) {
    const noopRef = { current: null }
    return {
      clipboard: noopRef as React.RefObject<FileClipboardEntry | null>,
      cutSourcePath: noopRef as React.RefObject<string | null>,
      setClipboard: () => {},
      setCutSource: () => {},
      clipboardVersion: 0,
    }
  }
  return ctx
}