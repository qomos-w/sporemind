declare module 'bun:test' {
  export function describe(name: string, fn: () => void): void
  export function it(name: string, fn: () => void | Promise<void>): void
  export function expect<T>(value: T): {
    toEqual(expected: unknown): void
    toBe(expected: unknown): void
    toHaveLength(expected: number): void
  }
}
