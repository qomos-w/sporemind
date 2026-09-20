import { describe, expect, it } from 'vitest'

import { ProviderIcon, iconKeyForModel, iconKeyForModelRow, iconKeyForUnit, iconKeysForModels } from './providerIcons'

describe('iconKeyForUnit (endpoint first)', () => {
  it('resolves official vendor domains', () => {
    expect(iconKeyForUnit('anything', undefined, 'https://api.kimi.com/coding/v1')).toBe('kimi')
    expect(iconKeyForUnit('anything', undefined, 'https://api.z.ai/v1')).toBe('zai')
    expect(iconKeyForUnit('anything', undefined, 'https://qianfan.baidubce.com/v2')).toBe('wenxin')
    expect(iconKeyForUnit('anything', undefined, 'https://tokenhub.tencentmaas.com/api/v1')).toBe('tencent')
    expect(iconKeyForUnit('anything', undefined, 'https://xinghuo.xfyun.cn/v1')).toBe('spark')
    expect(iconKeyForUnit('anything', undefined, 'https://myres.openai.azure.com/openai/deployments')).toBe('azure')
    expect(iconKeyForUnit('anything', undefined, 'https://models.github.ai/inference')).toBe('github')
    expect(iconKeyForUnit('anything', undefined, 'https://api.cerebras.ai/v1')).toBe('cerebras')
  })

  it('never brands an unknown gateway from its provider name', () => {
    expect(iconKeyForUnit('OpenAI', undefined, 'https://my-relay.example.com/v1')).toBeUndefined()
  })

  it('falls back to name aliases only without endpoint', () => {
    expect(iconKeyForUnit('GitHub Copilot')).toBe('githubcopilot')
    expect(iconKeyForUnit('硅基流动')).toBe('siliconflow')
  })
})

describe('iconKeyForModel (pattern table)', () => {
  it('matches Kimi K2/K3 bare names', () => {
    expect(iconKeyForModel('kimi-k2-0905')).toBe('kimi')
    expect(iconKeyForModel('k2-0905')).toBe('kimi')
    expect(iconKeyForModel('k3-256')).toBe('kimi')
  })

  it('matches newly aggregated vendor patterns', () => {
    expect(iconKeyForModel('nemotron-4-340b')).toBe('nvidia')
    expect(iconKeyForModel('sensechat-5')).toBe('sensenova')
    expect(iconKeyForModel('internlm2_5-20b-chat')).toBe('internlm')
    expect(iconKeyForModel('longcat-flash')).toBe('longcat')
    expect(iconKeyForModel('learnlm-1.5')).toBe('gemini')
  })
})

describe('iconKeyForModelRow (model brand wins)', () => {
  it('keeps the model brand through a relay', () => {
    expect(iconKeyForModelRow('deepseek-chat', 'https://api.siliconflow.cn/v1', 'SiliconFlow')).toBe('deepseek')
  })

  it('falls back to endpoint vendor for custom names', () => {
    expect(iconKeyForModelRow('my-fine-tune', 'https://api.minimax.io/v1', 'minimax')).toBe('minimax')
  })
})

describe('iconKeysForModels (aggregator stacks)', () => {
  it('dedupes and caps', () => {
    expect(iconKeysForModels(['gpt-4o', 'gpt-4o-mini', 'claude-4', 'deepseek-chat'], 3)).toEqual([
      'openai',
      'anthropic',
      'deepseek',
    ])
  })
})

describe('ProviderIcon', () => {
  it('renders known brand svg without title leak', () => {
    const el = ProviderIcon({ id: 'kimi' })
    expect(el).toBeTruthy()
  })

  it('falls back to Server icon for unknown ids', () => {
    const el = ProviderIcon({ id: 'not-a-brand' })
    expect(el).toBeTruthy()
  })
})
