import * as React from 'react'
import { Tooltip as BaseTooltip } from '@base-ui/react/tooltip'
import { cn } from '../lib/utils'

const TooltipProvider = BaseTooltip.Provider

const TooltipRoot = BaseTooltip.Root

function TooltipTrigger({
  className,
  ...props
}: React.ComponentPropsWithoutRef<typeof BaseTooltip.Trigger>) {
  return (
    <BaseTooltip.Trigger
      className={cn('inline-block', className)}
      data-slot="tooltip-trigger"
      {...props}
    />
  )
}

const TooltipContent = React.forwardRef<
  HTMLDivElement,
  React.ComponentPropsWithoutRef<typeof BaseTooltip.Popup> & {
    sideOffset?: number
  }
>(({ className, sideOffset = 4, ...props }, ref) => (
  <BaseTooltip.Portal>
    <BaseTooltip.Positioner sideOffset={sideOffset} className="z-50">
      <BaseTooltip.Popup
        ref={ref}
        className={cn(
          'shadcn-scope z-50 overflow-hidden rounded-md bg-primary px-3 py-1.5 text-xs text-primary-foreground animate-in fade-in-0 zoom-in-95',
          className,
        )}
        data-slot="tooltip-content"
        {...props}
      />
    </BaseTooltip.Positioner>
  </BaseTooltip.Portal>
))
TooltipContent.displayName = 'TooltipContent'

export { TooltipProvider, TooltipRoot, TooltipTrigger, TooltipContent }