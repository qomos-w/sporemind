import React, { useState, useSyncExternalStore } from 'react'
import { Check, GitBranch, RefreshCw, File } from 'lucide-react'
import { useI18n } from '../../../i18n'
import { gitStore } from '../../panels/git-store'
import type { GitFileStatus } from '../../../gen-clients/system/types'
import './GitQuickbar.css'

interface GitQuickbarProps {
  projectId: string | null | undefined
  onOpenDiff?: (filePath: string, diffContent: string) => void
  /** Callback to open the full git mode panel. */
  onOpenGitMode?: () => void
}

export const GitQuickbar: React.FC<GitQuickbarProps> = ({
  projectId,
  onOpenDiff,
  onOpenGitMode,
}) => {
  useI18n()

  useSyncExternalStore(gitStore.subscribe.bind(gitStore), gitStore.getVersion)

  const [gitMenuOpen, setGitMenuOpen] = useState(false)

  const state = gitStore.state
  const gitBranch = state.branch || null
  const changedFiles = state.files
  const changedFilesCount = changedFiles.length
  const worktreeId = state.worktreeId

  // Only the project's own (local, non-trunk) branches — no remotes, no
  // local/remote sectioning, no master/main noise. The current branch is
  // already shown in the trigger button.
  const ownBranches = state.branches.filter(b => !b.IsRemote && b.Name !== 'master' && b.Name !== 'main')

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

  async function handleGitFileClick(f: GitFileStatus) {
    if (!projectId || !onOpenDiff) return
    setGitMenuOpen(false)
    const diff = await gitStore.selectFile(f)
    const diffContent = diff.join('\n')
    if (diffContent) {
      onOpenDiff(f.Path, diffContent)
    }
  }

  const handleOpenGitMode = () => {
    setGitMenuOpen(false)
    onOpenGitMode?.()
  }

  if (!projectId) return null

  return (
    <div className="ai-conversation-quickbar-git">
      {gitMenuOpen && (
        <>
          <div className="ai-conversation-quickbar-git-backdrop" onClick={() => setGitMenuOpen(false)} />
          <div className="ai-conversation-quickbar-git-dropdown">
            {changedFilesCount > 0 && (
              <>
                <div className="ai-conversation-quickbar-git-section">
                  <File size={10} />
                  Files ({changedFilesCount})
                </div>
                <div className="ai-conversation-quickbar-git-files">
                  {changedFiles.map(f => {
                    const ch = fileDisplayStatus(f)
                    return (
                      <div
                        key={f.Path}
                        className="ai-conversation-quickbar-git-file-item"
                        title={f.Path}
                        onClick={() => void handleGitFileClick(f)}
                        role="button"
                        tabIndex={0}
                        onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') void handleGitFileClick(f) }}
                      >
                        <span className="ai-conversation-quickbar-git-file-status" style={{ color: fileStatusColor(ch) }}>{ch}</span>
                        <span className="ai-conversation-quickbar-git-file-path">{f.Path}</span>
                      </div>
                    )
                  })}
                </div>
                <div className="ai-conversation-quickbar-git-sep" />
              </>
            )}
            <div className="ai-conversation-quickbar-git-section">
              <GitBranch size={10} />
              {gitBranch ?? 'Git'}
              {worktreeId && <span className="ai-conversation-quickbar-git-wt-badge">WT</span>}
            </div>
            <button className="ai-conversation-quickbar-git-item" type="button" onClick={() => { gitStore.refresh(); setGitMenuOpen(false) }}>
              <RefreshCw size={10} />
              Refresh Status
            </button>
            {ownBranches.length > 0 && (
              <>
                <div className="ai-conversation-quickbar-git-sep" />
                {ownBranches.map(b => (
                  <button
                    key={b.Name}
                    type="button"
                    className={`ai-conversation-quickbar-git-item${b.Current ? ' active' : ''}`}
                    onClick={() => {
                      setGitMenuOpen(false)
                      if (!b.Current) void gitStore.checkout(b.Name)
                    }}
                  >
                    <GitBranch size={10} />
                    <span className="ai-conversation-quickbar-git-item-label">{b.Name}</span>
                    <span className="ai-conversation-quickbar-git-item-hash">{b.Hash}</span>
                    {b.Current && <Check size={10} />}
                  </button>
                ))}
              </>
            )}
            <div className="ai-conversation-quickbar-git-sep" />
            <button className="ai-conversation-quickbar-git-item" type="button" onClick={handleOpenGitMode}>
              <GitBranch size={10} />
              Open Git Mode
            </button>
          </div>
        </>
      )}
      <button
        className="ai-conversation-quickbar-git-btn"
        type="button"
        title={gitBranch ? `Git: ${gitBranch}${changedFilesCount ? ` (${changedFilesCount} changes)` : ''}` : 'Git'}
        onClick={() => { gitStore.refresh(); setGitMenuOpen(v => !v) }}
      >
        <GitBranch size={10} />
        {gitBranch && <span className="ai-conversation-quickbar-git-branch">{gitBranch}</span>}
        {worktreeId && <span className="ai-conversation-quickbar-git-wt-badge">WT</span>}
        {changedFilesCount > 0 && <span className="ai-conversation-quickbar-git-badge">{changedFilesCount}</span>}
      </button>
    </div>
  )
}