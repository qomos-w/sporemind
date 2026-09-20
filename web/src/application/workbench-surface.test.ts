import { afterEach, describe, expect, it } from 'vitest'
import { WORKBENCH_SURFACE_CLASS, applyWorkbenchSurface } from './workbench-surface'

afterEach(() => {
  document.body.classList.remove(WORKBENCH_SURFACE_CLASS)
})

describe('applyWorkbenchSurface', () => {
  it('adds the body-level marker class when the board is mounted', () => {
    applyWorkbenchSurface(true)
    expect(document.body.classList.contains(WORKBENCH_SURFACE_CLASS)).toBe(true)
  })

  it('removes the body-level marker class when the board unmounts', () => {
    applyWorkbenchSurface(true)
    applyWorkbenchSurface(false)
    expect(document.body.classList.contains(WORKBENCH_SURFACE_CLASS)).toBe(false)
  })
})
