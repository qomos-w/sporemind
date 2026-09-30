import { useCallback, useEffect, useState } from 'react'
import { AlertTriangle, ArrowLeft, HardDrive, Loader2, RefreshCw, Server } from 'lucide-react'
import { useI18n } from '../../../i18n'
import { Modal } from '../../../ui/components/Modal'
import {
  listRemoteAgents,
  listRemoteConnections,
  remoteImportReplaceContext,
  type RemoteAgentBrief,
  type RemoteConnectionBrief,
} from '../../../application/remote-import'
import './RemoteImportModal.css'

export interface RemoteImportAgentRef {
  actorId: string
  displayName: string
}

export interface RemoteImportModalProps {
  /** Controls visibility; the modal renders nothing while closed. */
  open: boolean
  /** The local agent whose conversation history will be replaced. */
  agent: RemoteImportAgentRef | null
  onClose: () => void
}

type ImportStep = 'connection' | 'agent' | 'confirm'

/** Host bindings report auth/permission failures with 401/403 status codes. */
function isPermissionError(err: unknown): boolean {
  const message = String((err as Error)?.message ?? err ?? '')
  return /\b(401|403)\b/.test(message) || /permission|forbidden|denied|unauthor/i.test(message)
}

function errorText(err: unknown): string {
  return String((err as Error)?.message ?? err ?? '')
}

/**
 * Three-step wizard that imports a remote agent's context and REPLACES the
 * local agent's conversation history: pick a saved connection -> pick a remote
 * agent -> confirm. Data access goes through web/src/application/remote-import.
 */
export function RemoteImportModal({ open, agent, onClose }: RemoteImportModalProps) {
  const { t } = useI18n()

  const [step, setStep] = useState<ImportStep>('connection')
  const [connections, setConnections] = useState<RemoteConnectionBrief[]>([])
  const [connectionsLoading, setConnectionsLoading] = useState(false)
  const [connectionsError, setConnectionsError] = useState('')
  const [connection, setConnection] = useState<RemoteConnectionBrief | null>(null)

  const [remoteAgents, setRemoteAgents] = useState<RemoteAgentBrief[]>([])
  const [agentsLoading, setAgentsLoading] = useState(false)
  const [agentsError, setAgentsError] = useState('')
  const [agentsDenied, setAgentsDenied] = useState(false)
  const [remoteAgent, setRemoteAgent] = useState<RemoteAgentBrief | null>(null)

  const [importing, setImporting] = useState(false)
  const [importError, setImportError] = useState('')
  const [acceptedTurns, setAcceptedTurns] = useState<number | null>(null)

  const loadConnections = useCallback(async () => {
    setConnectionsLoading(true)
    setConnectionsError('')
    try {
      setConnections((await listRemoteConnections()) ?? [])
    } catch (err) {
      setConnections([])
      setConnectionsError(errorText(err))
    } finally {
      setConnectionsLoading(false)
    }
  }, [])

  const loadAgents = useCallback(async (connId: string) => {
    setAgentsLoading(true)
    setAgentsError('')
    setAgentsDenied(false)
    try {
      setRemoteAgents((await listRemoteAgents(connId)) ?? [])
    } catch (err) {
      setRemoteAgents([])
      setAgentsDenied(isPermissionError(err))
      setAgentsError(errorText(err))
    } finally {
      setAgentsLoading(false)
    }
  }, [])

  // Reset to the first step whenever the modal is (re)opened.
  useEffect(() => {
    if (!open) return
    setStep('connection')
    setConnections([])
    setConnectionsError('')
    setConnection(null)
    setRemoteAgents([])
    setAgentsError('')
    setAgentsDenied(false)
    setRemoteAgent(null)
    setImporting(false)
    setImportError('')
    setAcceptedTurns(null)
    void loadConnections()
  }, [open, loadConnections])

  const handlePickConnection = useCallback((conn: RemoteConnectionBrief) => {
    setConnection(conn)
    setRemoteAgent(null)
    setStep('agent')
    void loadAgents(conn.id)
  }, [loadAgents])

  const handlePickAgent = useCallback((picked: RemoteAgentBrief) => {
    setRemoteAgent(picked)
    setStep('confirm')
  }, [])

  const handleConfirm = useCallback(async () => {
    if (!connection || !remoteAgent || !agent) return
    setImporting(true)
    setImportError('')
    try {
      const result = await remoteImportReplaceContext(connection.id, remoteAgent.ActorId, agent.actorId)
      setAcceptedTurns(result?.acceptedTurns ?? 0)
    } catch (err) {
      setImportError(errorText(err))
    } finally {
      setImporting(false)
    }
  }, [agent, connection, remoteAgent])

  if (!open || !agent) return null

  const succeeded = acceptedTurns !== null

  const footer = succeeded ? (
    <div className="remote-import-footer">
      <div className="remote-import-footer-spacer" />
      <button type="button" className="modal-action modal-action--primary" onClick={onClose} data-testid="remote-import-close">
        {t('remoteImport.close')}
      </button>
    </div>
  ) : (
    <div className="remote-import-footer">
      {step !== 'connection' && (
        <button
          type="button"
          className="modal-action modal-action--secondary"
          onClick={() => setStep(step === 'confirm' ? 'agent' : 'connection')}
          data-testid="remote-import-back"
        >
          <ArrowLeft size={14} />
          {t('remoteImport.back')}
        </button>
      )}
      <div className="remote-import-footer-spacer" />
      <button type="button" className="modal-action modal-action--secondary" onClick={onClose} data-testid="remote-import-cancel">
        {t('remoteImport.cancel')}
      </button>
      {step === 'confirm' && (
        <button
          type="button"
          className="modal-action modal-action--primary"
          disabled={importing}
          onClick={() => { void handleConfirm() }}
          data-testid="remote-import-confirm"
        >
          {importing ? (
            <>
              <Loader2 size={14} className="spin" />
              {t('remoteImport.importing')}
            </>
          ) : (
            t('remoteImport.confirm')
          )}
        </button>
      )}
    </div>
  )

  return (
    <Modal open={open} title={t('remoteImport.title')} onClose={onClose} size="sm" footer={footer}>
      <div className="remote-import" data-testid="remote-import-modal">
        {succeeded ? (
          <div className="remote-import-success" data-testid="remote-import-success" role="status">
            {t('remoteImport.success', { count: acceptedTurns ?? 0 })}
          </div>
        ) : step === 'connection' ? (
          <div className="remote-import-step" data-testid="remote-import-connection-step">
            <div className="remote-import-heading">{t('remoteImport.chooseConnection')}</div>
            {connectionsLoading ? (
              <div className="remote-import-loading" data-testid="remote-import-connections-loading">
                <Loader2 size={14} className="spin" />
                {t('remoteImport.loading')}
              </div>
            ) : connectionsError ? (
              <div className="remote-import-error" role="alert" data-testid="remote-import-error">
                <AlertTriangle size={14} />
                <span>{t('remoteImport.failed')}</span>
                <button type="button" className="btn-ghost" onClick={() => { void loadConnections() }} data-testid="remote-import-retry">
                  <RefreshCw size={12} />
                  {t('remoteImport.retry')}
                </button>
              </div>
            ) : connections.length === 0 ? (
              <div className="remote-import-empty" data-testid="remote-import-no-connections">
                <div>{t('remoteImport.noConnections')}</div>
                <div className="remote-import-hint">{t('remoteImport.noConnectionsHint')}</div>
              </div>
            ) : (
              <div className="remote-import-list">
                {connections.map(conn => (
                  <button
                    key={conn.id}
                    type="button"
                    className="remote-import-item"
                    onClick={() => handlePickConnection(conn)}
                    data-testid={`remote-import-connection-${conn.id}`}
                  >
                    <Server size={14} />
                    <span className="remote-import-item-text">
                      <span className="remote-import-item-name">{conn.name}</span>
                      <span className="remote-import-item-sub">{conn.host}:{conn.port}</span>
                    </span>
                  </button>
                ))}
              </div>
            )}
          </div>
        ) : step === 'agent' ? (
          <div className="remote-import-step" data-testid="remote-import-agent-step">
            <div className="remote-import-heading">{t('remoteImport.chooseAgent')}</div>
            {agentsLoading ? (
              <div className="remote-import-loading" data-testid="remote-import-agents-loading">
                <Loader2 size={14} className="spin" />
                {t('remoteImport.loading')}
              </div>
            ) : agentsError ? (
              <div className="remote-import-error" role="alert" data-testid="remote-import-error">
                <AlertTriangle size={14} />
                <span>{agentsDenied ? t('remoteImport.permissionDenied') : t('remoteImport.failed')}</span>
                <button
                  type="button"
                  className="btn-ghost"
                  onClick={() => { if (connection) void loadAgents(connection.id) }}
                  data-testid="remote-import-retry"
                >
                  <RefreshCw size={12} />
                  {t('remoteImport.retry')}
                </button>
              </div>
            ) : remoteAgents.length === 0 ? (
              <div className="remote-import-empty" data-testid="remote-import-no-agents">
                <div>{t('remoteImport.chooseAgent')}</div>
              </div>
            ) : (
              <div className="remote-import-list">
                {remoteAgents.map(ra => (
                  <button
                    key={ra.ActorId}
                    type="button"
                    className="remote-import-item"
                    onClick={() => handlePickAgent(ra)}
                    data-testid={`remote-import-agent-${ra.ActorId}`}
                  >
                    <Server size={14} />
                    <span className="remote-import-item-text">
                      <span className="remote-import-item-name">{ra.DisplayName || ra.ActorId}</span>
                      {ra.ProjectName ? <span className="remote-import-item-sub">{ra.ProjectName}</span> : null}
                    </span>
                  </button>
                ))}
              </div>
            )}
          </div>
        ) : (
          <div className="remote-import-step" data-testid="remote-import-confirm-step">
            <div className="remote-import-heading">{t('remoteImport.remoteAgent')}</div>
            <div className="remote-import-confirm-agent" data-testid="remote-import-confirm-agent">
              <HardDrive size={14} />
              <span>{remoteAgent?.DisplayName || remoteAgent?.ActorId || ''}</span>
            </div>
            <div className="remote-import-warning" data-testid="remote-import-warning">
              <AlertTriangle size={14} />
              <span>{t('remoteImport.replaceWarning', { agent: agent.displayName })}</span>
            </div>
            {importError ? (
              <div className="remote-import-error" role="alert" data-testid="remote-import-error">
                <AlertTriangle size={14} />
                <span>{t('remoteImport.failed')}</span>
              </div>
            ) : null}
          </div>
        )}
      </div>
    </Modal>
  )
}
