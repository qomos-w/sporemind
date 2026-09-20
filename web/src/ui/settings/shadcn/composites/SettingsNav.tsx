import * as React from 'react'
import { cn } from '../lib/utils'

export interface NavItem {
  id: string
  label: React.ReactNode
  icon?: React.ReactNode
}

export interface NavGroup {
  label: React.ReactNode
  items: NavItem[]
}

export interface SettingsNavProps extends React.HTMLAttributes<HTMLElement> {
  groups: NavGroup[]
  activeId: string
  onNavigate: (id: string) => void
}

export function SettingsNav({
  groups,
  activeId,
  onNavigate,
  className,
  ...props
}: SettingsNavProps) {
  return (
    <nav aria-label="设置导航" className={cn('flex flex-col gap-6', className)} {...props}>
      {groups.map((group, gi) => (
        <div key={gi} className="flex flex-col gap-1">
          <p className="px-2.5 pb-1 text-xs font-medium tracking-wide text-muted-foreground/70 uppercase">
            {group.label}
          </p>
          {group.items.map((item) => {
            const isActive = activeId === item.id
            return (
              <button
                key={item.id}
                type="button"
                onClick={() => onNavigate(item.id)}
                aria-current={isActive ? 'page' : undefined}
                data-guide-id={`settings/category/${item.id}`}
                className={cn(
                  'group flex items-center gap-2.5 rounded-md px-2.5 py-2 text-left text-sm font-medium transition-colors outline-none focus-visible:ring-3 focus-visible:ring-ring/50',
                  isActive
                    ? 'bg-sidebar-accent text-sidebar-accent-foreground'
                    : 'text-muted-foreground hover:bg-sidebar-accent/60 hover:text-foreground',
                )}
              >
                {item.icon ? (
                  <span
                    className={cn(
                      'shrink-0 transition-colors [&_svg]:size-4',
                      isActive
                        ? 'text-foreground'
                        : 'text-muted-foreground/70 group-hover:text-foreground',
                    )}
                    aria-hidden
                  >
                    {item.icon}
                  </span>
                ) : null}
                {item.label}
              </button>
            )
          })}
        </div>
      ))}
    </nav>
  )
}
