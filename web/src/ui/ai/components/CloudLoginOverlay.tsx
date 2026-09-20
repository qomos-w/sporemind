import { useEffect, useRef, useState } from 'react'
import { Loader2, X, CheckCircle2, AlertCircle, Cloud } from 'lucide-react'
import { Events } from '@wailsio/runtime'
import { client } from '../../../application/generated-client'
import { isWails } from '../../../application/runtime'
import { useBrowserOverlay } from '../browserOverlay'
import * as cloudaccount from '../../../gen-clients/cloudaccount/client'
import { refreshInsiderAccess } from '../hooks/useInsiderAccess'
import type {
  CloudAccountLinkReq,
  CloudAccountStatus,
} from '../../../gen-clients/system/types'
import { OpenSystemLogin } from '../../../bindings/github.com/qomos-w/sporemind/pkg/desktop/app'
import { useI18n } from '../../../i18n'
import './CloudLoginOverlay.css'

interface CloudLoginOverlayProps {
  open: boolean
  onClose: () => void
  /** Called after the token has been stored in the cloudaccount actor. */
  onLinked?: (status: CloudAccountStatus) => void
}

type Phase = 'opening' | 'waiting' | 'linking' | 'success' | 'error'

// Raw token fields emitted by the desktop Go callback server (snake_case API
// names). See pkg/desktop/system_login.go loginTokenFields.
interface RawLoginToken {
  account_id: string
  access_token: string
  refresh_token: string
  expires_in: number
  display_name?: string
  avatar_url?: string
}

function isRawLoginToken(d: unknown): d is RawLoginToken {
  const r = d as Record<string, unknown> | undefined
  return !!r && typeof r.account_id === 'string' && typeof r.access_token === 'string' && typeof r.refresh_token === 'string'
}

/**
 * CloudLoginOverlay opens the sporemind login page in the system default
 * browser (pkg/desktop OpenSystemLogin), receives the OAuth token via the
 * "cloud-login:token" event (M3-2), and stores it in the cloudaccount actor
 * via cloudaccount.link. The React overlay is the status/launcher surface;
 * the actual login UI lives in the system browser. Existing LoginPage
 * (web/src/ui/auth/LoginPage.tsx) is left untouched.
 */
export function CloudLoginOverlay({ open, onClose, onLinked }: CloudLoginOverlayProps) {
  const { t } = useI18n()
  const [phase, setPhase] = useState<Phase>('opening')
  const [error, setError] = useState('')
  const closingRef = useRef(false)

  // Modal backdrop floating above the embedded browser window area — must be
  // registered with the BrowserOverlayManager (0→1 hides native panes).
  useBrowserOverlay(open)

  // Open the system default browser for login when the overlay becomes visible.
  useEffect(() => {
    if (!open) return
    closingRef.current = false
    setPhase('opening')
    setError('')

    if (!isWails()) {
      setPhase('error')
      setError(t('settings.cloudAccount.loginOverlay.desktopOnly'))
      return
    }

    let cancelled = false
    // Empty string => the Go side uses the configured sporemind login URL and
    // appends the local desktop_callback parameter.
    OpenSystemLogin('')
      .then(() => {
        if (!cancelled) setPhase('waiting')
      })
      .catch((e: unknown) => {
        if (!cancelled) {
          setPhase('error')
          setError(String(e instanceof Error ? e.message : e))
        }
      })

    return () => {
      cancelled = true
    }
  }, [open, t])

  // Listen for the token handed back from the login window.
  useEffect(() => {
    if (!open) return
    const off = Events.On('cloud-login:token', async (event) => {
      if (!isRawLoginToken((event as { data?: unknown })?.data)) return
      const raw = (event as { data: RawLoginToken }).data
      setPhase('linking')
      const req: CloudAccountLinkReq = {
        AccountId: raw.account_id,
        AccessToken: raw.access_token,
        RefreshToken: raw.refresh_token,
        ExpiresIn: raw.expires_in ?? 0,
        DisplayName: raw.display_name,
        AvatarUrl: raw.avatar_url,
      }
      try {
        const status = await cloudaccount.link(client, req)
        setPhase('success')
        void refreshInsiderAccess()
        onLinked?.(status)
        // Close the overlay after a short delay so the user can read the
        // success page in the system browser.
        window.setTimeout(() => handleClose(), 900)
      } catch (e: unknown) {
        setPhase('error')
        setError(String(e instanceof Error ? e.message : e))
      }
    })
    return () => {
      // @wailsio/runtime On returns an unsubscribe function.
      if (typeof off === 'function') off()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  function handleClose() {
    if (closingRef.current) return
    closingRef.current = true
    onClose()
  }

  if (!open) return null

  return (
    <div className="cloud-login-overlay-backdrop" role="dialog" aria-modal="true">
      <div className="cloud-login-overlay">
        <button className="cloud-login-overlay-close" onClick={handleClose} aria-label={t('settings.cloudAccount.loginOverlay.cancel')}>
          <X size={16} />
        </button>
        <div className="cloud-login-overlay-body">
          <div className="cloud-login-overlay-icon">
            <Cloud size={28} />
          </div>
          <h3 className="cloud-login-overlay-title">{t('settings.cloudAccount.loginOverlay.title')}</h3>

          {phase === 'opening' && (
            <p className="cloud-login-overlay-status">
              <Loader2 size={14} className="spin" /> {t('settings.cloudAccount.loginOverlay.opening')}
            </p>
          )}
          {phase === 'waiting' && (
            <p className="cloud-login-overlay-status">
              <Loader2 size={14} className="spin" /> {t('settings.cloudAccount.loginOverlay.waiting')}
            </p>
          )}
          {phase === 'linking' && (
            <p className="cloud-login-overlay-status">
              <Loader2 size={14} className="spin" /> {t('settings.cloudAccount.loginOverlay.linking')}
            </p>
          )}
          {phase === 'success' && (
            <p className="cloud-login-overlay-status ok">
              <CheckCircle2 size={14} /> {t('settings.cloudAccount.loginOverlay.success')}
            </p>
          )}
          {phase === 'error' && (
            <p className="cloud-login-overlay-status err">
              <AlertCircle size={14} /> {error || t('settings.cloudAccount.loginOverlay.error')}
            </p>
          )}
        </div>
        <div className="cloud-login-overlay-actions">
          <button type="button" className="cloud-login-overlay-btn" onClick={handleClose}>
            {t('settings.cloudAccount.loginOverlay.cancel')}
          </button>
        </div>
      </div>
    </div>
  )
}
