import React, { useCallback, useEffect, useRef, useState } from 'react'
import { ArrowUpCircle, Check, ChevronDown, File, GitBranch, RefreshCw, Sparkles, X } from 'lucide-react'
import type { GitFileStatus } from '../../../../gen-clients/system/types'
import { gitStore } from '../../../panels/git-store'
import * as workspace from '../../../../gen-clients/workspace/client'
import { client } from '../../../../application/generated-client'
import type { CompleteMessageReq } from '../../../../gen-clients/system/types'
import { completeMessage_meta } from '../../../../gen-clients/local/client'
import type { SummarizeResp } from '../../../../gen-clients/system/types'
import type { ProviderOption } from '../AIComposer'
import { ProviderIcon, iconKeyForModelRow } from '../providerIcons'
import { useViewportMode } from '../../../../application/useViewportMode'
import { MobileCardComposer } from '../MobileCardComposer'
import { useBrowserOverlay } from '../../browserOverlay'
import './CommitModal.css'
import { useI18n } from '../../../../i18n'

interface CommitModalProps {
  projectId: string
  open: boolean
  onClose: () => void
  /** Called with the commit message when user wants to commit */
  onCommit: (message: string) => void
  /** Active agent ActorId for AI commit message generation, or null if none */
  agentActorId: string | null
  /** Active agent display name */
  agentName?: string
  /** Available AI providers from the aggregator */
  providers?: ProviderOption[]
  /** Default active provider id from the shell context */
  activeProviderId?: string
}

const PROVIDER_STORAGE_KEY = 'sporemind:commit-gen-provider'

function providerStorageKey(agentActorId: string | null): string {
  return agentActorId ? `${PROVIDER_STORAGE_KEY}:${agentActorId}` : PROVIDER_STORAGE_KEY
}

export const CommitModal: React.FC<CommitModalProps> = ({
  projectId,
  open,
  onClose,
  onCommit,
  agentActorId,
  agentName,
  providers = [],
  activeProviderId,
}) => {
  const [files, setFiles] = useState<GitFileStatus[]>([])
  const [branch, setBranch] = useState('')
  const [commitMsg, setCommitMsg] = useState('')
  const [selectedFile, setSelectedFile] = useState<string | null>(null)
  const [diffContent, setDiffContent] = useState<string>('')
  const [diffLoading, setDiffLoading] = useState(false)
  const [committing, setCommitting] = useState(false)
  const [generating, setGenerating] = useState(false)
  const [genResult, setGenResult] = useState<string | null>(null)
  const [providerOpen, setProviderOpen] = useState(false)
  const [selectedProviderId, setSelectedProviderId] = useState<string>(activeProviderId ?? '')
  const msgRef = useRef<HTMLTextAreaElement>(null)
  const recognitionRef = useRef<any>(null)
  const [isRecording, setIsRecording] = useState(false)
  const [composerOpen, setComposerOpen] = useState(false)
  const mode = useViewportMode()
  const isMobile = mode === 'mobile'
  const { t } = useI18n()
  useBrowserOverlay(open)

  // Auto-resize commit message textarea as content grows.
  useEffect(() => {
    const el = msgRef.current
    if (!el) return
    el.style.height = 'auto'
    const lineHeight = parseInt(getComputedStyle(el).lineHeight) || 20
    const maxHeight = Math.max(lineHeight * 8, 160)
    el.style.height = `${Math.min(el.scrollHeight, maxHeight)}px`
  }, [commitMsg])

  // Restore serialized provider selection when the modal opens.
  useEffect(() => {
    if (!open) return
    const saved = localStorage.getItem(providerStorageKey(agentActorId))
    if (saved && providers.some(p => p.id === saved)) {
      setSelectedProviderId(saved)
      return
    }
    if (activeProviderId && providers.some(p => p.id === activeProviderId)) {
      setSelectedProviderId(activeProviderId)
      return
    }
    setSelectedProviderId(providers[0]?.id ?? '')
  }, [open, agentActorId, activeProviderId, providers])

  const selectedProvider = providers.find(p => p.id === selectedProviderId) ?? providers[0] ?? null

  // Reload files when modal opens
  useEffect(() => {
    if (!open) return
    const s = gitStore.state
    setFiles(s.files)
    setBranch(s.branch)
    setSelectedFile(null)
    setDiffContent('')
    setCommitMsg('')
    setGenResult(null)
    setCommitting(false)
    setGenerating(false)
  }, [open])

  // Clean up speech recognition on unmount / modal close
  useEffect(() => {
    if (!open && recognitionRef.current) {
      recognitionRef.current.stop()
      recognitionRef.current = null
      setIsRecording(false)
    }
  }, [open])

  // Watch git store for changes
  useEffect(() => {
    if (!open) return
    const unsub = gitStore.subscribe(() => {
      setFiles(gitStore.state.files)
      setBranch(gitStore.state.branch)
    })
    return unsub
  }, [open])

  // Load diff when file is selected
  useEffect(() => {
    if (!selectedFile || !projectId) {
      setDiffContent('')
      return
    }
    let cancelled = false
    setDiffLoading(true)
    workspace.gitDiff(client, { ProjectId: projectId, FilePath: selectedFile, CommitHash: '' })
      .then(resp => {
        if (!cancelled) {
          setDiffContent((resp.Diff ?? []).join('\n'))
          setDiffLoading(false)
        }
      })
      .catch(() => { if (!cancelled) { setDiffContent(''); setDiffLoading(false) } })
    return () => { cancelled = true }
  }, [selectedFile, projectId])

  // Stage/unstage a file
  const handleStageFile = useCallback(async (path: string) => {
    await gitStore.stageFile(path)
  }, [])

  // Stage all
  const handleStageAll = useCallback(async () => {
    await gitStore.stageAll()
  }, [])

  // Commit
  const handleCommit = useCallback(async () => {
    const msg = commitMsg.trim()
    if (!msg) return
    setCommitting(true)
    try {
      await Promise.resolve(onCommit(msg))
    } finally {
      setCommitting(false)
      onClose()
    }
  }, [commitMsg, onCommit, onClose])

  const handleProviderChange = useCallback((id: string) => {
    setSelectedProviderId(id)
    if (agentActorId) {
      localStorage.setItem(providerStorageKey(agentActorId), id)
    }
    setProviderOpen(false)
  }, [agentActorId])

  // Generate commit message with AI — clean non-streaming call, no turn loop pollution
  const handleGenerate = useCallback(async () => {
    if (!agentActorId) return

    setGenerating(true)
    setGenResult(null)
    setCommitMsg('')

    try {
      // Build diff summary
      const changedPaths = files.map(f => `  ${f.Staging !== ' ' ? f.Staging : f.Worktree} ${f.Path}`).join('\n')

      // Get full diff of all unstaged/staged changes
      let fullDiff = ''
      try {
        const resp = await Promise.all(
          files.map(f =>
            workspace.gitDiff(client, { ProjectId: projectId, FilePath: f.Path, CommitHash: '' })
              .then(r => ({ path: f.Path, diff: (r.Diff ?? []).join('\n') }))
              .catch(() => null),
          ),
        )
        fullDiff = resp.filter(Boolean).map(r => `${r!.path}\n${r!.diff}`).join('\n\n')
      } catch {
        fullDiff = t('dialog.commit.diffLoadError')
      }

      // Get recent commit log for context
      let recentCommits = ''
      try {
        const logResp = await workspace.gitLog(client, { ProjectId: projectId, Limit: 2 })
        recentCommits = logResp.Commits.map(c => `  ${c.Short} ${c.Message}`).join('\n')
      } catch { /* ok */ }

      const prompt = `Generate a concise git commit message for the following changes.

${recentCommits ? `Recent commits:\n${recentCommits}\n\n` : ''}Changes:
${changedPaths}

${fullDiff ? `Full diff:\n${fullDiff}` : ''}

Return ONLY the commit message, no explanation, no markdown formatting. Keep it under 72 characters per line. Use conventional commit format (e.g. "feat: ...", "fix: ...", "refactor: ...").`

      // Clean unary LLM call — no turn loop involvement
      const resp = await client.invoke<CompleteMessageReq, SummarizeResp>(
        'agent.complete_message',
        { UserText: prompt, System: '', Unit: selectedProvider?.unit },
        { reqSchemaId: completeMessage_meta.reqSchemaId, resSchemaId: completeMessage_meta.resSchemaId, target: agentActorId },
      )
      if (resp.Text?.trim()) {
        setCommitMsg(resp.Text.trim())
        setGenResult(null)
      } else {
        setGenResult(t('dialog.commit.aiEmpty'))
      }
    } catch (err) {
      setGenResult(t('dialog.commit.aiFailed', { error: err instanceof Error ? err.message : String(err) }))
    } finally {
      setGenerating(false)
    }
  }, [agentActorId, files, projectId, selectedProvider?.unit])

  // Voice input for commit message
  const handleVoiceToggle = useCallback(() => {
    if (isRecording) {
      recognitionRef.current?.stop()
      setIsRecording(false)
      return
    }

    const SpeechRecognitionAPI = (window as any).SpeechRecognition || (window as any).webkitSpeechRecognition
    if (!SpeechRecognitionAPI) {
      setGenResult(t('dialog.commit.speechNotSupported'))
      return
    }

    const recognition = new SpeechRecognitionAPI()
    recognition.lang = 'zh-CN'
    recognition.interimResults = true
    recognition.continuous = true

    recognition.onresult = (event: any) => {
      let transcript = ''
      for (let i = event.resultIndex; i < event.results.length; i++) {
        transcript += event.results[i][0].transcript
      }
      setCommitMsg(prev => prev + transcript)
    }

    recognition.onerror = () => {
      setIsRecording(false)
      setGenResult(t('dialog.commit.voiceError'))
    }

    recognition.onend = () => {
      setIsRecording(false)
    }

    recognitionRef.current = recognition
    recognition.start()
    setIsRecording(true)
    setGenResult(null)
  }, [isRecording])

  const stagedCount = files.filter(f => f.Staging !== ' ' && f.Staging !== '?').length
  const unstagedCount = files.filter(f => f.Worktree !== ' ' || f.Staging === '?').length

  if (!open) return null

  return (
    <div className="commit-modal-backdrop" onClick={onClose}>
      <div className="commit-modal" onClick={e => e.stopPropagation()}>
        {/* Header */}
        <div className="commit-modal-header">
          <div className="commit-modal-header-left">
            <GitBranch size={14} />
            <span className="commit-modal-branch">{branch || t('dialog.commit.noBranch')}</span>
            <span className="commit-modal-file-count">
              {stagedCount > 0 && <span className="commit-modal-count-staged">{t('dialog.commit.staged', { count: stagedCount })}</span>}
              {unstagedCount > 0 && <span className="commit-modal-count-unstaged">{t('dialog.commit.unstaged', { count: unstagedCount })}</span>}
              {files.length === 0 && <span className="commit-modal-count-clean">{t('dialog.commit.clean')}</span>}
            </span>
          </div>
          <button className="commit-modal-close-btn" type="button" onClick={onClose} title={t('dialog.commit.close')}>
            <X size={14} />
          </button>
        </div>

        <div className="commit-modal-body">
          {/* Left: File list */}
          <div className="commit-modal-files">
            <div className="commit-modal-files-header">
              <span>{t('dialog.commit.changedFiles')}</span>
              {files.length > 0 && (
                <button className="commit-modal-stage-all-btn" type="button" onClick={handleStageAll} title={t('dialog.commit.stageAll')}>
                  <Check size={12} /> {t('dialog.commit.stageAll')}
                </button>
              )}
            </div>
            <div className="commit-modal-files-list">
              {files.length === 0 && (
                <div className="commit-modal-files-empty">{t('dialog.commit.noChanges')}</div>
              )}
              {files.map(f => {
                const ch = fileDisplayStatus(f)
                const isSelected = selectedFile === f.Path
                const isStaged = f.Staging !== ' ' && f.Staging !== '?'
                return (
                  <div
                    key={f.Path}
                    className={`commit-modal-file-item ${isSelected ? 'selected' : ''}`}
                    onClick={() => setSelectedFile(isSelected ? null : f.Path)}
                  >
                    <span
                      className="commit-modal-file-status"
                      style={{ color: fileStatusColor(ch) }}
                      onClick={e => { e.stopPropagation(); handleStageFile(f.Path) }}
                      title={isStaged ? t('dialog.commit.unstage') : t('dialog.commit.stage')}
                    >
                      {isStaged ? <Check size={10} /> : ch}
                    </span>
                    <span className="commit-modal-file-path">{f.Path}</span>
                    <span className="commit-modal-file-action" onClick={e => { e.stopPropagation(); handleStageFile(f.Path) }}>
                      {isStaged ? t('dialog.commit.staged_') : t('dialog.commit.stage')}
                    </span>
                  </div>
                )
              })}
            </div>
          </div>

          {/* Right: Diff / Message */}
          <div className="commit-modal-right">
            {/* Diff preview */}
            {selectedFile ? (
              <div className="commit-modal-diff">
                <div className="commit-modal-diff-header">
                  <File size={12} />
                  {selectedFile}
                  <button className="commit-modal-diff-refresh" type="button" onClick={() => gitStore.refresh()} title={t('dialog.commit.refresh')}>
                    <RefreshCw size={11} />
                  </button>
                </div>
                <div className="commit-modal-diff-content">
                  {diffLoading ? (
                    <div className="commit-modal-diff-loading">{t('dialog.commit.loadingDiff')}</div>
                  ) : diffContent ? (
                    <pre className="commit-modal-diff-text">{diffContent}</pre>
                  ) : (
                    <div className="commit-modal-diff-empty">{t('dialog.commit.noDiff')}</div>
                  )}
                </div>
              </div>
            ) : (
              diffContent && (
                <div className="commit-modal-diff">
                  <div className="commit-modal-diff-header">
                    <File size={12} />
                    {selectedFile}
                  </div>
                  <div className="commit-modal-diff-content">
                    <pre className="commit-modal-diff-text">{diffContent}</pre>
                  </div>
                </div>
              )
            )}

            {!selectedFile && !diffContent && (
              <div className="commit-modal-diff-placeholder">
                {t('dialog.commit.diffPlaceholder')}
              </div>
            )}

            {/* Commit message area */}
            <div className="commit-modal-message-area">
              <div className="commit-modal-composer">
                <div className={`commit-modal-input-area ${isMobile ? 'commit-modal-input-area--mobile' : ''}`}>
                  {isMobile ? (
                    <button
                      type="button"
                      className={`commit-modal-message-input commit-modal-message-input--mobile ${commitMsg.trim() ? '' : 'commit-modal-message-input--placeholder'}`}
                      onClick={() => setComposerOpen(true)}
                    >
                      {commitMsg.trim() || t('dialog.commit.messagePlaceholder')}
                    </button>
                  ) : (
                    <textarea
                      ref={msgRef}
                      className="commit-modal-message-input"
                      placeholder={t('dialog.commit.messagePlaceholder')}
                      value={commitMsg}
                      onChange={e => setCommitMsg(e.target.value)}
                      onKeyDown={e => {
                        if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
                          e.preventDefault()
                          handleCommit()
                        }
                      }}
                    />
                  )}
                </div>
                <div className="commit-modal-actions">
                  {agentActorId && providers.length > 0 && (
                    <div className="commit-modal-provider">
                      {providerOpen && (
                        <>
                          <div className="commit-modal-provider-backdrop" onClick={() => setProviderOpen(false)} />
                          <div className="commit-modal-provider-dropdown">
                            <div className="commit-modal-provider-col-header">{t('dialog.commit.model')}</div>
                            {providers.map(p => (
                              <button
                                key={p.id}
                                className={`commit-modal-provider-item ${p.id === selectedProviderId ? 'active' : ''}`}
                                type="button"
                                onClick={() => handleProviderChange(p.id)}
                              >
                                {(() => {
                                  const iconKey = iconKeyForModelRow(p.label, p.endpoint, p.subtitle)
                                  return iconKey ? <ProviderIcon id={iconKey} size={12} className="commit-modal-provider-item-icon" /> : null
                                })()}
                                <span className="commit-modal-provider-item-label">{p.label}</span>
                                {p.subtitle && (
                                  <span className="commit-modal-provider-item-subtitle">{p.subtitle}</span>
                                )}
                                {p.id === selectedProviderId && <Check size={12} />}
                              </button>
                            ))}
                          </div>
                        </>
                      )}
                      <button
                        className="commit-modal-provider-btn"
                        type="button"
                        onClick={() => setProviderOpen(v => !v)}
                        disabled={generating}
                        title={t('dialog.commit.selectProvider')}
                      >
                        {(() => {
                          const iconKey = selectedProvider ? iconKeyForModelRow(selectedProvider.label, selectedProvider.endpoint, selectedProvider.subtitle) : undefined
                          return iconKey ? <ProviderIcon id={iconKey} size={12} className="commit-modal-provider-btn-icon" /> : null
                        })()}
                        <span className="commit-modal-provider-text">
                          <span className="commit-modal-provider-label">{selectedProvider?.label ?? t('dialog.commit.select')}</span>
                          {selectedProvider?.subtitle && (
                            <span className="commit-modal-provider-subtitle">{selectedProvider.subtitle}</span>
                          )}
                        </span>
                        <ChevronDown size={12} />
                      </button>
                    </div>
                  )}
                  <div className="commit-modal-actions-right">
                    {genResult && <span className="commit-modal-gen-result">{genResult}</span>}
                    {agentActorId && (
                      <button
                        className="commit-modal-gen-btn"
                        type="button"
                        onClick={handleGenerate}
                        disabled={generating}
                        title={t('dialog.commit.generateTitle', { name: agentName || 'AI' })}
                      >
                        <Sparkles size={14} />
                        <span className="commit-modal-gen-text">{generating ? t('dialog.commit.generating') : t('dialog.commit.generate')}</span>
                      </button>
                    )}
                    {!isMobile && (
                      <>
                        <button
                          className={`commit-modal-mic-btn ${isRecording ? 'recording' : ''}`}
                          type="button"
                          onClick={handleVoiceToggle}
                          title={isRecording ? t('dialog.commit.stopRecording') : t('dialog.commit.voiceInput')}
                        >
                          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                            <path d="M12 1a3 3 0 0 0-3 3v8a3 3 0 0 0 6 0V4a3 3 0 0 0-3-3z"/>
                            <path d="M19 10v2a7 7 0 0 1-14 0v-2"/>
                            <line x1="12" y1="19" x2="12" y2="23"/>
                            <line x1="8" y1="23" x2="16" y2="23"/>
                          </svg>
                        </button>
                        <button
                          className="commit-modal-commit-btn"
                          type="button"
                          onClick={handleCommit}
                          disabled={!commitMsg.trim() || committing}
                          title={committing ? t('dialog.commit.committing') : t('dialog.commit.commit')}
                        >
                          <ArrowUpCircle size={18} />
                        </button>
                      </>
                    )}
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
      <MobileCardComposer
        open={composerOpen}
        value={commitMsg}
        onChange={setCommitMsg}
        onSubmit={handleCommit}
        onClose={() => setComposerOpen(false)}
        title={t('dialog.commit.commit')}
        placeholder={t('dialog.commit.messagePlaceholder')}
        submitLabel={t('dialog.commit.commit')}
        multiline={true}
      />
    </div>
  )
}

function fileStatusColor(ch: string): string {
  if (ch === 'M') return 'var(--git-modified, #e2c08d)'
  if (ch === 'A') return 'var(--git-added, #81b88b)'
  if (ch === 'D') return 'var(--git-deleted, #c74e39)'
  if (ch === '?') return 'var(--git-untracked, #73c991)'
  return 'var(--text-secondary)'
}

function fileDisplayStatus(f: { Staging: string; Worktree: string }): string {
  const s = f.Staging !== ' ' ? f.Staging : f.Worktree
  return s === '?' ? 'U' : s
}
