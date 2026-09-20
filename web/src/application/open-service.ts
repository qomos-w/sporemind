import type { OpenTarget } from '../gen-clients/system/types'

type OpenHandler = (target: OpenTarget) => void

let handler: OpenHandler | null = null

export function registerOpenHandler(h: OpenHandler): void {
  handler = h
}

export function openWorkbenchTarget(target: OpenTarget): void {
  if (!handler) {
    console.warn('[openService] no open handler registered')
    return
  }
  handler(target)
}
