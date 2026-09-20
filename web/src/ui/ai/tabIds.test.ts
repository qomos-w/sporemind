import { describe, it, expect } from 'vitest'
import { sshTabId, dbTabId, pluginTabId } from './tabIds'

describe('sshTabId', () => {
  it('keys the tab on hostId with the ssh-host- prefix', () => {
    expect(sshTabId('host-1')).toBe('ssh-host-host-1')
  })

  it('is stable regardless of the session (same host maps to the same tab)', () => {
    expect(sshTabId('my-host')).toBe(sshTabId('my-host'))
  })

  it('distinguishes different hosts', () => {
    expect(sshTabId('host-a')).not.toBe(sshTabId('host-b'))
  })

  it('does not accidentally equal a session-keyed id (no session leakage)', () => {
    // The old scheme was `ssh-${sessionId}`; a sessionId like "host-1" must
    // never collide with the new hostId key.
    expect(sshTabId('sess-1')).not.toBe('ssh-sess-1')
  })
})

describe('pluginTabId', () => {
  it('namespaces the view id by pluginID', () => {
    expect(pluginTabId('plugin-a', 'main')).toBe('plugin-view-plugin-a-main')
  })

  it('distinguishes same-named views in different plugins', () => {
    expect(pluginTabId('plugin-a', 'main')).not.toBe(pluginTabId('plugin-b', 'main'))
  })

  it('distinguishes different views within the same plugin', () => {
    expect(pluginTabId('plugin-a', 'main')).not.toBe(pluginTabId('plugin-a', 'settings'))
  })
})

describe('dbTabId', () => {
  it('keys the tab on profileId with the db-profile- prefix', () => {
    expect(dbTabId('prof-1')).toBe('db-profile-prof-1')
  })

  it('is stable for the same profileId', () => {
    expect(dbTabId('my-prof')).toBe(dbTabId('my-prof'))
  })

  it('distinguishes different profiles', () => {
    expect(dbTabId('a')).not.toBe(dbTabId('b'))
  })

  it('does not collide with sshTabId', () => {
    // Mirroring the sshTabId guard: a profileId that happens to match an
    // ssh hostId must not produce the same tab key.
    expect(dbTabId('host-1')).not.toBe(sshTabId('host-1'))
  })
})