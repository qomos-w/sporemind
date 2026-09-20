import { client } from '../../application/generated-client'
import * as voice from '../../gen-clients/voice/client'
import { webmToPCM } from '../ai/voice-api'

// Desktop counterpart of the mobile native voice shell. Plugin panels speak
// the voice:* postMessage protocol (voice:start/stop → ack/result/error,
// correlated by requestId — same wire shape the Capacitor shell answers, see
// ui/ai/voice-api.ts CapacitorVoiceDriver and novelking frontend voice.ts).
// Inside the Capacitor shell the host SPA relays those frames one level up to
// the native app; on desktop (wails) and flat web the SPA is the top-level
// window, so it answers the protocol itself: main-window getUserMedia +
// MediaRecorder → host voice.recognize. The main window needs no iframe
// allow-list and the WebView2 global permission already covers it.

const MAX_MS = 60_000

interface VoiceMessage {
  _sporemind?: unknown
  type?: unknown
  data?: { requestId?: unknown } & Record<string, unknown>
}

interface ActiveSession {
  requestId: number
  stream: MediaStream
  recorder: MediaRecorder
  chunks: Blob[]
  timer: number
}

export class PluginVoiceHost {
  private session: ActiveSession | null = null

  constructor(private readonly post: (msg: unknown) => void) {}

  /** Feed a message received from the plugin iframe (already source-checked). */
  handle(data: unknown): void {
    const msg = data as VoiceMessage | undefined
    if (!msg || msg._sporemind !== true || typeof msg.type !== 'string') return
    const requestId = Number(msg.data?.requestId)
    if (!Number.isFinite(requestId)) return
    if (msg.type === 'voice:start') void this.start(requestId)
    else if (msg.type === 'voice:stop' && this.session?.requestId === requestId) this.stop()
  }

  /** Release the mic without emitting any reply (panel unmount / reload). */
  dispose(): void {
    const s = this.session
    if (!s) return
    this.session = null
    clearTimeout(s.timer)
    s.recorder.onstop = null
    try { if (s.recorder.state !== 'inactive') s.recorder.stop() } catch { /* already inactive */ }
    s.stream.getTracks().forEach((t) => t.stop())
  }

  private reply(type: string, requestId: number, extra: Record<string, unknown> = {}): void {
    this.post({ _sporemind: true, type, data: { requestId, ...extra } })
  }

  private async start(requestId: number): Promise<void> {
    // A fresh start (plugin re-arm after auto-stop) replaces any live session;
    // the replaced session still transcribes and replies under its own id,
    // which the plugin's requestId filter ignores.
    if (this.session) this.stop()
    if (!window.isSecureContext || !navigator.mediaDevices?.getUserMedia) {
      this.reply('voice:error', requestId, { error: '语音需要 HTTPS 访问(当前页面非安全上下文,浏览器禁用麦克风)' })
      return
    }
    let stream: MediaStream
    try {
      stream = await navigator.mediaDevices.getUserMedia({ audio: true })
    } catch (err) {
      this.reply('voice:error', requestId, { error: `麦克风不可用:${err instanceof Error ? err.message : String(err)}` })
      return
    }
    let recorder: MediaRecorder
    try {
      recorder = new MediaRecorder(stream)
    } catch (err) {
      stream.getTracks().forEach((t) => t.stop())
      this.reply('voice:error', requestId, { error: `录音器创建失败:${err instanceof Error ? err.message : String(err)}` })
      return
    }
    const chunks: Blob[] = []
    recorder.ondataavailable = (e) => { if (e.data.size > 0) chunks.push(e.data) }
    const timer = window.setTimeout(() => this.stop(), MAX_MS)
    const session: ActiveSession = { requestId, stream, recorder, chunks, timer }
    recorder.onstop = () => {
      if (this.session === session) this.session = null
      clearTimeout(timer)
      stream.getTracks().forEach((t) => t.stop())
      void this.transcribe(requestId, new Blob(chunks, { type: 'audio/webm' }))
    }
    this.session = session
    try {
      recorder.start()
    } catch (err) {
      this.dispose()
      this.reply('voice:error', requestId, { error: `录音启动失败:${err instanceof Error ? err.message : String(err)}` })
      return
    }
    // Ack mirrors the native shell: the panel flips to "recording" on it.
    this.reply('voice:start', requestId)
  }

  private stop(): void {
    const s = this.session
    if (!s) return
    try {
      s.recorder.stop()
    } catch {
      // Inactive without onstop having run (should not happen): release the
      // mic synchronously; the panel's stop-timeout covers the missing reply.
      this.session = null
      clearTimeout(s.timer)
      s.stream.getTracks().forEach((t) => t.stop())
    }
  }

  private async transcribe(requestId: number, blob: Blob): Promise<void> {
    try {
      if (blob.size === 0) {
        this.reply('voice:error', requestId, { error: '没有采到音频' })
        return
      }
      const pcm = await webmToPCM(blob)
      const resp = await voice.recognize(client, { Audio: { AudioType: 2, Data: pcm } })
      // Always reply (empty text included) so the panel can finish its
      // transcribing state without waiting for its own stop-timeout.
      this.reply('voice:result', requestId, { text: resp.Text || '' })
    } catch (err) {
      this.reply('voice:error', requestId, { error: err instanceof Error ? err.message : String(err) })
    }
  }
}
