import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useI18n } from '../../i18n'
import { ConfirmDialog } from '../components/ConfirmDialog'
import {
  Plus, Trash2, Edit2, KeyRound, Users, Shield, UserCircle,
  Check, X, Search, LogOut, Loader2,
} from 'lucide-react'
import { client } from '../../application/generated-client'
import * as userAccount from '../../gen-clients/user/client'
import * as userGroup from '../../gen-clients/user/client'
import * as userPermission from '../../gen-clients/user/client'
import type { AccountView, Group, PermissionMatrix } from '../../gen-clients/system/types'
import {
  getToken, getAccount, clearAuth, isAdmin,
} from '../../application/auth-store'
import {
  Badge, Button, Field, FieldLabel, Input,
  SelectRoot, SelectTrigger, SelectValue, SelectContent, SelectItem, SelectItemText,
  Switch, TabsRoot, TabsList, TabsTrigger,
} from '../settings/shadcn/ui'

const ALL_ROLES = ['admin', 'developer', 'viewer', 'operator'] as const
type BadgeVariant = 'default' | 'secondary' | 'destructive' | 'outline'

const roleBadgeVariant = (role: string): BadgeVariant =>
  role === 'admin' ? 'destructive'
    : role === 'developer' ? 'default'
      : role === 'operator' ? 'secondary'
        : 'outline'

// ---------------------------------------------------------------------------
// Dialog shell
// ---------------------------------------------------------------------------

function DialogShell({
  title, onCancel, children, footer,
}: {
  title: string
  onCancel: () => void
  children: ReactNode
  footer: ReactNode
}) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50" onClick={onCancel}>
      <div
        className="flex w-[420px] max-w-[92vw] flex-col rounded-xl border border-border bg-background shadow-lg"
        onClick={e => e.stopPropagation()}
      >
        <div className="flex items-center justify-between border-b border-border px-4 py-3">
          <span className="text-sm font-semibold">{title}</span>
          <Button type="button" variant="ghost" size="icon-sm" onClick={onCancel} aria-label={title}>
            <X />
          </Button>
        </div>
        <div className="flex flex-col gap-4 px-4 py-4">{children}</div>
        <div className="flex justify-end gap-2 border-t border-border px-4 py-3">{footer}</div>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// UserDialog
// ---------------------------------------------------------------------------

interface UserDialogProps {
  open: boolean
  user: AccountView | null
  onSave: (data: { username?: string; password?: string; displayName: string; roles: string[]; groups: string[]; status?: string }) => void
  onCancel: () => void
}

function UserDialog({ open, user, onSave, onCancel }: UserDialogProps) {
  const { t } = useI18n()
  const [username, setUsername] = useState(user?.Username ?? '')
  const [password, setPassword] = useState('')
  const [displayName, setDisplayName] = useState(user?.DisplayName ?? '')
  const [roles, setRoles] = useState<string[]>(user?.Roles ?? [])
  const [groups, setGroups] = useState<string[]>(user?.Groups ?? [])
  const [status, setStatus] = useState(user?.Status ?? 'active')
  const [allGroups, setAllGroups] = useState<Group[]>([])
  const isEdit = !!user
  const isBuiltinAdmin = isEdit && user?.Id === 'admin' && user?.Username === 'admin'

  useEffect(() => {
    if (!open) return
    setUsername(user?.Username ?? '')
    setPassword('')
    setDisplayName(user?.DisplayName ?? '')
    setRoles(user?.Roles ?? [])
    setGroups(user?.Groups ?? [])
    setStatus(user?.Status ?? 'active')
    userGroup.groupList(client, {}).then(r => setAllGroups(r.Items)).catch(() => {})
  }, [open, user])

  if (!open) return null

  return (
    <DialogShell
      title={t(isEdit ? 'settings.user.editUserTitle' : 'settings.user.addUserTitle')}
      onCancel={onCancel}
      footer={
        <>
          <Button type="button" variant="outline" onClick={onCancel}>{t('common.cancel')}</Button>
          <Button
            type="button"
            disabled={!username.trim() || (!isEdit && !password)}
            onClick={() => onSave({ username: username.trim(), password: password || undefined, displayName: displayName.trim(), roles, groups, status })}
          >
            {t(isEdit ? 'common.save' : 'settings.user.addUser')}
          </Button>
        </>
      }
    >
      {!isEdit && (
        <Field>
          <FieldLabel>{t('settings.user.username')}</FieldLabel>
          <Input value={username} onChange={e => setUsername(e.target.value)} autoComplete="off" />
        </Field>
      )}
      {!isEdit && (
        <Field>
          <FieldLabel>{t('settings.user.password')}</FieldLabel>
          <Input type="password" value={password} onChange={e => setPassword(e.target.value)} autoComplete="new-password" />
        </Field>
      )}
      {isBuiltinAdmin && (
        <p className="text-xs text-muted-foreground">{t('settings.user.builtinAdminLocked')}</p>
      )}
      <Field>
        <FieldLabel>{t('settings.user.displayName')}</FieldLabel>
        <Input value={displayName} onChange={e => setDisplayName(e.target.value)} disabled={isBuiltinAdmin} />
      </Field>
      <Field>
        <FieldLabel>{t('settings.user.status')}</FieldLabel>
        <SelectRoot
          value={status}
          onValueChange={(v) => setStatus(v as string)}
          items={[
            { value: 'active', label: t('settings.user.statusActive') },
            { value: 'disabled', label: t('settings.user.statusDisabled') },
          ]}
        >
          <SelectTrigger className="w-40"><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="active"><SelectItemText>{t('settings.user.statusActive')}</SelectItemText></SelectItem>
            <SelectItem value="disabled"><SelectItemText>{t('settings.user.statusDisabled')}</SelectItemText></SelectItem>
          </SelectContent>
        </SelectRoot>
      </Field>
      <Field>
        <FieldLabel>{t('settings.user.roles')}</FieldLabel>
        <div className="flex flex-wrap gap-2">
          {ALL_ROLES.map(r => (
            <Button
              key={r}
              type="button"
              size="sm"
              variant={roles.includes(r) ? 'default' : 'outline'}
              disabled={isBuiltinAdmin}
              onClick={() => setRoles(prev => prev.includes(r) ? prev.filter(x => x !== r) : [...prev, r])}
            >
              {roles.includes(r) && <Check size={10} />}{r}
            </Button>
          ))}
        </div>
      </Field>
      {allGroups.length > 0 && (
        <Field>
          <FieldLabel>{t('settings.user.groups')}</FieldLabel>
          <div className="flex flex-wrap gap-2">
            {allGroups.map(g => (
              <Button
                key={g.Id}
                type="button"
                size="sm"
                variant={groups.includes(g.Name) ? 'default' : 'outline'}
                disabled={isBuiltinAdmin}
                onClick={() => setGroups(prev => prev.includes(g.Name) ? prev.filter(x => x !== g.Name) : [...prev, g.Name])}
              >
                {groups.includes(g.Name) && <Check size={10} />}{g.Name}
              </Button>
            ))}
          </div>
        </Field>
      )}
    </DialogShell>
  )
}

// ---------------------------------------------------------------------------
// GroupDialog
// ---------------------------------------------------------------------------

interface GroupDialogProps {
  open: boolean
  group: Group | null
  onSave: (data: { name: string; description: string; roles: string[] }) => void
  onCancel: () => void
}

function GroupDialog({ open, group, onSave, onCancel }: GroupDialogProps) {
  const { t } = useI18n()
  const [name, setName] = useState(group?.Name ?? '')
  const [description, setDescription] = useState(group?.Description ?? '')
  const [roles, setRoles] = useState<string[]>(group?.Roles ?? [])
  const isEdit = !!group

  useEffect(() => {
    if (!open) return
    setName(group?.Name ?? '')
    setDescription(group?.Description ?? '')
    setRoles(group?.Roles ?? [])
  }, [open, group])

  if (!open) return null

  return (
    <DialogShell
      title={t(isEdit ? 'settings.user.editGroupTitle' : 'settings.user.addGroupTitle')}
      onCancel={onCancel}
      footer={
        <>
          <Button type="button" variant="outline" onClick={onCancel}>{t('common.cancel')}</Button>
          <Button type="button" disabled={!name.trim()} onClick={() => onSave({ name: name.trim(), description: description.trim(), roles })}>
            {t(isEdit ? 'common.save' : 'settings.user.addGroup')}
          </Button>
        </>
      }
    >
      <Field>
        <FieldLabel>{t('settings.user.groupName')}</FieldLabel>
        <Input value={name} onChange={e => setName(e.target.value)} disabled={isEdit} />
      </Field>
      <Field>
        <FieldLabel>{t('settings.user.groupDescription')}</FieldLabel>
        <Input value={description} onChange={e => setDescription(e.target.value)} />
      </Field>
      <Field>
        <FieldLabel>{t('settings.user.defaultRoles')}</FieldLabel>
        <div className="flex flex-wrap gap-2">
          {ALL_ROLES.map(r => (
            <Button
              key={r}
              type="button"
              size="sm"
              variant={roles.includes(r) ? 'default' : 'outline'}
              onClick={() => setRoles(prev => prev.includes(r) ? prev.filter(x => x !== r) : [...prev, r])}
            >
              {roles.includes(r) && <Check size={10} />}{r}
            </Button>
          ))}
        </div>
      </Field>
    </DialogShell>
  )
}

// ---------------------------------------------------------------------------
// ResetPasswordDialog
// ---------------------------------------------------------------------------

interface ResetPasswordDialogProps {
  open: boolean
  userId: string
  username: string
  onSave: (password: string) => Promise<void>
  onCancel: () => void
}

function ResetPasswordDialog({ open, username, onSave, onCancel }: ResetPasswordDialogProps) {
  const { t } = useI18n()
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [submitError, setSubmitError] = useState('')

  useEffect(() => {
    if (!open) {
      setPassword('')
      setConfirm('')
      setSubmitting(false)
      setSubmitError('')
    }
  }, [open])

  if (!open) return null

  const validationError = !password
    ? t('settings.user.passwordRequired')
    : password.length < 8
      ? t('settings.user.passwordMinLength')
      : password !== confirm
        ? t('settings.user.passwordMismatch')
        : ''

  const handleSubmit = async () => {
    if (validationError) {
      setSubmitError(validationError)
      return
    }
    setSubmitting(true)
    setSubmitError('')
    try {
      await onSave(password)
    } catch (err) {
      setSubmitError(err instanceof Error ? err.message : String(err))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <DialogShell
      title={t('settings.user.resetPasswordTitle')}
      onCancel={onCancel}
      footer={
        <>
          <Button type="button" variant="outline" onClick={onCancel} disabled={submitting}>{t('common.cancel')}</Button>
          <Button type="button" disabled={submitting} onClick={handleSubmit}>
            {submitting ? t('settings.user.resetting') : t('common.reset')}
          </Button>
        </>
      }
    >
      <Field>
        <FieldLabel>{t('settings.user.userLabel')}</FieldLabel>
        <Input value={username} disabled />
      </Field>
      <Field>
        <FieldLabel>{t('settings.user.newPassword')}</FieldLabel>
        <Input type="password" value={password} onChange={e => setPassword(e.target.value)} />
      </Field>
      <Field>
        <FieldLabel>{t('settings.user.confirmPassword')}</FieldLabel>
        <Input type="password" value={confirm} onChange={e => setConfirm(e.target.value)} />
      </Field>
      {submitError && <p className="text-sm text-destructive">{submitError}</p>}
    </DialogShell>
  )
}

// ---------------------------------------------------------------------------
// UserCard
// ---------------------------------------------------------------------------

function UserCard({
  user,
  admin,
  currentUsername,
  onEdit,
  onReset,
  onDelete,
}: {
  user: AccountView
  admin: boolean
  currentUsername: string
  onEdit: () => void
  onReset: () => void
  onDelete: () => void
}) {
  const { t } = useI18n()
  return (
    <div className="flex items-center gap-2 rounded-lg border border-border px-3 py-2">
      <span className={`mt-0.5 size-1.5 shrink-0 rounded-full ${user.Status === 'active' ? 'bg-emerald-500' : 'bg-zinc-400'}`} />
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="flex flex-wrap items-center gap-1.5 text-sm font-medium">
          {user.DisplayName || user.Username}
          {user.Roles.map(r => <Badge key={r} variant={roleBadgeVariant(r)}>{r}</Badge>)}
        </span>
        <span className="text-xs text-muted-foreground">@{user.Username} · {t(user.Status === 'active' ? 'settings.user.statusActive' : 'settings.user.statusDisabled')}</span>
        {user.Groups.length > 0 && (
          <span className="flex flex-wrap gap-1">
            {user.Groups.map(g => <Badge key={g} variant="outline">{g}</Badge>)}
          </span>
        )}
      </div>
      {(admin || currentUsername === user.Username) && (
        <Button type="button" variant="ghost" size="icon-sm" title={t('common.edit')} onClick={onEdit}><Edit2 /></Button>
      )}
      <Button type="button" variant="ghost" size="icon-sm" title={t('settings.user.resetPassword')} onClick={onReset}><KeyRound /></Button>
      {admin && user.Username !== 'admin' && (
        <Button type="button" variant="ghost" size="icon-sm" className="text-destructive hover:text-destructive" title={t('common.delete')} onClick={onDelete}><Trash2 /></Button>
      )}
    </div>
  )
}

// ---------------------------------------------------------------------------
// GroupCard
// ---------------------------------------------------------------------------

function GroupCard({
  group,
  admin,
  onEdit,
  onDelete,
}: {
  group: Group
  admin: boolean
  onEdit: () => void
  onDelete: () => void
}) {
  const { t } = useI18n()
  return (
    <div className="flex items-center gap-2 rounded-lg border border-border px-3 py-2">
      <Users className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="flex flex-wrap items-center gap-1.5 text-sm font-medium">
          {group.Name}
          {group.Roles.map((r: string) => <Badge key={r} variant={roleBadgeVariant(r)}>{r}</Badge>)}
        </span>
        {group.Description && <span className="text-xs text-muted-foreground">{group.Description}</span>}
        <span className="text-xs text-muted-foreground">{t('settings.user.memberCount', { count: group.MemberCount })}</span>
      </div>
      {admin && (
        <>
          <Button type="button" variant="ghost" size="icon-sm" title={t('common.edit')} onClick={onEdit}><Edit2 /></Button>
          <Button type="button" variant="ghost" size="icon-sm" className="text-destructive hover:text-destructive" title={t('common.delete')} onClick={onDelete}><Trash2 /></Button>
        </>
      )}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Main Panel
// ---------------------------------------------------------------------------

type Tab = 'accounts' | 'groups' | 'permissions'

export function UserPanel() {
  const { t } = useI18n()
  const [activeTab, setActiveTab] = useState<Tab>('accounts')
  const [loggedIn] = useState(() => !!getToken())
  const [account, setLocalAccount] = useState<AccountView | null>(getAccount)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const [users, setUsers] = useState<AccountView[]>([])
  const [groups, setGroups] = useState<Group[]>([])
  const [permissions, setPermissions] = useState<PermissionMatrix>({ Entries: [] })

  const [searchQuery, setSearchQuery] = useState('')

  const [userDialogOpen, setUserDialogOpen] = useState(false)
  const [editingUser, setEditingUser] = useState<AccountView | null>(null)
  const [groupDialogOpen, setGroupDialogOpen] = useState(false)
  const [editingGroup, setEditingGroup] = useState<Group | null>(null)
  const [resetDialogOpen, setResetDialogOpen] = useState(false)
  const [resetTarget, setResetTarget] = useState<AccountView | null>(null)
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [confirmTitle, setConfirmTitle] = useState('')
  const [confirmDescription, setConfirmDescription] = useState('')
  const confirmActionRef = useRef<(() => void) | null>(null)

  const token = getToken()
  const admin = isAdmin()

  const handleLogout = useCallback(() => {
    clearAuth()
    setLocalAccount(null)
    setUsers([])
    setGroups([])
    setPermissions({ Entries: [] })
    window.dispatchEvent(new CustomEvent('sporemind:logout'))
  }, [])

  const loadData = useCallback(async () => {
    if (!token) return
    setLoading(true)
    setError('')
    try {
      const [usersResp, groupsResp, permResp] = await Promise.all([
        userAccount.list(client, {}),
        userGroup.groupList(client, {}),
        userPermission.permissionGet(client, {}),
      ])
      setUsers(usersResp.Items)
      setGroups(groupsResp.Items)
      setPermissions(permResp)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      if (String(err).includes('invalid token') || String(err).includes('token expired')) {
        handleLogout()
      }
    } finally {
      setLoading(false)
    }
  }, [token, handleLogout])

  useEffect(() => {
    if (loggedIn) {
      void loadData()
    }
  }, [loggedIn, loadData])

  const filteredUsers = useMemo(() => {
    if (!searchQuery.trim()) return users
    const q = searchQuery.trim().toLowerCase()
    return users.filter(u => u.Username.toLowerCase().includes(q) || u.DisplayName.toLowerCase().includes(q) || u.Roles.some(r => r.toLowerCase().includes(q)))
  }, [users, searchQuery])

  const filteredGroups = useMemo(() => {
    if (!searchQuery.trim()) return groups
    const q = searchQuery.trim().toLowerCase()
    return groups.filter(g => g.Name.toLowerCase().includes(q) || g.Description.toLowerCase().includes(q))
  }, [groups, searchQuery])

  const allActions = useMemo(() => {
    const set = new Set<string>()
    permissions.Entries.forEach(e => {
      if (e.Actions) Object.keys(e.Actions).forEach(a => set.add(a))
    })
    return [...set].sort()
  }, [permissions])

  const handleAddUser = useCallback(() => { setEditingUser(null); setUserDialogOpen(true) }, [])
  const handleEditUser = useCallback((u: AccountView) => { setEditingUser(u); setUserDialogOpen(true) }, [])

  const handleSaveUser = useCallback(async (data: { username?: string; password?: string; displayName: string; roles: string[]; groups: string[]; status?: string }) => {
    if (!token) return
    try {
      if (editingUser) {
        const updated = await userAccount.update(client, { Id: editingUser.Id, DisplayName: data.displayName, Roles: data.roles, Groups: data.groups, Status: data.status })
        setUsers(prev => prev.map(u => u.Id === updated.Id ? updated : u))
      } else if (data.username && data.password) {
        const created = await userAccount.create(client, { Username: data.username, Password: data.password, DisplayName: data.displayName, Roles: data.roles, Groups: data.groups })
        setUsers(prev => [...prev, created])
      }
      setUserDialogOpen(false)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }, [token, editingUser])

  const handleDeleteUser = useCallback((id: string) => {
    const u = users.find(x => x.Id === id)
    setConfirmTitle(t('settings.user.deleteUserTitle'))
    setConfirmDescription(t('settings.user.deleteUserDescription', { name: u?.DisplayName || u?.Username || id }))
    confirmActionRef.current = async () => {
      try {
        await userAccount.remove(client, { Id: id })
        setUsers(prev => prev.filter(x => x.Id !== id))
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err))
      }
    }
    setConfirmOpen(true)
  }, [users, t])

  const handleResetPassword = useCallback(async (password: string) => {
    if (!token || !resetTarget) return
    await userAccount.resetPassword(client, { Id: resetTarget.Id, Password: password })
    setResetDialogOpen(false)
  }, [token, resetTarget])

  const handleSaveGroup = useCallback(async (data: { name: string; description: string; roles: string[] }) => {
    if (!token) return
    try {
      if (editingGroup) {
        const updated = await userGroup.groupUpdate(client, { Id: editingGroup.Id, Description: data.description, Roles: data.roles })
        setGroups(prev => prev.map(g => g.Id === updated.Id ? updated : g))
      } else {
        const created = await userGroup.groupCreate(client, { Name: data.name, Description: data.description, Roles: data.roles })
        setGroups(prev => [...prev, created])
      }
      setGroupDialogOpen(false)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }, [token, editingGroup])

  const handleDeleteGroup = useCallback((id: string) => {
    const g = groups.find(x => x.Id === id)
    setConfirmTitle(t('settings.user.deleteGroupTitle'))
    setConfirmDescription(t('settings.user.deleteGroupDescription', { name: g?.Name || id }))
    confirmActionRef.current = async () => {
      try {
        await userGroup.groupRemove(client, { Id: id })
        setGroups(prev => prev.filter(x => x.Id !== id))
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err))
      }
    }
    setConfirmOpen(true)
  }, [groups, t])

  const togglePermission = useCallback(async (role: string, action: string) => {
    const entry = permissions.Entries.find(p => p.Role === role)
    if (!entry) return
    const newVal = !entry.Actions[action]
    const updated = { ...entry, Actions: { ...entry.Actions, [action]: newVal } }
    const optimistic = { Entries: permissions.Entries.map(p => p.Role === role ? updated : p) }
    setPermissions(optimistic)
    try {
      await userPermission.permissionUpdate(client, { Role: role, Actions: updated.Actions })
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setPermissions(permissions)
    }
  }, [permissions])

  const showConfirm = useCallback(() => {
    setConfirmOpen(false)
    confirmActionRef.current?.()
    confirmActionRef.current = null
  }, [])

  return (
    <div className="flex flex-col gap-3">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2 text-sm">
          <UserCircle className="size-4 text-muted-foreground" />
          <span className="font-medium">{account?.DisplayName || account?.Username || t('settings.user.currentUserFallback')}</span>
          {account?.Roles.map(r => <Badge key={r} variant={roleBadgeVariant(r)}>{r}</Badge>)}
        </div>
        <Button type="button" variant="ghost" size="sm" onClick={handleLogout}>
          <LogOut /> {t('settings.user.signOut')}
        </Button>
      </div>

      {/* Tabs */}
      <TabsRoot value={activeTab} onValueChange={(v) => setActiveTab(v as Tab)}>
        <TabsList>
          <TabsTrigger value="accounts" data-guide-id="settings/account/tab-users">
            <UserCircle className="size-3.5" /> {t('settings.user.usersTab')}
          </TabsTrigger>
          <TabsTrigger value="groups" data-guide-id="settings/account/tab-groups">
            <Users className="size-3.5" /> {t('settings.user.groupsTab')}
          </TabsTrigger>
          <TabsTrigger value="permissions" data-guide-id="settings/account/tab-permissions">
            <Shield className="size-3.5" /> {t('settings.user.permissionsTab')}
          </TabsTrigger>
        </TabsList>
      </TabsRoot>

      {/* Toolbar */}
      <div className="flex items-center gap-2">
        <div className="relative flex-1">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            className="pl-8"
            value={searchQuery}
            onChange={e => setSearchQuery(e.target.value)}
            placeholder={t('settings.user.searchPlaceholder', { tab: t(activeTab === 'accounts' ? 'settings.user.usersTab' : activeTab === 'groups' ? 'settings.user.groupsTab' : 'settings.user.permissionsTab') })}
          />
        </div>
        {activeTab === 'accounts' && admin && (
          <Button type="button" size="sm" onClick={handleAddUser} data-guide-id="settings/account/add-user">
            <Plus /> {t('settings.user.addUser')}
          </Button>
        )}
        {activeTab === 'groups' && admin && (
          <Button type="button" size="sm" onClick={() => { setEditingGroup(null); setGroupDialogOpen(true) }} data-guide-id="settings/account/add-group">
            <Plus /> {t('settings.user.addGroup')}
          </Button>
        )}
      </div>

      {/* Error */}
      {error && (
        <p className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>
      )}

      {/* Content */}
      {loading ? (
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" /> {t('settings.user.loading')}
        </div>
      ) : activeTab === 'accounts' ? (
        <div className="flex flex-col gap-2">
          {filteredUsers.map(u => (
            <UserCard
              key={u.Id}
              user={u}
              admin={admin}
              currentUsername={account?.Username || ''}
              onEdit={() => handleEditUser(u)}
              onReset={() => { setResetTarget(u); setResetDialogOpen(true) }}
              onDelete={() => handleDeleteUser(u.Id)}
            />
          ))}
          {filteredUsers.length === 0 && (
            <p className="text-sm text-muted-foreground">{t('settings.user.noUsersFound')}</p>
          )}
        </div>
      ) : activeTab === 'groups' ? (
        <div className="flex flex-col gap-2">
          {filteredGroups.map(g => (
            <GroupCard
              key={g.Id}
              group={g}
              admin={admin}
              onEdit={() => { setEditingGroup(g); setGroupDialogOpen(true) }}
              onDelete={() => handleDeleteGroup(g.Id)}
            />
          ))}
          {filteredGroups.length === 0 && (
            <p className="text-sm text-muted-foreground">{t('settings.user.noGroupsFound')}</p>
          )}
        </div>
      ) : (
        <div className="flex flex-col gap-3">
          <div className="overflow-x-auto rounded-lg border border-border">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-border bg-muted/50">
                  <th className="px-3 py-2 text-left text-xs font-medium text-muted-foreground">{t('settings.user.roles')}</th>
                  {allActions.map(action => (
                    <th key={action} className="px-3 py-2 text-center text-xs font-medium text-muted-foreground">{action}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {permissions.Entries.map(entry => (
                  <tr key={entry.Role} className="border-b border-border last:border-b-0">
                    <td className="px-3 py-2"><Badge variant={roleBadgeVariant(entry.Role)}>{entry.Role}</Badge></td>
                    {allActions.map(action => (
                      <td key={action} className="px-3 py-2 text-center">
                        <Switch
                          size="sm"
                          checked={!!entry.Actions[action]}
                          disabled={!admin}
                          onCheckedChange={() => togglePermission(entry.Role, action)}
                          aria-label={`${entry.Role} ${action}`}
                          data-guide-id={`settings/account/perm/${entry.Role}/${action}`}
                        />
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <p className="text-xs text-muted-foreground">{t('settings.user.permissionsNote')}</p>
        </div>
      )}

      {/* Dialogs */}
      <UserDialog open={userDialogOpen} user={editingUser} onSave={handleSaveUser} onCancel={() => setUserDialogOpen(false)} />
      <GroupDialog open={groupDialogOpen} group={editingGroup} onSave={handleSaveGroup} onCancel={() => setGroupDialogOpen(false)} />
      {resetTarget && (
        <ResetPasswordDialog
          open={resetDialogOpen}
          userId={resetTarget.Id}
          username={resetTarget.Username}
          onSave={handleResetPassword}
          onCancel={() => setResetDialogOpen(false)}
        />
      )}
      <ConfirmDialog
        open={confirmOpen}
        title={confirmTitle}
        description={confirmDescription}
        confirmLabel={t('common.delete')}
        danger
        onConfirm={showConfirm}
        onCancel={() => setConfirmOpen(false)}
      />
    </div>
  )
}
