import { useSyncExternalStore, useCallback } from 'react'
import { editorStore, type OpenFile } from './editorStore'

/**
 * Subscribe to a single file's changes in editorStore.
 * Unlike the global subscribe+getVersion pattern, this only triggers a
 * React re-render when the specific file's content or state changes.
 */
export function useEditorFile(projectId: string, filePath: string): OpenFile | undefined {
  const key = editorStore.fileKey(projectId, filePath)

  useSyncExternalStore(
    useCallback((cb: () => void) => editorStore.subscribeFile(key, cb), [key]),
    useCallback(() => editorStore.getFileVersion(key), [key]),
  )

  return editorStore.getFile(projectId, filePath)
}

/**
 * Subscribe to a single file and also return its dirty status.
 */
export function useEditorDirty(projectId: string, filePath: string): boolean {
  const key = editorStore.fileKey(projectId, filePath)
  // Subscribe to file version changes; React re-renders when version increments
  useSyncExternalStore(
    useCallback((cb: () => void) => editorStore.subscribeFile(key, cb), [key]),
    useCallback(() => editorStore.getFileVersion(key), [key]),
  )
  return editorStore.isDirty(projectId, filePath)
}
