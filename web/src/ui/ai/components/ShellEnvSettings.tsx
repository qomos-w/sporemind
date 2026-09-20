import { useEffect, useState } from 'react'
import { Loader2, CircleCheck, Terminal } from 'lucide-react'
import { client } from '../../../application/generated-client'
import * as workspace from '../../../gen-clients/workspace/client'
import type { ShellEnvProbeResp } from '../../../gen-clients/system/types'
import { useI18n } from '../../../i18n'
import { FeatureCard } from '../../settings/shadcn/composites'
import { Badge } from '../../settings/shadcn/ui'

const SHELL_KIND_ORDER = ['powershell5', 'powershell7', 'bash', 'gitbash', 'cmd'] as const

const SHELL_EXE: Record<string, string> = {
  powershell5: 'powershell.exe',
  powershell7: 'pwsh.exe',
  bash: 'bash',
  gitbash: 'git-bash.exe',
  cmd: 'cmd.exe',
}

function CurrentBanner({ text, path }: { text: string; path?: string }) {
  return (
    <div className="flex items-center gap-2 rounded-lg border border-border bg-muted/50 px-3 py-2 text-sm">
      <CircleCheck size={15} className="shrink-0 text-primary" />
      <span>{text}</span>
      {path && <code className="truncate font-mono text-xs text-muted-foreground">{path}</code>}
    </div>
  )
}

export function ShellEnvSettings() {
  const { t } = useI18n()
  const [probe, setProbe] = useState<ShellEnvProbeResp | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  const load = async () => {
    setError('')
    try {
      const resp = await workspace.shellEnvProbe(client)
      setProbe(resp)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { void load() }, [])

  const handleSelect = async (kind: string) => {
    if (saving) return
    setSaving(true)
    setError('')
    try {
      const resp = await workspace.shellPrefSave(client, { RequestId: '', Kind: kind })
      setProbe(prev => prev ? { ...prev, Current: resp.Current } : prev)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setSaving(false)
    }
  }

  if (loading) {
    return (
      <FeatureCard icon={<Terminal size={16} />} title={t('settings.environment.title')} description={t('settings.environment.desc')}>
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 size={16} className="animate-spin" />
          <span>{t('settings.environment.loading')}</span>
        </div>
      </FeatureCard>
    )
  }

  const isWindows = probe?.Platform === 'windows'
  const candidates = probe?.Candidates ?? []
  const currentKind = probe?.Current?.Kind ?? ''

  if (!isWindows) {
    return (
      <FeatureCard icon={<Terminal size={16} />} title={t('settings.environment.title')} description={t('settings.environment.desc')}>
        <CurrentBanner text={t('settings.environment.nonWindows')} path={probe?.Current?.Executable} />
      </FeatureCard>
    )
  }

  const savedKind = candidates.some(c => c.Kind === currentKind && c.Available) ? currentKind : ''

  const renderOption = (opts: {
    kind: string
    checked: boolean
    available: boolean
    name: string
    desc: string
    exe?: string
    path?: string
  }) => {
    const { kind, checked, available, name, desc, exe, path } = opts
    return (
      <button
        key={kind || 'auto'}
        type="button"
        role="radio"
        aria-checked={checked}
        className={`flex w-full items-start gap-3 rounded-lg border px-3 py-2.5 text-left transition-colors ${
          checked ? 'border-primary bg-primary/5' : 'border-border hover:bg-muted/50'
        } ${!available ? 'opacity-50' : ''}`}
        onClick={() => available && void handleSelect(kind)}
        disabled={saving || !available}
        data-guide-id={kind ? `settings/environment/${kind}` : 'settings/environment/auto'}
      >
        <span className="mt-0.5 flex size-4 shrink-0 items-center justify-center rounded-full border border-border" aria-hidden="true">
          {checked && <span className="size-2 rounded-full bg-primary" />}
        </span>
        <span className="flex min-w-0 flex-1 flex-col gap-0.5">
          <span className="flex items-center gap-2">
            <span className="text-sm font-medium">{name}</span>
            {exe && <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">{exe}</code>}
            {exe && (
              <Badge variant={available ? 'secondary' : 'outline'}>
                {available ? t('settings.environment.installed') : t('settings.environment.notInstalled')}
              </Badge>
            )}
          </span>
          <span className="text-xs text-muted-foreground">{desc}</span>
          {path !== undefined && (
            <code className="truncate font-mono text-[11px] text-muted-foreground">{path || t('settings.environment.notDetected')}</code>
          )}
        </span>
        {saving && checked && <Loader2 size={14} className="shrink-0 animate-spin text-muted-foreground" />}
      </button>
    )
  }

  return (
    <FeatureCard icon={<Terminal size={16} />} title={t('settings.environment.title')} description={t('settings.environment.desc')}>
      {error && (
        <p className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">{error}</p>
      )}

      <div className="flex flex-col gap-2" role="radiogroup" aria-label={t('settings.environment.title')}>
        {renderOption({
          kind: '',
          checked: savedKind === '',
          available: true,
          name: t('settings.environment.auto'),
          desc: t('settings.environment.autoDesc'),
        })}

        {SHELL_KIND_ORDER.map(kind => {
          const candidate = candidates.find(c => c.Kind === kind)
          const available = candidate?.Available ?? false
          return renderOption({
            kind,
            checked: savedKind === kind,
            available,
            name: t(`settings.environment.${kind}`),
            desc: t(`settings.environment.${kind}Desc`),
            exe: SHELL_EXE[kind],
            path: candidate?.Executable || '',
          })
        })}
      </div>

      {probe?.Current?.Executable && (
        <CurrentBanner text={t('settings.environment.current')} path={probe.Current.Executable} />
      )}

      <p className="text-xs text-muted-foreground">{t('settings.environment.hint')}</p>
    </FeatureCard>
  )
}
