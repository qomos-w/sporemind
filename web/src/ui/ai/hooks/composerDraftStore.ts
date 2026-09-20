import type { AttachmentEntry, ImageEntry } from '../../../gen-types/agent.chat'

const drafts = new Map<string, string>()

export function getComposerDraft(agentId: string): string {
  return drafts.get(agentId) ?? ''
}

export function setComposerDraft(agentId: string, text: string): void {
  drafts.set(agentId, text)
}

export function clearComposerDraft(agentId: string): void {
  drafts.delete(agentId)
}

// Pending images/attachments are bound to the composer (agent) they were
// inserted in, mirroring the text draft above.
export interface ComposerMediaDraft {
  images: ImageEntry[]
  attachments: AttachmentEntry[]
}

const mediaDrafts = new Map<string, ComposerMediaDraft>()

export function getComposerMedia(agentId: string): ComposerMediaDraft {
  return mediaDrafts.get(agentId) ?? { images: [], attachments: [] }
}

export function setComposerMedia(agentId: string, media: ComposerMediaDraft): void {
  if (media.images.length === 0 && media.attachments.length === 0) {
    mediaDrafts.delete(agentId)
    return
  }
  mediaDrafts.set(agentId, media)
}
