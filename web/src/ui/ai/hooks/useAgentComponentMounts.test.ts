import { describe, expect, it } from 'vitest'
import { isDeveloperOnlyBundleCard, isInsiderOnlyBundleCard, isDevBuildOnlyBundleCard } from './useAgentComponentMounts'
import type { MonoCardListItem } from '../../../gen-clients/system/types'

const bundleCard = (over: Partial<MonoCardListItem> = {}): MonoCardListItem => ({
  Id: 'some:bundle',
  Title: 'Some Bundle',
  Type: 'bundle',
  Data: { componentKind: 'bundle' },
  ...over,
} as MonoCardListItem)

describe('isDeveloperOnlyBundleCard', () => {
  it('flags the builtin debug bundle', () => {
    expect(isDeveloperOnlyBundleCard(bundleCard({ Id: 'builtin:bundle:debug' }))).toBe(true)
  })

  it('flags plugin bundles projected from the appmanager registry', () => {
    expect(isDeveloperOnlyBundleCard(bundleCard({ Id: 'app:com.example', Source: 'appmanager' }))).toBe(true)
  })

  it('leaves regular bundles visible', () => {
    expect(isDeveloperOnlyBundleCard(bundleCard({ Id: 'builtin:bundle:file-tools', Source: 'builtin' }))).toBe(false)
  })

  it('never flags non-bundle cards, even with an appmanager source', () => {
    const modeCard = bundleCard({ Id: 'builtin:mode:git', Type: 'mode', Data: { componentKind: 'mode' }, Source: 'appmanager' })
    expect(isDeveloperOnlyBundleCard(modeCard)).toBe(false)
  })
})

describe('isInsiderOnlyBundleCard', () => {
  it('flags the browser-crawl bundle', () => {
    expect(isInsiderOnlyBundleCard(bundleCard({ Id: 'builtin:bundle:browser-crawl' }))).toBe(true)
  })

  it('leaves regular bundles visible', () => {
    expect(isInsiderOnlyBundleCard(bundleCard({ Id: 'builtin:bundle:web-search', Source: 'builtin' }))).toBe(false)
  })

  it('never flags non-bundle cards, even the crawl id', () => {
    const modeCard = bundleCard({ Id: 'builtin:bundle:browser-crawl', Type: 'mode', Data: { componentKind: 'mode' } })
    expect(isInsiderOnlyBundleCard(modeCard)).toBe(false)
  })
})

describe('isDevBuildOnlyBundleCard', () => {
  it('flags bundles whose card data declares devOnly', () => {
    expect(isDevBuildOnlyBundleCard(bundleCard({ Id: 'builtin:bundle:coordinator-wearable', Data: { componentKind: 'bundle', devOnly: true } }))).toBe(true)
  })

  it('leaves bundles without the flag visible', () => {
    expect(isDevBuildOnlyBundleCard(bundleCard({ Id: 'builtin:bundle:coordinator-wearable', Data: { componentKind: 'bundle' } }))).toBe(false)
  })

  it('never flags non-bundle cards, even with devOnly data', () => {
    const modeCard = bundleCard({ Id: 'builtin:mode:git', Type: 'mode', Data: { componentKind: 'mode', devOnly: true } })
    expect(isDevBuildOnlyBundleCard(modeCard)).toBe(false)
  })
})

