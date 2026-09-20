import { useEffect, useState } from 'react'
import { Hexagon } from 'lucide-react'
import { wailsWindowController } from '../../application/window-controller'
import { AIShellWindowControls } from '../ai/components/AIShellWindowControls'
import './auth.css'

export function AuthTitleBar() {
  const [maximised, setMaximised] = useState(false)
  const isHost = wailsWindowController.isHostMode()

  useEffect(() => {
    if (!isHost) return
    wailsWindowController.isMaximised().then(setMaximised).catch(() => {})
  }, [isHost])

  if (!isHost) return null

  return (
    <div className="auth-titlebar">
      <div className="auth-titlebar-icon">
        <Hexagon size={14} />
      </div>
      <div
        className="auth-titlebar-drag"
        onDoubleClick={() => {
          wailsWindowController.toggleMaximise().finally(() => {
            wailsWindowController.isMaximised().then(setMaximised).catch(() => {})
          })
        }}
      />
      <AIShellWindowControls maximised={maximised} onMaximisedChange={setMaximised} />
    </div>
  )
}
