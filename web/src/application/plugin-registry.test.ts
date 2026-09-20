import { describe, it, expect, vi } from 'vitest'
import { pluginRegistry } from './plugin-registry'

describe('pluginRegistry', () => {
  it('registers and unregisters a plugin', () => {
    pluginRegistry.unregisterPlugin('com.example.test')

    pluginRegistry.registerPlugin({
      id: 'com.example.test',
      name: 'Test Plugin',
      version: '1.0.0',
      views: [
        { id: 'test.view', pluginID: 'com.example.test', title: 'Test View', route: '/' },
      ],
      panels: [],
      commands: [],
    })

    expect(pluginRegistry.getPlugins()).toHaveLength(1)
    expect(pluginRegistry.getViews()).toHaveLength(1)

    pluginRegistry.unregisterPlugin('com.example.test')
    expect(pluginRegistry.getPlugins()).toHaveLength(0)
    expect(pluginRegistry.getViews()).toHaveLength(0)
  })

  it('notifies listeners on change', () => {
    pluginRegistry.unregisterPlugin('com.example.test')
    const listener = vi.fn()
    const unsubscribe = pluginRegistry.subscribe(listener)

    pluginRegistry.registerPlugin({
      id: 'com.example.test',
      name: 'Test Plugin',
      version: '1.0.0',
      views: [],
      panels: [],
      commands: [],
    })

    expect(listener).toHaveBeenCalled()
    unsubscribe()
  })
})
