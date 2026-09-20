import React, { useEffect, useState } from 'react'
import { useI18n } from '../../../i18n'
import { client } from '../../../application/generated-client'
import * as workspaceClient from '../../../gen-clients/workspace/client'
import { Modal } from '../../components/Modal'
import {
  Button,
  Input,
  SelectRoot,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
  SelectItemText,
} from '../../settings/shadcn/ui'

export interface AgentKindOption {
  kind: string
  displayName: string
}

export interface NewAgentKindDialogProps {
  open: boolean
  /** All known templates, used to populate the base-template dropdown. */
  kinds: AgentKindOption[]
  onClose: () => void
  /** Called with the created template's kind so the caller can select it. */
  onCreated: (kind: string) => void
}

// Backend accepts lowercase letters, digits and hyphens for the kind slug.
const SLUG_RE = /^[a-z0-9-]+$/
const DEFAULT_BASE_KIND = 'coder'

/**
 * Creates a custom agent template (agent kind) by cloning an existing
 * template's configuration. The backend owns slug/conflict validation; this
 * dialog only gates the obvious client-side cases and surfaces server errors.
 */
export const NewAgentKindDialog: React.FC<NewAgentKindDialogProps> = ({ open, kinds, onClose, onCreated }) => {
  const { t } = useI18n()
  const [slug, setSlug] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [baseKind, setBaseKind] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!open) return
    setSlug('')
    setDisplayName('')
    setBaseKind(kinds.find(k => k.kind === DEFAULT_BASE_KIND)?.kind ?? kinds[0]?.kind ?? '')
    setSubmitting(false)
    setError(null)
  }, [open, kinds])

  const slugTrimmed = slug.trim()
  const nameTrimmed = displayName.trim()
  const slugValid = slugTrimmed.length > 0 && SLUG_RE.test(slugTrimmed)
  const canSubmit = slugValid && nameTrimmed.length > 0 && !submitting

  const handleSubmit = async (event: React.FormEvent) => {
    event.preventDefault()
    if (!canSubmit) return
    setSubmitting(true)
    setError(null)
    try {
      const resp = await workspaceClient.createAgentKind(client, {
        Kind: slugTrimmed,
        DisplayName: nameTrimmed,
        BaseKind: baseKind || undefined,
      })
      onCreated(resp.Kind)
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      setSubmitting(false)
    }
  }

  return (
    <Modal open={open} title={t('settings.agent.template.newTitle')} onClose={onClose} size="sm">
      <form className="flex flex-col gap-4" onSubmit={handleSubmit}>
        <label className="flex flex-col gap-1 text-sm">
          <span className="font-medium">{t('settings.agent.template.kindSlug')}</span>
          <Input
            value={slug}
            onChange={e => setSlug(e.target.value)}
            placeholder={t('settings.agent.template.kindSlugPlaceholder')}
            autoFocus
            spellCheck={false}
            autoComplete="off"
            data-testid="new-kind-slug"
          />
        </label>

        <label className="flex flex-col gap-1 text-sm">
          <span className="font-medium">{t('settings.agent.template.displayName')}</span>
          <Input
            value={displayName}
            onChange={e => setDisplayName(e.target.value)}
            placeholder={t('settings.agent.template.displayNamePlaceholder')}
            data-testid="new-kind-display-name"
          />
        </label>

        <div className="flex flex-col gap-1 text-sm">
          <span className="font-medium">{t('settings.agent.template.baseKind')}</span>
          <SelectRoot
            value={baseKind}
            onValueChange={v => setBaseKind(v as string)}
            items={kinds.map(k => ({ value: k.kind, label: k.displayName }))}
          >
            <SelectTrigger className="w-full" aria-label={t('settings.agent.template.baseKind')} data-testid="new-kind-base">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {kinds.map(k => (
                <SelectItem key={k.kind} value={k.kind}>
                  <SelectItemText>{k.displayName}</SelectItemText>
                </SelectItem>
              ))}
            </SelectContent>
          </SelectRoot>
        </div>

        {slugTrimmed.length > 0 && !slugValid && (
          <p className="text-xs text-destructive">{t('settings.agent.template.invalidSlug')}</p>
        )}
        {error && (
          <p className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive" data-testid="new-kind-error">
            {error}
          </p>
        )}

        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" size="sm" onClick={onClose}>
            {t('common.cancel')}
          </Button>
          <Button type="submit" size="sm" disabled={!canSubmit} data-testid="new-kind-submit">
            {submitting ? t('settings.agent.template.creating') : t('settings.agent.template.create')}
          </Button>
        </div>
      </form>
    </Modal>
  )
}
