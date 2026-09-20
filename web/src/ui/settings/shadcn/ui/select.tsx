import * as React from 'react'
import { Select as BaseSelect } from '@base-ui/react/select'
import { ChevronDownIcon, CheckIcon } from 'lucide-react'
import { cn } from '../lib/utils'

const SelectRoot = BaseSelect.Root

const SelectGroup = BaseSelect.Group

function SelectValue({
  className,
  ...props
}: React.ComponentPropsWithoutRef<typeof BaseSelect.Value>) {
  return (
    <BaseSelect.Value
      data-slot="select-value"
      className={cn('flex flex-1 text-left text-sm', className)}
      {...props}
    />
  )
}

function SelectTrigger({
  className,
  size = 'default',
  children,
  ...props
}: React.ComponentPropsWithoutRef<typeof BaseSelect.Trigger> & {
  size?: 'sm' | 'default'
}) {
  return (
    <BaseSelect.Trigger
      data-slot="select-trigger"
      data-size={size}
      className={cn(
        "flex w-fit items-center justify-between gap-1.5 rounded-lg border border-input bg-transparent py-2 pr-2 pl-2.5 text-sm whitespace-nowrap transition-colors outline-none select-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive aria-invalid:ring-3 aria-invalid:ring-destructive/20 data-[size=default]:h-8 data-[size=sm]:h-7 data-[size=sm]:rounded-[min(var(--radius-md),10px)] *:data-[slot=select-value]:line-clamp-1 *:data-[slot=select-value]:flex *:data-[slot=select-value]:items-center *:data-[slot=select-value]:gap-1.5 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
        className,
      )}
      {...props}
    >
      {children}
      <BaseSelect.Icon
        render={
          <ChevronDownIcon className="pointer-events-none size-4 text-muted-foreground" />
        }
      />
    </BaseSelect.Trigger>
  )
}

const SelectPortal = BaseSelect.Portal
const SelectPositioner = BaseSelect.Positioner

function SelectContent({
  className,
  children,
  side = 'bottom',
  sideOffset = 4,
  align = 'center',
  alignOffset = 0,
  // false (the shadcn-upstream default): the popup stays anchored under the
  // trigger. Base UI's native "align selected item with trigger" behavior
  // shifts the popup sideways by the selected item's offset, which reads as
  // a misplaced dropdown in left-aligned settings forms.
  alignItemWithTrigger = false,
  ...props
}: React.ComponentPropsWithoutRef<typeof BaseSelect.Popup> &
  Pick<
    React.ComponentPropsWithoutRef<typeof BaseSelect.Positioner>,
    'align' | 'alignOffset' | 'side' | 'sideOffset' | 'alignItemWithTrigger'
  >) {
  return (
    <BaseSelect.Portal>
      <BaseSelect.Positioner
        side={side}
        sideOffset={sideOffset}
        align={align}
        alignOffset={alignOffset}
        alignItemWithTrigger={alignItemWithTrigger}
        className="isolate z-1100"
      >
        <BaseSelect.Popup
          data-slot="select-content"
          className={cn(
            'shadcn-scope relative isolate z-1100 max-h-(--available-height) w-(--anchor-width) min-w-36 origin-(--transform-origin) overflow-x-hidden overflow-y-auto rounded-lg bg-popover p-1 text-popover-foreground shadow-md ring-1 ring-foreground/10',
            className,
          )}
          {...props}
        >
          {children}
        </BaseSelect.Popup>
      </BaseSelect.Positioner>
    </BaseSelect.Portal>
  )
}

function SelectItem({
  className,
  children,
  ...props
}: React.ComponentPropsWithoutRef<typeof BaseSelect.Item>) {
  return (
    <BaseSelect.Item
      data-slot="select-item"
      className={cn(
        "relative flex w-full cursor-default items-center gap-1.5 rounded-md py-1 pr-8 pl-1.5 text-sm outline-none select-none focus:bg-accent focus:text-accent-foreground data-[disabled]:pointer-events-none data-[disabled]:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
        className,
      )}
      {...props}
    >
      {children}
      <BaseSelect.ItemIndicator
        render={
          <span className="pointer-events-none absolute right-2 flex size-4 items-center justify-center" />
        }
      >
        <CheckIcon className="pointer-events-none" />
      </BaseSelect.ItemIndicator>
    </BaseSelect.Item>
  )
}

function SelectItemText({
  className,
  ...props
}: React.ComponentPropsWithoutRef<typeof BaseSelect.ItemText>) {
  return (
    <BaseSelect.ItemText
      data-slot="select-item-text"
      className={cn('flex flex-1 shrink-0 gap-2 text-sm whitespace-nowrap', className)}
      {...props}
    />
  )
}

const SelectSeparator = React.forwardRef<
  HTMLDivElement,
  React.ComponentPropsWithoutRef<typeof BaseSelect.Separator>
>(({ className, ...props }, ref) => (
  <BaseSelect.Separator
    ref={ref}
    data-slot="select-separator"
    className={cn('pointer-events-none -mx-1 my-1 h-px bg-border', className)}
    {...props}
  />
))
SelectSeparator.displayName = 'SelectSeparator'

export {
  SelectRoot,
  SelectGroup,
  SelectTrigger,
  SelectValue,
  SelectPortal,
  SelectPositioner,
  SelectContent,
  SelectItem,
  SelectItemText,
  SelectSeparator,
}
