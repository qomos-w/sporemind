import { describe, it, expect } from 'vitest'
import {
  MODEL_CONTEXT_DEFAULTS,
  lookupModelDefault,
} from '../../../../../const/modelContextDefaults'
import type { ModelDefault } from '../../../gen-clients/system/types'
import {
  isBuiltinDefault,
  isUnsetMaxTokens,
  isUnsetModality,
  modelDefaultsLookupTable,
} from './modelDefaults'

const GLM52_BASELINE = MODEL_CONTEXT_DEFAULTS['glm-5.2'] as number

describe('modelDefaults helpers', () => {
  describe('isUnsetMaxTokens', () => {
    it('treats undefined as unset', () => {
      expect(isUnsetMaxTokens(undefined)).toBe(true)
    })
    it('treats zero as unset', () => {
      expect(isUnsetMaxTokens(0)).toBe(true)
    })
    it('treats negative as unset', () => {
      expect(isUnsetMaxTokens(-5)).toBe(true)
    })
    it('treats positive as set', () => {
      expect(isUnsetMaxTokens(16384)).toBe(false)
    })
  })

  describe('isUnsetModality', () => {
    it('treats undefined as unset', () => {
      expect(isUnsetModality(undefined)).toBe(true)
    })
    it('treats empty as unset', () => {
      expect(isUnsetModality('')).toBe(true)
    })
    it('treats chat as unset', () => {
      expect(isUnsetModality('chat')).toBe(true)
    })
    it('treats image as set', () => {
      expect(isUnsetModality('image')).toBe(false)
    })
  })

  describe('isBuiltinDefault', () => {
    it('returns false for prefix not in baseline', () => {
      expect(isBuiltinDefault({ Prefix: 'my-model', MaxContextLength: 128000 })).toBe(false)
    })
    it('returns true for pure baseline copy', () => {
      const row: ModelDefault = { Prefix: 'glm-5.2', MaxContextLength: GLM52_BASELINE }
      expect(isBuiltinDefault(row)).toBe(true)
    })
    it('returns false when context differs from baseline', () => {
      const row: ModelDefault = { Prefix: 'glm-5.2', MaxContextLength: 999999 }
      expect(isBuiltinDefault(row)).toBe(false)
    })
    it('returns false when maxTokens is set on a baseline row', () => {
      const row: ModelDefault = {
        Prefix: 'glm-5.2',
        MaxContextLength: GLM52_BASELINE,
        MaxTokens: 8192,
      }
      expect(isBuiltinDefault(row)).toBe(false)
    })
    it('returns false when modality is image on a baseline row', () => {
      const row: ModelDefault = {
        Prefix: 'glm-5.2',
        MaxContextLength: GLM52_BASELINE,
        Modality: 'image',
      }
      expect(isBuiltinDefault(row)).toBe(false)
    })
    it('returns false when cost input is set on a baseline row', () => {
      const row: ModelDefault = {
        Prefix: 'glm-5.2',
        MaxContextLength: GLM52_BASELINE,
        CostInput: 1.5,
      }
      expect(isBuiltinDefault(row)).toBe(false)
    })
    it('returns true for baseline row with modality chat (implicit default)', () => {
      const row: ModelDefault = {
        Prefix: 'glm-5.2',
        MaxContextLength: GLM52_BASELINE,
        Modality: 'chat',
      }
      expect(isBuiltinDefault(row)).toBe(true)
    })
  })

  describe('modelDefaultsLookupTable', () => {
    it('returns the baseline when given no user rows', () => {
      const table = modelDefaultsLookupTable([])
      expect(table['glm-5.2']).toBe(GLM52_BASELINE)
    })
    it('user rows override baseline entries', () => {
      const baseline = GLM52_BASELINE
      const table = modelDefaultsLookupTable([{ Prefix: 'glm-5.2', MaxContextLength: baseline + 100 }])
      expect(table['glm-5.2']).toBe(baseline + 100)
    })
    it('user rows augment the table with new prefixes', () => {
      const table = modelDefaultsLookupTable([{ Prefix: 'my-custom-', MaxContextLength: 999 }])
      expect(table['my-custom-']).toBe(999)
      expect(table['glm-5.2']).toBe(GLM52_BASELINE)
    })
    it('skips rows with empty prefix or non-positive context', () => {
      const table = modelDefaultsLookupTable([
        { Prefix: '', MaxContextLength: 100 },
        { Prefix: 'bad', MaxContextLength: 0 },
        { Prefix: 'good', MaxContextLength: 200 },
      ])
      expect(table['good']).toBe(200)
      expect(table['']).toBeUndefined()
      expect(table['bad']).toBeUndefined()
    })
    it('feeds lookupModelDefault so prefix matching resolves against the merged table', () => {
      const table = modelDefaultsLookupTable([{ Prefix: 'my-custom-', MaxContextLength: 999 }])
      expect(lookupModelDefault('my-custom-v1', table)).toBe(999)
      expect(lookupModelDefault('glm-5.2-0733', table)).toBe(GLM52_BASELINE)
      expect(lookupModelDefault('no-match', table)).toBeUndefined()
    })
  })
})
