import type { DbProfileView } from '../../../gen-types/dbmanager'
import { DbManagerSettings } from './DbManagerSettings'
import './ShellDbContent.css'

export interface ShellDbContentProps {
  /** Profiles open as right-panel tabs owned by AIShellLayout. */
  onOpenClient: (profile: DbProfileView) => void
  /** Whether the db mode is the active content mode. */
  isActive?: boolean
}

/** Main-content "Databases" mode: dbmanager profile list + CRUD. The client
 * itself lives in right-panel session tabs (ShellDbClientView). */
export function ShellDbContent({ onOpenClient, isActive = true }: ShellDbContentProps) {
  void isActive
  return (
    <div className="shell-db-content shadcn-scope">
      <DbManagerSettings onOpenClient={onOpenClient} />
    </div>
  )
}
