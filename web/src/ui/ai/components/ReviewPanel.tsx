import React, { useEffect, useState, useSyncExternalStore } from 'react'
import { FileText, GitBranch, RefreshCw } from 'lucide-react'
import { gitStore } from '../../panels/git-store'
import type { GitFileStatus } from '../../../gen-clients/system/types'
import { useAIShellContext } from '../context/AIShellContext'
import { client } from '../../../application/generated-client'
import * as workspace from '../../../gen-clients/workspace/client'
import { normalizePath } from './parts/tool-display'
import { useI18n } from '../../../i18n'
import './ReviewPanel.css'

function statusChar(f: GitFileStatus): string {
  const s = f.Staging !== ' ' ? f.Staging : f.Worktree
  return s === '?' ? '?' : s
}

function statusColor(ch: string): string {
  if (ch === 'M') return 'var(--git-modified, #e2c08d)'
  if (ch === 'A') return 'var(--git-added, #81b88b)'
  if (ch === 'D') return 'var(--git-deleted, #c74e39)'
  if (ch === '?') return 'var(--git-untracked, #73c991)'
  return 'var(--text-secondary)'
}

interface FileRowProps {
  file: GitFileStatus
  onClick: (file: GitFileStatus) => void
}

const FileRow: React.FC<FileRowProps> = ({ file, onClick }) => {
  const ch = statusChar(file)
  const path = normalizePath(file.Path) ?? file.Path
  const parts = path.split('/')
  const name = parts[parts.length - 1]
  const dir = parts.slice(0, -1).join('/')

  return (
    <button
      type="button"
      className="review-file-row"
      onClick={() => onClick(file)}
      title={path}
    >
      <span className="review-file-status" style={{ color: statusColor(ch) }}>{ch}</span>
      <span className="review-file-name">{name}</span>
      {dir && <span className="review-file-dir">{dir}</span>}
    </button>
  )
}

export const ReviewPanel: React.FC = () => {
  const { t } = useI18n()
  const _ = useSyncExternalStore(gitStore.subscribe.bind(gitStore), gitStore.getVersion)
  void _
  const { onOpenFile } = useAIShellContext()
  const [opening, setOpening] = useState<string | null>(null)

  useEffect(() => {
    void gitStore.loadProjects()
    gitStore.startMountListener()
    return () => { gitStore.stopMountListener() }
  }, [])

  const allFiles = gitStore.state.files
  const staged = allFiles.filter(f => f.Staging !== ' ' && f.Staging !== '?')
  const unstaged = allFiles.filter(f => f.Worktree !== ' ' || f.Staging === '?')

  const handleClick = async (file: GitFileStatus) => {
    if (!onOpenFile || !gitStore.state.projectId) return
    setOpening(file.Path)
    try {
      const result = await workspace.gitDiff(client, {
        ProjectId: gitStore.state.projectId,
        WorktreeID: gitStore.state.worktreeId ?? undefined,
        FilePath: file.Path,
        CommitHash: '',
      })
      onOpenFile(file.Path, result.Diff?.join('\n'))
    } catch {
      onOpenFile(file.Path)
    } finally {
      setOpening(null)
    }
  }

  const handleRefresh = () => {
    void gitStore.refresh()
  }

  return (
    <div className="review-panel">
      <div className="review-header">
        <div className="review-title">
          <FileText size={14} />
          <span>{t('reviewPanel.refresh')}</span>
        </div>
        <div className="review-actions">
          {gitStore.state.branch && (
            <span className="review-branch">
              <GitBranch size={10} />
              {gitStore.state.branch}
            </span>
          )}
          <button
            type="button"
            className="review-icon-btn"
            title="Refresh"
            onClick={handleRefresh}
            disabled={gitStore.state.loading}
          >
            <RefreshCw size={12} className={gitStore.state.loading ? 'spin' : ''} />
          </button>
        </div>
      </div>

      {gitStore.state.error && (
        <div className="review-error">{gitStore.state.error}</div>
      )}

      {!gitStore.state.projectId ? (
        <div className="review-empty">No active project.</div>
      ) : (
        <div className="review-body">
          {unstaged.length > 0 && (
            <div className="review-section">
              <div className="review-section-title">Changes ({unstaged.length})</div>
              <div className="review-section-body">
                {unstaged.map(f => (
                  <FileRow
                    key={f.Path}
                    file={f}
                    onClick={handleClick}
                  />
                ))}
              </div>
            </div>
          )}
          {staged.length > 0 && (
            <div className="review-section">
              <div className="review-section-title">Staged ({staged.length})</div>
              <div className="review-section-body">
                {staged.map(f => (
                  <FileRow
                    key={f.Path}
                    file={f}
                    onClick={handleClick}
                  />
                ))}
              </div>
            </div>
          )}
          {unstaged.length === 0 && staged.length === 0 && (
            <div className="review-empty">No changed files</div>
          )}
        </div>
      )}

      {opening && (
        <div className="review-toast">Opening {opening}...</div>
      )}
    </div>
  )
}
