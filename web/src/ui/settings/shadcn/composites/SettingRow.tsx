import * as React from 'react'
import {
  Field,
  FieldContent,
  FieldLabel,
  FieldDescription,
} from '../ui/field'

export interface SettingRowProps extends React.HTMLAttributes<HTMLDivElement> {
  label: React.ReactNode
  description?: string
  htmlFor?: string
  /** Control aligned to the right on the same line (switch, select, small input). */
  control?: React.ReactNode
  /** Full-width control rendered below the label (textarea, slider, radio group). */
  children?: React.ReactNode
}

export function SettingRow({
  label,
  description,
  htmlFor,
  control,
  children,
  ...props
}: SettingRowProps) {
  if (children) {
    return (
      <Field {...props}>
        <FieldContent>
          <FieldLabel htmlFor={htmlFor}>{label}</FieldLabel>
          {description ? (
            <FieldDescription>{description}</FieldDescription>
          ) : null}
        </FieldContent>
        {children}
      </Field>
    )
  }

  return (
    <Field orientation="horizontal" {...props}>
      <FieldContent>
        <FieldLabel htmlFor={htmlFor}>{label}</FieldLabel>
        {description ? (
          <FieldDescription>{description}</FieldDescription>
        ) : null}
      </FieldContent>
      {control}
    </Field>
  )
}
