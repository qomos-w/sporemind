import { client } from '../../application/generated-client'
import * as voice from '../../gen-clients/voice/client'

// Strip markdown to a speakable plain-text approximation. TTS input should be
// human-readable prose; code fences, front-matter and link targets add noise.
function toSpeakableText(markdown: string): string {
  return markdown
    .replace(/```[\s\S]*?```/g, ' ') // fenced code blocks
    .replace(/`[^`]*`/g, ' ')        // inline code
    .replace(/!\[[^\]]*\]\([^)]*\)/g, ' ') // images
    .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1') // links -> label
    .replace(/^[#>\-*+]/gm, ' ')      // heading/quote/list markers
    .replace(/\|/g, ' ')              // table pipes
    .replace(/[*_~]/g, '')            // emphasis markers
    .replace(/\s+/g, ' ')
    .trim()
}

let currentAudio: HTMLAudioElement | null = null

export interface SpeakOptions {
  voice?: string
}

/** Synthesize and play text via the active voice account's TTS provider. */
export async function speak(text: string, opts?: SpeakOptions): Promise<void> {
  const input = toSpeakableText(text)
  if (!input) return

  // Stop any playback already in flight.
  stop()

  const resp = await voice.synthesize(client, { Input: input, Voice: opts?.voice })
  if (!resp.Audio?.Data?.length) return

  // Backend returns mp3 (default) or wav. Wrap the raw bytes in a Blob and
  // use HTMLAudioElement — simpler and more broadly supported than
  // decodeAudioData for compressed formats.
  const mime = resp.Audio.AudioType === 3 ? 'audio/wav' : 'audio/mpeg'
  const blob = new Blob([resp.Audio.Data], { type: mime })
  const url = URL.createObjectURL(blob)

  const audio = new Audio(url)
  currentAudio = audio
  audio.onended = () => {
    if (currentAudio === audio) currentAudio = null
    URL.revokeObjectURL(url)
  }
  await audio.play()
}

/** Stop current TTS playback, if any. */
export function stop(): void {
  if (currentAudio) {
    const url = currentAudio.src
    currentAudio.pause()
    currentAudio = null
    if (url.startsWith('blob:')) URL.revokeObjectURL(url)
  }
}

/** Whether TTS audio is currently playing. */
export function isSpeaking(): boolean {
  return !!currentAudio && !currentAudio.paused
}
