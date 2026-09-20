const IMAGE_EXTS = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'ico', 'bmp'])

const MIME_BY_EXT: Record<string, string> = {
  png: 'image/png',
  jpg: 'image/jpeg',
  jpeg: 'image/jpeg',
  gif: 'image/gif',
  webp: 'image/webp',
  svg: 'image/svg+xml',
  ico: 'image/x-icon',
  bmp: 'image/bmp',
}

function extOf(path: string): string {
  return path.split('.').pop()?.toLowerCase() ?? ''
}

export function isImageExt(path: string): boolean {
  return IMAGE_EXTS.has(extOf(path))
}

export function mimeFromExt(path: string): string {
  return MIME_BY_EXT[extOf(path)] ?? 'application/octet-stream'
}
