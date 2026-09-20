import { useCallback, useState } from 'react'
import { LogIn, Loader2, AlertCircle } from 'lucide-react'
import { httpLogin } from '../../application/gateway'
import { connectClient, waitForClientReady, AUTH_CONNECTION_TIMEOUT_MS } from '../../application/generated-client'
import { setToken, setAccount, setRefreshToken, startProactiveRefreshTimer } from '../../application/auth-store'
import { AuthTitleBar } from './AuthTitleBar'
import './auth.css'
import { useI18n } from '../../i18n'

interface LoginPageProps {
  onLoggedIn: () => void
}

export function LoginPage({ onLoggedIn }: LoginPageProps) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const { t } = useI18n()

  const handleSubmit = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const resp = await httpLogin(username, password)
      setToken(resp.Token)
      setAccount(resp.Account)
      setRefreshToken(resp.RefreshToken)
      await connectClient()
      await waitForClientReady(AUTH_CONNECTION_TIMEOUT_MS)
      startProactiveRefreshTimer()
      onLoggedIn()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setLoading(false)
    }
  }, [username, password, onLoggedIn])

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') void handleSubmit()
  }

  return (
    <div className="auth-page">
      <AuthTitleBar />
      <div className="auth-page-body">
        <div className="auth-card">
        <div className="auth-header">
          <span className="auth-logo">sporemind</span>
          <span className="auth-title">Sign In</span>
        </div>

        {error && (
          <div className="auth-error">
            <AlertCircle size={14} />
            <span>{error}</span>
          </div>
        )}

        <div className="auth-field">
          <label>Username</label>
          <input
            value={username}
            onChange={e => setUsername(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder={t('auth.placeholder.username')}
            autoFocus
          />
        </div>

        <div className="auth-field">
          <label>Password</label>
          <input
            type="password"
            value={password}
            onChange={e => setPassword(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder={t('auth.placeholder.password')}
          />
        </div>

        <button
          className="auth-submit"
          onClick={() => void handleSubmit()}
          disabled={loading || !username.trim() || !password}
        >
          {loading ? <Loader2 size={16} className="spin" /> : <LogIn size={16} />}
          Sign In
        </button>
        </div>
      </div>
    </div>
  )
}
