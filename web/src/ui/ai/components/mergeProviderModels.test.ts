import { describe, it, expect } from 'vitest'
import { mergeProviderModels } from './ShellProviderSettings'
import type { ProviderModel } from '../../../gen-clients/system/types'

describe('mergeProviderModels', () => {
  it('appends new upstream models and preserves local-only models', () => {
    const existing: ProviderModel[] = [
      { Name: 'keep-me', MaxContextLength: 200000, MaxTokens: 8192, Protocol: 'openai' },
    ]
    const fetched: ProviderModel[] = [
      { Name: 'keep-me', Modality: 'chat', Protocol: 'openai' },
      { Name: 'new-from-upstream', Modality: 'image', Protocol: 'openai' },
    ]

    const merged = mergeProviderModels(existing, fetched, 'openai')

    const names = merged.map(m => m.Name)
    expect(names).toEqual(['keep-me', 'new-from-upstream'])
  })

  it('preserves local-only models removed from upstream at the tail', () => {
    const existing: ProviderModel[] = [
      { Name: 'gpt-5', MaxContextLength: 128000, MaxTokens: 16384 },
      { Name: 'gpt-4', MaxContextLength: 8192, MaxTokens: 4096 },
    ]
    const fetched: ProviderModel[] = [
      { Name: 'gpt-5', Modality: 'chat', Protocol: 'openai' },
    ]

    const merged = mergeProviderModels(existing, fetched, 'openai')

    expect(merged.map(m => m.Name)).toEqual(['gpt-5', 'gpt-4'])
    const gpt4 = merged.find(m => m.Name === 'gpt-4')!
    expect(gpt4.MaxContextLength).toBe(8192)
    expect(gpt4.MaxTokens).toBe(4096)
  })

  it('keeps every user-configured field untouched on same-name re-fetch', () => {
    const existing: ProviderModel[] = [{
      Name: 'gpt-5',
      MaxContextLength: 256000,
      MaxTokens: 32768,
      CostInput: 5,
      CostOutput: 15,
      CostCacheRead: 0.5,
      CostCacheWrite: 6.25,
      CostTiers: [{ InputTokensAbove: 0, CostInput: 5, CostOutput: 15, CostCacheRead: 0.5, CostCacheWrite: 6.25 }],
      ProbeState: 'ok',
      ProbeLatencyMs: 321,
      ProbeError: '',
      ProbeAt: 1234567890,
      Modality: 'chat',
      Protocol: 'openai',
    }]
    const fetched: ProviderModel[] = [{
      Name: 'gpt-5',
      Modality: 'chat',
      Protocol: 'openai',
      MaxContextLength: 999,
      MaxTokens: 999,
      CostInput: 999,
      CostOutput: 999,
    }]

    const merged = mergeProviderModels(existing, fetched, 'openai')
    const m = merged[0]!

    expect(m.MaxContextLength).toBe(256000)
    expect(m.MaxTokens).toBe(32768)
    expect(m.CostInput).toBe(5)
    expect(m.CostOutput).toBe(15)
    expect(m.CostCacheRead).toBe(0.5)
    expect(m.CostCacheWrite).toBe(6.25)
    expect(m.CostTiers).toEqual([{ InputTokensAbove: 0, CostInput: 5, CostOutput: 15, CostCacheRead: 0.5, CostCacheWrite: 6.25 }])
    expect(m.ProbeState).toBe('ok')
    expect(m.ProbeLatencyMs).toBe(321)
    expect(m.ProbeError).toBe('')
    expect(m.ProbeAt).toBe(1234567890)
  })

  it('updates Modality from upstream when the local value is the inferred default (undefined)', () => {
    const existing: ProviderModel[] = [{ Name: 'm', Protocol: 'openai' }]
    const fetched: ProviderModel[] = [{ Name: 'm', Modality: 'image', Protocol: 'openai' }]

    const merged = mergeProviderModels(existing, fetched, 'openai')

    expect(merged[0]!.Modality).toBe('image')
  })

  it('updates Modality from upstream when the local value is the inferred default (chat)', () => {
    const existing: ProviderModel[] = [{ Name: 'm', Modality: 'chat', Protocol: 'openai' }]
    const fetched: ProviderModel[] = [{ Name: 'm', Modality: 'image', Protocol: 'openai' }]

    const merged = mergeProviderModels(existing, fetched, 'openai')

    expect(merged[0]!.Modality).toBe('image')
  })

  it('does not overwrite a user-set Modality (e.g. image) on re-fetch', () => {
    const existing: ProviderModel[] = [{ Name: 'm', Modality: 'image', Protocol: 'openai' }]
    const fetched: ProviderModel[] = [{ Name: 'm', Modality: 'chat', Protocol: 'openai' }]

    const merged = mergeProviderModels(existing, fetched, 'openai')

    expect(merged[0]!.Modality).toBe('image')
  })

  it('does not update Modality when the upstream does not advertise one', () => {
    const existing: ProviderModel[] = [{ Name: 'm', Modality: 'image', Protocol: 'openai' }]
    const fetched: ProviderModel[] = [{ Name: 'm', Protocol: 'openai' }]

    const merged = mergeProviderModels(existing, fetched, 'openai')

    expect(merged[0]!.Modality).toBe('image')
  })

  it('updates Protocol from upstream when the local value is undefined', () => {
    const existing: ProviderModel[] = [{ Name: 'm', MaxTokens: 8192 }]
    const fetched: ProviderModel[] = [{ Name: 'm', Modality: 'chat', Protocol: 'anthropic' }]

    const merged = mergeProviderModels(existing, fetched, 'openai')

    expect(merged[0]!.Protocol).toBe('anthropic')
  })

  it('updates Protocol from upstream when the local value equals the effective kind', () => {
    const existing: ProviderModel[] = [{ Name: 'm', Protocol: 'openai', MaxTokens: 8192 }]
    const fetched: ProviderModel[] = [{ Name: 'm', Modality: 'chat', Protocol: 'anthropic' }]

    const merged = mergeProviderModels(existing, fetched, 'openai')

    expect(merged[0]!.Protocol).toBe('anthropic')
  })

  it('does not overwrite a user-set Protocol on re-fetch', () => {
    const existing: ProviderModel[] = [{ Name: 'm', Protocol: 'gemini', MaxTokens: 8192 }]
    const fetched: ProviderModel[] = [{ Name: 'm', Modality: 'chat', Protocol: 'openai' }]

    const merged = mergeProviderModels(existing, fetched, 'openai')

    expect(merged[0]!.Protocol).toBe('gemini')
  })

  it('does not update Protocol when upstream omits it', () => {
    const existing: ProviderModel[] = [{ Name: 'm', Protocol: 'openai', MaxTokens: 8192 }]
    const fetched: ProviderModel[] = [{ Name: 'm', Modality: 'chat' }]

    const merged = mergeProviderModels(existing, fetched, 'openai')

    expect(merged[0]!.Protocol).toBe('openai')
  })

  it('handles duplicate upstream names by merging each against the existing model', () => {
    // Duplicate upstream names are unusual but should not crash; each upstream
    // entry resolves against the same existing model and keeps its user fields.
    const existing: ProviderModel[] = [{ Name: 'dup', MaxTokens: 8192, Protocol: 'openai' }]
    const fetched: ProviderModel[] = [
      { Name: 'dup', Modality: 'chat', Protocol: 'openai' },
      { Name: 'dup', Modality: 'image', Protocol: 'openai' },
    ]

    const merged = mergeProviderModels(existing, fetched, 'openai')

    expect(merged.filter(m => m.Name === 'dup')).toHaveLength(2)
    // Both entries keep the existing user-configured MaxTokens.
    expect(merged[0]!.MaxTokens).toBe(8192)
    expect(merged[1]!.MaxTokens).toBe(8192)
    // The existing Modality is undefined (inferred); each entry adopts its own
    // upstream Modality.
    expect(merged[0]!.Modality).toBe('chat')
    expect(merged[1]!.Modality).toBe('image')
  })

  it('returns upstream models as-is when there are no existing models', () => {
    const fetched: ProviderModel[] = [
      { Name: 'a', Modality: 'chat', Protocol: 'openai' },
      { Name: 'b', Modality: 'image', Protocol: 'openai' },
    ]

    const merged = mergeProviderModels([], fetched, 'openai')

    expect(merged).toEqual(fetched)
  })
})
