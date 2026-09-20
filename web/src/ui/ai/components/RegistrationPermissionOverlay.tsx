import React from 'react'
import { ShieldCheck, Puzzle, PackagePlus, HardDriveDownload, CloudDownload, CircleDashed } from 'lucide-react'
import type { PermissionRequestFrame, TurnEnvelope } from '../model/frame-types'
import { extractPendingInteraction } from '../model/pending-interaction'
import { isRegistrationPermission } from '../model/app-registration'
import type { RegistrationHostPermissions } from '../model/registration-host-permissions'
import { registrationHostPermissionsFromFrame } from '../model/registration-host-permissions'
import { useBrowserOverlay } from '../browserOverlay'
import { useStepInteraction } from '../hooks/useStepInteraction'
import { getCapabilityInfo } from '../../../application/capability-catalog'
import type { I18nKey } from '../../../i18n/types'
import { useI18n } from '../../../i18n'
import './RegistrationPermissionOverlay.css'

interface RegistrationPermissionOverlayProps {
  /** Envelopes of the active conversation; the overlay derives the pending
   * registration permission frame from them. */
  envelopes: TurnEnvelope[]
}

interface PermissionMeta {
  icon: React.ComponentType<{ size?: number | string; className?: string }>
  titleKey:
    | 'ai.overlay.registration.perm.register'
    | 'ai.overlay.registration.perm.register_project'
    | 'ai.overlay.registration.perm.install_local'
    | 'ai.overlay.registration.perm.content_install'
    | 'ai.overlay.registration.perm.unknown'
}

// Android-install style: each intercepted callable renders as one
// icon + title row; the raw payload stays available under Details.
const PERMISSION_META: Record<string, PermissionMeta> = {
  'appmanager.register': { icon: Puzzle, titleKey: 'ai.overlay.registration.perm.register' },
  'appmanager.register_project': { icon: PackagePlus, titleKey: 'ai.overlay.registration.perm.register_project' },
  'appmanager.install_local': { icon: HardDriveDownload, titleKey: 'ai.overlay.registration.perm.install_local' },
  'cloudaccount.content_install': { icon: CloudDownload, titleKey: 'ai.overlay.registration.perm.content_install' },
}

function formatInput(input?: string): string {
  if (!input) return ''
  try {
    const parsed: unknown = JSON.parse(input)
    const pretty = JSON.stringify(parsed, null, 2) ?? ''
    return pretty.length > 2000 ? pretty.slice(0, 2000) + '\n…' : pretty
  } catch {
    return input.length > 2000 ? input.slice(0, 2000) + '…' : input
  }
}

// Compact one-line summary of the payload: the first identifying field
// (AppDir / Name / Path / ID) so the user sees what is being registered.
function summarizeInput(input?: string): string {
  if (!input) return ''
  try {
    const parsed = JSON.parse(input) as Record<string, unknown>
    for (const key of ['AppDir', 'Dir', 'Name', 'Path', 'AppID', 'ID', 'ContentID']) {
      const v = parsed[key]
      if (typeof v === 'string' && v) return v
    }
    return ''
  } catch {
    return ''
  }
}

const HOST_PERM_RISK_ORDER: Record<string, number> = { high: 0, medium: 1, low: 2 }

const RISK_KEY: Record<string, I18nKey> = {
  high: 'capability.risk.high',
  medium: 'capability.risk.medium',
  low: 'capability.risk.low',
  unknown: 'capability.risk.unknown',
}

/** Host-capability rows for one intercepted call, worst risk first. */
function hostPermissionIds(group: RegistrationHostPermissions): string[] {
  return [...group.permissions].sort((a, b) => {
    const ra = HOST_PERM_RISK_ORDER[getCapabilityInfo(a)?.riskLevel ?? 'high'] ?? -1
    const rb = HOST_PERM_RISK_ORDER[getCapabilityInfo(b)?.riskLevel ?? 'high'] ?? -1
    if (ra !== rb) return ra - rb
    return a.localeCompare(b)
  })
}

/**
 * Authorization overlay for the forced app-registration interception. The
 * turn engine suspends the agent's turn on the permission interaction for
 * every registration callable (any permission mode); this overlay is the
 * confirm/cancel surface, while the timeline ai-step shows the waiting
 * state. Floating above the embedded browser panes, so it must register
 * with the BrowserOverlayManager via useBrowserOverlay. Each intercepted
 * call renders as one card: app identity headline, action line, then the
 * 系统权限 it grants as risk-sorted chips derived from fields the turn
 * engine attached to the permission event at interception time.
 */
export const RegistrationPermissionOverlay: React.FC<RegistrationPermissionOverlayProps> = ({ envelopes }) => {
  const { t } = useI18n()
  const pendingInteraction = React.useMemo(() => extractPendingInteraction(envelopes), [envelopes])
  const frame: PermissionRequestFrame | null =
    pendingInteraction?.kind === 'permission_request' && isRegistrationPermission(pendingInteraction.frame)
      ? pendingInteraction.frame
      : null
  const open = frame != null
  const requestId = frame?.requestId ?? ''
  const { submitPermission, submitting } = useStepInteraction(requestId)
  const groupsByCallId = React.useMemo(() => {
    const map = new Map<string, RegistrationHostPermissions>()
    if (frame) for (const g of frame.toolCalls.map(registrationHostPermissionsFromFrame)) map.set(g.callId, g)
    return map
  }, [frame])

  useBrowserOverlay(open)

  if (!open || !frame) return null

  const answer = (allowed: boolean) => {
    submitPermission(allowed)
  }

  return (
    <div className="reg-perm-overlay-backdrop" role="dialog" aria-modal="true">
      <div className="reg-perm-overlay">
        <div className="reg-perm-overlay-header">
          <ShieldCheck size={16} className="reg-perm-overlay-header-icon" />
          <h3 className="reg-perm-overlay-title">{t('ai.overlay.registration.title')}</h3>
        </div>
        <div className="reg-perm-overlay-body">
          <p className="reg-perm-overlay-desc">{t('ai.overlay.registration.description')}</p>
          {frame.toolCalls.map(call => {
            const meta = PERMISSION_META[call.callableId]
            const Icon = meta?.icon ?? CircleDashed
            const actionTitle = meta ? t(meta.titleKey) : t('ai.overlay.registration.perm.unknown')
            const summary = summarizeInput(call.input)
            const group = groupsByCallId.get(call.id)
            const appHeadline = group?.appName || group?.appId
            return (
              <div key={call.id} className="reg-perm-overlay-card">
                {appHeadline && (
                  <div className="reg-perm-overlay-app" title={group?.appId}>{appHeadline}</div>
                )}
                <div className="reg-perm-overlay-action">
                  <span className="reg-perm-overlay-action-icon"><Icon size={14} /></span>
                  <span className="reg-perm-overlay-action-title">{actionTitle}</span>
                  {summary && <span className="reg-perm-overlay-action-summary" title={summary}>{summary}</span>}
                </div>
                {!group || group.status === 'unavailable' ? (
                  <p className="reg-perm-overlay-note warn">
                    {t('ai.overlay.registration.hostPermissions.unavailable')}
                    {group?.note && <span className="reg-perm-overlay-note-reason"> · {group.note}</span>}
                  </p>
                ) : group.permissions.length === 0 ? (
                  <p className="reg-perm-overlay-note">{t('ai.overlay.registration.hostPermissions.none')}</p>
                ) : (
                  <div className="reg-perm-overlay-perms">
                    <span className="reg-perm-overlay-perms-label">{t('ai.overlay.registration.hostPermissions')}</span>
                    <div className="reg-perm-overlay-chips">
                      {hostPermissionIds(group).map(id => {
                        const info = getCapabilityInfo(id)
                        const risk = info?.riskLevel ?? 'unknown'
                        return (
                          <span
                            key={id}
                            className={`reg-perm-overlay-chip risk-${risk}`}
                            title={info ? `${t(info.titleKey as I18nKey)} · ${t(RISK_KEY[risk] ?? 'capability.risk.unknown')}\n${t(info.descriptionKey as I18nKey)}` : id}
                          >
                            <span className="reg-perm-overlay-chip-dot" />
                            {info ? t(info.titleKey as I18nKey) : id}
                          </span>
                        )
                      })}
                    </div>
                  </div>
                )}
              </div>
            )
          })}
          <details className="reg-perm-overlay-details">
            <summary>{t('ai.overlay.registration.details')}</summary>
            {frame.toolCalls.map(call => (
              <div key={call.id} className="reg-perm-overlay-detail-call">
                <span className="reg-perm-overlay-callable">{call.callableId}</span>
                {call.input && <pre className="reg-perm-overlay-input">{formatInput(call.input)}</pre>}
              </div>
            ))}
          </details>
        </div>
        <div className="reg-perm-overlay-footer">
          <button
            type="button"
            className="reg-perm-overlay-btn cancel"
            onClick={() => answer(false)}
            disabled={submitting}
          >
            {t('common.cancel')}
          </button>
          <button
            type="button"
            className="reg-perm-overlay-btn confirm"
            onClick={() => answer(true)}
            disabled={submitting}
          >
            {t('common.confirm')}
          </button>
        </div>
      </div>
    </div>
  )
}
