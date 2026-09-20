import { loadPreference, savePreference } from './theme-persist'

export interface AppBackground {
  /** data: URLs of the (possibly downscaled) images. */
  images: string[]
  /** Index of the image currently applied as the app background; -1 means default (no image). */
  active: number
}

const PREF_KEY = 'background.app.v1'
const MAX_DIMENSION = 1920
const KEEP_ORIGINAL_MAX_BYTES = 800 * 1024
let writeVersion = 0

export function parseAppBackground(raw: string | undefined): AppBackground | null {
  if (!raw) return null
  try {
    const parsed = JSON.parse(raw) as { images?: unknown; active?: unknown; image?: unknown }
    if (Array.isArray(parsed.images)) {
      const images = parsed.images.filter(
        (i): i is string => typeof i === 'string' && i.startsWith('data:image/'),
      )
      if (images.length === 0) return null
      const active = typeof parsed.active === 'number' ? parsed.active : 0
      return { images, active: Math.min(Math.max(Math.trunc(active), -1), images.length - 1) }
    }
    // Legacy single-image entry.
    if (typeof parsed.image === 'string' && parsed.image.startsWith('data:image/')) {
      return { images: [parsed.image], active: 0 }
    }
  } catch {
    // corrupted entry — treat as unset
  }
  return null
}

export function activeBackgroundImage(bg: AppBackground | null): string | null {
  if (!bg || bg.active < 0) return null
  return bg.images[bg.active] ?? null
}

export function addBackgroundImage(bg: AppBackground | null, image: string): AppBackground {
  const images = [...(bg?.images ?? []), image]
  return { images, active: images.length - 1 }
}

export function selectBackgroundImage(bg: AppBackground, index: number): AppBackground {
  if (index < -1 || index >= bg.images.length || index === bg.active) return bg
  return { ...bg, active: index }
}

export function removeBackgroundImage(bg: AppBackground, index: number): AppBackground | null {
  if (index < 0 || index >= bg.images.length) return bg
  const images = bg.images.filter((_, i) => i !== index)
  if (images.length === 0) return null
  let active = bg.active
  if (index < active) active -= 1
  if (active >= images.length) active = images.length - 1
  return { images, active }
}

export async function loadAppBackground(): Promise<AppBackground | null> {
  return parseAppBackground(await loadPreference(PREF_KEY))
}

export async function persistAppBackground(bg: AppBackground | null): Promise<void> {
  await savePreference(PREF_KEY, bg ? JSON.stringify(bg) : '', 'app-background', { v: writeVersion })
}

export function applyAppBackground(bg: AppBackground | null): void {
  const body = document.body
  const image = activeBackgroundImage(bg)
  if (!image) {
    body.classList.remove('app-bg-active')
    body.style.removeProperty('--app-bg-image')
    return
  }
  body.style.setProperty('--app-bg-image', `url("${image}")`)
  body.classList.add('app-bg-active')
}

/**
 * Read a picked image file into a data URL, downscaling to MAX_DIMENSION and
 * re-encoding as webp when the original is large. Animated/vector formats and
 * decode failures fall back to the original data URL.
 */
export async function imageFileToDataUrl(file: File): Promise<string> {
  const original = await readFileAsDataUrl(file)
  if (file.type === 'image/gif' || file.type === 'image/svg+xml') return original
  try {
    const bitmap = await createImageBitmap(file)
    try {
      const scale = Math.min(1, MAX_DIMENSION / Math.max(bitmap.width, bitmap.height))
      if (scale === 1 && file.size <= KEEP_ORIGINAL_MAX_BYTES) return original
      const canvas = document.createElement('canvas')
      canvas.width = Math.max(1, Math.round(bitmap.width * scale))
      canvas.height = Math.max(1, Math.round(bitmap.height * scale))
      const ctx = canvas.getContext('2d')
      if (!ctx) return original
      ctx.drawImage(bitmap, 0, 0, canvas.width, canvas.height)
      const webp = canvas.toDataURL('image/webp', 0.85)
      return webp.startsWith('data:image/webp') ? webp : canvas.toDataURL('image/jpeg', 0.85)
    } finally {
      bitmap.close()
    }
  } catch {
    return original
  }
}

function readFileAsDataUrl(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result))
    reader.onerror = () => reject(reader.error)
    reader.readAsDataURL(file)
  })
}
