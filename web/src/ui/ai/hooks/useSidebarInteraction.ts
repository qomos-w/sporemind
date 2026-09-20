import { useCallback } from 'react'

export interface SidebarInteractionResult {
  showSidebar: boolean
  handleToggleSidebar: () => void
}

interface SidebarInteractionOptions {
  sidebarVisible: boolean
  dispatchToggleSidebar: () => void
}

export function useSidebarInteraction({
  sidebarVisible,
  dispatchToggleSidebar,
}: SidebarInteractionOptions): SidebarInteractionResult {
  const handleToggleSidebar = useCallback(() => {
    dispatchToggleSidebar()
  }, [dispatchToggleSidebar])

  return {
    showSidebar: sidebarVisible,
    handleToggleSidebar,
  }
}
