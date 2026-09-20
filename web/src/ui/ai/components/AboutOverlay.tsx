import React, { useCallback } from 'react'
import { useI18n } from '../../../i18n'
import { useBrowserOverlay } from '../browserOverlay'
import { buildVersion, buildType } from '../../../config/buildConfig'
import { computeAppName, useBuildInfo } from './build-info'
import './AboutOverlay.css'

/**
 * Brief "software info" overlay opened from the About menu.
 *
 * Shows the app name, the software description (with the build version) and a
 * few build identity rows. Because this overlay covers the native browser
 * window area, it registers with `useBrowserOverlay(open)` so the
 * BrowserOverlayManager hides the native window while it is visible.
 */
export const AboutOverlay: React.FC<{ open: boolean; onClose: () => void }> = ({ open, onClose }) => {
  const { t } = useI18n()
  const buildInfo = useBuildInfo(open)

  useBrowserOverlay(open)

  const handleClose = useCallback(() => onClose(), [onClose])

  if (!open) return null

  const version = `${buildVersion}${buildInfo?.dirty ? '-dirty' : ''}`
  const commit = buildInfo?.commit ?? ''
  const buildTime = buildInfo?.build_time ?? ''

  return (
    <div className="about-overlay-root" role="dialog" aria-modal="true" aria-label={t('shell.topbar.menu.about.software')}>
      {/* Semi-transparent backdrop — click closes the overlay */}
      <div className="about-overlay-backdrop" onClick={handleClose} />

      <div className="about-overlay-panel">
        <div className="about-overlay-header">
          <h2 className="about-overlay-title">{t('shell.topbar.menu.about.software')}</h2>
          <button
            type="button"
            className="about-overlay-close"
            onClick={handleClose}
            aria-label={t('aboutOverlay.close')}
          >
            <svg width="20" height="20" viewBox="0 0 20 20" fill="none" aria-hidden="true">
              <path
                d="M5 5 L15 15 M15 5 L5 15"
                stroke="currentColor"
                strokeWidth="2"
                strokeLinecap="round"
              />
            </svg>
          </button>
        </div>

        <div className="about-overlay-body">
          <div className="about-overlay-app-name">{computeAppName(buildType)}</div>
          <p className="about-overlay-desc">{t('settings.about.softwareDesc', { version })}</p>

          <div className="about-overlay-meta">
            <div className="about-overlay-row">
              <span className="about-overlay-row-label">{t('settings.about.buildType')}</span>
              <span className="about-overlay-row-value">{buildType}</span>
            </div>
            {commit && (
              <div className="about-overlay-row">
                <span className="about-overlay-row-label">{t('settings.about.commit')}</span>
                <code className="about-overlay-commit">{commit.slice(0, 8)}</code>
              </div>
            )}
            {buildTime && (
              <div className="about-overlay-row">
                <span className="about-overlay-row-label">{t('settings.about.buildTime')}</span>
                <span className="about-overlay-row-value">{new Date(buildTime).toLocaleString()}</span>
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
