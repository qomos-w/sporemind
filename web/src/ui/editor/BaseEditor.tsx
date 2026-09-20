import React from 'react'

// All editor panels in the main viewport must implement this props interface.
// This allows the editor registry to swap in different implementations
// (code editor, image viewer, markdown preview, diff editor, etc.) based on file type.

export interface BaseEditorProps {
  projectId: string
  filePath: string
  initialLine?: number
}

export type EditorComponent = React.ComponentType<BaseEditorProps>

// Maps lowercase file extensions to their editor component.
const registry = new Map<string, EditorComponent>()

let defaultEditor: EditorComponent | null = null

export function registerEditor(
  extensions: string | string[],
  component: EditorComponent,
): void {
  const exts = Array.isArray(extensions) ? extensions : [extensions]
  for (const ext of exts) {
    registry.set(ext.toLowerCase().replace(/^\./, ''), component)
  }
}

export function setDefaultEditor(component: EditorComponent): void {
  defaultEditor = component
}

export function resolveEditor(filePath: string): EditorComponent {
  const ext = filePath.split('.').pop()?.toLowerCase() ?? ''
  return registry.get(ext) ?? defaultEditor ?? FallbackEditor
}

const FallbackEditor: EditorComponent = ({ filePath }) => (
  <div style={{ padding: 16, color: 'var(--text-tertiary)', fontFamily: 'monospace' }}>
    No editor registered for: {filePath}
  </div>
)
