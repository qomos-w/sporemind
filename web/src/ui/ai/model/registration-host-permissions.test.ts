import { describe, expect, it } from 'vitest'
import { registrationHostPermissionsFromFrame } from './registration-host-permissions'

describe('registrationHostPermissionsFromFrame', () => {
  it('derives resolved permissions from engine-attached fields', () => {
    const res = registrationHostPermissionsFromFrame({
      id: 'c1',
      callableId: 'appmanager.register_project',
      appId: 'app.demo',
      appName: 'Demo',
      permissions: ['shell.exec', 'fs.read'],
    })
    expect(res).toMatchObject({
      callId: 'c1',
      callableId: 'appmanager.register_project',
      status: 'resolved',
      appId: 'app.demo',
      appName: 'Demo',
      permissions: ['shell.exec', 'fs.read'],
    })
  })

  it('treats an app with zero permissions as resolved (not unavailable)', () => {
    const res = registrationHostPermissionsFromFrame({
      id: 'c1',
      callableId: 'appmanager.register',
      appId: 'app.plain',
      permissions: [],
    })
    expect(res.status).toBe('resolved')
    expect(res.permissions).toEqual([])
  })

  it('filters non-string entries defensively', () => {
    const res = registrationHostPermissionsFromFrame({
      id: 'c1',
      callableId: 'cloudaccount.content_install',
      appId: 'app.cloud',
      // @ts-expect-error runtime payload may carry junk
      permissions: ['shell.exec', 42, null, 'fs.read'],
    })
    expect(res.permissions).toEqual(['shell.exec', 'fs.read'])
  })

  it('degrades to unavailable with the backend note when nothing was attached', () => {
    const res = registrationHostPermissionsFromFrame({
      id: 'c1',
      callableId: 'appmanager.register_project',
      input: '{"AppDir":"."}',
      permissionNote: 'appmanager: read manifest: no app.manifest.json',
    })
    expect(res.status).toBe('unavailable')
    expect(res.permissions).toEqual([])
    expect(res.note).toBe('appmanager: read manifest: no app.manifest.json')
  })

  it('degrades to unavailable without a note for older hosts', () => {
    const res = registrationHostPermissionsFromFrame({
      id: 'c1',
      callableId: 'appmanager.register',
      input: '{}',
    })
    expect(res).toMatchObject({ status: 'unavailable', permissions: [] })
    expect(res.note).toBeUndefined()
  })

  it('returns unavailable for non-registration callables', () => {
    const res = registrationHostPermissionsFromFrame({
      id: 'c1',
      callableId: 'project.git_push',
      appId: 'ignored',
      permissions: ['shell.exec'],
    })
    expect(res.status).toBe('unavailable')
    expect(res.permissions).toEqual([])
  })
})
