export interface PluginView {
  id: string
  pluginID: string
  title: string
  route: string
  icon?: string
  /** Bundle-declared hex color for the view glyph; undefined when none. */
  color?: string
  /** Dock zone for panels; omitted for main views. */
  zone?: 'main' | 'left' | 'right' | 'bottom'
  defaultWidth?: number
  defaultHeight?: number
}

export interface PluginPanel {
  id: string
  pluginID: string
  title: string
  route: string
  icon?: string
  zone: 'left' | 'right' | 'bottom'
  defaultWidth?: number
  defaultHeight?: number
}

export interface PluginCommand {
  id: string
  pluginID: string
  title: string
  keybinding?: string
}

export interface PluginDescriptor {
  id: string
  name: string
  version: string
  views: PluginView[]
  panels: PluginPanel[]
  commands: PluginCommand[]
}

export type PluginRegistryListener = () => void

class PluginRegistry {
  private plugins = new Map<string, PluginDescriptor>()
  private listeners = new Set<PluginRegistryListener>()
  private viewsSnapshot: PluginView[] = []
  private panelsSnapshot: PluginPanel[] = []
  private commandsSnapshot: PluginCommand[] = []
  private pluginsSnapshot: PluginDescriptor[] = []

  subscribe(listener: PluginRegistryListener): () => void {
    this.listeners.add(listener)
    return () => { this.listeners.delete(listener) }
  }

  private invalidate() {
    this.viewsSnapshot = Array.from(this.plugins.values()).flatMap(p => p.views)
    this.panelsSnapshot = Array.from(this.plugins.values()).flatMap(p => p.panels)
    this.commandsSnapshot = Array.from(this.plugins.values()).flatMap(p => p.commands)
    this.pluginsSnapshot = Array.from(this.plugins.values())
  }

  private notify() {
    for (const listener of this.listeners) {
      try { listener() } catch (e) { console.error('[PluginRegistry] listener error:', e) }
    }
  }

  registerPlugin(descriptor: PluginDescriptor): void {
    this.plugins.set(descriptor.id, descriptor)
    this.invalidate()
    this.notify()
  }

  unregisterPlugin(pluginID: string): void {
    this.plugins.delete(pluginID)
    this.invalidate()
    this.notify()
  }

  registerView(pluginID: string, view: PluginView): void {
    const plugin = this.plugins.get(pluginID)
    if (!plugin) {
      this.plugins.set(pluginID, {
        id: pluginID,
        name: pluginID,
        version: '',
        views: [view],
        panels: [],
        commands: [],
      })
    } else {
      plugin.views = plugin.views.filter(v => v.id !== view.id).concat(view)
    }
    this.invalidate()
    this.notify()
  }

  getPlugin(pluginID: string): PluginDescriptor | undefined {
    return this.plugins.get(pluginID)
  }

  getPlugins(): PluginDescriptor[] {
    return this.pluginsSnapshot
  }

  getViews(): PluginView[] {
    return this.viewsSnapshot
  }

  getPanels(): PluginPanel[] {
    return this.panelsSnapshot
  }

  getCommands(): PluginCommand[] {
    return this.commandsSnapshot
  }

  getView(viewID: string): PluginView | undefined {
    return this.viewsSnapshot.find(v => v.id === viewID)
  }
}

export const pluginRegistry = new PluginRegistry()
