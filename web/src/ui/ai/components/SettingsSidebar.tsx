import React, { useEffect } from 'react'
import { ArrowLeft, Check } from 'lucide-react'
import { settingsGroups } from './settings-data'
import { useI18n } from '../../../i18n'
import './SettingsSidebar.css'

interface SettingsSidebarProps {
  activeIds: string[]
  onSelect: (id: string) => void
  onToggle: (id: string) => void
  onBack: () => void
}

export const SettingsSidebar: React.FC<SettingsSidebarProps> = ({
  activeIds,
  onSelect,
  onToggle,
  onBack,
}) => {
  const { t } = useI18n()

  useEffect(() => {
    if (activeIds.length === 0) {
      const firstGroup = settingsGroups[0]
      const first = firstGroup?.sections[0]
      if (first) onSelect(first.id)
    }
  }, [activeIds, onSelect])

  return (
    <div className="sp-settings-sidebar" data-guide-id="settings.sidebar">
      <button
        type="button"
        className="sp-settings-back-btn"
        data-guide-id="settings.sidebar.back"
        onClick={onBack}
        title={t('settingsSidebar.backToWorkspace')}
        aria-label={t('settingsSidebar.backToWorkspace')}
      >
        <ArrowLeft size={16} />
        <span className="sp-settings-sidebar-title">{t('settingsSidebar.backToWorkspace')}</span>
      </button>
      <div className="sp-settings-sidebar-list">
        {settingsGroups.map((group) => (
          <div key={group.labelKey} className="sp-settings-sidebar-group">
            <div className="sp-settings-sidebar-group-label">
              {t(group.labelKey)}
            </div>
            {group.sections.map((cat) => {
              const active = activeIds.includes(cat.id)
              return (
                <button
                  key={cat.id}
                  type="button"
                  className={`sp-settings-sidebar-item ${active ? 'active' : ''}`}
                  data-guide-id={`settings.category.${cat.id}`}
                  onClick={() => onSelect(cat.id)}
                  onContextMenu={(e) => {
                    e.preventDefault()
                    onToggle(cat.id)
                  }}
                  title={active ? t('settingsSidebar.selected') : t('settingsSidebar.clickToSelect')}
                >
                  <span className="sp-settings-sidebar-icon">{cat.icon}</span>
                  <span className="sp-settings-sidebar-label">{t(cat.labelKey)}</span>
                  {active && <Check size={12} className="sp-settings-sidebar-check" />}
                </button>
              )
            })}
          </div>
        ))}
      </div>
    </div>
  )
}
