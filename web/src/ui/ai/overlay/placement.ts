export interface Rect {
  x: number
  y: number
  width: number
  height: number
}

export interface Size {
  width: number
  height: number
}

export interface Viewport {
  width: number
  height: number
}

export interface PlacementOpts {
  side: 'top' | 'bottom' | 'left' | 'right'
  align?: 'start' | 'center' | 'end'
  offset?: number
  viewportPadding?: number
}

export interface PlacementResult {
  x: number
  y: number
  actualSide: 'top' | 'bottom' | 'left' | 'right'
}

type Side = 'top' | 'bottom' | 'left' | 'right'

/**
 * Bounding box of two rects. Used to grow the anchor rect by a transient
 * "avoid" rect (e.g. a dropdown that expands from the anchor) so the
 * overlay is placed clear of both.
 */
export function unionRect(a: Rect, b: Rect): Rect {
  const x = Math.min(a.x, b.x)
  const y = Math.min(a.y, b.y)
  const right = Math.max(a.x + a.width, b.x + b.width)
  const bottom = Math.max(a.y + a.height, b.y + b.height)
  return { x, y, width: right - x, height: bottom - y }
}

const OPPOSITE: Record<Side, Side> = {
  top: 'bottom',
  bottom: 'top',
  left: 'right',
  right: 'left',
}

function clamp(value: number, min: number, max: number): number {
  return Math.max(min, Math.min(value, max))
}

function computeRawPosition(
  anchorRect: Rect,
  popupSize: Size,
  side: Side,
  align: NonNullable<PlacementOpts['align']>
): { x: number; y: number } {
  let x = 0
  let y = 0

  switch (side) {
    case 'top':
      y = anchorRect.y - popupSize.height
      break
    case 'bottom':
      y = anchorRect.y + anchorRect.height
      break
    case 'left':
      x = anchorRect.x - popupSize.width
      break
    case 'right':
      x = anchorRect.x + anchorRect.width
      break
  }

  if (side === 'top' || side === 'bottom') {
    switch (align) {
      case 'start':
        x = anchorRect.x
        break
      case 'end':
        x = anchorRect.x + anchorRect.width - popupSize.width
        break
      case 'center':
      default:
        x = anchorRect.x + anchorRect.width / 2 - popupSize.width / 2
        break
    }
  } else {
    switch (align) {
      case 'start':
        y = anchorRect.y
        break
      case 'end':
        y = anchorRect.y + anchorRect.height - popupSize.height
        break
      case 'center':
      default:
        y = anchorRect.y + anchorRect.height / 2 - popupSize.height / 2
        break
    }
  }

  return { x, y }
}

function primaryFits(
  anchorRect: Rect,
  popupSize: Size,
  side: Side,
  offset: number,
  padding: number,
  viewport: Viewport
): boolean {
  const { x, y } = computeRawPosition(anchorRect, popupSize, side, 'start')
  switch (side) {
    case 'top':
      return y - offset >= padding
    case 'bottom':
      return y + popupSize.height + offset <= viewport.height - padding
    case 'left':
      return x - offset >= padding
    case 'right':
      return x + popupSize.width + offset <= viewport.width - padding
  }
}

export function computePlacement(
  anchorRect: Rect,
  popupSize: Size,
  opts: PlacementOpts,
  viewport: Viewport
): PlacementResult {
  const {
    side: preferredSide,
    align = 'center',
    offset = 0,
    viewportPadding = 0,
  } = opts

  const padding = viewportPadding

  const actualSide = primaryFits(anchorRect, popupSize, preferredSide, offset, padding, viewport)
    ? preferredSide
    : OPPOSITE[preferredSide]

  let { x, y } = computeRawPosition(anchorRect, popupSize, actualSide, align)

  switch (actualSide) {
    case 'top':
      y -= offset
      break
    case 'bottom':
      y += offset
      break
    case 'left':
      x -= offset
      break
    case 'right':
      x += offset
      break
  }

  const maxX = viewport.width - padding - popupSize.width
  const maxY = viewport.height - padding - popupSize.height

  return {
    x: clamp(x, padding, maxX),
    y: clamp(y, padding, maxY),
    actualSide,
  }
}
