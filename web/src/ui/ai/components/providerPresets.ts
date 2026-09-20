import type { ProviderModel } from '../../../gen-clients/system/types'
import type { I18nKey } from '../../../i18n'

export type ProviderKind = 'openai' | 'anthropic' | 'gemini' | 'responses'
export const PROVIDER_KINDS: readonly ProviderKind[] = ['openai', 'anthropic', 'gemini', 'responses']

export interface ProviderPreset {
  id: string
  label: string
  kind: 'openai' | 'anthropic' | 'gemini'
  endpoint: string
  models: ProviderModel[]
  /** Official console page where the user can create an API key. */
  keyUrl?: string
  /** One-line description shown in the onboarding subtitle. Frontend-only. */
  description?: string
  /** Brand key for ProviderIcon when it differs from the preset id. */
  icon?: string
}

export const PRESETS: ProviderPreset[] = [
  {
    id: 'openai',
    label: 'OpenAI',
    kind: 'openai',
    endpoint: 'https://api.openai.com/v1',
    keyUrl: 'https://platform.openai.com/api-keys',
    models: [
      { Name: 'gpt-5', MaxContextLength: 400000, MaxTokens: 128000 },
      { Name: 'gpt-5.5', MaxContextLength: 1050000, MaxTokens: 128000 },
    ],
  },
  {
    id: 'anthropic',
    label: 'Anthropic',
    kind: 'anthropic',
    endpoint: 'https://api.anthropic.com',
    keyUrl: 'https://console.anthropic.com/settings/keys',
    models: [
      { Name: 'claude-sonnet-4-6', MaxContextLength: 1048576, MaxTokens: 16384 },
      { Name: 'claude-opus-4-7', MaxContextLength: 1048576, MaxTokens: 16384 },
    ],
  },
  {
    id: 'deepseek',
    label: 'DeepSeek',
    kind: 'openai',
    endpoint: 'https://api.deepseek.com/v1',
    keyUrl: 'https://platform.deepseek.com/api_keys',
    models: [{ Name: 'deepseek-chat', MaxContextLength: 65536, MaxTokens: 8192 }],
  },
  {
    id: 'qwen',
    label: 'Qwen',
    kind: 'openai',
    endpoint: 'https://dashscope.aliyuncs.com/compatible-mode/v1',
    keyUrl: 'https://dashscope.console.aliyun.com/apiKey',
    models: [{ Name: 'qwen-max', MaxContextLength: 131072, MaxTokens: 8192 }],
  },
  {
    id: 'moonshot',
    label: 'Moonshot',
    kind: 'openai',
    endpoint: 'https://api.moonshot.cn/v1',
    keyUrl: 'https://platform.moonshot.cn/console/api-keys',
    models: [{ Name: 'moonshot-v1-128k', MaxContextLength: 128000, MaxTokens: 8192 }],
  },
  {
    id: 'moonshot-coding',
    label: 'Kimi Coding Plan',
    kind: 'openai',
    endpoint: 'https://api.kimi.com/coding/v1',
    keyUrl: 'https://www.kimi.com/code/console',
    icon: 'moonshot',
    description: 'Kimi 编程套餐(Kimi Code),OpenAI 兼容端点;高速版模型 ID 为 kimi-for-coding-highspeed。',
    models: [{ Name: 'kimi-for-coding', MaxContextLength: 256000, MaxTokens: 8192 }],
  },
  {
    id: 'zhipu',
    label: 'Zhipu GLM',
    kind: 'openai',
    endpoint: 'https://open.bigmodel.cn/api/paas/v4',
    keyUrl: 'https://open.bigmodel.cn/usercenter/apikeys',
    models: [{ Name: 'glm-4-plus', MaxContextLength: 128000, MaxTokens: 8192 }],
  },
  {
    id: 'zhipu-coding',
    label: 'Zhipu GLM Coding Plan',
    kind: 'anthropic',
    endpoint: 'https://open.bigmodel.cn/api/anthropic',
    keyUrl: 'https://open.bigmodel.cn/usercenter/apikeys',
    icon: 'zhipu',
    description: '智谱 GLM 编程套餐(Coding Plan)专用 Anthropic 兼容端点,供 Claude Code 等工具使用。',
    models: [{ Name: 'glm-5.3', MaxContextLength: 200000, MaxTokens: 128000 }],
  },
  {
    id: 'zai-coding',
    label: 'Z.ai GLM Coding Plan',
    kind: 'anthropic',
    endpoint: 'https://api.z.ai/api/anthropic',
    keyUrl: 'https://z.ai/manage/apikey',
    icon: 'zai',
    description: 'Z.ai GLM 编程套餐(国际版),Anthropic 兼容端点。',
    models: [{ Name: 'glm-5.3', MaxContextLength: 200000, MaxTokens: 128000 }],
  },
  {
    id: 'siliconflow',
    label: 'SiliconFlow',
    kind: 'openai',
    endpoint: 'https://api.siliconflow.cn/v1',
    keyUrl: 'https://cloud.siliconflow.cn/account/ak',
    models: [{ Name: 'Qwen/Qwen2.5-72B-Instruct', MaxContextLength: 32768, MaxTokens: 8192 }],
  },
  {
    id: 'openrouter',
    label: 'OpenRouter',
    kind: 'openai',
    endpoint: 'https://openrouter.ai/api/v1',
    keyUrl: 'https://openrouter.ai/settings/keys',
    models: [{ Name: 'openai/gpt-4o', MaxContextLength: 128000, MaxTokens: 16384 }],
  },
  {
    id: 'mistral',
    label: 'Mistral',
    kind: 'openai',
    endpoint: 'https://api.mistral.ai/v1',
    keyUrl: 'https://console.mistral.ai/api-keys',
    description: 'European LLM lab; openai-compatible /v1 endpoint.',
    models: [
      { Name: 'mistral-large-latest', MaxContextLength: 128000, MaxTokens: 8192 },
      { Name: 'mistral-small-latest', MaxContextLength: 32000, MaxTokens: 8192 },
      { Name: 'codestral-latest', MaxContextLength: 256000, MaxTokens: 8192 },
    ],
  },
  {
    id: 'xai',
    label: 'xAI (Grok)',
    kind: 'openai',
    endpoint: 'https://api.x.ai/v1',
    keyUrl: 'https://console.x.ai',
    description: 'Elon Musk lab; Grok models via openai-compatible /v1.',
    models: [
      { Name: 'grok-4', MaxContextLength: 256000, MaxTokens: 8192 },
      { Name: 'grok-3', MaxContextLength: 131072, MaxTokens: 8192 },
      { Name: 'grok-3-mini', MaxContextLength: 131072, MaxTokens: 8192 },
    ],
  },
  {
    id: 'groq',
    label: 'Groq',
    kind: 'openai',
    endpoint: 'https://api.groq.com/openai/v1',
    keyUrl: 'https://console.groq.com/keys',
    description: 'Ultra-low-latency inference (Llama, Mixtral).',
    models: [
      { Name: 'llama-3.3-70b-versatile', MaxContextLength: 131072, MaxTokens: 8192 },
      { Name: 'llama-3.1-8b-instant', MaxContextLength: 131072, MaxTokens: 8192 },
    ],
  },
  {
    id: 'together',
    label: 'Together AI',
    kind: 'openai',
    endpoint: 'https://api.together.xyz/v1',
    keyUrl: 'https://api.together.ai/settings/api-keys',
    description: 'Hosted open-weight models (Llama, Qwen, DeepSeek).',
    models: [
      { Name: 'meta-llama/Llama-3.3-70B-Instruct-Turbo', MaxContextLength: 131072, MaxTokens: 8192 },
      { Name: 'Qwen/Qwen2.5-72B-Instruct-Turbo', MaxContextLength: 32768, MaxTokens: 8192 },
    ],
  },
  {
    id: 'perplexity',
    label: 'Perplexity',
    kind: 'openai',
    endpoint: 'https://api.perplexity.ai',
    keyUrl: 'https://www.perplexity.ai/settings/api',
    description: 'Online Sonar models with built-in web search.',
    models: [
      { Name: 'sonar-pro', MaxContextLength: 200000, MaxTokens: 8192 },
      { Name: 'sonar', MaxContextLength: 127072, MaxTokens: 8192 },
      { Name: 'sonar-reasoning', MaxContextLength: 127072, MaxTokens: 8192 },
    ],
  },
  {
    id: 'fireworks',
    label: 'Fireworks AI',
    kind: 'openai',
    endpoint: 'https://api.fireworks.ai/inference/v1',
    keyUrl: 'https://fireworks.ai/account/api-keys',
    description: 'Fast open-weight model hosting.',
    models: [
      { Name: 'accounts/fireworks/models/llama-v3p1-70b-instruct', MaxContextLength: 131072, MaxTokens: 8192 },
      { Name: 'accounts/fireworks/models/llama-v3p1-8b-instruct', MaxContextLength: 131072, MaxTokens: 8192 },
    ],
  },
  {
    id: 'baichuan',
    label: 'Baichuan',
    kind: 'openai',
    endpoint: 'https://api.baichuan-ai.com/v1',
    keyUrl: 'https://platform.baichuan-ai.com/console/apikey',
    description: '百川智能 Baichuan4,OpenAI 兼容 /v1。',
    models: [
      { Name: 'Baichuan4-Turbo', MaxContextLength: 32768, MaxTokens: 4096 },
    ],
  },
  {
    id: 'hunyuan',
    label: 'Tencent Hunyuan',
    kind: 'openai',
    endpoint: 'https://api.hunyuan.cloud.tencent.com/v1',
    keyUrl: 'https://console.cloud.tencent.com/hunyuan/api-key',
    description: '腾讯混元,OpenAI 兼容端点。',
    models: [
      { Name: 'hunyuan-turbos-latest', MaxContextLength: 256000, MaxTokens: 4096 },
      { Name: 'hunyuan-pro', MaxContextLength: 32768, MaxTokens: 4096 },
    ],
  },
  {
    id: 'wenxin',
    label: 'Baidu ERNIE',
    kind: 'openai',
    endpoint: 'https://qianfan.baidubce.com/v2',
    keyUrl: 'https://console.bce.baidu.com/qianfan/ais/console/applicationConsole/application',
    description: '百度文心 ERNIE,千帆 v2 OpenAI 兼容端点。',
    models: [
      { Name: 'ernie-5.1', MaxContextLength: 128000, MaxTokens: 65536 },
      { Name: 'ernie-4.5-turbo-128k', MaxContextLength: 128000, MaxTokens: 8192 },
    ],
  },
  {
    id: 'doubao',
    label: 'Volcengine Doubao',
    kind: 'openai',
    endpoint: 'https://ark.cn-beijing.volces.com/api/v3',
    keyUrl: 'https://console.volcengine.com/ark/region:ark+cn-beijing/apiKey',
    description: '字节火山引擎豆包,Ark v3 OpenAI 兼容端点。',
    models: [
      { Name: 'doubao-1.5-pro-256k', MaxContextLength: 256000, MaxTokens: 4096 },
      { Name: 'doubao-pro-32k', MaxContextLength: 32000, MaxTokens: 4096 },
    ],
  },
  {
    id: 'minimax',
    label: 'MiniMax',
    kind: 'openai',
    endpoint: 'https://api.minimaxi.com/v1',
    keyUrl: 'https://platform.minimaxi.com/user-center/basic-information/interface-key',
    description: 'MiniMax M3,OpenAI 兼容 /v1。',
    models: [
      { Name: 'MiniMax-M3', MaxContextLength: 1000000, MaxTokens: 8192 },
      { Name: 'MiniMax-Text-01', MaxContextLength: 1000000, MaxTokens: 8192 },
    ],
  },
  {
    id: 'minimax-coding',
    label: 'MiniMax Coding Plan',
    kind: 'anthropic',
    endpoint: 'https://api.minimaxi.com/anthropic',
    keyUrl: 'https://platform.minimaxi.com/user-center/basic-information/interface-key',
    icon: 'minimax',
    description: 'MiniMax 编程套餐,Anthropic 兼容端点。',
    models: [{ Name: 'MiniMax-M3', MaxContextLength: 1000000, MaxTokens: 8192 }],
  },
  {
    id: 'longcat-coding',
    label: 'LongCat Coding Plan',
    kind: 'anthropic',
    endpoint: 'https://api.longcat.chat/anthropic',
    icon: 'longcat',
    description: '美团龙猫 LongCat 编程套餐,Anthropic 兼容端点。',
    models: [{ Name: 'LongCat-2.0', MaxContextLength: 128000, MaxTokens: 8192 }],
  },
  {
    id: 'spark',
    label: 'iFlyTek Spark',
    kind: 'openai',
    endpoint: 'https://spark-api-open.xf-yun.com/v1',
    keyUrl: 'https://console.xfyun.cn',
    description: '科大讯飞星火,OpenAI 兼容端点。',
    models: [
      { Name: '4.0Ultra', MaxContextLength: 128000, MaxTokens: 4096 },
      { Name: 'generalv3.5', MaxContextLength: 8000, MaxTokens: 4096 },
    ],
  },
  {
    id: 'stepfun',
    label: 'StepFun',
    kind: 'openai',
    endpoint: 'https://api.stepfun.com/v1',
    keyUrl: 'https://platform.stepfun.com',
    description: '阶跃星辰 Step 系列,OpenAI 兼容 /v1。',
    models: [
      { Name: 'step-2-16k', MaxContextLength: 16384, MaxTokens: 4096 },
      { Name: 'step-1x-32k', MaxContextLength: 32768, MaxTokens: 8192 },
    ],
  },
  {
    id: 'zeroone',
    label: '01.AI (Yi)',
    kind: 'openai',
    endpoint: 'https://api.lingyiwanwu.com/v1',
    keyUrl: 'https://platform.lingyiwanwu.com/apikeys',
    description: '零一万物 Yi 系列,OpenAI 兼容 /v1。',
    models: [
      { Name: 'yi-large', MaxContextLength: 32768, MaxTokens: 4096 },
      { Name: 'yi-lightning', MaxContextLength: 16384, MaxTokens: 4096 },
    ],
  },
  {
    id: 'modelscope',
    label: 'ModelScope',
    kind: 'openai',
    endpoint: 'https://api-inference.modelscope.cn/v1',
    keyUrl: 'https://modelscope.cn/my/myaccesstoken',
    description: '魔搭社区推理服务,聚合开源模型。',
    models: [
      { Name: 'Qwen/Qwen2.5-72B-Instruct', MaxContextLength: 32768, MaxTokens: 8192 },
      { Name: 'deepseek-ai/DeepSeek-V3', MaxContextLength: 64000, MaxTokens: 8192 },
    ],
  },
  {
    id: 'lmstudio',
    label: 'LM Studio (Local)',
    kind: 'openai',
    endpoint: 'http://localhost:1234/v1',
    description: '本地 LM Studio;无需 API Key,模型名由本地加载的 GGUF 决定,留空后可探测。',
    models: [],
  },
]

// inferKindFromEndpoint guesses the API protocol from the endpoint URL so the
// user rarely needs to pick Kind manually for Custom providers.
//   googleapis / gemini        → gemini (native generateContent)
//   anthropic                  → anthropic
//   everything else            → openai (the de-facto compat format)
export function inferKindFromEndpoint(endpoint: string): 'openai' | 'anthropic' | 'gemini' {
  const e = endpoint.toLowerCase()
  if (e.includes('googleapis.com') || e.includes('gemini')) return 'gemini'
  if (e.includes('anthropic.com') || e.includes('/anthropic')) return 'anthropic'
  return 'openai'
}

export function effectiveKind(kind: 'auto' | ProviderKind, endpoint: string): ProviderKind {
  return kind === 'auto' ? inferKindFromEndpoint(endpoint) : kind
}

// ── Preset localization ──────────────────────────────────────────────────────
// Labels/descriptions resolve through i18n keys (`settings.provider.preset.<id>.label`
// / `.desc`) and fall back to the preset's built-in copy when a catalog entry is
// missing. t() returns the key itself for unknown entries, which detects the miss.

type Translate = (key: I18nKey) => string

export function presetLabel(p: ProviderPreset, t: Translate): string {
  const key = `settings.provider.preset.${p.id}.label` as I18nKey
  const s = t(key)
  return s === key ? p.label : s
}

export function presetDescription(p: ProviderPreset, t: Translate): string | undefined {
  if (!p.description) return undefined
  const key = `settings.provider.preset.${p.id}.desc` as I18nKey
  const s = t(key)
  return s === key ? p.description : s
}
