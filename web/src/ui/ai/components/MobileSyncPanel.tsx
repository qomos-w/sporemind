import React, { useCallback, useEffect, useMemo, useState } from 'react'
import { X, Copy, Smartphone, ExternalLink, Loader2, Play, Square, Trash2 } from 'lucide-react'
import QRCode from 'qrcode'
import { client } from '../../../application/generated-client'
import * as frpmanager from '../../../gen-clients/frpmanager/client'
import type { FrpInstance, FrpManagerListResp } from '../../../gen-types/frp'
import { GetLocalNetworkGatewayURL, GetLocalIPv4 } from '../../../bindings/github.com/qomos-w/sporemind/pkg/desktop/app'
import { useI18n } from '../../../i18n'
import { isWails } from '../../../application/runtime'
import { ConfirmDialog } from '../../components/ConfirmDialog'
import { GATEWAY_DEFAULT_PORT } from '../../../application/gateway'
import { loadDesktopConfig, saveDesktopConfig } from '../../../application/desktop-config'
import './MobileSyncPanel.css'

interface MobileSyncPanelProps {
  open: boolean
  onClose: () => void
  onOpenFrpSettings?: () => void
}

interface FrpFormState {
  name: string
  serverHost: string
  serverPort: string
  remotePort: string
  token: string
}

function isLanGatewayAddr(addr: string, bindAddrs: string[] = []): boolean {
  // Primary addr has a non-loopback host
  const idx = addr.lastIndexOf(':')
  const host = idx >= 0 ? addr.slice(0, idx) : addr
  if (host !== '0.0.0.0' && host !== '' && host !== '127.0.0.1' && host !== 'localhost') return true
  if (host === '0.0.0.0' || host === '') return true
  // Extra bind addrs have a non-loopback host (dual binding)
  for (const a of bindAddrs) {
    const i = a.lastIndexOf(':')
    const h = i >= 0 ? a.slice(0, i) : a
    if (h && h !== '127.0.0.1' && h !== 'localhost') return true
  }
  return false
}

function gatewayPortOf(addr: string): string {
  const idx = addr.lastIndexOf(':')
  const port = idx >= 0 ? addr.slice(idx + 1) : ''
  return port || String(GATEWAY_DEFAULT_PORT)
}

export const MobileSyncPanel: React.FC<MobileSyncPanelProps> = ({
  open,
  onClose,
  onOpenFrpSettings,
}) => {
  const { t } = useI18n()
  const [loading, setLoading] = useState(false)
  const [list, setList] = useState<FrpManagerListResp | null>(null)
  const [rawLocalUrl, setRawLocalUrl] = useState('')
  const [tab, setTab] = useState<'local' | 'online'>('online')
  const [connectQr, setConnectQr] = useState('')
  const [downloadQr, setDownloadQr] = useState('')
  const [form, setForm] = useState<FrpFormState>({
    name: '',
    serverHost: '',
    serverPort: '7000',
    remotePort: '',
    token: '',
  })
  const [busy, setBusy] = useState(false)
  const [busyId, setBusyId] = useState('')
  const [message, setMessage] = useState('')
  const [lanAccess, setLanAccess] = useState<boolean | null>(null)
  const [lanBusy, setLanBusy] = useState(false)
  const [lanSaved, setLanSaved] = useState(false)
  const [lanConfirmOpen, setLanConfirmOpen] = useState(false)

  const localUrl = useMemo(() => {
    if (rawLocalUrl) return rawLocalUrl
    if (typeof window !== 'undefined') return window.location.origin
    return ''
  }, [rawLocalUrl])

  const onlineUrl = useMemo(() => getOnlineUrl(list), [list])

  // After toggling LAN access the in-memory config is updated immediately,
  // so GetLocalNetworkGatewayURL returns the LAN URL right away — but the
  // gateway HTTP server is still bound to the old address until restart.
  // Suppress the local URL until the user actually restarts.
  const localUrlEffective = lanSaved ? '' : localUrl

  const selectedUrl = useMemo(() => {
    if (tab === 'online' && onlineUrl) return onlineUrl
    return localUrlEffective
  }, [tab, onlineUrl, localUrlEffective])

  useEffect(() => {
    if (onlineUrl && tab === 'local') setTab('online')
    if (!onlineUrl && tab === 'online') setTab('local')
  }, [onlineUrl, tab])

  const loadData = useCallback(async () => {
    const [resp, url] = await Promise.all([
      frpmanager.list(client).catch(() => null),
      isWails() ? GetLocalNetworkGatewayURL().catch(() => '') : Promise.resolve(''),
    ])
    setList(resp)
    setRawLocalUrl(url)
  }, [])

  useEffect(() => {
    if (!open) return
    let cancelled = false
    setLoading(true)
    setMessage('')
    setLanSaved(false)
    if (isWails()) {
      loadDesktopConfig()
        .then(cfg => { if (!cancelled) setLanAccess(isLanGatewayAddr(cfg.gatewayAddr, cfg.gatewayBindAddrs)) })
        .catch(() => { if (!cancelled) setLanAccess(null) })
    }
    const run = async () => {
      try {
        await loadData()
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    run()
    const interval = setInterval(() => {
      if (!cancelled) loadData()
    }, 3000)
    return () => {
      cancelled = true
      clearInterval(interval)
    }
  }, [open, loadData])

  useEffect(() => {
    if (!selectedUrl) {
      setConnectQr('')
      setDownloadQr('')
      return
    }
    let cancelled = false
    const downloadUrl = `${selectedUrl}/download/mobile.apk`
    Promise.all([
      QRCode.toDataURL(selectedUrl, { width: 200, margin: 2, errorCorrectionLevel: 'M' }),
      QRCode.toDataURL(downloadUrl, { width: 200, margin: 2, errorCorrectionLevel: 'M' }),
    ]).then(([connect, download]) => {
      if (cancelled) return
      setConnectQr(connect)
      setDownloadQr(download)
    })
    return () => { cancelled = true }
  }, [selectedUrl])

  const handleCopy = async (text: string) => {
    if (!text) return
    try {
      await navigator.clipboard.writeText(text)
      setMessage(t('mobileSync.copied'))
      setTimeout(() => setMessage(''), 1500)
    } catch {
      setMessage(t('mobileSync.copyFailed'))
    }
  }

  const saveLanAccess = async (next: boolean) => {
    setLanConfirmOpen(false)
    setLanBusy(true)
    setMessage('')
    try {
      const cfg = await loadDesktopConfig()
      const port = gatewayPortOf(cfg.gatewayAddr)
      // Keep primary addr on loopback so frp (127.0.0.1:port) always works.
      // Add the LAN IP as an extra bind addr for dual binding — no 0.0.0.0 exposure.
      const primaryAddr = `127.0.0.1:${port}`
      let bindAddrs: string[] = []
      if (next) {
        const lanIp = isWails() ? await GetLocalIPv4().catch(() => '') : ''
        if (!lanIp) {
          setMessage(t('mobileSync.lanAccessNoIp'))
          setLanBusy(false)
          return
        }
        bindAddrs = [`${lanIp}:${port}`]
      }
      await saveDesktopConfig({
        transport: cfg.transport,
        gatewayAddr: primaryAddr,
        gatewayBindAddrs: bindAddrs,
      })
      setLanAccess(next)
      setLanSaved(true)
    } catch (e) {
      const detail = e instanceof Error ? e.message : String(e)
      setMessage(t('mobileSync.lanAccessSaveFailed') + (detail ? `: ${detail}` : ''))
    } finally {
      setLanBusy(false)
    }
  }

  const handleToggleLan = () => {
    if (lanAccess === null || lanBusy) return
    if (!lanAccess) {
      setLanConfirmOpen(true)
      return
    }
    void saveLanAccess(false)
  }

  const handleCreateFrp = async () => {
    if (!form.serverHost || !form.remotePort) return
    setBusy(true)
    setMessage('')
    try {
      const serverAddr = `${form.serverHost}:${form.serverPort || '7000'}`
      const gatewayPort = list?.GatewayPort ?? GATEWAY_DEFAULT_PORT
      await frpmanager.create(client, {
        Name: form.name.trim() !== '' ? form.name.trim() : 'mobile-sync',
        ServerAddr: serverAddr,
        Token: form.token,
        Proxies: [{
          Name: 'gateway',
          Kind: 'tcp',
          LocalIP: '127.0.0.1',
          LocalPort: gatewayPort,
          RemotePort: parseInt(form.remotePort, 10),
        }],
      })
      setMessage(t('mobileSync.frpCreated'))
      await loadData()
      setForm({ name: '', serverHost: '', serverPort: '7000', remotePort: '', token: '' })
    } catch (e) {
      const detail = e instanceof Error ? e.message : String(e)
      setMessage(t('mobileSync.frpCreateFailed') + (detail ? `: ${detail}` : ''))
    } finally {
      setBusy(false)
    }
  }

  const handleStart = async (id: string) => {
    setBusyId(id)
    setMessage('')
    try {
      await frpmanager.start(client, { Id: id })
      await loadData()
    } catch (e) {
      const detail = e instanceof Error ? e.message : String(e)
      setMessage(t('mobileSync.frpStartFailed') + (detail ? `: ${detail}` : ''))
    } finally {
      setBusyId('')
    }
  }

  const handleStop = async (id: string) => {
    setBusyId(id)
    setMessage('')
    try {
      await frpmanager.stop(client, { Id: id })
      await loadData()
    } catch (e) {
      const detail = e instanceof Error ? e.message : String(e)
      setMessage(t('mobileSync.frpStopFailed') + (detail ? `: ${detail}` : ''))
    } finally {
      setBusyId('')
    }
  }

  const handleRemove = async (id: string, name: string) => {
    if (!window.confirm(t('mobileSync.frpRemoveConfirm', { name }))) return
    setBusyId(id)
    setMessage('')
    try {
      await frpmanager.remove(client, { Id: id })
      await loadData()
    } catch (e) {
      const detail = e instanceof Error ? e.message : String(e)
      setMessage(t('mobileSync.frpRemoveFailed') + (detail ? `: ${detail}` : ''))
    } finally {
      setBusyId('')
    }
  }

  if (!open) return null

  return (
    <div className="mobile-sync-backdrop" onClick={onClose}>
      <div className="mobile-sync-panel" onClick={e => e.stopPropagation()}>
        <div className="mobile-sync-header">
          <div className="mobile-sync-header-title">
            <Smartphone size={18} />
            <h2>{t('mobileSync.title')}</h2>
          </div>
          <button
            type="button"
            className="mobile-sync-close-btn"
            onClick={onClose}
            aria-label={t('common.close')}
          >
            <X size={16} />
          </button>
        </div>
        <div className="mobile-sync-body">
          {loading ? (
            <div className="mobile-sync-loading">
              <Loader2 size={16} className="mobile-sync-spinner" />
              {t('common.loading')}
            </div>
          ) : (
            <>
              {isWails() && lanAccess !== null && (
                <div className="mobile-sync-section">
                  <div className="mobile-sync-lan-row">
                    <div className="mobile-sync-lan-text">
                      <h3 className="mobile-sync-section-title">{t('mobileSync.lanAccess')}</h3>
                      <p className="mobile-sync-hint">{t('mobileSync.lanAccessDesc')}</p>
                    </div>
                    <button
                      type="button"
                      role="switch"
                      aria-checked={lanAccess}
                      aria-label={t('mobileSync.lanAccess')}
                      className={`mobile-sync-switch ${lanAccess ? 'on' : ''}`}
                      onClick={handleToggleLan}
                      disabled={lanBusy}
                    >
                      <span className="mobile-sync-switch-thumb" />
                    </button>
                  </div>
                  {lanSaved && (
                    <p className="mobile-sync-hint">{t('mobileSync.lanAccessSaved')}</p>
                  )}
                </div>
              )}

              <div className="mobile-sync-section">
                <h3 className="mobile-sync-section-title">{t('mobileSync.serverUrl')}</h3>
                <div className="mobile-sync-url-row">
                  <input
                    type="text"
                    readOnly
                    value={selectedUrl}
                    placeholder={t('mobileSync.noServerUrl')}
                    className="mobile-sync-url-input"
                  />
                  <button
                    type="button"
                    className="mobile-sync-icon-btn"
                    onClick={() => handleCopy(selectedUrl)}
                    disabled={!selectedUrl}
                    title={t('common.copy')}
                  >
                    <Copy size={14} />
                  </button>
                </div>
                <p className="mobile-sync-hint">{t('mobileSync.serverUrlHint')}</p>
              </div>

              <div className="mobile-sync-qr-grid">
                {lanSaved && !onlineUrl ? (
                  <p className="mobile-sync-hint" style={{ gridColumn: 'span 2' }}>
                    {t('mobileSync.lanAccessRestartRequired')}
                  </p>
                ) : (
                  <>
                    <div className="mobile-sync-qr">
                      <h3 className="mobile-sync-qr-title">{t('mobileSync.connectQr')}</h3>
                      {connectQr ? (
                        <img src={connectQr} alt={t('mobileSync.connectQr')} className="mobile-sync-qr-img" />
                      ) : (
                        <div className="mobile-sync-qr-placeholder">—</div>
                      )}
                      <p className="mobile-sync-qr-desc">{t('mobileSync.connectQrDesc')}</p>
                    </div>
                    <div className="mobile-sync-qr">
                      <h3 className="mobile-sync-qr-title">{t('mobileSync.downloadQr')}</h3>
                      {downloadQr ? (
                        <img src={downloadQr} alt={t('mobileSync.downloadQr')} className="mobile-sync-qr-img" />
                      ) : (
                        <div className="mobile-sync-qr-placeholder">—</div>
                      )}
                      <p className="mobile-sync-qr-desc">{t('mobileSync.downloadQrDesc')}</p>
                    </div>
                  </>
                )}
              </div>

              <div className="mobile-sync-section">
                <h3 className="mobile-sync-section-title">{t('mobileSync.quickFrp')}</h3>
                <div className="mobile-sync-form">
                  <input
                    type="text"
                    placeholder={t('mobileSync.frpName')}
                    value={form.name}
                    onChange={e => setForm({ ...form, name: e.target.value })}
                  />
                  <input
                    type="text"
                    placeholder={t('mobileSync.frpServerHost')}
                    value={form.serverHost}
                    onChange={e => setForm({ ...form, serverHost: e.target.value })}
                  />
                  <input
                    type="text"
                    placeholder={t('mobileSync.frpServerPort')}
                    value={form.serverPort}
                    onChange={e => setForm({ ...form, serverPort: e.target.value })}
                  />
                  <input
                    type="text"
                    placeholder={t('mobileSync.frpRemotePort')}
                    value={form.remotePort}
                    onChange={e => setForm({ ...form, remotePort: e.target.value })}
                  />
                  <input
                    type="password"
                    placeholder={t('mobileSync.frpToken')}
                    value={form.token}
                    onChange={e => setForm({ ...form, token: e.target.value })}
                  />
                  <button
                    type="button"
                    className="mobile-sync-primary-btn"
                    onClick={handleCreateFrp}
                    disabled={busy || !form.serverHost || !form.remotePort}
                  >
                    {busy ? (
                      <>
                        <Loader2 size={12} className="mobile-sync-spinner" />
                        {t('common.loading')}
                      </>
                    ) : (
                      t('mobileSync.createFrp')
                    )}
                  </button>
                </div>
                {onOpenFrpSettings && (
                  <button
                    type="button"
                    className="mobile-sync-link"
                    onClick={onOpenFrpSettings}
                  >
                    {t('mobileSync.openFrpSettings')}
                    <ExternalLink size={12} />
                  </button>
                )}
                {message && (
                  <div className={`mobile-sync-message ${message.includes(t('mobileSync.frpCreated')) || message.includes(t('mobileSync.copied')) ? '' : 'error'}`}>
                    {message}
                  </div>
                )}
              </div>

              <div className="mobile-sync-section">
                <h3 className="mobile-sync-section-title">{t('mobileSync.frpHistory')}</h3>
                {list?.Items.length === 0 ? (
                  <div className="mobile-sync-history-empty">{t('mobileSync.frpNoRecords')}</div>
                ) : (
                  <div className="mobile-sync-history-list">
                    {list?.Items.map(inst => {
                      const isRunning = inst.Status.Running
                      const error = inst.Status.Error
                      const isItemBusy = busyId === inst.Config.Id
                      const remoteUrl = getItemRemoteUrl(inst, list?.GatewayPort ?? GATEWAY_DEFAULT_PORT)
                      return (
                        <div key={inst.Config.Id} className="mobile-sync-history-item">
                          <div className="mobile-sync-history-main">
                            <div className="mobile-sync-history-name">{inst.Config.Name}</div>
                            <div className="mobile-sync-history-meta">
                              <span className={`mobile-sync-history-status ${isRunning ? 'running' : error ? 'error' : 'stopped'}`}>
                                {isRunning
                                  ? t('mobileSync.frpRunning')
                                  : error
                                    ? t('mobileSync.frpError', { message: error })
                                    : t('mobileSync.frpStopped')}
                              </span>
                              {remoteUrl && (
                                <span className="mobile-sync-history-remote">{remoteUrl}</span>
                              )}
                            </div>
                            {error && <div className="mobile-sync-history-error">{error}</div>}
                          </div>
                          <div className="mobile-sync-history-actions">
                            <button
                              type="button"
                              className="mobile-sync-history-btn"
                              onClick={() => (isRunning ? handleStop(inst.Config.Id) : handleStart(inst.Config.Id))}
                              disabled={isItemBusy}
                              title={isRunning ? t('mobileSync.frpStop') : t('mobileSync.frpStart')}
                            >
                              {isItemBusy ? (
                                <Loader2 size={12} className="mobile-sync-spinner" />
                              ) : isRunning ? (
                                <Square size={12} />
                              ) : (
                                <Play size={12} />
                              )}
                              {isRunning ? t('mobileSync.frpStop') : t('mobileSync.frpStart')}
                            </button>
                            <button
                              type="button"
                              className="mobile-sync-history-btn danger"
                              onClick={() => handleRemove(inst.Config.Id, inst.Config.Name)}
                              disabled={isItemBusy}
                              title={t('mobileSync.frpRemove')}
                            >
                              {isItemBusy ? (
                                <Loader2 size={12} className="mobile-sync-spinner" />
                              ) : (
                                <Trash2 size={12} />
                              )}
                              {t('mobileSync.frpRemove')}
                            </button>
                          </div>
                        </div>
                      )
                    })}
                  </div>
                )}
              </div>
            </>
          )}
        </div>
      </div>
      <ConfirmDialog
        open={lanConfirmOpen}
        title={t('mobileSync.lanAccessConfirmTitle')}
        description={t('mobileSync.lanAccessConfirmDescription')}
        confirmLabel={t('mobileSync.lanAccessConfirm')}
        danger
        onConfirm={() => { void saveLanAccess(true) }}
        onCancel={() => setLanConfirmOpen(false)}
      />
    </div>
  )
}

function getOnlineUrl(list: FrpManagerListResp | null): string | null {
  if (!list) return null
  const gatewayPort = list.GatewayPort
  for (const inst of list.Items) {
    // Connected, not just Running: Running only means the frpc process is
    // alive — the QR link would be dead while it is still connecting or a
    // proxy failed to start.
    if (!inst.Status.Connected || inst.Config.Disabled) continue
    const url = resolveRemoteUrl(inst, gatewayPort)
    if (url) return url
  }
  return null
}

function parseHost(serverAddr: string): string {
  const idx = serverAddr.lastIndexOf(':')
  return idx > 0 ? serverAddr.slice(0, idx) : serverAddr
}

function getItemRemoteUrl(inst: FrpInstance, gatewayPort: number): string | null {
  return resolveRemoteUrl(inst, gatewayPort)
}

// resolveRemoteUrl picks the external URL for the instance's gateway tunnel.
// Prefer an explicit proxy entry matching the gateway port; fall back to the
// frp WebProxy mode (tcp only — http/https vhost URLs are not derivable from
// the config alone, so we don't guess them).
function resolveRemoteUrl(inst: FrpInstance, gatewayPort: number): string | null {
  const proxy = (inst.Config.Proxies ?? []).find(p => p.LocalPort === gatewayPort)
  let remotePort: number | undefined
  if (proxy) {
    remotePort = proxy.RemotePort
  } else {
    const wp = inst.Config.WebProxy
    if (wp?.Enabled && wp.Mode === 'tcp' && wp.RemotePort !== undefined) {
      remotePort = wp.RemotePort
    }
  }
  if (remotePort === undefined) return null
  const host = parseHost(inst.Config.ServerAddr)
  if (!host) return null
  return `http://${host}:${remotePort}`
}
