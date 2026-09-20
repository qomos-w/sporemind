import * as React from 'react'
import { Switch as BaseSwitch } from '@base-ui/react/switch'
import { cn } from '../lib/utils'

const Switch = React.forwardRef<
  HTMLElement,
  React.ComponentPropsWithoutRef<typeof BaseSwitch.Root> & {
    size?: 'sm' | 'default'
  }
>(({ className, size = 'default', ...props }, ref) => (
  <BaseSwitch.Root
    ref={ref}
    data-slot="switch"
    data-size={size}
    className={cn(
      'peer group/switch relative inline-flex shrink-0 cursor-pointer items-center rounded-full border border-transparent transition-colors outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 data-[size=default]:h-[18.4px] data-[size=default]:w-[32px] data-[size=sm]:h-[14px] data-[size=sm]:w-[24px] data-[checked]:bg-primary data-[unchecked]:bg-input disabled:cursor-not-allowed disabled:opacity-50',
      className,
    )}
    {...props}
  >
    <BaseSwitch.Thumb
      data-slot="switch-thumb"
      className={cn(
        'pointer-events-none block rounded-full bg-background ring-0 transition-transform group-data-[size=default]/switch:size-4 group-data-[size=sm]/switch:size-3 data-[checked]:translate-x-[calc(100%-2px)] data-[unchecked]:translate-x-0',
      )}
    />
  </BaseSwitch.Root>
))
Switch.displayName = 'Switch'

export { Switch }
