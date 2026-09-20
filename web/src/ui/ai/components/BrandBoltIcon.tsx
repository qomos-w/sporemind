interface BrandBoltIconProps {
  size?: number
  /** Bolt stroke width in viewBox units; 104 matches the app icon proportion. */
  strokeWidth?: number
}

// Brand bolt mark from the current app icon (assets/icon.svg) as an inline line
// icon. The viewBox is cropped to the artwork bounds (square 676 at center
// 512,512) so the mark fills the icon box.
export function BrandBoltIcon({ size = 16, strokeWidth = 104 }: BrandBoltIconProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="174 174 676 676"
      fill="none"
      stroke="currentColor"
      strokeWidth={strokeWidth}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M680 226.2 L344 384.2 L616 512 L344 639.8 L680 797.8" />
    </svg>
  )
}
