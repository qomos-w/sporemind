import { cn } from '../lib/utils'
import { Button } from '../ui/button'
import { Sun, Moon } from 'lucide-react'

export interface ThemePreviewProps {
  value: 'light' | 'dark'
  onChange: (mode: 'light' | 'dark') => void
}

export function ThemePreview({ value, onChange }: ThemePreviewProps) {
  return (
    <div className="flex gap-3" data-guide-id="settings/general/theme-mode">
      <Button
        type="button"
        variant={value === 'light' ? 'default' : 'outline'}
        onClick={() => onChange('light')}
        className={cn(
          'flex flex-col items-center gap-2 p-4',
          'h-auto w-auto',
        )}
      >
        <div className="h-20 w-24 rounded-md border bg-white shadow-sm" />
        <span className="flex items-center gap-1.5 text-sm">
          <Sun size={14} />
          Light
        </span>
      </Button>
      <Button
        type="button"
        variant={value === 'dark' ? 'default' : 'outline'}
        onClick={() => onChange('dark')}
        className={cn(
          'flex flex-col items-center gap-2 p-4',
          'h-auto w-auto',
        )}
      >
        <div className="h-20 w-24 rounded-md border bg-gray-900 shadow-sm" />
        <span className="flex items-center gap-1.5 text-sm">
          <Moon size={14} />
          Dark
        </span>
      </Button>
    </div>
  )
}