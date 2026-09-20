import { useEffect, useState, useCallback } from 'react'
import { Plus, Trash2, Loader2, Save, GitBranch, User, GitFork, Globe } from 'lucide-react'
import { useSyncExternalStore } from 'react'
import { client } from '../../../application/generated-client'
import * as workspace from '../../../gen-clients/workspace/client'
import { gitStore } from '../../panels/git-store'
import { useI18n } from '../../../i18n'
import { FeatureCard, SettingRow } from '../../settings/shadcn/composites'
import { Button, Input } from '../../settings/shadcn/ui'
import type { GitRemoteInfo } from '../../../gen-clients/system/types'

const CONFIG_KEYS = {
  userName: 'user.name',
  userEmail: 'user.email',
  httpProxy: 'http.proxy',
  httpsProxy: 'https.proxy',
} as const

function formatError(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

export function ShellGitSettings() {
  const { t } = useI18n()
  useSyncExternalStore(gitStore.subscribe.bind(gitStore), gitStore.getVersion)

  const projectId = gitStore.state.projectId
  const worktreeId = gitStore.state.worktreeId

  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const [userName, setUserName] = useState('')
  const [userEmail, setUserEmail] = useState('')
  const [savingAccount, setSavingAccount] = useState(false)

  const [httpProxy, setHttpProxy] = useState('')
  const [httpsProxy, setHttpsProxy] = useState('')
  const [savingProxy, setSavingProxy] = useState(false)

  const [remotes, setRemotes] = useState<GitRemoteInfo[]>([])
  const [newRemoteName, setNewRemoteName] = useState('')
  const [newRemoteUrl, setNewRemoteUrl] = useState('')
  const [remotesBusy, setRemotesBusy] = useState(false)

  const baseReq = useCallback(() => ({
    ProjectId: projectId,
    WorktreeID: worktreeId ?? undefined,
  }), [projectId, worktreeId])

  const load = useCallback(async () => {
    if (!projectId) {
      setLoading(false)
      return
    }
    setLoading(true)
    setError('')
    try {
      const [nameResp, emailResp, httpResp, httpsResp, remotesResp] = await Promise.all([
        workspace.gitConfigGet(client, { ...baseReq(), Key: CONFIG_KEYS.userName }),
        workspace.gitConfigGet(client, { ...baseReq(), Key: CONFIG_KEYS.userEmail }),
        workspace.gitConfigGet(client, { ...baseReq(), Key: CONFIG_KEYS.httpProxy }),
        workspace.gitConfigGet(client, { ...baseReq(), Key: CONFIG_KEYS.httpsProxy }),
        workspace.gitRemoteList(client, baseReq()),
      ])
      setUserName(nameResp.Value ?? '')
      setUserEmail(emailResp.Value ?? '')
      setHttpProxy(httpResp.Value ?? '')
      setHttpsProxy(httpsResp.Value ?? '')
      setRemotes(remotesResp.Remotes ?? [])
    } catch (e) {
      setError(t('settings.git.error', { error: formatError(e) }))
    } finally {
      setLoading(false)
    }
  }, [projectId, worktreeId, baseReq, t])

  useEffect(() => {
    void load()
  }, [load])

  const saveAccount = async () => {
    if (!projectId || savingAccount) return
    setSavingAccount(true)
    setError('')
    try {
      await Promise.all([
        workspace.gitConfigSet(client, { ...baseReq(), Key: CONFIG_KEYS.userName, Value: userName.trim(), Global: false }),
        workspace.gitConfigSet(client, { ...baseReq(), Key: CONFIG_KEYS.userEmail, Value: userEmail.trim(), Global: false }),
      ])
      await load()
    } catch (e) {
      setError(t('settings.git.error', { error: formatError(e) }))
    } finally {
      setSavingAccount(false)
    }
  }

  const saveProxy = async () => {
    if (!projectId || savingProxy) return
    setSavingProxy(true)
    setError('')
    try {
      await Promise.all([
        workspace.gitConfigSet(client, { ...baseReq(), Key: CONFIG_KEYS.httpProxy, Value: httpProxy.trim(), Global: false }),
        workspace.gitConfigSet(client, { ...baseReq(), Key: CONFIG_KEYS.httpsProxy, Value: httpsProxy.trim(), Global: false }),
      ])
      await load()
    } catch (e) {
      setError(t('settings.git.error', { error: formatError(e) }))
    } finally {
      setSavingProxy(false)
    }
  }

  const addRemote = async () => {
    const name = newRemoteName.trim()
    const url = newRemoteUrl.trim()
    if (!projectId || !name || !url || remotesBusy) return
    setRemotesBusy(true)
    setError('')
    try {
      await workspace.gitRemoteAdd(client, { ...baseReq(), Name: name, Url: url })
      setNewRemoteName('')
      setNewRemoteUrl('')
      await load()
    } catch (e) {
      setError(t('settings.git.error', { error: formatError(e) }))
    } finally {
      setRemotesBusy(false)
    }
  }

  const removeRemote = async (name: string) => {
    if (!projectId || remotesBusy) return
    setRemotesBusy(true)
    setError('')
    try {
      await workspace.gitRemoteRemove(client, { ...baseReq(), Name: name })
      await load()
    } catch (e) {
      setError(t('settings.git.error', { error: formatError(e) }))
    } finally {
      setRemotesBusy(false)
    }
  }

  if (!projectId) {
    return (
      <FeatureCard icon={<GitBranch size={16} />} title={t('settings.git.title')} description={t('settings.git.desc')}>
        <p className="text-sm text-muted-foreground">{t('settings.git.noProject')}</p>
      </FeatureCard>
    )
  }

  if (loading) {
    return (
      <FeatureCard icon={<GitBranch size={16} />} title={t('settings.git.title')} description={t('settings.git.desc')}>
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 size={16} className="animate-spin" />
          <span>{t('settings.git.loading')}</span>
        </div>
      </FeatureCard>
    )
  }

  return (
    <>
      {error && (
        <div className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive" role="alert">
          {error}
        </div>
      )}

      <FeatureCard icon={<User size={16} />} title={t('settings.git.account.title')} description={t('settings.git.account.desc')}>
        <SettingRow label={t('settings.git.account.name')}>
          <Input
            value={userName}
            onChange={(e) => setUserName(e.target.value)}
            placeholder={t('settings.git.account.namePlaceholder')}
            data-guide-id="settings/git/account/name"
          />
        </SettingRow>
        <SettingRow label={t('settings.git.account.email')}>
          <Input
            value={userEmail}
            onChange={(e) => setUserEmail(e.target.value)}
            placeholder={t('settings.git.account.emailPlaceholder')}
            data-guide-id="settings/git/account/email"
          />
        </SettingRow>
        <div className="flex justify-end">
          <Button
            type="button"
            size="sm"
            disabled={savingAccount}
            onClick={() => void saveAccount()}
            data-guide-id="settings/git/account/save"
          >
            {savingAccount ? <Loader2 size={14} className="animate-spin" /> : <Save size={14} />}
            {savingAccount ? t('settings.git.account.saving') : t('settings.git.account.save')}
          </Button>
        </div>
      </FeatureCard>

      <FeatureCard icon={<GitFork size={16} />} title={t('settings.git.remotes.title')} description={t('settings.git.remotes.desc')}>
        {remotes.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t('settings.git.remotes.noRemotes')}</p>
        ) : (
          <div className="flex flex-col gap-2">
            {remotes.map((remote) => (
              <div
                key={remote.Name}
                className="flex items-center justify-between gap-2 rounded-lg border border-border px-3 py-2"
              >
                <div className="flex min-w-0 flex-col">
                  <span className="text-sm font-medium">{remote.Name}</span>
                  <span className="truncate text-xs text-muted-foreground font-mono">{(remote.Urls ?? []).join(', ')}</span>
                </div>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  disabled={remotesBusy}
                  onClick={() => void removeRemote(remote.Name)}
                  title={t('settings.git.remotes.remove')}
                  className="text-destructive hover:text-destructive shrink-0"
                  data-guide-id={`settings/git/remotes/remove/${remote.Name}`}
                >
                  <Trash2 size={14} />
                </Button>
              </div>
            ))}
          </div>
        )}
        <div className="flex items-end gap-2 pt-2">
          <div className="flex-1">
            <Input
              value={newRemoteName}
              onChange={(e) => setNewRemoteName(e.target.value)}
              placeholder={t('settings.git.remotes.namePlaceholder')}
              data-guide-id="settings/git/remotes/name"
            />
          </div>
          <div className="flex-[2]">
            <Input
              value={newRemoteUrl}
              onChange={(e) => setNewRemoteUrl(e.target.value)}
              placeholder={t('settings.git.remotes.urlPlaceholder')}
              data-guide-id="settings/git/remotes/url"
            />
          </div>
          <Button
            type="button"
            size="sm"
            disabled={remotesBusy || !newRemoteName.trim() || !newRemoteUrl.trim()}
            onClick={() => void addRemote()}
            data-guide-id="settings/git/remotes/add"
          >
            <Plus size={14} />
            {t('settings.git.remotes.add')}
          </Button>
        </div>
      </FeatureCard>

      <FeatureCard icon={<Globe size={16} />} title={t('settings.git.proxy.title')} description={t('settings.git.proxy.desc')}>
        <SettingRow label={t('settings.git.proxy.http')}>
          <Input
            value={httpProxy}
            onChange={(e) => setHttpProxy(e.target.value)}
            placeholder={t('settings.git.proxy.placeholder')}
            data-guide-id="settings/git/proxy/http"
          />
        </SettingRow>
        <SettingRow label={t('settings.git.proxy.https')}>
          <Input
            value={httpsProxy}
            onChange={(e) => setHttpsProxy(e.target.value)}
            placeholder={t('settings.git.proxy.placeholder')}
            data-guide-id="settings/git/proxy/https"
          />
        </SettingRow>
        <div className="flex justify-end">
          <Button
            type="button"
            size="sm"
            disabled={savingProxy}
            onClick={() => void saveProxy()}
            data-guide-id="settings/git/proxy/save"
          >
            {savingProxy ? <Loader2 size={14} className="animate-spin" /> : <Save size={14} />}
            {savingProxy ? t('settings.git.proxy.saving') : t('settings.git.proxy.save')}
          </Button>
        </div>
      </FeatureCard>
    </>
  )
}
