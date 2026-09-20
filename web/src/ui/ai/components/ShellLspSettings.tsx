import { useCallback, useEffect, useState } from 'react'
import { Download, Loader2, Trash2, Database } from 'lucide-react'
import type { I18nKey } from '../../../i18n'
import { useI18n } from '../../../i18n'
import { client as gatewayClient } from '../../../application/generated-client'
import { clearCache, stateGet, stateSave } from '../../../gen-clients/lsp/client'
import type { LspInstallProgressEvent, LspLanguageInstallState } from '../../../gen-types/lsp'
import { FeatureCard, SettingRow } from '../../settings/shadcn/composites'
import { Badge, Button, Separator, Switch } from '../../settings/shadcn/ui'
import { lspInstall, lspStatus, onLspInstallProgress } from './lsp-install-client'

interface LspLanguageDef {
  key: string
  labelKey: I18nKey
  descKey: I18nKey
  builtin?: boolean
}

const LSP_LANGUAGES: LspLanguageDef[] = [
  { key: 'go', labelKey: 'settings.lsp.lang.go' as I18nKey, descKey: 'settings.lsp.lang.goDesc' as I18nKey, builtin: true },
  { key: 'typescript', labelKey: 'settings.lsp.lang.typescript' as I18nKey, descKey: 'settings.lsp.lang.typescriptDesc' as I18nKey },
  { key: 'javascript', labelKey: 'settings.lsp.lang.javascript' as I18nKey, descKey: 'settings.lsp.lang.javascriptDesc' as I18nKey },
  { key: 'python', labelKey: 'settings.lsp.lang.python' as I18nKey, descKey: 'settings.lsp.lang.pythonDesc' as I18nKey },
  { key: 'rust', labelKey: 'settings.lsp.lang.rust' as I18nKey, descKey: 'settings.lsp.lang.rustDesc' as I18nKey },
  { key: 'cpp', labelKey: 'settings.lsp.lang.cpp' as I18nKey, descKey: 'settings.lsp.lang.cppDesc' as I18nKey },
  { key: 'css', labelKey: 'settings.lsp.lang.css' as I18nKey, descKey: 'settings.lsp.lang.cssDesc' as I18nKey },
  { key: 'html', labelKey: 'settings.lsp.lang.html' as I18nKey, descKey: 'settings.lsp.lang.htmlDesc' as I18nKey },
  { key: 'json', labelKey: 'settings.lsp.lang.json' as I18nKey, descKey: 'settings.lsp.lang.jsonDesc' as I18nKey },
  { key: 'bash', labelKey: 'settings.lsp.lang.bash' as I18nKey, descKey: 'settings.lsp.lang.bashDesc' as I18nKey },
]

interface InstallProgress {
  percent: number
  state: string
  error?: string
}

export function ShellLspSettings() {
  const { t } = useI18n()
  const [states, setStates] = useState<Record<string, boolean> | null>(null)
  const [savingLang, setSavingLang] = useState<string | null>(null)
  const [clearing, setClearing] = useState(false)
  const [cleared, setCleared] = useState<number | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [installStates, setInstallStates] = useState<Record<string, LspLanguageInstallState | null> | null>(null)
  const [installProgress, setInstallProgress] = useState<Record<string, InstallProgress>>({})
  const [installingLang, setInstallingLang] = useState<string | null>(null)

  const loadInstallStates = useCallback(async () => {
    try {
      const resp = await lspStatus(gatewayClient, { Language: '' })
      const map: Record<string, LspLanguageInstallState | null> = {}
      for (const st of resp.Languages ?? []) {
        map[st.Language] = st
      }
      setInstallStates(map)
    } catch {
      // Cannot determine install state (backend not wired yet, server
      // unreachable, ...). Keep the install CTA visible so the user can
      // still trigger an install.
      setInstallStates({})
    }
  }, [])

  useEffect(() => {
    stateGet(gatewayClient)
      .then((resp) => {
        const map: Record<string, boolean> = {}
        for (const lang of resp.Languages) {
          map[lang.Language] = lang.Enabled
        }
        setStates(map)
      })
      .catch((err) => {
        setError(err instanceof Error ? err.message : String(err))
        // Default all languages to enabled on fetch failure (matches old behaviour)
        const defaults: Record<string, boolean> = {}
        for (const def of LSP_LANGUAGES) defaults[def.key] = true
        setStates(defaults)
      })
  }, [])

  useEffect(() => {
    void loadInstallStates()
  }, [loadInstallStates])

  // Live install progress: lspserver emits lsp.install_progress while a
  // managed install is running. On completion refresh the install state so
  // the badge flips to "已安装 vX".
  useEffect(() => {
    return onLspInstallProgress(gatewayClient, (ev: LspInstallProgressEvent) => {
      setInstallProgress((prev) => ({ ...prev, [ev.Language]: { percent: ev.Percent, state: ev.State, error: ev.Error } }))
      if (ev.State === 'done' || ev.State === 'failed') {
        setInstallingLang((cur) => (cur === ev.Language ? null : cur))
      }
      if (ev.State === 'done') {
        void loadInstallStates()
      }
    })
  }, [loadInstallStates])

  const handleToggle = async (lang: string, value: boolean) => {
    setStates((prev) => ({ ...prev, [lang]: value }))
    setSavingLang(lang)
    setError(null)
    try {
      await stateSave(gatewayClient, { Language: lang, Enabled: value })
    } catch (err) {
      setStates((prev) => ({ ...prev, [lang]: !value }))
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSavingLang(null)
    }
  }

  const handleInstall = async (lang: string) => {
    setInstallingLang(lang)
    setError(null)
    try {
      const resp = await lspInstall(gatewayClient, { Language: lang })
      if (!resp.Started) {
        setInstallingLang((cur) => (cur === lang ? null : cur))
        setError(t('settings.lsp.installNotStarted'))
      }
      // Started: progress arrives via lsp.install_progress events.
    } catch (err) {
      setInstallingLang((cur) => (cur === lang ? null : cur))
      setError(err instanceof Error ? err.message : String(err))
    }
  }

  const handleClear = async () => {
    setClearing(true)
    setError(null)
    setCleared(null)
    try {
      const resp = await clearCache(gatewayClient)
      setCleared(resp.Evicted)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setClearing(false)
    }
  }

  const loaded = states !== null
  const installStatesLoaded = installStates !== null
  const anyEnabled = loaded && Object.values(states).some(Boolean)

  return (
    <FeatureCard icon={<Database size={16} />} title={t('settings.lsp.title')} description={t('settings.lsp.desc')}>
      {LSP_LANGUAGES.map((def) => {
        const installInfo = installStates?.[def.key]
        const progress = installProgress[def.key]
        const installing = installingLang === def.key || progress?.state === 'running'
        const installed = installInfo?.Installed === true || def.builtin

        let statusBadge: React.ReactNode
        if (installing) {
          statusBadge = <Badge variant="outline">{t('settings.lsp.installing')}</Badge>
        } else if (!def.builtin && !installStatesLoaded) {
          // Install states still loading: show a neutral placeholder instead
          // of flashing "未安装" + an install button before the real state
          // arrives.
          statusBadge = <Badge variant="outline">{t('settings.lsp.status.checking')}</Badge>
        } else if (installed) {
          statusBadge = installInfo?.Version
            ? (
                <Badge variant="secondary">
                  {t('settings.lsp.status.installed', { version: installInfo.Version })}
                </Badge>
              )
            : (
                <Badge variant="secondary">{t('settings.lsp.status.installedNoVersion')}</Badge>
              )
        } else if (progress?.state === 'failed') {
          statusBadge = <Badge variant="destructive">{t('settings.lsp.status.installFailed')}</Badge>
        } else {
          statusBadge = <Badge variant="destructive">{t('settings.lsp.status.notInstalled')}</Badge>
        }

        // While install states are still loading, keep the row neutral: no
        // install button, no "未安装" flash. builtin languages are always
        // installed so they skip the loading state entirely.
        const stateKnown = installStatesLoaded || def.builtin
        const showInstallButton = !installed && stateKnown

        return (
          <div key={def.key}>
            <SettingRow
              label={
                <span className="inline-flex items-center gap-2">
                  {t(def.labelKey)}
                  {statusBadge}
                </span>
              }
            description={t(def.descKey)}
            control={
              <div className="flex items-center gap-2">
                {showInstallButton && (
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    disabled={installing}
                    onClick={() => handleInstall(def.key)}
                    data-guide-id={`settings/lsp/install/${def.key}`}
                    aria-label={`${t('settings.lsp.install')} ${t(def.labelKey)}`}
                  >
                    {installing ? <Loader2 size={14} className="animate-spin" /> : <Download size={14} />}
                    <span>{installing ? t('settings.lsp.installing') : t('settings.lsp.install')}</span>
                  </Button>
                )}
                <Switch
                  checked={states?.[def.key] ?? false}
                  onCheckedChange={(value) => handleToggle(def.key, value)}
                  disabled={!loaded || savingLang === def.key}
                  data-guide-id={`settings/lsp/enable/${def.key}`}
                  aria-label={t(def.labelKey)}
                />
              </div>
            }
            />
            {installing && (
              <div className="px-4 pb-2">
                <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted" role="progressbar" aria-valuenow={progress?.percent ?? 0} aria-valuemin={0} aria-valuemax={100}>
                  {(progress?.percent ?? 0) > 0
                    ? (
                        <div className="h-full rounded-full bg-primary transition-all duration-300" style={{ width: `${progress?.percent ?? 0}%` }} />
                      )
                    : (
                        <div className="h-full w-1/3 animate-pulse rounded-full bg-primary" />
                      )}
                </div>
                <p className="mt-1 text-xs text-muted-foreground">
                  {t('settings.lsp.status.installing', { percent: progress?.percent ?? 0 })}
                </p>
              </div>
            )}
            {progress?.state === 'failed' && progress.error && (
              <p className="px-4 pb-2 text-xs text-destructive">{progress.error}</p>
            )}
          </div>
        )
      })}
      <Separator />
      <SettingRow
        label={t('settings.lsp.clear')}
        description={t('settings.lsp.clearDesc')}
        control={
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={clearing || !anyEnabled}
            onClick={handleClear}
            data-guide-id="settings/lsp/clear"
          >
            <Trash2 size={14} />
            <span>{clearing ? t('settings.lsp.clearing') : t('settings.lsp.clear')}</span>
          </Button>
        }
      />
      {cleared !== null && (
        <p className="text-xs text-muted-foreground">{t('settings.lsp.cleared', { count: cleared })}</p>
      )}
      {error && (
        <p className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">{error}</p>
      )}
    </FeatureCard>
  )
}