import * as React from 'react'
import { cn } from '../lib/utils'
import {
  Card,
  CardHeader,
  CardTitle,
  CardDescription,
  CardAction,
  CardContent,
  CardFooter,
} from '../ui/card'
import { FieldGroup } from '../ui/field'

export interface FeatureCardProps extends React.HTMLAttributes<HTMLDivElement> {
  icon?: React.ReactNode
  title: string
  description?: string
  /** Optional action rendered in the card header (e.g. an "add" button or badge). */
  action?: React.ReactNode
  children?: React.ReactNode
  footer?: React.ReactNode
}

export function FeatureCard({
  icon,
  title,
  description,
  action,
  children,
  footer,
  className,
  ...props
}: FeatureCardProps) {
  return (
    <Card className={cn('gap-0 py-0', className)} {...props}>
      <CardHeader className="border-b border-border py-4">
        <CardTitle className="text-sm">
          {icon ? (
            <span className="flex items-center gap-2">
              <span className="shrink-0 text-muted-foreground [&_svg]:size-4">{icon}</span>
              {title}
            </span>
          ) : (
            title
          )}
        </CardTitle>
        {description ? <CardDescription>{description}</CardDescription> : null}
        {action ? <CardAction>{action}</CardAction> : null}
      </CardHeader>
      {children ? (
        <CardContent className="py-5">
          <FieldGroup className="gap-6">{children}</FieldGroup>
        </CardContent>
      ) : null}
      {footer ? <CardFooter>{footer}</CardFooter> : null}
    </Card>
  )
}
