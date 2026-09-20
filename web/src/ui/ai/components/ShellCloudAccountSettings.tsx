import { useCallback, useEffect, useState } from 'react'
import { Loader2, Cloud, Unlink, AlertCircle, Ticket, RefreshCw } from 'lucide-react'
import { client } from '../../../application/generated-client'
import { isWails } from '../../../application/runtime'
import * as cloudaccount from '../../../gen-clients/cloudaccount/client'
import type {
  CloudAccountStatus,
  CloudAccountGetEntitlementsResp,
} from '../../../gen-clients/system/types'
import { useI18n } from '../../../i18n'
import { FeatureCard } from '../../settings/shadcn/composites'
import { Button, Input } from '../../settings/shadcn/ui'
import { CloudLoginOverlay } from './CloudLoginOverlay'
import { ExpBadge, TierBadge, resolveAccountTierState } from './tier-badges'
import { refreshInsiderAccess } from '../hooks/useInsiderAccess'
import './ShellCloudAccountSettings.css'

/**
 * ShellCloudAccountSettings is the cloud-account section of the settings panel
 * (M3-6). It shows the current sporemind cloud linkage — avatar, display name,
 * subscription badges and CDKey redemption — and lets the user link (open the
 * login overlay, M3-3) or unlink their cloud account. State is read via
 * cloudaccount.status / cloudaccount.get_entitlements; the OAuth token never
 * crosses this boundary.
 */
export function ShellCloudAccountSettings() {
  const { t } = useI18n()
  const [status, setStatus] = useState<CloudAccountStatus | null>(null)
  const [entitlements, setEntitlements] = useState<CloudAccountGetEntitlementsResp | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [overlayOpen, setOverlayOpen] = useState(false)
  const [redeemCode, setRedeemCode] = useState('')
  const [redeemBusy, setRedeemBusy] = useState(false)
  const [redeemMsg, setRedeemMsg] = useState('')
  const [redeemErr, setRedeemErr] = useState('')
  const [resyncBusy, setResyncBusy] = useState(false)

  const refresh = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const st = await cloudaccount.status(client)
      setStatus(st)
      if (st.Linked) {
        try {
          const ent = await cloudaccount.getEntitlements(client)
          setEntitlements(ent)
        } catch {
          setEntitlements(null)
        }
      } else {
        setEntitlements(null)
      }
      // Keep the shared Insider gate (browser-crawl, %-mention) in sync with
      // tier changes observed here (link / unlink / redeem).
      void refreshInsiderAccess()
    } catch (e: unknown) {
      setError(String(e instanceof Error ? e.message : e))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void refresh()
  }, [refresh])

  async function handleUnlink() {
    setBusy(true)
    setError('')
    try {
      await cloudaccount.unlink(client)
      await refresh()
    } catch (e: unknown) {
      setError(String(e instanceof Error ? e.message : e))
    } finally {
      setBusy(false)
    }
  }

  // Redeems a CDKey for the linked account. The actor re-syncs entitlements
  // after a successful redeem, so a plain refresh() reflects the new tier.
  async function handleRedeem() {
    setRedeemBusy(true)
    setRedeemMsg('')
    setRedeemErr('')
    try {
      const resp = await cloudaccount.redeem(client, { Code: redeemCode })
      const expires = resp.EndsAt ? new Date(resp.EndsAt).toLocaleDateString() : ''
      setRedeemMsg(t('settings.cloudAccount.redeem.success', { days: resp.PeriodDays, expires }))
      setRedeemCode('')
      await refresh()
    } catch (e: unknown) {
      setRedeemErr(String(e instanceof Error ? e.message : e))
    } finally {
      setRedeemBusy(false)
    }
  }

  // Manual cloud resync (cloudaccount.sync): forces a token-refresh +
  // entitlements re-fetch on the actor instead of waiting for the background
  // loop. The response is the fresh status; entitlements re-read follows so
  // tier badges update from the actor's cache.
  async function handleResync() {
    setResyncBusy(true)
    setError('')
    try {
      const st = await cloudaccount.sync(client)
      setStatus(st)
      if (st.Linked) {
        try {
          setEntitlements(await cloudaccount.getEntitlements(client))
        } catch {
          setEntitlements(null)
        }
      } else {
        setEntitlements(null)
      }
    } catch (e: unknown) {
      setError(String(e instanceof Error ? e.message : e))
    } finally {
      setResyncBusy(false)
    }
  }

  const linked = !!status?.Linked

  // Three-state subscription badge: Ultimate (paid-purple, includes Insider),
  // active Insider pass (gold + expiry, 9999 = permanent), free (gray).
  const tierState = resolveAccountTierState(status?.Tier, entitlements?.Entitlements?.Pass)

  return (
    <FeatureCard icon={<Cloud size={16} />} title={t('settings.cloudAccount.title')} description={t('settings.cloudAccount.desc')}>
      {error && (
        <div className="cloud-account-error">
          <AlertCircle size={14} /> {error}
        </div>
      )}

      {loading && !status ? (
        <div className="cloud-account-loading">
          <Loader2 size={16} className="spin" /> {t('settings.cloudAccount.loading')}
        </div>
      ) : linked ? (
        <div className="cloud-account-linked">
          <div className="cloud-account-id">
            <CloudAccountAvatar url={status?.AvatarUrl} />
            <div className="cloud-account-id-text">
              <div className="cloud-account-name">{status?.DisplayName || t('settings.cloudAccount.unknownUser')}</div>
              <div className="cloud-account-tier">
                {tierState.kind === 'ultimate' && (
                  <>
                    <TierBadge tier="ultimate" label={t('settings.cloudAccount.subscription.ultimate')} />
                    <ExpBadge label={t('settings.cloudAccount.subscription.experimental')} />
                  </>
                )}
                {tierState.kind === 'insider' && (
                  <ExpBadge
                    label={t('settings.cloudAccount.subscription.insider', {
                      expires: tierState.permanent
                        ? t('settings.cloudAccount.subscription.permanent')
                        : formatPassExpiry(tierState.endsAt),
                    })}
                    title={tierState.endsAt ? tierState.endsAt : undefined}
                  />
                )}
                {tierState.kind === 'free' && (
                  <TierBadge tier="free" label={t('settings.cloudAccount.subscription.free')} />
                )}
                {status?.EntitlementsDegraded && (
                  <span
                    className="cloud-account-badge degraded"
                    title={t('settings.cloudAccount.degradedHint')}
                  >
                    {t('settings.cloudAccount.degraded')}
                    <button
                      type="button"
                      className="cloud-account-badge-resync"
                      onClick={() => void handleResync()}
                      disabled={resyncBusy}
                      title={t('settings.cloudAccount.resync')}
                    >
                      {resyncBusy ? <Loader2 size={11} className="spin" /> : <RefreshCw size={11} />}
                    </button>
                  </span>
                )}
              </div>
            </div>
          </div>

          <div className="cloud-account-redeem">
            <div className="cloud-account-redeem-label">
              <Ticket size={13} /> {t('settings.cloudAccount.redeem.title')}
            </div>
            <div className="cloud-account-redeem-row">
              <Input
                value={redeemCode}
                onChange={(e) => { setRedeemCode(e.target.value); setRedeemErr(''); setRedeemMsg('') }}
                placeholder={t('settings.cloudAccount.redeem.placeholder')}
                disabled={redeemBusy}
                data-guide-id="settings/account/redeem-code"
              />
              <Button
                type="button"
                size="sm"
                onClick={() => void handleRedeem()}
                disabled={redeemBusy || !redeemCode.trim()}
                data-guide-id="settings/account/redeem-submit"
              >
                {redeemBusy ? <Loader2 size={14} className="spin" /> : <Ticket size={14} />}
                {t('settings.cloudAccount.redeem.submit')}
              </Button>
            </div>
            {redeemMsg && <div className="cloud-account-redeem-msg success">{redeemMsg}</div>}
            {redeemErr && <div className="cloud-account-redeem-msg error">{redeemErr}</div>}
          </div>

          <div className="cloud-account-actions">
            <Button
              type="button"
              variant="destructive"
              size="sm"
              onClick={handleUnlink}
              disabled={busy}
              data-guide-id="settings/account/cloud-unlink"
            >
              {busy ? <Loader2 size={14} className="spin" /> : <Unlink size={14} />}
              {t('settings.cloudAccount.unlink')}
            </Button>
          </div>
        </div>
      ) : (
        <div className="cloud-account-unlinked">
          <div className="cloud-account-unlinked-icon">
            <Cloud size={24} />
          </div>
          <div className="cloud-account-unlinked-text">
            <div className="cloud-account-name">{t('settings.cloudAccount.notLinkedTitle')}</div>
            <p className="cloud-account-head-desc">{t('settings.cloudAccount.notLinkedDesc')}</p>
          </div>
          <Button
            type="button"
            size="sm"
            onClick={() => setOverlayOpen(true)}
            disabled={!isWails()}
            title={!isWails() ? t('settings.cloudAccount.loginOverlay.desktopOnly') : undefined}
            data-guide-id="settings/account/cloud-link"
          >
            {t('settings.cloudAccount.link')}
          </Button>
        </div>
      )}

      <CloudLoginOverlay
        open={overlayOpen}
        onClose={() => setOverlayOpen(false)}
        onLinked={() => void refresh()}
      />
    </FeatureCard>
  )
}

function CloudAccountAvatar({ url }: { url?: string }) {
  const [failed, setFailed] = useState(false)
  if (!url || failed) {
    return (
      <div className="cloud-account-avatar placeholder">
        <Cloud size={18} />
      </div>
    )
  }
  return <img className="cloud-account-avatar" src={url} alt="" onError={() => setFailed(true)} />
}

/** Renders the pass expiry in the user's local time zone (display layer). */
function formatPassExpiry(endsAt?: string): string {
  if (!endsAt) return ''
  const date = new Date(endsAt)
  if (Number.isNaN(date.getTime())) return ''
  return date.toLocaleDateString()
}