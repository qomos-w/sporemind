import { useEffect, useState } from 'react'
import { Code } from 'lucide-react'
import { useI18n } from '../../../i18n'
import { isWails } from '../../../application/runtime'
import { isAdmin } from '../../../application/auth-store'
import { buildType, buildFlavor, buildVersion } from '../../../config/buildConfig'
import { featureFlags } from '../../../config/featureFlags'
import {
  getDesktopConfig,
  loadDesktopConfig,
  saveDesktopConfig,
  type DesktopRuntimeConfig,
} from '../../../application/desktop-config'
import { ThemeModeButton } from './settings-data'
import { FeatureCard, SettingRow } from '../../settings/shadcn/composites'
import { Button, Input, Switch } from '../../settings/shadcn/ui'
import { GATEWAY_DEFAULT_PORT } from '../../../application/gateway'

export function ShellDeveloperSettings({
  developerMode,
  onDeveloperModeChange,
  providerUserAgentVisible,
  onProviderUserAgentVisibleChange,
}: {
  developerMode?: boolean
  onDeveloperModeChange?: (enabled: boolean) => void
  providerUserAgentVisible?: boolean
  onProviderUserAgentVisibleChange?: (visible: boolean) => void
}) {
  const { t } = useI18n()
  // Developer mode is the master switch for every other option in this panel
  // (transport/gateway tools and experimental toggles).
  const showDeveloperOptions = !!developerMode
  const [config, setConfig] = useState<DesktopRuntimeConfig | null>(getDesktopConfig)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!showDeveloperOptions) {
      setLoading(false)
      return
    }
    let cancelled = false
    setLoading(true)
    loadDesktopConfig()
      .then((cfg) => {
        if (cancelled) return
        setConfig(cfg)
      })
      .catch((err) => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err))
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => { cancelled = true }
  }, [showDeveloperOptions])

  const handleTransportChange = (transport: 'wails' | 'ws') => {
    setConfig((prev) => (prev ? { ...prev, transport } : null))
    setSaved(false)
  }

  const handlePortChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const port = e.target.value
    setConfig((prev) => {
      if (!prev) return null
      const newAddr = `127.0.0.1:${port}`
      // Also update the port in any existing bind addrs (dual binding).
      const newBindAddrs = prev.gatewayBindAddrs.map((a) => {
        const i = a.lastIndexOf(':')
        const h = i >= 0 ? a.slice(0, i) : a
        return `${h}:${port}`
      })
      return { ...prev, gatewayAddr: newAddr, gatewayBindAddrs: newBindAddrs }
    })
    setSaved(false)
  }

  const handleSave = async () => {
    if (!config) return
    setSaving(true)
    setError(null)
    try {
      await saveDesktopConfig(config)
      setSaved(true)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  const devModeRow = (
    <SettingRow
      label={t('settings.developer.devMode')}
      description={t('settings.developer.devModeDesc')}
      control={
        <Switch
          checked={!!developerMode}
          onCheckedChange={(v) => onDeveloperModeChange?.(v)}
          disabled={!onDeveloperModeChange}
          data-guide-id="settings/developer/dev-mode"
          aria-label={t('settings.developer.devMode')}
        />
      }
    />
  )

  // Experimental options live under developer settings; each stays hidden
  // until developer mode is on.
  const userAgentRow = (
    <SettingRow
      label={t('settings.developer.userAgent')}
      description={t('settings.developer.userAgentDesc')}
      control={
        <Switch
          checked={!!providerUserAgentVisible}
          onCheckedChange={(v) => onProviderUserAgentVisibleChange?.(v)}
          disabled={!onProviderUserAgentVisibleChange}
          aria-label={t('settings.developer.userAgent')}
        />
      }
    />
  )

  if (!isWails()) {
    return (
      <FeatureCard icon={<Code size={16} />} title={t('settings.developer.title')} description={t('settings.developer.desc')}>
        {devModeRow}
        {showDeveloperOptions && userAgentRow}
        <p>{t('settings.developer.wailsOnly')}</p>
      </FeatureCard>
    )
  }

  // Build badge — visible in all modes to show the feature flag system is working.
  const buildBadgeLabel =
    buildType === 'release' ? 'Release' : buildType === 'beta' ? 'Beta' : `Dev (${buildFlavor})`
  const buildBadgeColor =
    buildType === 'release' ? 'bg-green-700 text-green-100' : buildType === 'beta' ? 'bg-purple-700 text-purple-100' : 'bg-amber-700 text-amber-100'

  if (showDeveloperOptions && loading) {
    return (
      <FeatureCard icon={<Code size={16} />} title={t('settings.developer.title')} description={t('settings.developer.desc')}>
        <p>Loading...</p>
      </FeatureCard>
    )
  }

  if (showDeveloperOptions && error && !config) {
    return (
      <FeatureCard icon={<Code size={16} />} title={t('settings.developer.title')} description={t('settings.developer.desc')}>
        <p>{error}</p>
      </FeatureCard>
    )
  }

  return (
    <FeatureCard icon={<Code size={16} />} title={t('settings.developer.title')} description={t('settings.developer.desc')}>
      {/* Build info badge — demonstrates feature flag gating */}
      <div className="mb-4 flex items-center gap-3 rounded-lg border px-3 py-2 text-xs">
        <span className={`rounded px-2 py-0.5 font-mono font-semibold ${buildBadgeColor}`}>
          {buildBadgeLabel}
        </span>
        <span className="text-muted-foreground">
          flavor={buildFlavor} · flags: labMode={String(featureFlags.labMode)}, autoUpdate={String(featureFlags.autoUpdate)}, crashReport={String(featureFlags.crashReport)}, internalTag={String(featureFlags.internalTag)}, multiInstance={String(featureFlags.multiInstance)} · v{buildVersion}
        </span>
      </div>
      {devModeRow}

      {showDeveloperOptions && userAgentRow}

      {showDeveloperOptions && (
        <>
          <SettingRow
            label={t('settings.developer.transport')}
            description={t('settings.developer.transportDesc')}
            control={
              <div className="flex items-center gap-2">
                <ThemeModeButton
                  label={t('settings.developer.wailsTransport')}
                  icon={<Code size={14} />}
                  active={config?.transport === 'wails'}
                  onClick={() => handleTransportChange('wails')}
                  guideId="settings/developer/transport-wails"
                />
                <ThemeModeButton
                  label={t('settings.developer.wsTransport')}
                  icon={<Code size={14} />}
                  active={config?.transport === 'ws'}
                  onClick={() => handleTransportChange('ws')}
                  guideId="settings/developer/transport-ws"
                />
              </div>
            }
          />

          {isAdmin() && (
            <SettingRow
              label={t('settings.developer.gatewayPort')}
              description={t('settings.developer.gatewayPortDesc')}
              control={
                <div className="flex items-center gap-2">
                  <span className="text-sm text-muted-foreground whitespace-nowrap">127.0.0.1 :</span>
                  <Input
                    type="number"
                    min={1}
                    max={65535}
                    value={config?.gatewayAddr?.split(':').pop() ?? String(GATEWAY_DEFAULT_PORT)}
                    onChange={handlePortChange}
                    placeholder={String(GATEWAY_DEFAULT_PORT)}
                    aria-label={t('settings.developer.gatewayPort')}
                    className="w-24"
                    data-guide-id="settings/developer/gateway"
                  />
                </div>
              }
            />
          )}

          {error && <p className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">{error}</p>}

          <SettingRow
            label={t('settings.developer.save')}
            description={t('settings.developer.saveDesc')}
            control={
              <Button
                type="button"
                size="sm"
                disabled={saving || !config}
                onClick={handleSave}
                data-guide-id="settings/developer/save"
              >
                {saving ? '...' : t('settings.developer.save')}
              </Button>
            }
          />

          {saved && (
            <p className="text-xs text-muted-foreground">{t('settings.developer.restartNotice')}</p>
          )}
        </>
      )}

    </FeatureCard>
  )
}
