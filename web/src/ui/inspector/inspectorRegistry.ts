import type { InspectRef } from '../../gen-clients/system/types'

export type InspectorPageDef = {
  id: string
  icon: React.ReactNode
  label: string
  component: React.FC<{ ref: InspectRef }>
}

const componentRegistry: Record<string, React.FC<{ ref: InspectRef }>> = {}

export function registerInspectorComponent(
  pageId: string,
  component: React.FC<{ ref: InspectRef }>
): void {
  componentRegistry[pageId] = component
}

export function getInspectorComponent(
  pageId: string
): React.FC<{ ref: InspectRef }> | undefined {
  return componentRegistry[pageId]
}
