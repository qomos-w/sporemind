import { SshManagerPanel } from '../../panels/SshManagerPanel'
import './ShellSshContent.css'

export interface ShellSshContentProps {
  /** Sessions open as right-panel tabs owned by AIShellLayout. */
  onOpenSession: (sessionId: string, hostId: string, hostName: string) => void
  /** Whether the SSH mode is the active content mode. When false, polling is paused. */
  isActive?: boolean
}

export function ShellSshContent({ onOpenSession, isActive = true }: ShellSshContentProps) {
  return (
    <div className="shell-ssh-content manager">
      <SshManagerPanel onOpenSession={onOpenSession} isActive={isActive} />
    </div>
  )
}
