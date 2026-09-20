import { isWails } from './runtime'
import { GATEWAY_DEFAULT_ADDR } from './gateway'
import * as desktop from '../bindings/github.com/qomos-w/sporemind/pkg/desktop/app'

export interface DesktopRuntimeConfig {
  /** Desktop transport mode configured in sporemind.yaml. */
  transport: 'wails' | 'ws'
  /** Gateway address as written in sporemind.yaml (e.g. "127.0.0.1:18080"). */
  gatewayAddr: string
  /** Extra gateway bind addresses for dual binding (e.g. LAN IP). Empty when
   * LAN access is off. */
  gatewayBindAddrs: string[]
}

let cached: DesktopRuntimeConfig | null = null

function defaultConfig(): DesktopRuntimeConfig {
  return {
    transport: 'wails',
    gatewayAddr: GATEWAY_DEFAULT_ADDR,
    gatewayBindAddrs: [],
  }
}

/** Load the desktop runtime configuration from the backend. In non-Wails
 * environments this returns the default immediately. */
export async function loadDesktopConfig(): Promise<DesktopRuntimeConfig> {
  if (!isWails()) {
    cached = defaultConfig()
    return cached
  }

  try {
    const [transport, gatewayAddr, bindAddrs] = await Promise.all([
      desktop.GetDesktopTransport(),
      desktop.GetRawGatewayAddr(),
      desktop.GetGatewayBindAddrs().catch(() => [] as string[]),
    ])
    cached = {
      transport: transport === 'ws' ? 'ws' : 'wails',
      gatewayAddr: gatewayAddr || GATEWAY_DEFAULT_ADDR,
      gatewayBindAddrs: Array.isArray(bindAddrs) ? bindAddrs : [],
    }
  } catch (err) {
    console.warn('[desktop-config] failed to load backend config, using defaults', err)
    cached = defaultConfig()
  }

  return cached
}

/** Return the cached desktop runtime configuration. Returns null if the
 * config has not been loaded yet. */
export function getDesktopConfig(): DesktopRuntimeConfig | null {
  return cached
}

/** Persist the given desktop runtime configuration to sporemind.yaml.
 * A restart is required for transport changes to take effect. */
export function saveDesktopConfig(
  cfg: DesktopRuntimeConfig
): Promise<void> {
  if (!isWails()) {
    return Promise.reject(new Error('saveDesktopConfig is only available in Wails desktop mode'))
  }

  desktop.SetDesktopTransport(cfg.transport)
  desktop.SetGatewayAddr(cfg.gatewayAddr)
  desktop.SetGatewayBindAddrs(cfg.gatewayBindAddrs)
  return desktop.SaveConfig()
    .then(() => {
      cached = { ...cfg }
    })
}

/** Test-only: override the cached config. */
export function setDesktopConfigForTest(cfg: DesktopRuntimeConfig | null): void {
  cached = cfg
}
