/** Runtime gate for the streaming pipeline's hot-path debug logs
 *  ([step.recv] / [step.flush] / [step.reducer]).
 *
 *  In the desktop build every console call costs a stack capture + JSON
 *  encode + a Wails IPC round-trip to the console log store — streaming
 *  deltas used to emit 4 debug lines per event, which saturated the renderer
 *  main thread on long sessions. The logs stay available for debugging:
 *  set `window.__SPORE_STEP_DEBUG__ = true` in DevTools to enable them. */
export function stepDebugEnabled(): boolean {
  return (globalThis as { __SPORE_STEP_DEBUG__?: boolean }).__SPORE_STEP_DEBUG__ === true
}
