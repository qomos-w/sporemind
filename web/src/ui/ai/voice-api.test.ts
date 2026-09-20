import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

// Mock generated/network dependencies so importing voice-api stays side-effect
// free. recognize() is only reached on the MediaRecorder transcribe path.
vi.mock('../../application/generated-client', () => ({ client: {} }))
vi.mock('../../gen-clients/voice/client', () => ({ recognize: vi.fn() }))
vi.mock('../../application/runtime', () => ({ isCapacitor: () => false }))

import { voiceAPI } from './voice-api'

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

interface FakeRecInstance {
  onstart: (() => void) | null
  onresult: ((e: unknown) => void) | null
  onerror: ((e: { error: string }) => void) | null
  onend: (() => void) | null
  start: ReturnType<typeof vi.fn>
  stop: ReturnType<typeof vi.fn>
  abort: ReturnType<typeof vi.fn>
}

function installFakeSpeechRecognition(autoFireOnStart = false) {
  let inst: FakeRecInstance | null = null
  class FakeSR {
    onstart: (() => void) | null = null
    onresult: ((e: unknown) => void) | null = null
    onerror: ((e: { error: string }) => void) | null = null
    onend: (() => void) | null = null
    start = vi.fn(() => {
      if (autoFireOnStart && inst) inst.onstart?.()
    })
    stop = vi.fn()
    abort = vi.fn()
    constructor() {
      inst = this as unknown as FakeRecInstance
    }
  }
  ;(window as unknown as Record<string, unknown>).webkitSpeechRecognition = FakeSR
  // Instance only exists after voiceAPI.start() constructs the SR.
  return { getInstance: () => inst }
}

interface FakeMRInstance {
  state: string
  ondataavailable: ((e: unknown) => void) | null
  onstop: (() => void) | null
  onerror: (() => void) | null
  start: () => void
  stop: () => void
}

function installFakeMediaRecorder(autoStopOnStop = true): { getInstance: () => FakeMRInstance | null } {
  let inst: FakeMRInstance | null = null
  class FakeMR {
    state = 'inactive'
    ondataavailable: ((e: unknown) => void) | null = null
    onstop: (() => void) | null = null
    onerror: (() => void) | null = null
    start() { this.state = 'recording' }
    stop() {
      this.state = 'inactive'
      if (autoStopOnStop) this.onstop?.()
    }
    constructor() {
      inst = this as unknown as FakeMRInstance
    }
  }
  ;(window as unknown as Record<string, unknown>).MediaRecorder = FakeMR
  return { getInstance: () => inst }
}

function mockGetUserMedia(trackStop: ReturnType<typeof vi.fn>) {
  const getUserMedia = vi.fn(async () => ({
    getTracks: () => [{ stop: trackStop }],
  }))
  Object.defineProperty(navigator, 'mediaDevices', {
    value: { getUserMedia },
    configurable: true,
  })
  return getUserMedia
}

function restoreWindow(key: string, saved: unknown) {
  const w = window as unknown as Record<string, unknown>
  if (saved === undefined) delete w[key]
  else w[key] = saved
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('voice-api WebSpeechDriver', () => {
  let savedSR: unknown
  let savedMR: unknown

  beforeEach(() => {
    const w = window as unknown as Record<string, unknown>
    savedSR = w.webkitSpeechRecognition
    savedMR = w.MediaRecorder
  })

  afterEach(() => {
    restoreWindow('webkitSpeechRecognition', savedSR)
    restoreWindow('MediaRecorder', savedMR)
  })

  it('resolves when onstart fires', async () => {
    const rec = installFakeSpeechRecognition(true)
    await expect(voiceAPI.start({})).resolves.toBeUndefined()
    expect(rec.getInstance()!.start).toHaveBeenCalled()
    await voiceAPI.stop()
  })

  it('starts and stops only on active-state edges', async () => {
    const rec = installFakeSpeechRecognition(true)
    await voiceAPI.setActive(true, {})
    await voiceAPI.setActive(true, {})
    expect(rec.getInstance()!.start).toHaveBeenCalledTimes(1)

    await voiceAPI.setActive(false)
    await voiceAPI.setActive(false)
    expect(rec.getInstance()!.stop).toHaveBeenCalledTimes(1)
  })

  it('rejects with a timeout when onstart never fires', async () => {
    vi.useFakeTimers()
    const rec = installFakeSpeechRecognition(false) // onstart will NOT auto-fire

    const p = voiceAPI.start({})
    // Pre-attach a catcher so the delayed rejection (fired by the timer below)
    // is never reported as an unhandled rejection.
    const caught = p.then(
      () => new Error('expected rejection but resolved'),
      (e) => e,
    )
    // Advance past the 8s start safety timeout.
    await vi.advanceTimersByTimeAsync(8000)
    const err = await caught
    expect(err).toBeInstanceOf(Error)
    expect((err as Error).message).toBe('voice:start timeout')
    // The timed-out recognition must be aborted so it does not linger.
    expect(rec.getInstance()!.abort).toHaveBeenCalled()
    vi.useRealTimers()
  })

  it('rejects immediately when onerror fires before onstart', async () => {
    const rec = installFakeSpeechRecognition(false)
    // Defer the error until after start() has constructed the SR instance.
    queueMicrotask(() => rec.getInstance()!.onerror?.({ error: 'not-allowed' }))

    await expect(voiceAPI.start({})).rejects.toThrow('not-allowed')
  })
})

describe('voice-api MediaRecorderDriver', () => {
  let savedSR: unknown
  let savedMR: unknown

  beforeEach(() => {
    const w = window as unknown as Record<string, unknown>
    savedSR = w.webkitSpeechRecognition
    savedMR = w.MediaRecorder
    // Ensure Web Speech path is NOT taken so getDriver selects MediaRecorder.
    delete w.webkitSpeechRecognition
  })

  afterEach(() => {
    restoreWindow('webkitSpeechRecognition', savedSR)
    restoreWindow('MediaRecorder', savedMR)
  })

  it('releases the mic stream tracks on stop', async () => {
    const trackStop = vi.fn()
    installFakeMediaRecorder()
    mockGetUserMedia(trackStop)

    await voiceAPI.start({})
    await voiceAPI.stop()

    // The defining fix: stream tracks must be stopped so the mic does not
    // keep recording in the background.
    expect(trackStop).toHaveBeenCalled()
  })

  it('releases the mic stream tracks on onerror', async () => {
    const trackStop = vi.fn()
    const mr = installFakeMediaRecorder()
    mockGetUserMedia(trackStop)

    const onError = vi.fn()
    await voiceAPI.start({ onError })
    // Simulate a MediaRecorder error mid-session.
    mr.getInstance()!.onerror?.()
    expect(trackStop).toHaveBeenCalled()
    expect(onError).toHaveBeenCalledWith(expect.any(Error))
  })

  it('re-acquires the mic for a second input started right after stop', async () => {
    const trackStop = vi.fn()
    installFakeMediaRecorder()
    const getUserMedia = mockGetUserMedia(trackStop)

    await voiceAPI.start({})
    await voiceAPI.stop()
    await voiceAPI.start({})
    // Both inputs grabbed the device (the bug: a too-close restart dropped).
    expect(getUserMedia).toHaveBeenCalledTimes(2)
    // The first session released the mic before the second acquired it.
    expect(trackStop).toHaveBeenCalledTimes(1)
    await voiceAPI.stop()
  })

  it('retries getUserMedia when the device is briefly busy', async () => {
    const trackStop = vi.fn()
    installFakeMediaRecorder()
    const busy = Object.assign(new Error('Could not start audio source'), { name: 'NotReadableError' })
    const getUserMedia = vi.fn()
      .mockRejectedValueOnce(busy)
      .mockResolvedValueOnce({ getTracks: () => [{ stop: trackStop }] })
    Object.defineProperty(navigator, 'mediaDevices', {
      value: { getUserMedia },
      configurable: true,
    })

    await expect(voiceAPI.start({})).resolves.toBeUndefined()
    expect(getUserMedia).toHaveBeenCalledTimes(2)
    await voiceAPI.stop()
  })

  it('resolves stop() only after the recorder emits onstop', async () => {
    const trackStop = vi.fn()
    const mr = installFakeMediaRecorder(false) // onstop fires manually below
    mockGetUserMedia(trackStop)

    await voiceAPI.start({})
    let resolved = false
    const stopped = voiceAPI.stop().then(() => { resolved = true })
    await Promise.resolve()
    // stop() is still pending: teardown must complete before the next start().
    expect(resolved).toBe(false)
    expect(trackStop).not.toHaveBeenCalled()

    mr.getInstance()!.onstop?.()
    await stopped
    expect(resolved).toBe(true)
    expect(trackStop).toHaveBeenCalled()
  })
})
