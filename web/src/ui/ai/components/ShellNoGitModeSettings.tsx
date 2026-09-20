import { useCallback, useEffect, useState } from 'react'
import { useSyncExternalStore } from 'react'
import { GitBranch, Loader2 } from 'lucide-react'
import { client } from '../../../application/generated-client'
import * as projectClient from '../../../gen-clients/project/client'
import { useI18n } from '../../../i18n'
import { FeatureCard, SettingRow } from '../../settings/shadcn/composites'
import { Switch } from '../../settings/shadcn/ui'
import { gitStore } from '../../panels/git-store'

function formatError(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

/**
 * 项目级「无 Git 模式」开关（第一个项目级设置项）。
 * 状态完全由 project actor 持有：挂载时经 project.no_git_mode_get 加载，
 * 切换时经 project.no_git_mode_set 持久化，前端不落任何本地存储。
 * invoke 必须带 { target: projectId } 路由到对应的 project actor，
 * 否则落在默认 cell 上报 "call ID not registered"。
 */
export function ShellNoGitModeSettings() {
  const { t } = useI18n()
  useSyncExternalStore(gitStore.subscribe.bind(gitStore), gitStore.getVersion)
  const projectId = gitStore.state.projectId
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [noGitMode, setNoGitMode] = useState(false)
  const [hasGitRepo, setHasGitRepo] = useState(true)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    if (!projectId) {
      setLoading(false)
      return
    }
    setLoading(true)
    setError('')
    try {
      const resp = await projectClient.noGitModeGet(client, {}, { target: projectId })
      setNoGitMode(resp.NoGitMode)
      setHasGitRepo(resp.HasGitRepo)
    } catch (e) {
      setError(t('settings.git.noGitMode.error', { error: formatError(e) }))
    } finally {
      setLoading(false)
    }
  }, [t, projectId])

  useEffect(() => {
    void load()
  }, [load])

  const handleToggle = async (enabled: boolean) => {
    if (saving || !projectId) return
    setSaving(true)
    setError('')
    try {
      const resp = await projectClient.noGitModeSet(client, { NoGitMode: enabled }, { target: projectId })
      setNoGitMode(resp.NoGitMode)
    } catch (e) {
      setError(t('settings.git.noGitMode.error', { error: formatError(e) }))
    } finally {
      setSaving(false)
    }
  }

  return (
    <FeatureCard icon={<GitBranch size={16} />} title={t('settings.git.noGitMode')} description={t('settings.git.noGitMode.desc')}>
      {!hasGitRepo && (
        <div
          className="rounded-lg border border-border bg-muted/50 px-3 py-2 text-sm text-muted-foreground"
          data-guide-id="settings/git/noGitMode/noGitRepoHint"
        >
          {t('settings.git.noGitMode.noGitRepoHint')}
        </div>
      )}
      {error && (
        <div className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">
          {error}
        </div>
      )}
      <SettingRow
        label={t('settings.git.noGitMode')}
        description={t('settings.git.noGitMode.desc')}
        control={
          loading ? (
            <Loader2 size={16} className="animate-spin text-muted-foreground" />
          ) : (
            <Switch
              checked={noGitMode}
              onCheckedChange={(v) => void handleToggle(v)}
              disabled={saving}
              data-guide-id="settings/git/noGitMode/toggle"
              aria-label={t('settings.git.noGitMode')}
            />
          )
        }
      />
    </FeatureCard>
  )
}