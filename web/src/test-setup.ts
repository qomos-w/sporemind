globalThis.requestAnimationFrame = (cb) => setTimeout(cb, 0) as unknown as number
globalThis.cancelAnimationFrame = (id) => clearTimeout(id)
