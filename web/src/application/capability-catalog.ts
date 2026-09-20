/**
 * Client-side mirror of pkg/appbinding/capability_catalog.go.
 *
 * Used by the install preview dialog to render human-readable titles,
 * descriptions, and risk levels for manifest permission strings before the
 * app is registered.
 */

export interface CapabilityCatalogInfo {
  id: string
  riskLevel: 'low' | 'medium' | 'high'
  titleKey: string
  descriptionKey: string
}

export const KNOWN_CAPABILITY_IDS = [
  'llm.invoke',
  'fs.read',
  'fs.write',
  'shell.exec',
  'config.read',
  'provider.read',
  'aggregator.read',
  'app.state',
  'app.data',
  'app.emit',
  'registry.read',
  'ssh.invoke',
  'voice.stt',
  'voice.tts',
  'voice.read',
  'media.read',
  'image.gen',
  'video.gen',
  'dialog.openFile',
  'dialog.openFolder',
  'dialog.saveFile',
  'clipboard.write',
  'clipboard.read',
] as const

export const CAPABILITY_RISK_LEVEL: Record<string, 'low' | 'medium' | 'high'> = {
  'llm.invoke': 'medium',
  'fs.read': 'low',
  'fs.write': 'high',
  'shell.exec': 'high',
  'config.read': 'low',
  'provider.read': 'low',
  'aggregator.read': 'low',
  'app.state': 'low',
  'app.data': 'medium',
  'app.emit': 'low',
  'registry.read': 'low',
  'ssh.invoke': 'high',
  'voice.stt': 'medium',
  'voice.tts': 'medium',
  'voice.read': 'low',
  'media.read': 'low',
  'image.gen': 'medium',
  'video.gen': 'medium',
  'dialog.openFile': 'low',
  'dialog.openFolder': 'low',
  'dialog.saveFile': 'low',
  'clipboard.write': 'low',
  'clipboard.read': 'medium',
}

export function getCapabilityInfo(id: string): CapabilityCatalogInfo | null {
  if (!(KNOWN_CAPABILITY_IDS as readonly string[]).includes(id)) return null
  return {
    id,
    riskLevel: CAPABILITY_RISK_LEVEL[id] ?? 'low',
    titleKey: `capability.${id}.title`,
    descriptionKey: `capability.${id}.description`,
  }
}
