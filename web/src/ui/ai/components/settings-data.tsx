import React, { useCallback, useEffect, useRef, useState } from 'react'
import {
  Settings,
  Shield,
  Server,
  Puzzle,
  Terminal,
  Database,
  Moon,
  Sun,
  Mic,
  Network,
  UserCog,
  Bot,
  Wrench,
  FileCode,
  Code,
  Plus,
  X,
  Image,
  ImageOff,
  Search,
  Sparkles,
  Layers,
  SlidersHorizontal,
  GitBranch,
  MessageCircle,
  Info,
  Keyboard,
  Palette,
  Languages,
  LayoutGrid,
  RotateCcw,
} from 'lucide-react'
import type { ThemeState } from '@qomos/sporemind-theme'
import { FONT_SIZE_MAX, FONT_SIZE_MIN, HUE_MAX, HUE_MIN } from '@qomos/sporemind-theme'
import type { I18nKey } from '../../../i18n'
import { useI18n, type Locale, supportedLocales, localeLabels } from '../../../i18n'
import { persistLocale } from '../../../application/locale-persist'
import {
  addBackgroundImage,
  applyAppBackground,
  imageFileToDataUrl,
  loadAppBackground,
  persistAppBackground,
  removeBackgroundImage,
  selectBackgroundImage,
  type AppBackground,
} from '../../../application/app-background'
import { client } from '../../../application/generated-client'
import * as workspaceClient from '../../../gen-clients/workspace/client'
import { DEFAULT_UI_EXPAND_DURATION_MS, UI_EXPANSION_KINDS, type UiExpandAction, type TurnTailMetricsVisible, type ComposerExtrasVisible } from '../../../application/workspace-ui-state'
import { resolveUiExpansionAction } from '../context/SmoothStreamContext'
import { ShellProviderSettings } from './ShellProviderSettings'
import { VoiceSettingsPanel } from './VoiceSettingsPanel'
import { MediaSettingsPanel } from './MediaSettingsPanel'
import { ShellFrpSettings } from './ShellFrpSettings'
import { ShellUserSettings } from './ShellUserSettings'
import { ShellCloudAccountSettings } from './ShellCloudAccountSettings'
import { ShellSkillSettings } from './ShellSkillSettings'
import { PluginSettingsSection } from './PluginSettingsSection'
import type { AppEntry } from '../../../application/app-registry'
import { ShellPromptSettings } from './ShellPromptSettings'
import { ShellMcpSettings } from './ShellMcpSettings'
import { AgentKindConfigView } from '../../views/AgentKindConfigView'
import { NewAgentKindDialog } from './NewAgentKindDialog'
import { DeleteConfirmModal } from './parts/DeleteConfirmModal'
import { useBrowserOverlay } from '../browserOverlay'
import { ShellDeveloperSettings } from './ShellDeveloperSettings'
import { StorageSettingsSection } from './StorageSettings'
import { ShellLspSettings } from './ShellLspSettings'
import { ShellEnvSettings } from './ShellEnvSettings'
import { WebSearchSettingsPanel } from './WebSearchSettingsPanel'
import { PolicySettingsPanel } from './PolicySettingsPanel'
import { ImSettingsPanel } from './ImSettingsPanel'
import { ShellGitSettings } from './ShellGitSettings'
import { ShellNoGitModeSettings } from './ShellNoGitModeSettings'
import { ShellAboutSettings } from './ShellAboutSettings'

import { DbManagerSettings } from './DbManagerSettings'
import type { DbProfileView } from '../../../gen-types/dbmanager'
import { FeatureCard, SettingRow } from '../../settings/shadcn/composites'
import { KeymapSettingsSection } from './KeymapSettingsSection'
import { Switch, Button, Input, Slider, SelectRoot, SelectTrigger, SelectValue, SelectContent, SelectItem, SelectItemText } from '../../settings/shadcn/ui'

export interface SettingsCategory {
  id: string
  labelKey: I18nKey
  icon: React.ReactNode
}

function GeneralSettingsSection({
  theme,
  onThemeChange,
  configBadgesVisible,
  onConfigBadgesVisibleChange,
  turnTailMetrics,
  onTurnTailMetricsChange,
  composerExtras,
  onComposerExtrasChange,
}: {
  theme: ThemeState
  onThemeChange: (theme: ThemeState) => void
  configBadgesVisible?: boolean
  onConfigBadgesVisibleChange?: (visible: boolean) => void
  turnTailMetrics?: TurnTailMetricsVisible
  onTurnTailMetricsChange?: (metrics: TurnTailMetricsVisible) => void
  composerExtras?: ComposerExtrasVisible
  onComposerExtrasChange?: (extras: ComposerExtrasVisible) => void
}) {
  const { t, locale, setLocale } = useI18n()
  const [background, setBackground] = useState<AppBackground | null>(null)
  const bgFileInputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    let cancelled = false
    loadAppBackground()
      .then(bg => { if (!cancelled) setBackground(bg) })
      .catch(() => {})
    return () => { cancelled = true }
  }, [])

  const updateBackground = (next: AppBackground | null) => {
    setBackground(next)
    applyAppBackground(next)
    void persistAppBackground(next).catch(e => console.warn('[settings] persist background failed', e))
  }

  const handleBackgroundFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    e.target.value = ''
    if (!file) return
    try {
      const image = await imageFileToDataUrl(file)
      updateBackground(addBackgroundImage(background, image))
    } catch (err) {
      console.warn('[settings] read background image failed', err)
    }
  }

  const handleLocaleChange = (value: string) => {
    const next = value as Locale
    if (supportedLocales.includes(next)) {
      setLocale(next)
      void persistLocale(next)
    }
  }

  return (
    <>
    <FeatureCard icon={<Languages size={16} />} title={t('settings.general.languageCard')} description={t('settings.general.languageCardDesc')}>
      <SettingRow
        label={t('settings.general.language')}
        description={t('settings.general.languageDesc')}
        control={
          <SelectRoot
            value={locale}
            onValueChange={(v) => handleLocaleChange(v as string)}
            items={supportedLocales.map((loc) => ({ value: loc, label: localeLabels[loc] }))}
          >
            <SelectTrigger className="w-40" data-guide-id="settings/general/language">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {supportedLocales.map((loc) => (
                <SelectItem key={loc} value={loc} data-guide-id={`settings/general/language/${loc}`}>
                  <SelectItemText>{localeLabels[loc]}</SelectItemText>
                </SelectItem>
              ))}
            </SelectContent>
          </SelectRoot>
        }
      />
    </FeatureCard>
    <FeatureCard icon={<Palette size={16} />} title={t('settings.general.appearance')} description={t('settings.general.appearanceDesc')}>
      <SettingRow
        label={t('settings.general.interfaceMode')}
        description={t('settings.general.interfaceModeDesc')}
        control={
          <div className="flex items-center gap-2">
            <ThemeModeButton
              label={t('settings.general.light')}
              icon={<Sun size={14} />}
              active={theme.mode === 'light'}
              onClick={() => onThemeChange({ ...theme, mode: 'light' })}
              guideId="settings/general/theme-light"
            />
            <ThemeModeButton
              label={t('settings.general.dark')}
              icon={<Moon size={14} />}
              active={theme.mode === 'dark'}
              onClick={() => onThemeChange({ ...theme, mode: 'dark' })}
              guideId="settings/general/theme-dark"
            />
          </div>
        }
      />
      <SettingRow
        label={t('settings.general.fontSize')}
        description={t('settings.general.fontSizeDesc')}
      >
        <div className="flex items-center gap-3">
          <Slider
            min={FONT_SIZE_MIN}
            max={FONT_SIZE_MAX}
            step={1}
            value={theme.fontSize}
            onValueChange={(v) => onThemeChange({ ...theme, fontSize: Array.isArray(v) ? v[0] : v })}
            aria-label={t('settings.general.fontSize')}
            data-guide-id="settings/general/font-size"
            className="flex-1"
          />
          <span className="w-[42px] text-right text-sm text-muted-foreground">{theme.fontSize}px</span>
        </div>
      </SettingRow>
      <SettingRow
        label={t('settings.general.hue')}
        description={t('settings.general.hueDesc')}
      >
        <div className="flex items-center gap-3">
          <Slider
            min={HUE_MIN}
            max={HUE_MAX}
            step={1}
            value={theme.hue ?? HUE_MIN}
            disabled={theme.mode === 'dark'}
            onValueChange={(v) => onThemeChange({ ...theme, hue: Math.round(Array.isArray(v) ? v[0] : v) })}
            aria-label={t('settings.general.hue')}
            data-guide-id="settings/general/hue"
            className="flex-1"
          />
          <span className="w-[42px] text-right text-sm text-muted-foreground">
            {theme.hue != null ? `${theme.hue}°` : '—'}
          </span>
          {theme.hue != null && (
            <Button
              type="button"
              size="sm"
              variant="ghost"
              onClick={() => onThemeChange({ ...theme, hue: undefined })}
              aria-label={t('settings.general.hueReset')}
              data-guide-id="settings/general/hue-reset"
            >
              <RotateCcw size={13} />
            </Button>
          )}
        </div>
      </SettingRow>
      <SettingRow
        label={t('settings.general.background')}
        description={t('settings.general.backgroundDesc')}
      >
        <div className="sp-settings-bg-list">
            <div className={`sp-settings-bg-item ${!background || background.active === -1 ? 'active' : ''}`}>
              <button
                type="button"
                className="sp-settings-bg-thumb sp-settings-bg-default"
                onClick={() => { if (background) updateBackground(selectBackgroundImage(background, -1)) }}
                data-guide-id="settings/general/background-default"
              >
                <ImageOff size={14} />
                <span>{t('settings.general.backgroundDefault')}</span>
              </button>
            </div>
            {background?.images.map((img, i) => (
              <div key={i} className={`sp-settings-bg-item ${i === background.active ? 'active' : ''}`}>
                <button
                  type="button"
                  className="sp-settings-bg-thumb"
                  onClick={() => updateBackground(selectBackgroundImage(background, i))}
                  data-guide-id={`settings/general/background-image-${i}`}
                >
                  <img src={img} alt="" />
                </button>
                <button
                  type="button"
                  className="sp-settings-bg-delete"
                  aria-label={t('settings.general.backgroundClear')}
                  onClick={() => updateBackground(removeBackgroundImage(background, i))}
                  data-guide-id={`settings/general/background-delete-${i}`}
                >
                  <X size={10} />
                </button>
              </div>
            ))}
            <button
              type="button"
              className="sp-settings-bg-add"
              onClick={() => bgFileInputRef.current?.click()}
              data-guide-id="settings/general/background-add"
            >
              <Plus size={16} />
              <span>{t('settings.general.backgroundAdd')}</span>
            </button>
        </div>
      </SettingRow>
      <input
        ref={bgFileInputRef}
        type="file"
        accept="image/*"
        hidden
        onChange={(e) => { void handleBackgroundFile(e) }}
      />
    </FeatureCard>
    <FeatureCard icon={<LayoutGrid size={16} />} title={t('settings.general.interfaceCard')} description={t('settings.general.interfaceCardDesc')}>
      <SettingRow
        label={t('settings.general.configBadges')}
        description={t('settings.general.configBadgesDesc')}
        control={
          <Switch
            checked={!!configBadgesVisible}
            onCheckedChange={(v) => onConfigBadgesVisibleChange?.(v)}
            disabled={!onConfigBadgesVisibleChange}
            data-guide-id="settings/general/config-badges"
            aria-label={t('settings.general.configBadges')}
          />
        }
      />
      <SettingRow
        label={t('settings.general.turnBudgetBar')}
        description={t('settings.general.turnBudgetBarDesc')}
        control={
          <Switch
            checked={!!turnTailMetrics?.budgetBar}
            onCheckedChange={(v) => onTurnTailMetricsChange?.({ ...(turnTailMetrics ?? { budgetBar: false, tokenBadge: false, duration: false, throughput: false }), budgetBar: v })}
            disabled={!onTurnTailMetricsChange}
            data-guide-id="settings/general/turn-budget-bar"
            aria-label={t('settings.general.turnBudgetBar')}
          />
        }
      />
      <SettingRow
        label={t('settings.general.turnTokenBadge')}
        description={t('settings.general.turnTokenBadgeDesc')}
        control={
          <Switch
            checked={!!turnTailMetrics?.tokenBadge}
            onCheckedChange={(v) => onTurnTailMetricsChange?.({ ...(turnTailMetrics ?? { budgetBar: false, tokenBadge: false, duration: false, throughput: false }), tokenBadge: v })}
            disabled={!onTurnTailMetricsChange}
            data-guide-id="settings/general/turn-token-badge"
            aria-label={t('settings.general.turnTokenBadge')}
          />
        }
      />
      <SettingRow
        label={t('settings.general.turnDuration')}
        description={t('settings.general.turnDurationDesc')}
        control={
          <Switch
            checked={!!turnTailMetrics?.duration}
            onCheckedChange={(v) => onTurnTailMetricsChange?.({ ...(turnTailMetrics ?? { budgetBar: false, tokenBadge: false, duration: false, throughput: false }), duration: v })}
            disabled={!onTurnTailMetricsChange}
            data-guide-id="settings/general/turn-duration"
            aria-label={t('settings.general.turnDuration')}
          />
        }
      />
      <SettingRow
        label={t('settings.general.turnThroughput')}
        description={t('settings.general.turnThroughputDesc')}
        control={
          <Switch
            checked={!!turnTailMetrics?.throughput}
            onCheckedChange={(v) => onTurnTailMetricsChange?.({ ...(turnTailMetrics ?? { budgetBar: false, tokenBadge: false, duration: false, throughput: false }), throughput: v })}
            disabled={!onTurnTailMetricsChange}
            data-guide-id="settings/general/turn-throughput"
            aria-label={t('settings.general.turnThroughput')}
          />
        }
      />
      <SettingRow
        label={t('settings.general.quickGit')}
        description={t('settings.general.quickGitDesc')}
        control={
          <Switch
            checked={composerExtras?.quickGit !== false}
            onCheckedChange={(v) => onComposerExtrasChange?.({ ...(composerExtras ?? { quickGit: true, mobileContextAgent: true }), quickGit: v })}
            disabled={!onComposerExtrasChange}
            data-guide-id="settings/general/quick-git"
            aria-label={t('settings.general.quickGit')}
          />
        }
      />
      <SettingRow
        label={t('settings.general.mobileContextAgent')}
        description={t('settings.general.mobileContextAgentDesc')}
        control={
          <Switch
            checked={composerExtras?.mobileContextAgent !== false}
            onCheckedChange={(v) => onComposerExtrasChange?.({ ...(composerExtras ?? { quickGit: true, mobileContextAgent: true }), mobileContextAgent: v })}
            disabled={!onComposerExtrasChange}
            data-guide-id="settings/general/mobile-context-agent"
            aria-label={t('settings.general.mobileContextAgent')}
          />
        }
      />
    </FeatureCard>
    </>
  )
}

function AnimationSettingsSection({
  smoothStream,
  onSmoothStreamChange,
  expansionModes,
  onExpansionModeChange,
  expandDurationMs,
  onExpandDurationChange,
}: {
  smoothStream?: boolean
  onSmoothStreamChange?: (enabled: boolean) => void
  expansionModes?: Record<string, UiExpandAction>
  onExpansionModeChange?: (kind: string, action: UiExpandAction) => void
  expandDurationMs?: number
  onExpandDurationChange?: (ms: number) => void
}) {
  const { t } = useI18n()
  return (
    <FeatureCard icon={<Sparkles size={16} />} title={t('settings.animation.title')} description={t('settings.animation.desc')}>
      <SettingRow
        label={t('settings.general.smoothStream')}
        description={t('settings.general.smoothStreamDesc')}
        control={
          <Switch
            checked={!!smoothStream}
            onCheckedChange={(v) => onSmoothStreamChange?.(v)}
            disabled={!onSmoothStreamChange}
            data-guide-id="settings/animation/smooth-stream"
            aria-label={t('settings.general.smoothStream')}
          />
        }
      />
      <SettingRow
        label={t('settings.general.expandDuration')}
        description={t('settings.general.expandBehaviorDesc')}
        control={
          <div className="flex items-center gap-1.5">
            <Input
              type="number"
              min={0}
              max={10000}
              step={50}
              value={expandDurationMs ?? DEFAULT_UI_EXPAND_DURATION_MS}
              disabled={!onExpandDurationChange}
              onChange={e => onExpandDurationChange?.(Number(e.target.value))}
              aria-label={t('settings.general.expandDuration')}
              className="w-24"
              data-guide-id="settings/animation/expand-duration"
            />
            <span className="text-sm text-muted-foreground">ms</span>
          </div>
        }
      />
      {UI_EXPANSION_KINDS.map(({ kind, labelKey }) => (
        <SettingRow
          key={kind}
          label={t(labelKey as I18nKey)}
          control={
            <SelectRoot
              value={resolveUiExpansionAction(expansionModes ?? {}, kind)}
              onValueChange={(v) => onExpansionModeChange?.(kind, v as UiExpandAction)}
              disabled={!onExpansionModeChange}
              items={[
                { value: 'none', label: t('settings.general.expandModeNone') },
                { value: 'fold-expand-collapse', label: t('settings.general.expandModeFoldExpandCollapse') },
                { value: 'fold-expand', label: t('settings.general.expandModeFoldExpand') },
                { value: 'always-open', label: t('settings.general.expandModeAlwaysOpen') },
              ]}
            >
              <SelectTrigger
                className="w-48"
                aria-label={t(labelKey as I18nKey)}
                data-guide-id={`settings/animation/expand-${kind}`}
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="none" data-guide-id={`settings/animation/expand-${kind}/none`}>
                  <SelectItemText>{t('settings.general.expandModeNone')}</SelectItemText>
                </SelectItem>
                <SelectItem value="fold-expand-collapse" data-guide-id={`settings/animation/expand-${kind}/fold-expand-collapse`}>
                  <SelectItemText>{t('settings.general.expandModeFoldExpandCollapse')}</SelectItemText>
                </SelectItem>
                <SelectItem value="fold-expand" data-guide-id={`settings/animation/expand-${kind}/fold-expand`}>
                  <SelectItemText>{t('settings.general.expandModeFoldExpand')}</SelectItemText>
                </SelectItem>
                <SelectItem value="always-open" data-guide-id={`settings/animation/expand-${kind}/always-open`}>
                  <SelectItemText>{t('settings.general.expandModeAlwaysOpen')}</SelectItemText>
                </SelectItem>
              </SelectContent>
            </SelectRoot>
          }
        />
      ))}
    </FeatureCard>
  )
}

export const categories: SettingsCategory[] = [
  { id: 'general', labelKey: 'settings.general.title', icon: <Settings size={16} /> },
  { id: 'animation', labelKey: 'settings.animation.title', icon: <Sparkles size={16} /> },
  { id: 'account', labelKey: 'settings.account.title', icon: <UserCog size={16} /> },
  { id: 'model-providers', labelKey: 'settings.provider.providers', icon: <Server size={16} /> },
  { id: 'model-aggregators', labelKey: 'settings.provider.aggregators', icon: <Layers size={16} /> },
  { id: 'model-defaults', labelKey: 'settings.provider.modelDefaults', icon: <SlidersHorizontal size={16} /> },
  { id: 'agent', labelKey: 'settings.agent.title', icon: <Bot size={16} /> },
  { id: 'prompts', labelKey: 'settings.prompts.title', icon: <FileCode size={16} /> },
  { id: 'skills', labelKey: 'settings.skills.title', icon: <Wrench size={16} /> },
  { id: 'policy', labelKey: 'settings.policy.title', icon: <Shield size={16} /> },
  { id: 'voice', labelKey: 'settings.voice.title', icon: <Mic size={16} /> },
  { id: 'media', labelKey: 'settings.media.title', icon: <Image size={16} /> },
  { id: 'websearch', labelKey: 'settings.websearch.title', icon: <Search size={16} /> },
  { id: 'im', labelKey: 'settings.im.title', icon: <MessageCircle size={16} /> },
  { id: 'git', labelKey: 'settings.git.title', icon: <GitBranch size={16} /> },
  { id: 'database-manager', labelKey: 'settings.dbmanager.title', icon: <Database size={16} /> },
  { id: 'frp', labelKey: 'settings.frp.title', icon: <Network size={16} /> },
  { id: 'mcp', labelKey: 'settings.mcp.title', icon: <Server size={16} /> },
  { id: 'plugins', labelKey: 'settings.plugins.title', icon: <Puzzle size={16} /> },
  { id: 'commands', labelKey: 'settings.commands.title', icon: <Terminal size={16} /> },
  { id: 'keymap', labelKey: 'settings.keymap.title', icon: <Keyboard size={16} /> },
  { id: 'index', labelKey: 'settings.index.title', icon: <Database size={16} /> },
  { id: 'environment', labelKey: 'settings.environment.title', icon: <Terminal size={16} /> },
  { id: 'developer', labelKey: 'settings.developer.title', icon: <Code size={16} /> },
  { id: 'lsp', labelKey: 'settings.lsp.title', icon: <Database size={16} /> },
  { id: 'about', labelKey: 'settings.about.title', icon: <Info size={16} /> },
]

export interface SettingsGroup {
  labelKey: I18nKey
  sections: SettingsCategory[]
}

export const settingsGroups: SettingsGroup[] = [
  {
    labelKey: 'settingsSidebar.group.general' as I18nKey,
    sections: [
      { id: 'general', labelKey: 'settings.general.title' as I18nKey, icon: <Settings size={16} /> },
      { id: 'animation', labelKey: 'settings.animation.title' as I18nKey, icon: <Sparkles size={16} /> },
    ],
  },
  {
    labelKey: 'settingsSidebar.group.account' as I18nKey,
    sections: [
      { id: 'account', labelKey: 'settings.account.title' as I18nKey, icon: <UserCog size={16} /> },
      { id: 'model-providers', labelKey: 'settings.provider.providers' as I18nKey, icon: <Server size={16} /> },
      { id: 'model-aggregators', labelKey: 'settings.provider.aggregators' as I18nKey, icon: <Layers size={16} /> },
    ],
  },
  {
    labelKey: 'settingsSidebar.group.ai' as I18nKey,
    sections: [
      { id: 'agent', labelKey: 'settings.agent.title' as I18nKey, icon: <Bot size={16} /> },
      { id: 'prompts', labelKey: 'settings.prompts.title' as I18nKey, icon: <FileCode size={16} /> },
      { id: 'skills', labelKey: 'settings.skills.title' as I18nKey, icon: <Wrench size={16} /> },
      { id: 'policy', labelKey: 'settings.policy.title' as I18nKey, icon: <Shield size={16} /> },
    ],
  },
  {
    labelKey: 'settingsSidebar.group.media' as I18nKey,
    sections: [
      { id: 'voice', labelKey: 'settings.voice.title' as I18nKey, icon: <Mic size={16} /> },
      { id: 'media', labelKey: 'settings.media.title' as I18nKey, icon: <Image size={16} /> },
      { id: 'websearch', labelKey: 'settings.websearch.title' as I18nKey, icon: <Search size={16} /> },
      { id: 'im', labelKey: 'settings.im.title' as I18nKey, icon: <MessageCircle size={16} /> },
      { id: 'git', labelKey: 'settings.git.title' as I18nKey, icon: <GitBranch size={16} /> },
    ],
  },
  {
    labelKey: 'settingsSidebar.group.tools' as I18nKey,
    sections: [
      { id: 'database-manager', labelKey: 'settings.dbmanager.title' as I18nKey, icon: <Database size={16} /> },
      { id: 'frp', labelKey: 'settings.frp.title' as I18nKey, icon: <Network size={16} /> },
      { id: 'mcp', labelKey: 'settings.mcp.title' as I18nKey, icon: <Server size={16} /> },
      { id: 'plugins', labelKey: 'settings.plugins.title' as I18nKey, icon: <Puzzle size={16} /> },
    ],
  },
  {
    labelKey: 'settingsSidebar.group.advanced' as I18nKey,
    sections: [
      { id: 'model-defaults', labelKey: 'settings.provider.modelDefaults' as I18nKey, icon: <SlidersHorizontal size={16} /> },
      { id: 'commands', labelKey: 'settings.commands.title' as I18nKey, icon: <Terminal size={16} /> },
      { id: 'keymap', labelKey: 'settings.keymap.title' as I18nKey, icon: <Keyboard size={16} /> },
      { id: 'index', labelKey: 'settings.index.title' as I18nKey, icon: <Database size={16} /> },
      { id: 'environment', labelKey: 'settings.environment.title' as I18nKey, icon: <Terminal size={16} /> },
      { id: 'developer', labelKey: 'settings.developer.title' as I18nKey, icon: <Code size={16} /> },
      { id: 'lsp', labelKey: 'settings.lsp.title' as I18nKey, icon: <Database size={16} /> },
    ],
  },
  {
    labelKey: 'settingsSidebar.group.about' as I18nKey,
    sections: [
      { id: 'about', labelKey: 'settings.about.title' as I18nKey, icon: <Info size={16} /> },
    ],
  },
]

export function ThemeModeButton({
  label,
  icon,
  active,
  onClick,
  guideId,
}: {
  label: string
  icon: React.ReactNode
  active: boolean
  onClick: () => void
  guideId?: string
}) {
  return (
    <Button
      type="button"
      variant={active ? 'default' : 'outline'}
      size="sm"
      onClick={onClick}
      data-guide-id={guideId}
      aria-pressed={active}
    >
      {icon}
      <span>{label}</span>
    </Button>
  )
}

export function renderSettingsContent(
  id: string,
  theme: ThemeState,
  onThemeChange: (theme: ThemeState) => void,
  configBadgesVisible?: boolean,
  onConfigBadgesVisibleChange?: (visible: boolean) => void,
  turnTailMetrics?: TurnTailMetricsVisible,
  onTurnTailMetricsChange?: (metrics: TurnTailMetricsVisible) => void,
  composerExtras?: ComposerExtrasVisible,
  onComposerExtrasChange?: (extras: ComposerExtrasVisible) => void,
  smoothStream?: boolean,
  onSmoothStreamChange?: (enabled: boolean) => void,
  developerMode?: boolean,
  onDeveloperModeChange?: (enabled: boolean) => void,
  providerUserAgentVisible?: boolean,
  onProviderUserAgentVisibleChange?: (visible: boolean) => void,
  expansionModes?: Record<string, UiExpandAction>,
  onExpansionModeChange?: (kind: string, action: UiExpandAction) => void,
  expandDurationMs?: number,
  onExpandDurationChange?: (ms: number) => void,
  onOpenDbClient?: (profile: DbProfileView) => void,
  onOpenAppView?: (app: AppEntry) => void,
): React.ReactNode {
  const { t } = useI18n()
  switch (id) {
    case 'general':
      return (
        <GeneralSettingsSection
          theme={theme}
          onThemeChange={onThemeChange}
          configBadgesVisible={configBadgesVisible}
          onConfigBadgesVisibleChange={onConfigBadgesVisibleChange}
          turnTailMetrics={turnTailMetrics}
          onTurnTailMetricsChange={onTurnTailMetricsChange}
          composerExtras={composerExtras}
          onComposerExtrasChange={onComposerExtrasChange}
        />
      )
    case 'animation':
      return (
        <AnimationSettingsSection
          smoothStream={smoothStream}
          onSmoothStreamChange={onSmoothStreamChange}
          expansionModes={expansionModes}
          onExpansionModeChange={onExpansionModeChange}
          expandDurationMs={expandDurationMs}
          onExpandDurationChange={onExpandDurationChange}
        />
      )
    case 'account':
      return (
        <>
          <ShellUserSettings />
          <ShellCloudAccountSettings />
        </>
      )
    case 'voice':
      return <VoiceSettingsPanel />
    case 'media':
      return <MediaSettingsPanel />
    case 'websearch':
      return <WebSearchSettingsPanel />
    case 'im':
      return <ImSettingsPanel />
    case 'git':
      return (
        <>
          <ShellGitSettings />
          <ShellNoGitModeSettings />
        </>
      )
    case 'model-providers':
      return <ShellProviderSettings section="providers" userAgentVisible={providerUserAgentVisible} />
    case 'model-aggregators':
      return <ShellProviderSettings section="aggregators" userAgentVisible={providerUserAgentVisible} />
    case 'model-defaults':
      return <ShellProviderSettings section="defaults" />
    case 'agent':
      return <AgentSettingsCategory />
    case 'skills':
      return <ShellSkillSettings />
    case 'policy':
      return <PolicySettingsPanel />
    case 'prompts':
      return <ShellPromptSettings />
    case 'frp':
      return <ShellFrpSettings />
    case 'mcp':
      return <ShellMcpSettings />
    case 'plugins':
      return <PluginSettingsSection developerMode={developerMode} onDeveloperModeChange={onDeveloperModeChange} onOpenAppView={onOpenAppView} />
    case 'commands':
      return (
        <FeatureCard icon={<Terminal size={16} />} title={t('settings.commands.title')} description={t('settings.commands.desc')}>
          <div>{t('settings.commands.placeholder')}</div>
        </FeatureCard>
      )
    case 'keymap':
      return <KeymapSettingsSection />
    case 'index':
      return <StorageSettingsSection />
    case 'database-manager':
      return <DbManagerSettings onOpenClient={onOpenDbClient} />
    case 'developer':
      return <ShellDeveloperSettings developerMode={developerMode} onDeveloperModeChange={onDeveloperModeChange} providerUserAgentVisible={providerUserAgentVisible} onProviderUserAgentVisibleChange={onProviderUserAgentVisibleChange} />
    case 'lsp':
      return <ShellLspSettings />
    case 'environment':
      return <ShellEnvSettings />
    case 'about':
      return <ShellAboutSettings />
    default:
      return null
  }
}

/**
 * AgentSettingsCategory renders the unified agent template (agent kind)
 * configuration panel: a template selector bar, create/delete template
 * controls, and the per-template config view. Storage policy (formerly in
 * AgentSettingsPanel) is integrated into AgentKindConfigView's Advanced tab.
 */
export function AgentSettingsCategory() {
  const { t } = useI18n()
  const [kinds, setKinds] = useState<{ kind: string; displayName: string; builtin: boolean }[]>([])
  const [selectedKind, setSelectedKind] = useState<string>('')
  const [loadingKinds, setLoadingKinds] = useState(true)
  const [kindsError, setKindsError] = useState<string | null>(null)
  const [newKindOpen, setNewKindOpen] = useState(false)
  const [deleteKind, setDeleteKind] = useState<{ kind: string; displayName: string } | null>(null)
  const [deleteError, setDeleteError] = useState<string | null>(null)

  const loadKinds = useCallback(async (selectAfter?: string) => {
    setLoadingKinds(true)
    setKindsError(null)
    try {
      const resp = await workspaceClient.listAgentKinds(client)
      const items = (resp.Items || []).map(item => ({
        kind: item.Kind,
        displayName: item.DisplayName || item.Kind,
        builtin: item.Builtin === true,
      }))
      setKinds(items)
      // Keep the current selection when it still exists; otherwise fall back
      // to the freshly created template (selectAfter) or the first template.
      setSelectedKind(prev => {
        const target = selectAfter ?? prev
        if (target && items.some(i => i.kind === target)) return target
        return items[0]?.kind ?? ''
      })
    } catch (err) {
      setKindsError(err instanceof Error ? err.message : String(err))
    } finally {
      setLoadingKinds(false)
    }
  }, [])

  useEffect(() => {
    void loadKinds()
  }, [loadKinds])

  const selected = kinds.find(k => k.kind === selectedKind)

  const handleCreate = useCallback((kind: string) => {
    setNewKindOpen(false)
    setDeleteError(null)
    void loadKinds(kind)
  }, [loadKinds])

  const confirmDeleteKind = useCallback(async () => {
    if (!deleteKind) return
    const target = deleteKind
    setDeleteKind(null)
    setDeleteError(null)
    try {
      await workspaceClient.deleteAgentKind(client, { Kind: target.kind })
      await loadKinds()
    } catch (err) {
      setDeleteError(err instanceof Error ? err.message : String(err))
    }
  }, [deleteKind, loadKinds])

  // The delete confirmation is a modal overlay: register it so native browser
  // windows are hidden while it is open (project red line).
  useBrowserOverlay(deleteKind !== null)

  return (
    <>
      <FeatureCard icon={<Bot size={16} />} title={t('settings.agent.title')} description={t('settings.agent.desc')}>
        {/* Template selector */}
        <div className="flex flex-wrap gap-2">
          {kinds.map(k => (
            <Button
              key={k.kind}
              type="button"
              size="sm"
              variant={selectedKind === k.kind ? 'default' : 'outline'}
              onClick={() => setSelectedKind(k.kind)}
              data-guide-id={`settings/agent/kind/${k.kind}`}
            >
              {k.displayName}
            </Button>
          ))}
          <Button
            type="button"
            size="sm"
            variant="outline"
            onClick={() => setNewKindOpen(true)}
            data-testid="agent-kind-new"
            data-guide-id="settings/agent/kind-new"
          >
            <Plus size={14} />
            {t('settings.agent.template.new')}
          </Button>
        </div>

        {loadingKinds && (
          <p className="text-sm text-muted-foreground">{t('agentKind.loading')}</p>
        )}
        {kindsError && (
          <p className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">{kindsError}</p>
        )}
        {deleteError && (
          <p className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive" data-testid="agent-kind-delete-error">{deleteError}</p>
        )}
        {!loadingKinds && !kindsError && selectedKind && (
          <AgentKindConfigView
            kind={selectedKind}
            builtin={selected?.builtin}
            onRequestDelete={() => setDeleteKind({ kind: selectedKind, displayName: selected?.displayName ?? selectedKind })}
          />
        )}
      </FeatureCard>

      <NewAgentKindDialog
        open={newKindOpen}
        kinds={kinds}
        onClose={() => setNewKindOpen(false)}
        onCreated={handleCreate}
      />

      <DeleteConfirmModal
        open={deleteKind !== null}
        agentName={deleteKind?.displayName ?? deleteKind?.kind ?? ''}
        title={t('settings.agent.template.deleteTitle')}
        message={t('settings.agent.template.deleteMessage', { name: deleteKind?.displayName ?? deleteKind?.kind ?? '' })}
        onClose={() => setDeleteKind(null)}
        onConfirm={() => { void confirmDeleteKind() }}
      />
    </>
  )
}
