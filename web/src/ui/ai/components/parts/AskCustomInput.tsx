import React, { useState } from 'react'
import { useI18n } from '../../../../i18n'
import { useViewportMode } from '../../../../application/useViewportMode'
import { MobileCardComposer } from '../MobileCardComposer'

interface AskCustomInputProps {
  value: string
  onChange: (value: string) => void
  onSubmit: () => void
  /** Title shown in the mobile composer sheet. */
  title?: string
  placeholder?: string
  /** Extra class for the input element (e.g. compact peek-bar variant). */
  className?: string
}

/** Free-text answer input for ask_user interactions. Desktop renders an
 *  inline input with Enter/submit; mobile renders a readOnly field that opens
 *  the MobileCardComposer sheet (avoids keyboard/viewport issues inside
 *  scrollable cards). */
export const AskCustomInput: React.FC<AskCustomInputProps> = ({
  value,
  onChange,
  onSubmit,
  title,
  placeholder,
  className,
}) => {
  const { t } = useI18n()
  const isMobile = useViewportMode() === 'mobile'
  const [composerOpen, setComposerOpen] = useState(false)

  const resolvedPlaceholder = placeholder ?? t('askUser.placeholder.answer')

  if (isMobile) {
    return (
      <>
        <input
          type="text"
          readOnly
          className={`ai-ask-input ai-ask-input--mobile${className ? ` ${className}` : ''}`}
          placeholder={resolvedPlaceholder}
          value={value}
          onClick={() => setComposerOpen(true)}
        />
        <MobileCardComposer
          open={composerOpen}
          value={value}
          onChange={onChange}
          onSubmit={onSubmit}
          onClose={() => setComposerOpen(false)}
          title={title ?? t('ai.step.question')}
          placeholder={resolvedPlaceholder}
          submitLabel={t('common.submit')}
          multiline={false}
        />
      </>
    )
  }

  return (
    <>
      <input
        type="text"
        className={`ai-ask-input${className ? ` ${className}` : ''}`}
        placeholder={resolvedPlaceholder}
        value={value}
        onChange={e => onChange(e.target.value)}
        onKeyDown={e => { if (e.key === 'Enter') onSubmit() }}
      />
      {value.trim() && (
        <button className="ai-ask-submit" onClick={onSubmit}>{t('common.submit')}</button>
      )}
    </>
  )
}
