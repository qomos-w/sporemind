import { useState, useEffect, useMemo } from 'react'
import { Info, FileText, Scale, ChevronRight, ChevronDown, ChevronUp, GraduationCap } from 'lucide-react'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { useI18n } from '../../../i18n'
import { FeatureCard, SettingRow } from '../../settings/shadcn/composites'
import { Button } from '../../settings/shadcn/ui/button'
import { Badge } from '../../settings/shadcn/ui/badge'
import { buildVersion, buildType, buildFlavor } from '../../../config/buildConfig'
import { guideManager } from '../../../application/guide-manager'
import { TUTORIAL_CATEGORIES, getTutorialCategory, listTutorials, useDynamicTutorials, type TutorialContext } from '../../../application/tutorial-registry'
import { appRegistry } from '../../../application/app-registry'
import { hasLaunchableApps } from './SidebarLauncher'
import { client } from '../../../application/generated-client'
import * as projectClient from '../../../gen-clients/project/client'
import * as interfaceClient from '../../../gen-clients/interfacemanager/client'
import { computeAppName, useBuildInfo } from './build-info'
import licenses from '../../../about/third-party-licenses.generated.json'
import termsZhCN from '../../../../../docs/legal/user-terms.zh-CN.md?raw'
import termsEnUS from '../../../../../docs/legal/user-terms.en-US.md?raw'
import './ShellAboutSettings.css'

interface LicenseEntry {
  name: string
  version: string
  license: string
  homepage?: string
  copyright?: string
}

interface LicenseGroup {
  name: string
  entries: LicenseEntry[]
}

interface LicensesData {
  generatedAt: string
  sources: string[]
  groups: LicenseGroup[]
}

function LicenseGroupCard({ group, defaultExpanded }: { group: LicenseGroup; defaultExpanded?: boolean }) {
  const [expanded, setExpanded] = useState(defaultExpanded ?? false)
  return (
    <div className="sp-about-license-group">
      <button
        type="button"
        className="sp-about-license-group-header"
        onClick={() => setExpanded(!expanded)}
      >
        {expanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
        <span className="sp-about-license-group-name">{group.name}</span>
        <span className="sp-about-license-group-count">({group.entries.length})</span>
      </button>
      {expanded && (
        <div className="sp-about-license-group-body">
          {group.entries.map((entry, i) => (
            <div key={i} className="sp-about-license-entry">
              <div className="sp-about-license-entry-name">{entry.name}@{entry.version}</div>
              <div className="sp-about-license-entry-meta">
                <Badge variant="outline" className="sp-about-license-badge">{entry.license}</Badge>
                {entry.copyright && (
                  <span className="sp-about-license-copyright">{entry.copyright}</span>
                )}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

// The bundled EULA (docs/legal/user-terms.*.md) is still a non-binding draft;
// do not present it to end users until it is legally finalized.
const SHOW_USER_TERMS = false

export function ShellAboutSettings() {
  const { t, locale } = useI18n()
  const [termsOpen, setTermsOpen] = useState(false)
  const [tutorialCtx, setTutorialCtx] = useState<TutorialContext>({ hasProjects: false, hasAgents: false, hasApps: false })

  const data = licenses as unknown as LicensesData

  // Build-time constants from vite defines (VITE_BUILD_VERSION etc.)
  const version = buildVersion
  const type = buildType
  const flavor = buildFlavor

  const appName = computeAppName(type)

  // Runtime binding for full build info (commit, build_time, dirty).
  // Falls back silently to build-time values in non-desktop contexts.
  const buildInfo = useBuildInfo()

  // Query project list once to determine tutorial availability, and derive
  // the installed-app gate from the app registry snapshot (synced app-wide).
  useEffect(() => {
    let cancelled = false
    ;(async () => {
      try {
        const resp = await projectClient.cardList(client, {})
        if (!cancelled) {
          setTutorialCtx(prev => ({ ...prev, hasProjects: (resp.Refs ?? []).length > 0, hasApps: hasLaunchableApps(appRegistry.getAll()) }))
        }
      } catch {
        // Query failure — leave defaults (tutorials still replayable)
      }
    })()
    return () => { cancelled = true }
  }, [])

  const commit = buildInfo?.commit ?? ''
  const buildTime = buildInfo?.build_time ?? ''
  const dirty = buildInfo?.dirty ?? false

  const terms = locale === 'zh-CN' ? termsZhCN : termsEnUS

  const replayTutorial = useMemo(() => {
    return (categoryId: string) => {
      const cat = TUTORIAL_CATEGORIES.find(c => c.id === categoryId)
      if (!cat) return
      guideManager.replayGuide(cat.build(t, tutorialCtx), t(cat.labelKey))
    }
  }, [t, tutorialCtx])

  // Dynamic (agent-created) tutorials live in the actor-owned store; the
  // hook subscription re-renders this panel when the catalog loads or a
  // create/delete event flows back from the backend.
  useDynamicTutorials()
  const tutorials = listTutorials(t, tutorialCtx)

  /** Replay an agent-created tutorial as an external (non-tour) guide. */
  const replayDynamicTutorial = (id: string) => {
    const item = tutorials.find(i => i.isDynamic && i.id === id)
    if (!item) return
    guideManager.showGuide(item.steps, false, item.title, { source: 'external' })
  }

  /**
   * Request deletion of an agent-created tutorial. The store entry is removed
   * when the backend's delete event flows back (AIShellLayout wiring); the UI
   * does not optimistically drop the row.
   */
  const deleteDynamicTutorial = (id: string) => {
    interfaceClient.control(client, { Action: 'delete_tutorial', TutorialId: id }).catch((err) => {
      console.warn('[about-settings] failed to delete tutorial', id, err)
    })
  }

  return (
    <div className="sp-about-settings">
      {/* App Identity */}
      <FeatureCard
        icon={<Info size={16} />}
        title={t('settings.about.title')}
        description={t('settings.about.softwareDesc', { version: `${version}${dirty ? '-dirty' : ''}` })}
      >
        <SettingRow
          label={t('settings.about.appName')}
          description={t('settings.about.appNameDesc')}
          control={<span className="sp-about-value sp-about-app-name">{appName}</span>}
        />
        <SettingRow
          label={t('settings.about.buildType')}
          description={t('settings.about.buildTypeDesc')}
          control={<span className={`sp-about-badge sp-about-badge--${type}`}>{type}</span>}
        />
        {flavor !== 'default' && (
          <SettingRow
            label={t('settings.about.buildFlavor')}
            description={t('settings.about.buildFlavorDesc')}
            control={<span className="sp-about-badge sp-about-badge--flavor">{flavor}</span>}
          />
        )}
        {commit && (
          <SettingRow
            label={t('settings.about.commit')}
            description={t('settings.about.commitDesc')}
            control={<code className="sp-about-commit">{commit.slice(0, 8)}</code>}
          />
        )}
        {buildTime && (
          <SettingRow
            label={t('settings.about.buildTime')}
            description={t('settings.about.buildTimeDesc')}
            control={<span className="sp-about-time">{new Date(buildTime).toLocaleString()}</span>}
          />
        )}
        <SettingRow
          label={t('settings.about.license')}
          description={t('settings.about.licenseDesc')}
          control={<span className="sp-about-value">AGPL-3.0</span>}
        />
        <SettingRow
          label={t('settings.about.copyright')}
          description={t('settings.about.copyrightDesc')}
          control={<span className="sp-about-value">{t('settings.about.copyrightText')}</span>}
        />
      </FeatureCard>

      {/* Tutorial Library */}
      <FeatureCard
        icon={<GraduationCap size={16} />}
        title={t('onboarding.tutorial.library.title')}
        description={t('onboarding.tutorial.library.desc')}
      >
        {tutorials.map(item => {
          const cat = item.isDynamic ? undefined : getTutorialCategory(item.id)
          const available = cat?.available ? cat.available(tutorialCtx) : true
          return (
            <SettingRow
              key={`${item.isDynamic ? 'dynamic' : 'static'}-${item.id}`}
              label={
                <span className="sp-about-tutorial-label">
                  {item.title}
                  {item.isDynamic && (
                    <Badge className="sp-about-badge sp-about-badge--agent">
                      {t('onboarding.tutorial.agentBadge')}
                    </Badge>
                  )}
                </span>
              }
              description={item.description}
              control={
                <div className="sp-about-tutorial-actions">
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    data-guide-id={`settings/about/replay-${item.isDynamic ? `dynamic-${item.id}` : item.id}`}
                    disabled={!available}
                    onClick={() => (item.isDynamic ? replayDynamicTutorial(item.id) : replayTutorial(item.id))}
                  >
                    {t('onboarding.tutorial.replay')}
                  </Button>
                  {item.isDynamic && (
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      data-guide-id={`settings/about/delete-${item.id}`}
                      onClick={() => deleteDynamicTutorial(item.id)}
                    >
                      {t('onboarding.tutorial.delete')}
                    </Button>
                  )}
                </div>
              }
            />
          )
        })}
      </FeatureCard>

      {/* Legal (hidden until a finalized EULA exists) */}
      {SHOW_USER_TERMS && (
        <FeatureCard
          icon={<Scale size={16} />}
          title={t('settings.about.legal')}
          description={t('settings.about.legalDesc')}
        >
          <SettingRow
            label={t('settings.about.userTerms')}
            description={t('settings.about.userTermsDesc')}
          >
            <div className="flex flex-col gap-2">
              <div>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="sp-about-link-btn"
                  onClick={() => setTermsOpen(!termsOpen)}
                  data-guide-id="settings/about/user-terms"
                >
                  {termsOpen ? <ChevronUp size={14} /> : <ChevronDown size={14} />}
                  {termsOpen ? t('settings.about.hide') : t('settings.about.view')}
                </Button>
              </div>
              {termsOpen && (
                <div className="sp-about-terms">
                  <Markdown remarkPlugins={[remarkGfm]}>{terms}</Markdown>
                </div>
              )}
            </div>
          </SettingRow>
        </FeatureCard>
      )}

      {/* Third-Party Licenses */}
      <FeatureCard
        icon={<FileText size={16} />}
        title={t('settings.about.thirdPartyLicenses')}
        description={t('settings.about.thirdPartyLicensesDesc')}
      >
        <div className="sp-about-license-groups">
          {data.groups.map((group, i) => (
            <LicenseGroupCard key={i} group={group} />
          ))}
        </div>
      </FeatureCard>
    </div>
  )
}

export default ShellAboutSettings