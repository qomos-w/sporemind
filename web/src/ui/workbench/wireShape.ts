/**
 * Pascalize object keys in place. gospore publishes projection components with
 * lowerFirst field keys (`cards`, `actorId`, … — projection/snapshotStruct
 * contract) while schema-encoded actor callable replies arrive PascalCase;
 * both wire shapes must read identically downstream.
 */
export function pascalize<T>(value: T): T {
  if (Array.isArray(value)) return value.map(pascalize) as unknown as T
  if (value !== null && typeof value === 'object') {
    const out: Record<string, unknown> = {}
    for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
      out[/^[a-z]/.test(k) ? `${k[0]!.toUpperCase()}${k.slice(1)}` : k] = pascalize(v)
    }
    return out as T
  }
  return value
}
