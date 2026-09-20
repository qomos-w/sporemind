import { useCallback, useEffect, useMemo, useState } from 'react'
import { Keyboard } from 'lucide-react'
import { useI18n, type I18nKey } from '../../../i18n'
import { FeatureCard, SettingRow } from '../../settings/shadcn/composites'
import { Button, Input, SelectRoot, SelectTrigger, SelectValue, SelectContent, SelectItem, SelectItemText } from '../../settings/shadcn/ui'
import { useEditorKeymap } from '../../editor/EditorKeymapContext'
import {
  BIND_ACTIONS,
  BIND_CATEGORIES,
  effectiveKeysForAction,
  findActionHoldingKey,
  keyEventToBinding,
  resolveSchemeBindings,
  type BindActionId,
} from '../../editor/keymap-schemes'
import './KeymapSettingsSection.css'

const isMacPlatform = typeof navigator !== 'undefined' && /Mac/i.test(navigator.platform)

function displayKey(key: string): string {
  return key
    .replace(/-/g, '+')
    .replace(/\bMod\b/, isMacPlatform ? 'Cmd' : 'Ctrl')
}

export function KeymapSettingsSection() {
  const { t } = useI18n()
  const { state, customs, selectScheme, createCustom, deleteCustom, renameCustom, setBinding } = useEditorKeymap()

  const [newName, setNewName] = useState('')
  const [newSource, setNewSource] = useState<string>('vscode')
  const [capturing, setCapturing] = useState<BindActionId | null>(null)

  const selectedCustom = customs.find(c => c.id === state.selected)
  const resolved = useMemo(() => resolveSchemeBindings(state.selected, customs), [state.selected, customs])

  const sourceItems = useMemo(() => ([
    ...customs.map(c => ({ value: c.id, label: c.name })),
    { value: 'vscode', label: t('settings.keymap.presetVscode') },
    { value: 'idea', label: t('settings.keymap.presetIdea') },
  ]), [customs, t])

  const schemeItems = useMemo(() => sourceItems, [sourceItems])

  const handleCreate = useCallback(() => {
    if (!newName.trim()) return
    createCustom(newName.trim(), newSource)
    setNewName('')
  }, [newName, newSource, createCustom])

  useEffect(() => {
    if (!capturing) return
    const onKeyDown = (e: KeyboardEvent) => {
      e.preventDefault()
      e.stopPropagation()
      const custom = selectedCustom
      if (!custom) { setCapturing(null); return }
      if (e.key === 'Escape') { setCapturing(null); return }
      if (e.key === 'Backspace') { setBinding(custom.id, capturing, null); setCapturing(null); return }
      const binding = keyEventToBinding(e, isMacPlatform)
      if (!binding) return
      setBinding(custom.id, capturing, binding)
      setCapturing(null)
    }
    window.addEventListener('keydown', onKeyDown, { capture: true })
    return () => window.removeEventListener('keydown', onKeyDown, { capture: true } as EventListenerOptions)
  }, [capturing, selectedCustom, setBinding])

  const schemeLabel = (id: string): string => {
    if (id === 'vscode') return t('settings.keymap.presetVscode')
    if (id === 'idea') return t('settings.keymap.presetIdea')
    return customs.find(c => c.id === id)?.name ?? id
  }

  return (
    <FeatureCard icon={<Keyboard size={16} />} title={t('settings.keymap.title')} description={t('settings.keymap.desc')}>
      <SettingRow
        label={t('settings.keymap.preset')}
        description={selectedCustom
          ? t('settings.keymap.customDesc', { base: schemeLabel(selectedCustom.base) })
          : t('settings.keymap.presetDesc')}
        control={
          <SelectRoot
            value={state.selected}
            onValueChange={(v) => { setCapturing(null); selectScheme(v as string) }}
            items={schemeItems}
          >
            <SelectTrigger className="w-44" data-guide-id="settings/keymap/preset" aria-label={t('settings.keymap.preset')}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {schemeItems.map((o) => (
                <SelectItem key={o.value} value={o.value} data-guide-id={`settings/keymap/preset/${o.value}`}>
                  <SelectItemText>{o.label}</SelectItemText>
                </SelectItem>
              ))}
            </SelectContent>
          </SelectRoot>
        }
      />

      {selectedCustom && (
        <SettingRow
          label={t('settings.keymap.customName')}
          description={t('settings.keymap.customNameDesc')}
          control={
            <div className="km-custom-row">
              <Input
                className="w-44"
                value={selectedCustom.name}
                aria-label={t('settings.keymap.customName')}
                onChange={(e) => renameCustom(selectedCustom.id, e.target.value)}
                onBlur={(e) => renameCustom(selectedCustom.id, e.target.value.trim() || selectedCustom.name)}
              />
              <Button
                variant="outline"
                size="sm"
                onClick={() => { setCapturing(null); deleteCustom(selectedCustom.id) }}
                aria-label={t('settings.keymap.deleteCustom')}
              >
                {t('settings.keymap.deleteCustom')}
              </Button>
            </div>
          }
        />
      )}

      <div className="km-bindings">
        <div className="km-bindings-header">
          <span>{t('settings.keymap.actionHeader')}</span>
          <span>{t('settings.keymap.keyHeader')}</span>
        </div>
        {BIND_CATEGORIES.map((cat) => (
          <div key={cat} className="km-category">
            <div className="km-category-title">{t(`settings.keymap.category.${cat}` as I18nKey)}</div>
            {BIND_ACTIONS.filter(a => a.category === cat).map((action) => {
              const keys = effectiveKeysForAction(action.id, resolved)
              const overriding = selectedCustom?.overrides[action.id]
              const isCapturing = capturing === action.id
              const conflict = overriding
                ? findActionHoldingKey(overriding, resolved, action.id)
                : null
              return (
                <div key={action.id} className="km-binding-row">
                  <span className="km-action-label">{t(action.labelKey as I18nKey)}</span>
                  <button
                    type="button"
                    className={`km-key-chip ${isCapturing ? 'km-capturing' : ''} ${conflict ? 'km-conflict' : ''}`}
                    disabled={!selectedCustom}
                    onClick={() => setCapturing(isCapturing ? null : action.id)}
                    aria-label={t('settings.keymap.rebind', { action: t(action.labelKey as I18nKey) })}
                  >
                    {isCapturing
                      ? t('settings.keymap.capturing')
                      : keys.length > 0
                        ? keys.map(displayKey).join(' / ')
                        : t('settings.keymap.unbound')}
                  </button>
                  {conflict && (
                    <span className="km-conflict-hint">
                      {t('settings.keymap.conflict', { action: t(`settings.keymap.action.${conflict}` as I18nKey) })}
                    </span>
                  )}
                </div>
              )
            })}
          </div>
        ))}
        {selectedCustom && (
          <div className="km-capture-hint">{t('settings.keymap.captureHint')}</div>
        )}
        {!selectedCustom && (
          <div className="km-capture-hint">{t('settings.keymap.readonlyHint')}</div>
        )}
      </div>

      <div className="km-create">
        <div className="km-create-title">{t('settings.keymap.createTitle')}</div>
        <SettingRow
          label={t('settings.keymap.createName')}
          control={
            <div className="km-custom-row">
              <Input
                className="w-40"
                value={newName}
                placeholder={t('settings.keymap.createNamePlaceholder')}
                aria-label={t('settings.keymap.createName')}
                onChange={(e) => setNewName(e.target.value)}
                onKeyDown={(e) => { if (e.key === 'Enter') handleCreate() }}
              />
              <SelectRoot
                value={newSource}
                onValueChange={(v) => setNewSource(v as string)}
                items={sourceItems}
              >
                <SelectTrigger className="w-40" aria-label={t('settings.keymap.createSource')}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {sourceItems.map((o) => (
                    <SelectItem key={o.value} value={o.value}>
                      <SelectItemText>{o.label}</SelectItemText>
                    </SelectItem>
                  ))}
                </SelectContent>
              </SelectRoot>
              <Button
                variant="outline"
                size="sm"
                disabled={!newName.trim()}
                onClick={handleCreate}
                data-guide-id="settings/keymap/create"
              >
                {t('settings.keymap.createAction')}
              </Button>
            </div>
          }
        />
      </div>
    </FeatureCard>
  )
}
