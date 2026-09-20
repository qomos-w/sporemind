import { useEffect, useState } from 'react'
import { Database, HardDrive, FolderOpen } from 'lucide-react'
import { useI18n } from '../../../i18n'
import {
  getStorageSettings,
  loadStorageSettings,
  saveStorageSettings,
  storageSettingsEditable,
  type StorageSettingsForm,
} from '../../../application/storage-config'
import { ThemeModeButton } from './settings-data'
import { FeatureCard, SettingRow } from '../../settings/shadcn/composites'
import { Button, Input } from '../../settings/shadcn/ui'

type BackendChoice = '' | 'goleveldb' | 'fs'

function BackendPicker({
  value,
  onChange,
  labels,
}: {
  value: string
  onChange: (v: BackendChoice) => void
  labels: { def: string; ldb: string; fs: string }
}) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      <ThemeModeButton
        label={labels.def}
        icon={<Database size={14} />}
        active={value === ''}
        onClick={() => onChange('')}
      />
      <ThemeModeButton
        label={labels.ldb}
        icon={<HardDrive size={14} />}
        active={value === 'goleveldb'}
        onClick={() => onChange('goleveldb')}
      />
      <ThemeModeButton
        label={labels.fs}
        icon={<FolderOpen size={14} />}
        active={value === 'fs'}
        onClick={() => onChange('fs')}
      />
    </div>
  )
}

function RetentionInput({
  value,
  onChange,
  ariaLabel,
}: {
  value: number
  onChange: (v: number) => void
  ariaLabel: string
}) {
  return (
    <Input
      type="number"
      min={0}
      value={Number.isFinite(value) ? String(value) : ''}
      onChange={(e) => onChange(e.target.value === '' ? 0 : Number(e.target.value))}
      aria-label={ariaLabel}
      className="w-24"
    />
  )
}

/** Storage & index settings section: scoped storage backends (aistats /
 * logs) and retention windows, backed by sporemind.yaml via the desktop
 * config bindings. Backend and retention changes apply after restart. */
export function StorageSettingsSection() {
  const { t } = useI18n()
  const editable = storageSettingsEditable()
  const [settings, setSettings] = useState<StorageSettingsForm | null>(getStorageSettings)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    loadStorageSettings()
      .then((s) => {
        if (!cancelled) setSettings(s)
      })
      .catch((err) => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err))
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => { cancelled = true }
  }, [])

  const patch = (p: Partial<StorageSettingsForm>) => {
    setSettings((prev) => (prev ? { ...prev, ...p } : prev))
    setSaved(false)
  }

  const handleSave = async () => {
    if (!settings) return
    setSaving(true)
    setError(null)
    try {
      await saveStorageSettings(settings)
      setSaved(true)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  const backendLabels = {
    def: t('settings.index.storage.optionDefault'),
    ldb: t('settings.index.storage.optionLdb'),
    fs: t('settings.index.storage.optionFs'),
  }

  return (
    <FeatureCard
      icon={<Database size={16} />}
      title={t('settings.index.title')}
      description={t('settings.index.desc')}
    >
      {loading && <p>Loading...</p>}

      {!loading && settings && (
        <>
          <SettingRow
            label={t('settings.index.storage.aistatsBackend')}
            description={t('settings.index.storage.aistatsBackendDesc')}
            control={
              <BackendPicker
                value={settings.backendAistats}
                onChange={(v) => patch({ backendAistats: v })}
                labels={backendLabels}
              />
            }
          />
          <SettingRow
            label={t('settings.index.storage.logsBackend')}
            description={t('settings.index.storage.logsBackendDesc')}
            control={
              <BackendPicker
                value={settings.backendLogs}
                onChange={(v) => patch({ backendLogs: v })}
                labels={backendLabels}
              />
            }
          />
          <SettingRow
            label={t('settings.index.storage.logsRetention')}
            description={t('settings.index.storage.logsRetentionDesc')}
            control={
              <RetentionInput
                value={settings.logsRetentionDays}
                onChange={(v) => patch({ logsRetentionDays: v })}
                ariaLabel={t('settings.index.storage.logsRetention')}
              />
            }
          />
          <SettingRow
            label={t('settings.index.storage.rawRetention')}
            description={t('settings.index.storage.rawRetentionDesc')}
            control={
              <RetentionInput
                value={settings.aistatsRawDays}
                onChange={(v) => patch({ aistatsRawDays: v })}
                ariaLabel={t('settings.index.storage.rawRetention')}
              />
            }
          />
          <SettingRow
            label={t('settings.index.storage.dailyRetention')}
            description={t('settings.index.storage.dailyRetentionDesc')}
            control={
              <RetentionInput
                value={settings.aistatsDailyDays}
                onChange={(v) => patch({ aistatsDailyDays: v })}
                ariaLabel={t('settings.index.storage.dailyRetention')}
              />
            }
          />

          {editable ? (
            <>
              {error && (
                <p
                  className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive"
                  role="alert"
                >
                  {error}
                </p>
              )}
              <SettingRow
                label={t('settings.index.storage.save')}
                description={t('settings.index.storage.saveDesc')}
                control={
                  <Button type="button" size="sm" disabled={saving} onClick={handleSave}>
                    {saving ? '...' : t('settings.index.storage.save')}
                  </Button>
                }
              />
              {(saved || error) && (
                <p className="text-xs text-muted-foreground">
                  {t('settings.index.storage.restartNotice')}
                </p>
              )}
            </>
          ) : (
            <p className="text-xs text-muted-foreground">
              {t('settings.index.storage.wailsOnly')}
            </p>
          )}
        </>
      )}
    </FeatureCard>
  )
}
