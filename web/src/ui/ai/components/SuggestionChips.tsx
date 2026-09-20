import React from 'react'
import { useI18n } from '../../../i18n'
import type { I18nKey } from '../../../i18n'
import type { SuggestionContext } from './suggestions/suggestionRegistry'
import { selectSuggestions } from './suggestions/suggestionRegistry'
import './SuggestionChips.css'

export interface SuggestionChipsProps {
  context: SuggestionContext
  onPick: (text: string) => void
}

export const SuggestionChips: React.FC<SuggestionChipsProps> = ({ context, onPick }) => {
  const { t } = useI18n()

  const suggestions = selectSuggestions(context)

  return (
    <div className="suggestion-chips">
      {suggestions.map(s => {
        const label = s.text ?? t(s.labelKey as I18nKey)
        return (
          <button
            key={s.id}
            type="button"
            className="suggestion-chip"
            onClick={() => onPick(label)}
          >
            {label}
          </button>
        )
      })}
    </div>
  )
}
