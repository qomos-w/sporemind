// Brand logos for provider presets, sourced from @lobehub/icons-static-svg (MIT).
// We inline the raw SVG (?raw):
//  - color variants carry explicit fills — brand colors render in any theme;
//  - mono-only brands (OpenAI, Moonshot, xAI, Groq, Ollama, LM Studio) use
//    fill="currentColor" and inherit the surrounding text color.
import { Server } from 'lucide-react'
import type { CSSProperties } from 'react'

import openaiRaw from '@lobehub/icons-static-svg/icons/openai.svg?raw'
import claudeRaw from '@lobehub/icons-static-svg/icons/claude-color.svg?raw'
import geminiRaw from '@lobehub/icons-static-svg/icons/gemini-color.svg?raw'
import deepseekRaw from '@lobehub/icons-static-svg/icons/deepseek-color.svg?raw'
import qwenRaw from '@lobehub/icons-static-svg/icons/qwen-color.svg?raw'
import zhipuRaw from '@lobehub/icons-static-svg/icons/zhipu-color.svg?raw'
import siliconcloudRaw from '@lobehub/icons-static-svg/icons/siliconcloud-color.svg?raw'
import openrouterRaw from '@lobehub/icons-static-svg/icons/openrouter-color.svg?raw'
import mistralRaw from '@lobehub/icons-static-svg/icons/mistral-color.svg?raw'
import cohereRaw from '@lobehub/icons-static-svg/icons/cohere-color.svg?raw'
import togetherRaw from '@lobehub/icons-static-svg/icons/together-color.svg?raw'
import perplexityRaw from '@lobehub/icons-static-svg/icons/perplexity-color.svg?raw'
import fireworksRaw from '@lobehub/icons-static-svg/icons/fireworks-color.svg?raw'
import baichuanRaw from '@lobehub/icons-static-svg/icons/baichuan-color.svg?raw'
import hunyuanRaw from '@lobehub/icons-static-svg/icons/hunyuan-color.svg?raw'
import wenxinRaw from '@lobehub/icons-static-svg/icons/wenxin-color.svg?raw'
import doubaoRaw from '@lobehub/icons-static-svg/icons/doubao-color.svg?raw'
import minimaxRaw from '@lobehub/icons-static-svg/icons/minimax-color.svg?raw'
import sparkRaw from '@lobehub/icons-static-svg/icons/spark-color.svg?raw'
import stepfunRaw from '@lobehub/icons-static-svg/icons/stepfun-color.svg?raw'
import zerooneRaw from '@lobehub/icons-static-svg/icons/zeroone-color.svg?raw'
import modelscopeRaw from '@lobehub/icons-static-svg/icons/modelscope-color.svg?raw'
import moonshotRaw from '@lobehub/icons-static-svg/icons/moonshot.svg?raw'
import kimiRaw from '@lobehub/icons-static-svg/icons/kimi-color.svg?raw'
import xaiRaw from '@lobehub/icons-static-svg/icons/xai.svg?raw'
import groqRaw from '@lobehub/icons-static-svg/icons/groq.svg?raw'
import ollamaRaw from '@lobehub/icons-static-svg/icons/ollama.svg?raw'
import lmstudioRaw from '@lobehub/icons-static-svg/icons/lmstudio.svg?raw'
import metaRaw from '@lobehub/icons-static-svg/icons/meta-color.svg?raw'
import yiRaw from '@lobehub/icons-static-svg/icons/yi-color.svg?raw'
import sensenovaRaw from '@lobehub/icons-static-svg/icons/sensenova-color.svg?raw'
import xiaomimimoRaw from '@lobehub/icons-static-svg/icons/xiaomimimo.svg?raw'
import opencodeRaw from '@lobehub/icons-static-svg/icons/opencode.svg?raw'
import zaiRaw from '@lobehub/icons-static-svg/icons/zai.svg?raw'
import longcatRaw from '@lobehub/icons-static-svg/icons/longcat-color.svg?raw'
import cerebrasRaw from '@lobehub/icons-static-svg/icons/cerebras-color.svg?raw'
import sambanovaRaw from '@lobehub/icons-static-svg/icons/sambanova-color.svg?raw'
import novitaRaw from '@lobehub/icons-static-svg/icons/novita-color.svg?raw'
import nvidiaRaw from '@lobehub/icons-static-svg/icons/nvidia-color.svg?raw'
import huggingfaceRaw from '@lobehub/icons-static-svg/icons/huggingface-color.svg?raw'
import jinaRaw from '@lobehub/icons-static-svg/icons/jina.svg?raw'
import voyageRaw from '@lobehub/icons-static-svg/icons/voyage-color.svg?raw'
import githubcopilotRaw from '@lobehub/icons-static-svg/icons/githubcopilot.svg?raw'
import githubRaw from '@lobehub/icons-static-svg/icons/github.svg?raw'
import vercelRaw from '@lobehub/icons-static-svg/icons/vercel.svg?raw'
import replicateRaw from '@lobehub/icons-static-svg/icons/replicate-brand.svg?raw'
import nebiusRaw from '@lobehub/icons-static-svg/icons/nebius.svg?raw'
import giteeaiRaw from '@lobehub/icons-static-svg/icons/giteeai.svg?raw'
import akashchatRaw from '@lobehub/icons-static-svg/icons/akashchat-color.svg?raw'
import ai302Raw from '@lobehub/icons-static-svg/icons/ai302-color.svg?raw'
import aihubmixRaw from '@lobehub/icons-static-svg/icons/aihubmix-color.svg?raw'
import ppioRaw from '@lobehub/icons-static-svg/icons/ppio-color.svg?raw'
import qiniuRaw from '@lobehub/icons-static-svg/icons/qiniu-color.svg?raw'
import cometapiRaw from '@lobehub/icons-static-svg/icons/cometapi-color.svg?raw'
import infiniaiRaw from '@lobehub/icons-static-svg/icons/infinigence-color.svg?raw'
import internlmRaw from '@lobehub/icons-static-svg/icons/internlm-color.svg?raw'
import azureRaw from '@lobehub/icons-static-svg/icons/azure-color.svg?raw'
import tencentRaw from '@lobehub/icons-static-svg/icons/tencent-color.svg?raw'

const ICON_RAW: Record<string, string> = {
  openai: openaiRaw,
  anthropic: claudeRaw,
  gemini: geminiRaw,
  deepseek: deepseekRaw,
  qwen: qwenRaw,
  zhipu: zhipuRaw,
  siliconflow: siliconcloudRaw,
  openrouter: openrouterRaw,
  mistral: mistralRaw,
  cohere: cohereRaw,
  together: togetherRaw,
  perplexity: perplexityRaw,
  fireworks: fireworksRaw,
  baichuan: baichuanRaw,
  hunyuan: hunyuanRaw,
  wenxin: wenxinRaw,
  doubao: doubaoRaw,
  minimax: minimaxRaw,
  spark: sparkRaw,
  stepfun: stepfunRaw,
  zeroone: zerooneRaw,
  modelscope: modelscopeRaw,
  moonshot: moonshotRaw,
  // Kimi's mark is a white body + blue accent, designed for dark surfaces.
  // Swap the white body to currentColor so it follows the theme text color.
  kimi: kimiRaw.replace(/fill="#fff"/g, 'fill="currentColor"'),
  xai: xaiRaw,
  groq: groqRaw,
  ollama: ollamaRaw,
  lmstudio: lmstudioRaw,
  meta: metaRaw,
  yi: yiRaw,
  sensenova: sensenovaRaw,
  xiaomimimo: xiaomimimoRaw,
  opencode: opencodeRaw,
  zai: zaiRaw,
  longcat: longcatRaw,
  cerebras: cerebrasRaw,
  sambanova: sambanovaRaw,
  novita: novitaRaw,
  nvidia: nvidiaRaw,
  huggingface: huggingfaceRaw,
  jina: jinaRaw,
  voyage: voyageRaw,
  githubcopilot: githubcopilotRaw,
  github: githubRaw,
  vercel: vercelRaw,
  replicate: replicateRaw,
  nebius: nebiusRaw,
  giteeai: giteeaiRaw,
  akashchat: akashchatRaw,
  ai302: ai302Raw.replace(/fill="#fff"/g, 'fill="currentColor"'),
  aihubmix: aihubmixRaw.replace(/fill="#fff"/g, 'fill="currentColor"'),
  cometapi: cometapiRaw.replace(/fill="#fff"/g, 'fill="currentColor"'),
  ppio: ppioRaw,
  qiniu: qiniuRaw,
  infiniai: infiniaiRaw,
  internlm: internlmRaw,
  azure: azureRaw,
  tencent: tencentRaw,
}

export interface ProviderIconProps {
  /** preset id (or provider kind fallback handled by caller) */
  id?: string
  size?: number
  className?: string
}

const spanStyle = (size: number): CSSProperties => ({
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  width: size,
  height: size,
  fontSize: size,
  lineHeight: 1,
  color: 'currentColor',
})

export function ProviderIcon({ id, size = 16, className }: ProviderIconProps) {
  const raw = id ? ICON_RAW[id] : undefined
  if (raw) {
    // Drop <title> so it doesn't leak into the host's textContent (breaks
    // label-based queries / double SR announcements) — the label names it.
    const clean = raw.replace(/<title>.*?<\/title>/, '')
    return <span className={className} style={spanStyle(size)} aria-hidden dangerouslySetInnerHTML={{ __html: clean }} />
  }
  return <Server size={size} className={className} />
}

// ── Unit → brand resolution ─────────────────────────────────────────────────
// Resolves an icon key for a unit. The ENDPOINT is the authoritative signal:
// a unit served through a vendor's official API domain gets that vendor's
// brand. Name/model heuristics only fill in when no endpoint is available
// (label-only rows: aggregator routes/refs) or, for the model row, when the
// endpoint is an unrecognized gateway — then the model's own brand still
// applies (the model is genuinely that model, wherever it is relayed from).

/** Official API host (substring of the normalized endpoint) → icon key. */
const ENDPOINT_BRANDS: Array<[host: string, key: string]> = [
  ['api.openai.com', 'openai'],
  ['api.anthropic.com', 'anthropic'],
  ['generativelanguage.googleapis.com', 'gemini'], ['aistudio.googleapis.com', 'gemini'],
  ['api.deepseek.com', 'deepseek'],
  ['dashscope.aliyuncs.com', 'qwen'],
  ['open.bigmodel.cn', 'zhipu'],
  ['api.siliconflow.cn', 'siliconflow'], ['api.siliconflow.com', 'siliconflow'],
  ['openrouter.ai', 'openrouter'],
  ['api.moonshot.cn', 'moonshot'], ['api.moonshot.ai', 'moonshot'],
  ['api.kimi.com', 'kimi'],
  ['api.minimax.chat', 'minimax'], ['api.minimaxi.com', 'minimax'],
  ['api.lingyiwanwu.com', 'zeroone'],
  ['api.stepfun.com', 'stepfun'],
  ['api.baichuan-ai.com', 'baichuan'],
  ['hunyuan.tencent.com', 'hunyuan'], ['tencentcloudapi.com', 'hunyuan'],
  ['baidubce.com', 'wenxin'],
  ['volces.com', 'doubao'],
  ['api.mistral.ai', 'mistral'],
  ['api.cohere.com', 'cohere'], ['api.cohere.ai', 'cohere'],
  ['api.together.xyz', 'together'],
  ['api.perplexity.ai', 'perplexity'],
  ['api.fireworks.ai', 'fireworks'],
  ['api.groq.com', 'groq'],
  ['api.x.ai', 'xai'],
  ['api.lmstudio.ai', 'lmstudio'],
  ['ollama.com', 'ollama'],
  ['sensenova.cn', 'sensenova'],
  ['xiaomimimo.com', 'xiaomimimo'],
  ['opencode.ai', 'opencode'],
  // Aggregated from Cherry Studio packages/provider-registry/data/providers.json
  // and lobe-chat packages/model-bank/src/modelProviders/ (2026-08).
  ['api.z.ai', 'zai'],
  ['api.minimax.io', 'minimax'],
  ['api.together.ai', 'together'],
  ['api.longcat.chat', 'longcat'],
  ['api.cerebras.ai', 'cerebras'],
  ['api.sambanova.ai', 'sambanova'],
  ['api.novita.ai', 'novita'],
  ['api.upstage.ai', 'upstage'],
  ['integrate.api.nvidia.com', 'nvidia'],
  ['router.huggingface.co', 'huggingface'],
  ['api.jina.ai', 'jina'],
  ['api.voyageai.com', 'voyage'],
  ['api.poe.com', 'poe'],
  ['models.github.ai', 'github'],
  ['api.githubcopilot.com', 'githubcopilot'],
  ['ai-gateway.vercel.sh', 'vercel'],
  ['api.replicate.com', 'replicate'],
  ['api.studio.nebius.com', 'nebius'],
  ['ai.gitee.com', 'giteeai'],
  ['chatapi.akash.network', 'akashchat'],
  ['api.302.ai', 'ai302'],
  ['aihubmix.com', 'aihubmix'],
  ['api.ppinfra.com', 'ppio'],
  ['api.qnaigc.com', 'qiniu'],
  ['openai.qiniu.com', 'qiniu'],
  ['api.cometapi.com', 'cometapi'],
  ['cloud.infini-ai.com', 'infiniai'],
  ['intern-ai.org.cn', 'internlm'],
  ['openai.azure.com', 'azure'],
  ['aiplatform.googleapis.com', 'gemini'],
  ['tokenhub.tencentmaas.com', 'tencent'],
  ['lkeap.cloud.tencent.com', 'tencent'],
  ['xfyun.cn', 'spark'],
]

/** Alias → icon key, ordered so compound/specific names win over generic ones. */
const PROVIDER_ALIASES: Array<[alias: string, key: string]> = [
  ['siliconflow', 'siliconflow'], ['siliconcloud', 'siliconflow'], ['硅基流动', 'siliconflow'],
  ['openrouter', 'openrouter'],
  ['lmstudio', 'lmstudio'], ['lm studio', 'lmstudio'],
  ['moonshot', 'moonshot'],
  ['openai', 'openai'], ['chatgpt', 'openai'],
  ['anthropic', 'anthropic'], ['claude', 'anthropic'],
  ['gemini', 'gemini'], ['google', 'gemini'],
  ['deepseek', 'deepseek'], ['deep seek', 'deepseek'],
  ['qwen', 'qwen'], ['tongyi', 'qwen'], ['alibaba', 'qwen'], ['通义', 'qwen'], ['百炼', 'qwen'],
  ['zhipu', 'zhipu'], ['智谱', 'zhipu'], ['chatglm', 'zhipu'], ['glm', 'zhipu'],
  ['mistral', 'mistral'],
  ['cohere', 'cohere'],
  ['together', 'together'],
  ['perplexity', 'perplexity'],
  ['fireworks', 'fireworks'],
  ['baichuan', 'baichuan'], ['百川', 'baichuan'],
  ['hunyuan', 'hunyuan'], ['tencent', 'hunyuan'], ['腾讯', 'hunyuan'], ['混元', 'hunyuan'],
  ['wenxin', 'wenxin'], ['ernie', 'wenxin'], ['baidu', 'wenxin'], ['百度', 'wenxin'], ['文心', 'wenxin'],
  ['doubao', 'doubao'], ['豆包', 'doubao'], ['volc', 'doubao'], ['火山', 'doubao'],
  ['minimax', 'minimax'],
  ['iflytek', 'spark'], ['讯飞', 'spark'], ['星火', 'spark'], ['spark', 'spark'],
  ['stepfun', 'stepfun'], ['step fun', 'stepfun'], ['阶跃', 'stepfun'],
  ['zeroone', 'zeroone'], ['01.ai', 'zeroone'], ['01ai', 'zeroone'], ['零一', 'zeroone'],
  ['modelscope', 'modelscope'], ['魔搭', 'modelscope'],
  ['xai', 'xai'], ['x.ai', 'xai'], ['grok', 'xai'],
  ['groq', 'groq'],
  ['ollama', 'ollama'],
  ['kimi', 'kimi'],
  ['llama', 'meta'], ['meta', 'meta'],
  ['zai', 'zai'], ['z.ai', 'zai'],
  ['longcat', 'longcat'],
  ['cerebras', 'cerebras'],
  ['sambanova', 'sambanova'],
  ['novita', 'novita'],
  ['nvidia', 'nvidia'],
  ['huggingface', 'huggingface'],
  ['jina', 'jina'],
  ['voyage', 'voyage'],
  ['poe', 'poe'],
  ['github copilot', 'githubcopilot'], ['copilot', 'githubcopilot'], ['github', 'github'],
  ['vercel', 'vercel'],
  ['replicate', 'replicate'],
  ['nebius', 'nebius'],
  ['gitee', 'giteeai'],
  ['akash', 'akashchat'],
  ['302', 'ai302'],
  ['aihubmix', 'aihubmix'],
  ['ppio', 'ppio'], ['ppinfra', 'ppio'],
  ['qiniu', 'qiniu'], ['七牛', 'qiniu'],
  ['cometapi', 'cometapi'],
  ['infini', 'infiniai'],
  ['internlm', 'internlm'], ['书生', 'internlm'],
  ['azure', 'azure'], ['azure openai', 'azure'],
]

/** Model name pattern → icon key, anchored where the token is a prefix. */
const MODEL_PATTERNS: Array<[pattern: RegExp, key: string]> = [
  [/claude|anthropic/, 'anthropic'],
  [/\bgpt|chatgpt|dall[-·]?e|^sora|^codex|whisper|^text-embedding|^o[134](-|$)/, 'openai'],
  [/gemini|^gemma|^imagen|palm|^bard|^learnlm|^veo/, 'gemini'],
  [/deepseek/, 'deepseek'],
  [/qwen|^qwq/, 'qwen'],
  [/^glm|chatglm|codegeex|cogview|cogvideo/, 'zhipu'],
  [/^moonshot/, 'moonshot'],
  [/^kimi/, 'kimi'],
  // Kimi K2/K3 series are served both prefixed (kimi-k2-…) and bare (k2-0905,
  // k3-256) on Moonshot/Kimi endpoints.
  [/^k[23]([-_.]|$)/, 'kimi'],
  [/^ernie|^wenxin/, 'wenxin'],
  [/^doubao/, 'doubao'],
  [/^hunyuan/, 'hunyuan'],
  [/^baichuan/, 'baichuan'],
  [/^minimax|^abab/, 'minimax'],
  [/^spark/, 'spark'],
  [/^step[-_ ]?\d|^step-(?:v|mini|turbo|lite)/, 'stepfun'],
  [/^yi-/, 'yi'],
  [/llama/, 'meta'],
  [/^mistral|^mixtral|^codestral|^ministral|^pixtral|^magistral/, 'mistral'],
  [/^command-[ra]|^cohere|^aya/, 'cohere'],
  [/^grok/, 'xai'],
  [/nemotron/, 'nvidia'],
  [/^longcat/, 'longcat'],
  [/^internlm/, 'internlm'],
  [/^sense(chat|nova|novel|voice|embedding)/, 'sensenova'],
]

/**
 * Resolve a brand icon key for a unit.
 *
 * Priority:
 *  1. `endpoint` matches an official vendor API domain → that vendor (the
 *     authoritative signal of who actually serves the unit).
 *  2. `endpoint` present but unrecognized (a relay/gateway): the provider NAME
 *     is NOT trusted for branding — a relay named "OpenAI" is not official.
 *     With a `model`, the model's own brand still applies; without one
 *     (provider-level surfaces) no brand is returned.
 *  3. No `endpoint` (label-only rows: aggregator routes/refs): fall back to
 *     provider-name aliases, then the model pattern.
 */
export function iconKeyForUnit(provider?: string | null, model?: string | null, endpoint?: string | null): string | undefined {
  const e = (endpoint ?? '').trim().toLowerCase()
  if (e) {
    for (const [host, key] of ENDPOINT_BRANDS) {
      if (e.includes(host)) return key
    }
    // Unofficial gateway: model brand only; never guess from the name.
    return iconKeyForModel(model)
  }
  const p = (provider ?? '').trim().toLowerCase()
  if (p) {
    for (const [alias, key] of PROVIDER_ALIASES) {
      if (p.includes(alias)) return key
    }
  }
  return iconKeyForModel(model)
}

/** Model name pattern → brand key (lowercased input, anchored where noted). */
export function iconKeyForModel(model?: string | null): string | undefined {
  const m = (model ?? '').trim().toLowerCase()
  if (!m) return undefined
  for (const [pattern, key] of MODEL_PATTERNS) {
    if (pattern.test(m)) return key
  }
  return undefined
}

/**
 * Model-row resolution: the model NAME decides the brand (a DeepSeek model
 * served via SiliconFlow shows the DeepSeek mark). Falls back to the official
 * endpoint domain (custom model names on a vendor endpoint) and finally the
 * provider-name alias (label-only rows).
 */
export function iconKeyForModelRow(model?: string | null, endpoint?: string | null, provider?: string | null): string | undefined {
  return iconKeyForModel(model) ?? iconKeyForUnit(provider, undefined, endpoint)
}

/**
 * Distinct brand icon keys for a list of model names, first-seen order,
 * capped at `limit`. Used to decorate aggregator rows with the first few
 * brands they pool.
 */
export function iconKeysForModels(models: Array<string | null | undefined>, limit = 3): string[] {
  const out: string[] = []
  for (const m of models) {
    const k = iconKeyForModel(m)
    if (k && !out.includes(k)) {
      out.push(k)
      if (out.length >= limit) break
    }
  }
  return out
}
