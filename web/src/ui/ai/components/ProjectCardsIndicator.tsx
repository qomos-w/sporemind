import { useEffect, useState } from 'react'
import { projectCardStore, type ProjectCardState } from '../../../application/project-card-client'

export function ProjectCardsIndicator() {
  const [state, setState] = useState<ProjectCardState>(projectCardStore.getState())

  useEffect(() => projectCardStore.subscribe(setState), [])

  if (!state.projectId || state.loading || (!state.error && state.refs.length === 0)) return null
  if (state.error) return <span data-testid="project-cards-error">Cards: {state.error}</span>
  return <span data-testid="project-cards-count">Cards: {state.refs.length}</span>
}
