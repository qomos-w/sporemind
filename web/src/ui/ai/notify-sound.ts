// Notification sound synthesis + config management for agent task completion.
//
// Uses the Web Audio API to synthesize short tones (no audio asset files needed).
// Config is loaded from the voice actor (actor-owned backend state) — never
// localStorage — per the 前端状态后端化 constraint.

import { client } from '../../application/generated-client'
import * as voice from '../../gen-clients/voice/client'
import type { VoiceNotifyConfig } from '../../gen-clients/system/types'

export type NotifySoundId = 'chime' | 'bell' | 'pop' | 'ding' | 'soft' | 'marimba' | 'celesta' | 'guitar' | 'crystal' | 'koto'

export interface NotifySoundOption {
  id: NotifySoundId
}

export const NOTIFY_SOUNDS: NotifySoundOption[] = [
  { id: 'chime' },
  { id: 'bell' },
  { id: 'pop' },
  { id: 'ding' },
  { id: 'soft' },
  { id: 'marimba' },
  { id: 'celesta' },
  { id: 'guitar' },
  { id: 'crystal' },
  { id: 'koto' },
]

export const DEFAULT_NOTIFY_CONFIG: VoiceNotifyConfig = {
  Enabled: true,
  Volume: 70,
  Sound: 'chime',
  OnComplete: true,
  OnError: true,
  OnInteraction: true,
  OnAllComplete: true,
  SoundError: '',
  SoundInteraction: '',
  SoundAllComplete: '',
  VolumeComplete: 0,
  VolumeError: 0,
  VolumeInteraction: 0,
  VolumeAllComplete: 0,
  CustomSoundComplete: '',
  CustomSoundError: '',
  CustomSoundInteraction: '',
  CustomSoundAllComplete: '',
}

// ---------------------------------------------------------------------------
// Web Audio synthesis
// ---------------------------------------------------------------------------

let _ctx: AudioContext | null = null

function getCtx(): AudioContext | null {
  if (typeof window === 'undefined') return null
  if (_ctx && _ctx.state !== 'closed') return _ctx
  const Ctor = window.AudioContext || (window as any).webkitAudioContext
  if (!Ctor) return null
  _ctx = new Ctor()
  return _ctx
}

/** Browsers suspend AudioContext until a user gesture; call from a click. */
export function unlockAudio(): void {
  const ctx = getCtx()
  if (ctx && ctx.state === 'suspended') void ctx.resume()
}

interface ToneSpec {
  freq: number
  start: number      // seconds from now
  duration: number   // seconds
  type?: OscillatorType
  gain?: number      // peak gain (0..1) before volume scaling
}

// Each completion sound is a short sequence of tones.
const SOUND_PRESETS: Record<string, ToneSpec[]> = {
  chime: [
    { freq: 659.25, start: 0.00, duration: 0.12, type: 'sine', gain: 0.6 },   // E5
    { freq: 880.00, start: 0.10, duration: 0.28, type: 'sine', gain: 0.6 },   // A5
  ],
  bell: [
    { freq: 523.25, start: 0.00, duration: 0.55, type: 'triangle', gain: 0.5 }, // C5
    { freq: 1046.5, start: 0.00, duration: 0.55, type: 'sine',     gain: 0.15 }, // C6 overtone
  ],
  pop: [
    { freq: 392.00, start: 0.00, duration: 0.09, type: 'sine', gain: 0.7 },   // G4
    { freq: 587.33, start: 0.07, duration: 0.12, type: 'sine', gain: 0.6 },   // D5
  ],
  ding: [
    { freq: 523.25, start: 0.00, duration: 0.18, type: 'sine', gain: 0.55 },  // C5
    { freq: 783.99, start: 0.14, duration: 0.30, type: 'sine', gain: 0.55 },  // G5
  ],
  soft: [
    { freq: 440.00, start: 0.00, duration: 0.45, type: 'sine', gain: 0.35 },  // A4
    { freq: 554.37, start: 0.05, duration: 0.40, type: 'sine', gain: 0.25 },  // C#5
  ],
  marimba: [
    { freq: 523.25, start: 0.00, duration: 0.10, type: 'sine', gain: 0.6 },   // C5
    { freq: 659.25, start: 0.06, duration: 0.10, type: 'sine', gain: 0.55 },  // E5
    { freq: 880.00, start: 0.12, duration: 0.22, type: 'sine', gain: 0.5 },   // A5
  ],
  celesta: [
    { freq: 880.00, start: 0.00, duration: 0.14, type: 'triangle', gain: 0.45 }, // A5
    { freq: 1318.5, start: 0.08, duration: 0.14, type: 'triangle', gain: 0.45 }, // E6
    { freq: 1763.9, start: 0.16, duration: 0.30, type: 'triangle', gain: 0.4 },  // A6
  ],
  guitar: [
    { freq: 329.63, start: 0.00, duration: 0.16, type: 'triangle', gain: 0.5 }, // E4
    { freq: 440.00, start: 0.10, duration: 0.18, type: 'triangle', gain: 0.5 }, // A4
    { freq: 523.25, start: 0.22, duration: 0.30, type: 'triangle', gain: 0.45 }, // C5
  ],
  crystal: [
    { freq: 2093.0, start: 0.00, duration: 0.10, type: 'sine', gain: 0.4 },  // C7
    { freq: 2637.0, start: 0.06, duration: 0.10, type: 'sine', gain: 0.4 },  // E7
    { freq: 3135.9, start: 0.12, duration: 0.28, type: 'sine', gain: 0.35 }, // G7
  ],
  koto: [
    { freq: 440.00, start: 0.00, duration: 0.14, type: 'sawtooth', gain: 0.35 }, // A4
    { freq: 554.37, start: 0.10, duration: 0.14, type: 'sawtooth', gain: 0.35 }, // C#5
    { freq: 659.25, start: 0.20, duration: 0.28, type: 'sawtooth', gain: 0.3 },  // E5
  ],
}

// Distinct descending minor tone for errors (used when SoundError is empty).
const ERROR_DEFAULT_PRESET: ToneSpec[] = [
  { freq: 440.00, start: 0.00, duration: 0.16, type: 'sawtooth', gain: 0.35 }, // A4
  { freq: 349.23, start: 0.14, duration: 0.30, type: 'sawtooth', gain: 0.35 }, // F4
]

// Triumphant ascending major arpeggio for "everything is done" — the last
// active agent finished and no workflow remains active. Distinct from the
// per-agent completion presets (independent of Sound selection).
// Extended: a full ascending arpeggio C5→E5→G5→C6, a held C6, then a
// resolving C6→E6→G6→C7 flourish for a satisfying, longer conclusion.
// Used when SoundAllComplete is empty.
const ALL_COMPLETE_DEFAULT_PRESET: ToneSpec[] = [
  { freq: 523.25, start: 0.00, duration: 0.18, type: 'sine', gain: 0.5 }, // C5
  { freq: 659.25, start: 0.15, duration: 0.18, type: 'sine', gain: 0.5 }, // E5
  { freq: 783.99, start: 0.30, duration: 0.18, type: 'sine', gain: 0.5 }, // G5
  { freq: 1046.5, start: 0.45, duration: 0.55, type: 'sine', gain: 0.55 }, // C6 (held)
  { freq: 1318.5, start: 0.65, duration: 0.18, type: 'sine', gain: 0.45 }, // E6
  { freq: 1567.9, start: 0.80, duration: 0.18, type: 'sine', gain: 0.45 }, // G6
  { freq: 2093.0, start: 0.95, duration: 0.70, type: 'sine', gain: 0.5 },  // C7 (final)
]

// Wind-chime three-note phrase for interaction requests (ask_user, plan/goal
// submit, permission). Used when SoundInteraction is empty. Timbre: chime-like
// sine fundamentals with soft octave partials and long decaying tails (悦耳);
// contour: still a rising D-pentatonic D5→E5→A5 with the final note held and
// left unresolved on the dominant — the musical equivalent of a rising
// questioning intonation awaiting an answer.
const INTERACTION_DEFAULT_PRESET: ToneSpec[] = [
  // D5 strike + shimmer
  { freq: 587.33, start: 0.00, duration: 0.38, type: 'sine', gain: 0.45 },
  { freq: 1174.66, start: 0.00, duration: 0.24, type: 'sine', gain: 0.12 },
  // E5 strike + shimmer
  { freq: 659.25, start: 0.14, duration: 0.38, type: 'sine', gain: 0.45 },
  { freq: 1318.51, start: 0.14, duration: 0.24, type: 'sine', gain: 0.12 },
  // A5 strike, held long (questioning) + shimmer
  { freq: 880.00, start: 0.28, duration: 0.72, type: 'sine', gain: 0.42 },
  { freq: 1760.00, start: 0.28, duration: 0.50, type: 'sine', gain: 0.14 },
]

function playTones(tones: ToneSpec[], volumePct: number): void {
  const ctx = getCtx()
  if (!ctx) return
  if (ctx.state === 'suspended') void ctx.resume()
  const now = ctx.currentTime
  // Perceptual volume curve so 0..100 maps to a usable 0..1 range.
  const vol = Math.pow(Math.max(0, Math.min(100, volumePct)) / 100, 1.6)
  for (const t of tones) {
    const osc = ctx.createOscillator()
    const gain = ctx.createGain()
    osc.type = t.type ?? 'sine'
    osc.frequency.value = t.freq
    const peak = (t.gain ?? 0.5) * vol
    const t0 = now + t.start
    const t1 = t0 + t.duration
    // Attack-decay envelope (no click artifacts).
    gain.gain.setValueAtTime(0.0001, t0)
    gain.gain.exponentialRampToValueAtTime(Math.max(0.0002, peak), t0 + 0.012)
    gain.gain.exponentialRampToValueAtTime(0.0001, t1)
    osc.connect(gain).connect(ctx.destination)
    osc.start(t0)
    osc.stop(t1 + 0.02)
  }
}

function presetFor(sound: string): ToneSpec[] {
  return SOUND_PRESETS[sound] ?? SOUND_PRESETS.chime ?? CHIME_FALLBACK
}

function errorPresetFor(sound: string | undefined): ToneSpec[] {
  return sound ? (SOUND_PRESETS[sound] ?? ERROR_DEFAULT_PRESET) : ERROR_DEFAULT_PRESET
}

function interactionPresetFor(sound: string | undefined): ToneSpec[] {
  return sound ? (SOUND_PRESETS[sound] ?? INTERACTION_DEFAULT_PRESET) : INTERACTION_DEFAULT_PRESET
}

function allCompletePresetFor(sound: string | undefined): ToneSpec[] {
  return sound ? (SOUND_PRESETS[sound] ?? ALL_COMPLETE_DEFAULT_PRESET) : ALL_COMPLETE_DEFAULT_PRESET
}

const CHIME_FALLBACK: ToneSpec[] = SOUND_PRESETS.chime ?? [
  { freq: 659.25, start: 0.00, duration: 0.12, type: 'sine', gain: 0.6 },
  { freq: 880.00, start: 0.10, duration: 0.28, type: 'sine', gain: 0.6 },
]

// ---------------------------------------------------------------------------
// Custom audio file playback (data URL or object URL)
// ---------------------------------------------------------------------------

function playCustomAudio(audioSrc: string, volumePct: number): void {
  try {
    const audio = new Audio(audioSrc)
    audio.volume = Math.max(0, Math.min(1, volumePct / 100))
    void audio.play()
  } catch {
    // fall through silently — the preset will not play either
  }
}

function playOrCustom(custom: string | undefined, tones: ToneSpec[], volumePct: number): void {
  if (custom) {
    playCustomAudio(custom, volumePct)
    return
  }
  playTones(tones, volumePct)
}

/** Resolve per-event volume: fall back to global Volume when the per-event value is 0 or unset. */
function eventVolume(perEvent: number | undefined, globalVol: number): number {
  return perEvent && perEvent > 0 ? perEvent : globalVol
}

/** Play the completion sound selected in config. */
export function playCompleteSound(cfg: VoiceNotifyConfig): void {
  if (!cfg.Enabled || !cfg.OnComplete) return
  const vol = eventVolume(cfg.VolumeComplete, cfg.Volume)
  playOrCustom(cfg.CustomSoundComplete, presetFor(cfg.Sound), vol)
}

/** Play the error sound. */
export function playErrorSound(cfg: VoiceNotifyConfig): void {
  if (!cfg.Enabled || !cfg.OnError) return
  const vol = eventVolume(cfg.VolumeError, cfg.Volume)
  playOrCustom(cfg.CustomSoundError, errorPresetFor(cfg.SoundError), vol)
}

/** Play the interaction-request attention tone (ask_user, plan/goal submit, permission). */
export function playInteractionSound(cfg: VoiceNotifyConfig): void {
  if (!cfg.Enabled || !cfg.OnInteraction) return
  const vol = eventVolume(cfg.VolumeInteraction, cfg.Volume)
  playOrCustom(cfg.CustomSoundInteraction, interactionPresetFor(cfg.SoundInteraction), vol)
}

/** Play the all-complete cue (last active agent finished, no active workflow). */
export function playAllCompleteSound(cfg: VoiceNotifyConfig): void {
  if (!cfg.Enabled || !cfg.OnAllComplete) return
  const vol = eventVolume(cfg.VolumeAllComplete, cfg.Volume)
  playOrCustom(cfg.CustomSoundAllComplete, allCompletePresetFor(cfg.SoundAllComplete), vol)
}

/** Preview the all-complete cue at a given volume (settings panel). */
export function previewAllCompleteSound(volumePct: number, soundId: string, custom?: string): void {
  if (custom) {
    playCustomAudio(custom, volumePct)
    return
  }
  playTones(allCompletePresetFor(soundId), volumePct)
}

/** Preview a specific sound id at a given volume (settings panel). */
export function previewSound(sound: string, volumePct: number, custom?: string): void {
  if (custom) {
    playCustomAudio(custom, volumePct)
    return
  }
  playTones(presetFor(sound), volumePct)
}

/** Preview the error sound at a given volume (settings panel). */
export function previewErrorSound(volumePct: number, soundId: string, custom?: string): void {
  if (custom) {
    playCustomAudio(custom, volumePct)
    return
  }
  playTones(errorPresetFor(soundId), volumePct)
}

/** Preview the interaction sound at a given volume (settings panel). */
export function previewInteractionSound(volumePct: number, soundId: string, custom?: string): void {
  if (custom) {
    playCustomAudio(custom, volumePct)
    return
  }
  playTones(interactionPresetFor(soundId), volumePct)
}

// ---------------------------------------------------------------------------
// Config load / cache
// ---------------------------------------------------------------------------

let _cached: VoiceNotifyConfig | null = null
let _loadPromise: Promise<VoiceNotifyConfig> | null = null

/** Load the notify config from the voice actor, caching the result. */
export function loadNotifyConfig(force = false): Promise<VoiceNotifyConfig> {
  if (_cached && !force) return Promise.resolve(_cached)
  if (_loadPromise && !force) return _loadPromise
  const p = voice.getNotifyConfig(client)
    .then((resp): VoiceNotifyConfig => {
      const c = resp.Config
      _cached = {
        Enabled: c?.Enabled ?? DEFAULT_NOTIFY_CONFIG.Enabled,
        Volume: c?.Volume ?? DEFAULT_NOTIFY_CONFIG.Volume,
        Sound: c?.Sound || DEFAULT_NOTIFY_CONFIG.Sound,
        OnComplete: c?.OnComplete ?? DEFAULT_NOTIFY_CONFIG.OnComplete,
        OnError: c?.OnError ?? DEFAULT_NOTIFY_CONFIG.OnError,
        OnInteraction: c?.OnInteraction ?? DEFAULT_NOTIFY_CONFIG.OnInteraction,
        OnAllComplete: c?.OnAllComplete ?? DEFAULT_NOTIFY_CONFIG.OnAllComplete,
        SoundError: c?.SoundError || '',
        SoundInteraction: c?.SoundInteraction || '',
        SoundAllComplete: c?.SoundAllComplete || '',
        VolumeComplete: c?.VolumeComplete ?? 0,
        VolumeError: c?.VolumeError ?? 0,
        VolumeInteraction: c?.VolumeInteraction ?? 0,
        VolumeAllComplete: c?.VolumeAllComplete ?? 0,
        CustomSoundComplete: c?.CustomSoundComplete || '',
        CustomSoundError: c?.CustomSoundError || '',
        CustomSoundInteraction: c?.CustomSoundInteraction || '',
        CustomSoundAllComplete: c?.CustomSoundAllComplete || '',
      }
      return _cached
    })
    .catch((err: unknown): VoiceNotifyConfig => {
      console.warn('[notify-sound] failed to load config, using defaults:', err)
      _cached = { ...DEFAULT_NOTIFY_CONFIG }
      return _cached
    })
  _loadPromise = p
  p.finally(() => { _loadPromise = null })
  return p
}

/** Update the cached config after a settings save. */
export function setCachedNotifyConfig(cfg: VoiceNotifyConfig): void {
  _cached = { ...cfg }
}
