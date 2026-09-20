import { client } from '../../application/generated-client'
import * as voice from '../../gen-clients/voice/client'
import { isCapacitor } from '../../application/runtime'

export interface VoiceOptions {
  hotwords?: string[]
  onPartial?: (text: string) => void
  onResult?: (text: string) => void
  onError?: (error: Error) => void
  onStop?: () => void
  onVolume?: (level: number) => void
}

function createVolumeAnalyzer(
  stream: MediaStream,
  onVolume: (level: number) => void,
): () => void {
  const ctx = new AudioContext()
  if (ctx.state === 'suspended') ctx.resume()
  const src = ctx.createMediaStreamSource(stream)
  const analyser = ctx.createAnalyser()
  analyser.fftSize = 512
  analyser.smoothingTimeConstant = 0.6
  src.connect(analyser)

  const buf = new Uint8Array(analyser.frequencyBinCount)
  let active = true

  const poll = () => {
    if (!active) return
    analyser.getByteFrequencyData(buf)
    let sum = 0
    for (let i = 0; i < buf.length; i++) { const v = buf[i] ?? 0; sum += v * v }
    const rms = Math.sqrt(sum / buf.length) / 255
    onVolume(Math.min(1, rms * 4))
    requestAnimationFrame(poll)
  }
  requestAnimationFrame(poll)

  return () => {
    active = false
    ctx.close().catch(() => {})
  }
}

/** Some STT providers return "#" for silence/empty audio. Treat that as no
 *  speech so it is never written into the composer input. */
function isMeaningfulSpeech(text: string | undefined | null): boolean {
  if (!text) return false
  const t = text.trim()
  return t !== '' && t !== '#'
}

/** Forward a log message to the parent window (Capacitor debug overlay). */
function dbgLog(msg: string) {
  const full = '[CapacitorVoice] ' + msg
  console.log(full)
  if (window.self === window.top) return
  try {
    window.parent.postMessage({ _sporemind: true, type: 'debug:log', data: { msg: full } }, '*')
  } catch {}
}

/** Detect if we are inside the sporemind mobile shell iframe.
 *  Re-exported from runtime.ts — canonical detection now uses the
 *  ?server= contract that the mobile shell always sets on iframe src. */
const isCapacitorIframe = isCapacitor

/** Detect if Web Speech API is available (Chrome desktop/mobile). */
function hasWebSpeech(): boolean {
  return typeof window !== 'undefined' && ('SpeechRecognition' in window || 'webkitSpeechRecognition' in window)
}

/** Detect if MediaRecorder is available for fallback recording. */
function hasMediaRecorder(): boolean {
  return typeof window !== 'undefined' && 'MediaRecorder' in window
}

// ---------------------------------------------------------------------------
// Capacitor bridge (mobile app via postMessage)
// ---------------------------------------------------------------------------

// Monotonic request ID shared across all CapacitorVoiceDriver instances so a
// new driver's messages are never matched by a stale handler from a previous
// instance (each instance previously started from 0, so requestId collided).
let _capacitorRequestId = 0

class CapacitorVoiceDriver {
  private opts: VoiceOptions | null = null
  private requestId = 0
  private recording = false
  private startPending = false
  private stopQueued = false
  private userStopped = false
  private pendingStopResolves: Array<() => void> = []
  private handler: ((event: MessageEvent) => void) | null = null
  private stopResolve: (() => void) | null = null

  private cleanupHandler() {
    if (this.handler) {
      window.removeEventListener('message', this.handler)
      this.handler = null
    }
  }

  private finishStop() {
    this.recording = false
    this.startPending = false
    this.stopQueued = false
    this.cleanupHandler()
    if (this.stopResolve) {
      const resolve = this.stopResolve
      this.stopResolve = null
      resolve()
    }
    const pendingResolves = this.pendingStopResolves
    this.pendingStopResolves = []
    pendingResolves.forEach((resolve) => resolve())
  }

  // Re-arm a new native recording session so the mic follows the frontend's
  // recording state. Called when the native side stops on its own (result
  // delivered, 30s max duration) but the user hasn't explicitly stopped.
  private rearm() {
    this.recording = false
    this.startPending = true
    dbgLog('re-arming voice session')
    window.parent.postMessage(
      { _sporemind: true, type: 'voice:start', data: { requestId: this.requestId } },
      '*',
    )
  }

  start(opts: VoiceOptions): Promise<void> {
    return new Promise((resolve, reject) => {
      this.opts = opts
      _capacitorRequestId++
      this.requestId = _capacitorRequestId
      this.recording = false
      this.startPending = true
      this.stopQueued = false
      this.userStopped = false
      this.stopResolve = null
      // Clear any handler left over from a previous instance before adding a
      // new one so stale listeners can't intercept this session's messages.
      this.cleanupHandler()
      dbgLog('start → postMessage requestId=' + this.requestId)

      // Safety timeout: if parent doesn't respond with voice:start within 10s,
      // reject so caller can clean up (e.g. if shell-side race suppresses start).
      const startTimeout = setTimeout(() => {
        this.recording = false
        this.startPending = false
        this.stopQueued = false
        this.cleanupHandler()
        if (this.stopResolve) {
          const resolveStop = this.stopResolve
          this.stopResolve = null
          resolveStop()
        }
        const pendingResolves = this.pendingStopResolves
        this.pendingStopResolves = []
        pendingResolves.forEach((resolveStop) => resolveStop())
        reject(new Error('voice:start timeout'))
      }, 10000)

      this.handler = (event: MessageEvent) => {
        const msg = event.data || {}
        if (!msg._sporemind) return
        const data = msg.data || {}
        if (data.requestId !== this.requestId) return

        switch (msg.type) {
          case 'voice:start':
            clearTimeout(startTimeout)
            dbgLog('← received voice:start')
            this.startPending = false
            this.recording = true
            resolve()
            if (this.stopQueued) {
              this.stopQueued = false
              const completion = () => {}
              this.stopResolve = completion
              window.parent.postMessage(
                { _sporemind: true, type: 'voice:stop', data: { requestId: this.requestId } },
                '*',
              )
              setTimeout(() => {
                if (this.stopResolve === completion) this.finishStop()
              }, 10000)
            }
            break
          case 'voice:partial':
            dbgLog('← partial: ' + (data.text || ''))
            if (isMeaningfulSpeech(data.text)) {
              this.opts?.onPartial?.(data.text)
            }
            break
          case 'voice:result':
            clearTimeout(startTimeout)
            dbgLog('← result: ' + (data.text || ''))
            if (isMeaningfulSpeech(data.text)) {
              this.opts?.onResult?.(data.text)
            }
            if (this.userStopped) {
              this.finishStop()
            } else {
              this.rearm()
            }
            break
          case 'voice:error':
            clearTimeout(startTimeout)
            dbgLog('← error: ' + (data.error || ''))
            this.userStopped = true
            this.recording = false
            this.opts?.onError?.(new Error(data.error || 'voice error'))
            this.finishStop()
            break
          case 'voice:volume':
            this.opts?.onVolume?.(data.level)
            break
          case 'voice:fingerup':
            dbgLog('← fingerup (native push-to-talk release)')
            this.userStopped = true
            this.opts?.onStop?.()
            break
          case 'voice:data': {
            clearTimeout(startTimeout)
            const audioType = data.audioType || 3
            const buf = data.data
            dbgLog('← voice:data audioType=' + audioType + ' len=' + (buf?.byteLength || 0))
            if (buf instanceof ArrayBuffer) {
              voice
                .recognize(client, {
                  Audio: {
                    AudioType: audioType,
                    Data: new Uint8Array(buf),
                  },
                  Hotwords: this.opts?.hotwords,
                })
                .then((resp) => {
                  dbgLog('← recognize result: ' + (resp.Text || '(empty)'))
                  if (isMeaningfulSpeech(resp.Text)) {
                    this.opts?.onResult?.(resp.Text)
                  }
                  if (this.userStopped) {
                    this.finishStop()
                  } else {
                    this.rearm()
                  }
                })
                .catch((err) => {
                  const msg = err instanceof Error ? err.message : String(err)
                  dbgLog('← recognize error: ' + msg)
                  this.userStopped = true
                  this.recording = false
                  this.opts?.onError?.(new Error(msg))
                  this.finishStop()
                })
            } else {
              dbgLog('← voice:data invalid buffer type: ' + typeof buf)
              this.opts?.onError?.(new Error('voice:data invalid buffer'))
            }
            break
          }
          case 'voice:end':
            clearTimeout(startTimeout)
            dbgLog('← end')
            if (this.userStopped) {
              this.finishStop()
            } else if (!this.startPending) {
              this.rearm()
            }
            break
        }
      }
      window.addEventListener('message', this.handler)

      dbgLog('posting voice:start to parent')
      window.parent.postMessage(
        { _sporemind: true, type: 'voice:start', data: { requestId: this.requestId } },
        '*',
      )
    })
  }

  stop(): Promise<void> {
    return new Promise((resolve) => {
      this.userStopped = true
      dbgLog('stop called, recording=' + this.recording)

      // Driver already idle (native side auto-ended after result/error).
      // Resolve immediately so the caller isn't stuck waiting for a stop
      // ack that will never arrive.
      if (!this.recording && !this.startPending) {
        this.finishStop()
        resolve()
        return
      }

      // If start() is still pending, queue the stop until the shell confirms the
      // same request is recording. `recording` changes only on that confirmation.
      if (this.startPending) {
        this.stopQueued = true
        this.pendingStopResolves.push(resolve)
        return
      }

      this.stopResolve = resolve

      window.parent.postMessage(
        { _sporemind: true, type: 'voice:stop', data: { requestId: this.requestId } },
        '*',
      )

      // Safety timeout: if parent doesn't send voice:end within 10s, clean up anyway
      setTimeout(() => {
        if (this.stopResolve === resolve) {
          dbgLog('stop safety timeout — cleaning up')
          this.finishStop()
        }
      }, 10000)
    })
  }

  isRecording(): boolean {
    return this.recording || this.startPending
  }
}

// ---------------------------------------------------------------------------
// Web Speech API driver (Chrome)
// ---------------------------------------------------------------------------

class WebSpeechDriver {
  private recognition: any = null
  private recording = false
  private userStopped = false
  private stopVolume: (() => void) | null = null
  private volumeStream: MediaStream | null = null

  start(opts: VoiceOptions): Promise<void> {
    return new Promise((resolve, reject) => {
      this.userStopped = false
      const SR = (window as any).SpeechRecognition || (window as any).webkitSpeechRecognition
      if (!SR) {
        reject(new Error('Web Speech API not available'))
        return
      }

      if (opts.onVolume) {
        navigator.mediaDevices.getUserMedia({ audio: true })
          .then(stream => {
            this.volumeStream = stream
            this.stopVolume = createVolumeAnalyzer(stream, opts.onVolume!)
          })
          .catch(() => {})
      }

      this.recognition = new SR()
      this.recognition.lang = 'zh-CN'
      this.recognition.interimResults = true
      this.recognition.continuous = true

      // Safety timeout: if onstart never fires (revoked mic permission,
      // background tab, network failure reaching the speech service) the
      // promise would hang forever, leaving the UI stuck in the recording
      // state. Reject + abort so the caller can clean up.
      let startSettled = false
      const startTimeout = setTimeout(() => {
        if (startSettled) return
        startSettled = true
        try { this.recognition?.abort() } catch {}
        this.recording = false
        this.cleanupVolume()
        reject(new Error('voice:start timeout'))
      }, 8000)

      this.recognition.onstart = () => {
        if (startSettled) return
        startSettled = true
        clearTimeout(startTimeout)
        this.recording = true
        resolve()
      }

      this.recognition.onresult = (event: any) => {
        let interim = ''
        let final = ''
        for (let i = event.resultIndex; i < event.results.length; i++) {
          const transcript = event.results[i][0].transcript
          if (event.results[i].isFinal) {
            final += transcript
          } else {
            interim += transcript
          }
        }
        if (isMeaningfulSpeech(interim)) opts.onPartial?.(interim)
        if (isMeaningfulSpeech(final)) opts.onResult?.(final)
      }

      this.recognition.onerror = (event: any) => {
        this.recording = false
        const err = new Error(event.error || 'speech recognition error')
        // If the error happens before onstart, the start promise is still
        // pending — settle it now so the caller's await doesn't wait for the
        // timeout.
        if (!startSettled) {
          startSettled = true
          clearTimeout(startTimeout)
          this.cleanupVolume()
          reject(err)
          return
        }
        // Post-start errors (no-speech, network, etc.) are transient.
        // onend will fire next and auto-restart if the user hasn't stopped.
      }

      this.recognition.onend = () => {
        this.recording = false
        // If recognition ends before onstart fires, treat as a failed start.
        if (!startSettled) {
          startSettled = true
          clearTimeout(startTimeout)
          this.cleanupVolume()
          reject(new Error('voice: recognition ended before start'))
          return
        }
        // Browser auto-stopped (silence timeout). Restart to follow the
        // frontend's recording state — only stop when the user asks.
        if (!this.userStopped) {
          try {
            this.recognition.start()
            this.recording = true
          } catch {}
        } else {
          this.cleanupVolume()
        }
      }

      try {
        this.recognition.start()
      } catch (err) {
        reject(err instanceof Error ? err : new Error(String(err)))
      }
    })
  }

  private cleanupVolume() {
    if (this.stopVolume) { this.stopVolume(); this.stopVolume = null }
    if (this.volumeStream) {
      this.volumeStream.getTracks().forEach(t => t.stop())
      this.volumeStream = null
    }
  }

  stop(): Promise<void> {
    return new Promise((resolve) => {
      this.userStopped = true
      this.cleanupVolume()
      if (this.recognition) {
        try { this.recognition.stop() } catch {}
      }
      this.recording = false
      resolve()
    })
  }

  isRecording(): boolean {
    return this.recording
  }
}

// ---------------------------------------------------------------------------
// Audio format conversion (browser-side)
// ---------------------------------------------------------------------------

/** Decode webm/opus blob → 16kHz mono 16-bit PCM via Web Audio API. */
export async function webmToPCM(blob: Blob): Promise<Uint8Array> {
  const arrayBuffer = await blob.arrayBuffer()
  const audioCtx = new AudioContext({ sampleRate: 16000 })
  try {
    const audioBuffer = await audioCtx.decodeAudioData(arrayBuffer)
    const samples = audioBuffer.getChannelData(0) // mono
    const pcm = new Int16Array(samples.length)
    for (let i = 0; i < samples.length; i++) {
      const s = Math.max(-1, Math.min(1, samples[i] ?? 0))
      pcm[i] = s < 0 ? s * 0x8000 : s * 0x7FFF
    }
    return new Uint8Array(pcm.buffer)
  } finally {
    await audioCtx.close()
  }
}

// ---------------------------------------------------------------------------
// MediaRecorder fallback driver (records audio blob, uploads to backend)
// ---------------------------------------------------------------------------

class MediaRecorderDriver {
  private mediaRecorder: MediaRecorder | null = null
  private recording = false
  private stopVolume: (() => void) | null = null
  private stream: MediaStream | null = null
  // Bumped by stop() so a start() whose getUserMedia is still in flight knows
  // it was superseded and releases the device instead of recording.
  private token = 0
  // stop() callers waiting for the current recorder's onstop to fire.
  private stopWaiters: Array<() => void> = []
  private stopTimer: ReturnType<typeof setTimeout> | null = null

  // Idempotently stop the volume analyzer and release the mic stream tracks.
  // Called from every teardown path (onstop, onerror, stop) so the mic never
  // stays open in the background regardless of which path ends the session.
  private releaseStream() {
    if (this.stopVolume) { this.stopVolume(); this.stopVolume = null }
    if (this.stream) {
      this.stream.getTracks().forEach(t => t.stop())
      this.stream = null
    }
  }

  // Resolve every stop() awaiting the current session's teardown.
  private settleStop() {
    if (this.stopTimer) { clearTimeout(this.stopTimer); this.stopTimer = null }
    const waiters = this.stopWaiters
    this.stopWaiters = []
    for (const resolve of waiters) resolve()
  }

  // Two voice inputs taken in quick succession race the OS audio device: the
  // previous session's tracks are stopped, but WebView2/Windows may not have
  // released the mic yet, so the immediate getUserMedia fails with a transient
  // error (NotReadableError / AbortError). Retry briefly so the second input
  // still starts instead of silently dropping.
  private async acquireStream(): Promise<MediaStream> {
    const transient = new Set(['NotReadableError', 'AbortError', 'TrackStartError'])
    let lastError: unknown = null
    for (let attempt = 0; attempt < 4; attempt++) {
      try {
        return await navigator.mediaDevices.getUserMedia({ audio: true })
      } catch (err) {
        lastError = err
        const name = err instanceof Error ? err.name : ''
        if (!transient.has(name)) throw err
        await new Promise((r) => setTimeout(r, 60 * (attempt + 1)))
      }
    }
    throw lastError instanceof Error ? lastError : new Error(String(lastError))
  }

  start(opts: VoiceOptions): Promise<void> {
    return new Promise((resolve, reject) => {
      const token = ++this.token
      this.acquireStream()
        .then((stream) => {
          // stop() ran while we awaited the device: drop the stream so the mic
          // is not left recording with no owner.
          if (token !== this.token) {
            stream.getTracks().forEach(t => t.stop())
            reject(new Error('voice:start superseded'))
            return
          }
          this.stream = stream
          if (opts.onVolume) {
            this.stopVolume = createVolumeAnalyzer(stream, opts.onVolume)
          }
          const recorder = new MediaRecorder(stream)
          this.mediaRecorder = recorder
          const chunks: Blob[] = []

          recorder.ondataavailable = (e) => {
            if (e.data.size > 0) chunks.push(e.data)
          }

          recorder.onstop = () => {
            this.recording = false
            this.releaseStream()
            this.settleStop()

            const blob = new Blob(chunks, { type: 'audio/webm' })
            if (blob.size === 0) {
              opts.onError?.(new Error('no audio recorded'))
              return
            }

            void this.transcribeBlob(blob, opts)
              .then((text) => {
                if (isMeaningfulSpeech(text)) opts.onResult?.(text)
              })
              .catch((err) => {
                opts.onError?.(err instanceof Error ? err : new Error(String(err)))
              })
          }

          recorder.onerror = () => {
            this.recording = false
            this.releaseStream()
            this.settleStop()
            opts.onError?.(new Error('recording error'))
          }

          recorder.start()
          this.recording = true
          resolve()
        })
        .catch((err) => {
          this.releaseStream()
          reject(err instanceof Error ? err : new Error(String(err)))
        })
    })
  }

  stop(): Promise<void> {
    return new Promise((resolve) => {
      // Supersede any in-flight start() so it drops the device on arrival.
      this.token++
      this.recording = false
      const recorder = this.mediaRecorder

      if (!recorder || recorder.state === 'inactive') {
        this.releaseStream()
        resolve()
        return
      }

      // Wait for onstop so the recorder and its source tracks are fully torn
      // down before the next start() re-acquires the device — an immediate
      // re-acquire would otherwise race the still-closing device. The timeout
      // is a safety net for a recorder that never emits onstop.
      this.stopWaiters.push(resolve)
      this.stopTimer = setTimeout(() => {
        this.releaseStream()
        this.settleStop()
      }, 2000)
      try {
        recorder.stop()
      } catch {
        this.releaseStream()
        this.settleStop()
      }
    })
  }

  isRecording(): boolean {
    return this.recording
  }

  private async transcribeBlob(blob: Blob, opts: VoiceOptions): Promise<string> {
    const pcm = await webmToPCM(blob)
    const data = await voice.recognize(client, {
      Audio: {
        AudioType: 2,
        Data: pcm,
      },
      Hotwords: opts.hotwords,
    })
    return data.Text || ''
  }
}

// ---------------------------------------------------------------------------
// Public VoiceAPI
// ---------------------------------------------------------------------------

class VoiceAPI {
  private driver: CapacitorVoiceDriver | WebSpeechDriver | MediaRecorderDriver | null = null
  private active = false
  private desiredActive = false
  private options: VoiceOptions | null = null
  private transition: Promise<void> | null = null

  private getDriver(): CapacitorVoiceDriver | WebSpeechDriver | MediaRecorderDriver {
    const isCapacitor = isCapacitorIframe()
    const canWebMedia = hasMediaRecorder() && typeof window !== 'undefined' && (window as any).isSecureContext !== false
    console.log('[VoiceAPI] isCapacitor=' + isCapacitor + ' canWebMedia=' + canWebMedia + ' hasWebSpeech=' + hasWebSpeech() + ' hasMediaRecorder=' + hasMediaRecorder())

    // Capacitor shell (iframe): always use native bridge.
    // getUserMedia inside a non-HTTPS iframe is blocked by secure-context rules,
    // so MediaRecorderDriver fails immediately. The native plugin path is the
    // only reliable option across iOS and Android.
    if (isCapacitor) {
      console.log('[VoiceAPI] → CapacitorVoiceDriver')
      return new CapacitorVoiceDriver()
    }

    if (hasWebSpeech()) {
      console.log('[VoiceAPI] → WebSpeechDriver')
      return new WebSpeechDriver()
    }

    if (canWebMedia) {
      console.log('[VoiceAPI] → MediaRecorderDriver')
      return new MediaRecorderDriver()
    }

    throw new Error('No voice input driver available')
  }

  private async startDriver(opts: VoiceOptions): Promise<void> {
    this.driver = this.getDriver()
    await this.driver.start(opts)
  }

  private async stopDriver(): Promise<void> {
    await this.driver?.stop()
  }

  async setActive(active: boolean, opts?: VoiceOptions): Promise<void> {
    if (opts) this.options = opts
    if (active && !this.options) throw new Error('Voice options are required when activating voice input')
    if (this.desiredActive === active) return this.transition ?? Promise.resolve()

    this.desiredActive = active
    const previous = this.transition ?? Promise.resolve()
    const current = previous.then(async () => {
      while (this.active !== this.desiredActive) {
        const target = this.desiredActive
        if (target) await this.startDriver(this.options!)
        else await this.stopDriver()
        this.active = target
      }
    }).catch((err) => {
      this.active = false
      this.desiredActive = false
      throw err
    })
    const transition = current.finally(() => {
      if (this.transition === transition) this.transition = null
    })
    this.transition = transition
    return transition
  }

  async start(opts: VoiceOptions): Promise<void> {
    this.options = opts
    try {
      await this.startDriver(opts)
      this.active = true
      this.desiredActive = true
    } catch (err) {
      this.active = false
      this.desiredActive = false
      throw err
    }
  }

  async stop(): Promise<void> {
    await this.stopDriver()
    this.active = false
    this.desiredActive = false
  }

  isRecording(): boolean {
    return this.active
  }
}

export { dbgLog }
export const voiceAPI = new VoiceAPI()
