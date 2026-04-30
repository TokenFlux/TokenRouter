export type ProviderBrandKey =
  | 'anthropic'
  | 'deepseek'
  | 'google'
  | 'moonshot'
  | 'openai'
  | 'zhipu'
  | 'alibaba'
  | 'xai'
  | 'midjourney'
  | 'mistral'
  | 'meta'
  | 'cohere'
  | 'perplexity'
  | 'openrouter'
  | 'ollama'
  | 'baidu'
  | 'iflytek'
  | 'tencent'
  | 'ai360'
  | 'zeroone'
  | 'doubao'
  | 'xiaomi'
  | 'minimax'
  | 'suno'
  | 'dify'
  | 'coze'
  | 'cloudflare'
  | 'jina'
  | 'unknown'

export interface ProviderBrand {
  key: ProviderBrandKey
  label: string
  iconKey: string | null
  iconColor: string
  badgeClass: string
  iconWrapClass: string
}

export interface ProviderBrandOption {
  value: string
  label: string
  [key: string]: unknown
}

const providerBrands: Record<ProviderBrandKey, ProviderBrand> = {
  anthropic: {
    key: 'anthropic',
    label: 'Anthropic',
    iconKey: 'anthropic',
    iconColor: '#D97706',
    badgeClass: 'bg-orange-100 text-orange-900 ring-orange-200 dark:bg-orange-500/20 dark:text-orange-50 dark:ring-orange-400/30',
    iconWrapClass: 'bg-orange-50 text-orange-700 ring-orange-200 dark:bg-orange-500/15 dark:text-orange-200 dark:ring-orange-400/30',
  },
  deepseek: {
    key: 'deepseek',
    label: 'DeepSeek',
    iconKey: 'deepseek',
    iconColor: '#4D6BFE',
    badgeClass: 'bg-blue-100 text-blue-900 ring-blue-200 dark:bg-blue-500/20 dark:text-blue-50 dark:ring-blue-400/30',
    iconWrapClass: 'bg-blue-50 text-blue-700 ring-blue-200 dark:bg-blue-500/15 dark:text-blue-200 dark:ring-blue-400/30',
  },
  google: {
    key: 'google',
    label: 'Google',
    iconKey: 'google',
    iconColor: '#4285F4',
    badgeClass: 'bg-sky-100 text-sky-900 ring-sky-200 dark:bg-sky-500/20 dark:text-sky-50 dark:ring-sky-400/30',
    iconWrapClass: 'bg-sky-50 text-sky-700 ring-sky-200 dark:bg-sky-500/15 dark:text-sky-200 dark:ring-sky-400/30',
  },
  moonshot: {
    key: 'moonshot',
    label: 'Moonshot',
    iconKey: 'moonshot',
    iconColor: '#16191E',
    badgeClass: 'bg-zinc-100 text-zinc-900 ring-zinc-200 dark:bg-zinc-500/20 dark:text-zinc-50 dark:ring-zinc-400/30',
    iconWrapClass: 'bg-zinc-50 text-zinc-700 ring-zinc-200 dark:bg-zinc-500/15 dark:text-zinc-200 dark:ring-zinc-400/30',
  },
  openai: {
    key: 'openai',
    label: 'OpenAI',
    iconKey: 'openai',
    iconColor: '#10A37F',
    badgeClass: 'bg-emerald-100 text-emerald-900 ring-emerald-200 dark:bg-emerald-500/20 dark:text-emerald-50 dark:ring-emerald-400/30',
    iconWrapClass: 'bg-emerald-50 text-emerald-700 ring-emerald-200 dark:bg-emerald-500/15 dark:text-emerald-200 dark:ring-emerald-400/30',
  },
  zhipu: {
    key: 'zhipu',
    label: '智谱',
    iconKey: 'zhipu',
    iconColor: '#3859FF',
    badgeClass: 'bg-indigo-100 text-indigo-900 ring-indigo-200 dark:bg-indigo-500/20 dark:text-indigo-50 dark:ring-indigo-400/30',
    iconWrapClass: 'bg-indigo-50 text-indigo-700 ring-indigo-200 dark:bg-indigo-500/15 dark:text-indigo-200 dark:ring-indigo-400/30',
  },
  alibaba: {
    key: 'alibaba',
    label: '阿里巴巴',
    iconKey: 'alibaba',
    iconColor: '#FF6A00',
    badgeClass: 'bg-amber-100 text-amber-900 ring-amber-200 dark:bg-amber-500/20 dark:text-amber-50 dark:ring-amber-400/30',
    iconWrapClass: 'bg-amber-50 text-amber-700 ring-amber-200 dark:bg-amber-500/15 dark:text-amber-200 dark:ring-amber-400/30',
  },
  xai: {
    key: 'xai',
    label: 'xAI',
    iconKey: 'xai',
    iconColor: '#111827',
    badgeClass: 'bg-neutral-100 text-neutral-900 ring-neutral-200 dark:bg-neutral-500/20 dark:text-neutral-50 dark:ring-neutral-400/30',
    iconWrapClass: 'bg-neutral-50 text-neutral-800 ring-neutral-200 dark:bg-neutral-500/15 dark:text-neutral-200 dark:ring-neutral-400/30',
  },
  midjourney: {
    key: 'midjourney',
    label: 'Midjourney',
    iconKey: 'midjourney',
    iconColor: '#111827',
    badgeClass: 'bg-gray-100 text-gray-900 ring-gray-200 dark:bg-gray-500/20 dark:text-gray-50 dark:ring-gray-400/30',
    iconWrapClass: 'bg-gray-50 text-gray-800 ring-gray-200 dark:bg-gray-500/15 dark:text-gray-200 dark:ring-gray-400/30',
  },
  mistral: {
    key: 'mistral',
    label: 'Mistral',
    iconKey: 'mistral',
    iconColor: '#F7D046',
    badgeClass: 'bg-yellow-100 text-yellow-900 ring-yellow-200 dark:bg-yellow-500/20 dark:text-yellow-50 dark:ring-yellow-400/30',
    iconWrapClass: 'bg-yellow-50 text-yellow-700 ring-yellow-200 dark:bg-yellow-500/15 dark:text-yellow-200 dark:ring-yellow-400/30',
  },
  meta: {
    key: 'meta',
    label: 'Meta',
    iconKey: 'meta',
    iconColor: '#0668E1',
    badgeClass: 'bg-blue-100 text-blue-900 ring-blue-200 dark:bg-blue-500/20 dark:text-blue-50 dark:ring-blue-400/30',
    iconWrapClass: 'bg-blue-50 text-blue-700 ring-blue-200 dark:bg-blue-500/15 dark:text-blue-200 dark:ring-blue-400/30',
  },
  cohere: {
    key: 'cohere',
    label: 'Cohere',
    iconKey: 'cohere',
    iconColor: '#39594D',
    badgeClass: 'bg-teal-100 text-teal-900 ring-teal-200 dark:bg-teal-500/20 dark:text-teal-50 dark:ring-teal-400/30',
    iconWrapClass: 'bg-teal-50 text-teal-700 ring-teal-200 dark:bg-teal-500/15 dark:text-teal-200 dark:ring-teal-400/30',
  },
  perplexity: {
    key: 'perplexity',
    label: 'Perplexity',
    iconKey: 'perplexity',
    iconColor: '#22B8CD',
    badgeClass: 'bg-cyan-100 text-cyan-900 ring-cyan-200 dark:bg-cyan-500/20 dark:text-cyan-50 dark:ring-cyan-400/30',
    iconWrapClass: 'bg-cyan-50 text-cyan-700 ring-cyan-200 dark:bg-cyan-500/15 dark:text-cyan-200 dark:ring-cyan-400/30',
  },
  openrouter: {
    key: 'openrouter',
    label: 'OpenRouter',
    iconKey: 'openrouter',
    iconColor: '#6566F1',
    badgeClass: 'bg-violet-100 text-violet-900 ring-violet-200 dark:bg-violet-500/20 dark:text-violet-50 dark:ring-violet-400/30',
    iconWrapClass: 'bg-violet-50 text-violet-700 ring-violet-200 dark:bg-violet-500/15 dark:text-violet-200 dark:ring-violet-400/30',
  },
  ollama: {
    key: 'ollama',
    label: 'Ollama',
    iconKey: 'ollama',
    iconColor: '#111827',
    badgeClass: 'bg-stone-100 text-stone-900 ring-stone-200 dark:bg-stone-500/20 dark:text-stone-50 dark:ring-stone-400/30',
    iconWrapClass: 'bg-stone-50 text-stone-800 ring-stone-200 dark:bg-stone-500/15 dark:text-stone-200 dark:ring-stone-400/30',
  },
  baidu: {
    key: 'baidu',
    label: '百度千帆',
    iconKey: 'wenxin',
    iconColor: '#167ADF',
    badgeClass: 'bg-sky-100 text-sky-900 ring-sky-200 dark:bg-sky-500/20 dark:text-sky-50 dark:ring-sky-400/30',
    iconWrapClass: 'bg-sky-50 text-sky-700 ring-sky-200 dark:bg-sky-500/15 dark:text-sky-200 dark:ring-sky-400/30',
  },
  iflytek: {
    key: 'iflytek',
    label: '讯飞星火',
    iconKey: 'spark',
    iconColor: '#0070F0',
    badgeClass: 'bg-blue-100 text-blue-900 ring-blue-200 dark:bg-blue-500/20 dark:text-blue-50 dark:ring-blue-400/30',
    iconWrapClass: 'bg-blue-50 text-blue-700 ring-blue-200 dark:bg-blue-500/15 dark:text-blue-200 dark:ring-blue-400/30',
  },
  tencent: {
    key: 'tencent',
    label: '腾讯混元',
    iconKey: 'hunyuan',
    iconColor: '#0053E0',
    badgeClass: 'bg-indigo-100 text-indigo-900 ring-indigo-200 dark:bg-indigo-500/20 dark:text-indigo-50 dark:ring-indigo-400/30',
    iconWrapClass: 'bg-indigo-50 text-indigo-700 ring-indigo-200 dark:bg-indigo-500/15 dark:text-indigo-200 dark:ring-indigo-400/30',
  },
  ai360: {
    key: 'ai360',
    label: '360',
    iconKey: 'ai360',
    iconColor: '#23B7E5',
    badgeClass: 'bg-cyan-100 text-cyan-900 ring-cyan-200 dark:bg-cyan-500/20 dark:text-cyan-50 dark:ring-cyan-400/30',
    iconWrapClass: 'bg-cyan-50 text-cyan-700 ring-cyan-200 dark:bg-cyan-500/15 dark:text-cyan-200 dark:ring-cyan-400/30',
  },
  zeroone: {
    key: 'zeroone',
    label: '零一万物',
    iconKey: 'yi',
    iconColor: '#003425',
    badgeClass: 'bg-emerald-100 text-emerald-900 ring-emerald-200 dark:bg-emerald-500/20 dark:text-emerald-50 dark:ring-emerald-400/30',
    iconWrapClass: 'bg-emerald-50 text-emerald-700 ring-emerald-200 dark:bg-emerald-500/15 dark:text-emerald-200 dark:ring-emerald-400/30',
  },
  doubao: {
    key: 'doubao',
    label: '豆包',
    iconKey: 'doubao',
    iconColor: '#1C64F2',
    badgeClass: 'bg-blue-100 text-blue-900 ring-blue-200 dark:bg-blue-500/20 dark:text-blue-50 dark:ring-blue-400/30',
    iconWrapClass: 'bg-blue-50 text-blue-700 ring-blue-200 dark:bg-blue-500/15 dark:text-blue-200 dark:ring-blue-400/30',
  },
  xiaomi: {
    key: 'xiaomi',
    label: '小米',
    iconKey: 'xiaomimimo',
    iconColor: '#FF6900',
    badgeClass: 'bg-orange-100 text-orange-900 ring-orange-200 dark:bg-orange-500/20 dark:text-orange-50 dark:ring-orange-400/30',
    iconWrapClass: 'bg-orange-50 text-orange-700 ring-orange-200 dark:bg-orange-500/15 dark:text-orange-200 dark:ring-orange-400/30',
  },
  minimax: {
    key: 'minimax',
    label: 'MiniMax',
    iconKey: 'minimax',
    iconColor: '#F23F5D',
    badgeClass: 'bg-rose-100 text-rose-900 ring-rose-200 dark:bg-rose-500/20 dark:text-rose-50 dark:ring-rose-400/30',
    iconWrapClass: 'bg-rose-50 text-rose-700 ring-rose-200 dark:bg-rose-500/15 dark:text-rose-200 dark:ring-rose-400/30',
  },
  suno: {
    key: 'suno',
    label: 'Suno',
    iconKey: 'suno',
    iconColor: '#111827',
    badgeClass: 'bg-zinc-100 text-zinc-900 ring-zinc-200 dark:bg-zinc-500/20 dark:text-zinc-50 dark:ring-zinc-400/30',
    iconWrapClass: 'bg-zinc-50 text-zinc-800 ring-zinc-200 dark:bg-zinc-500/15 dark:text-zinc-200 dark:ring-zinc-400/30',
  },
  dify: {
    key: 'dify',
    label: 'Dify',
    iconKey: 'dify',
    iconColor: '#1677FF',
    badgeClass: 'bg-blue-100 text-blue-900 ring-blue-200 dark:bg-blue-500/20 dark:text-blue-50 dark:ring-blue-400/30',
    iconWrapClass: 'bg-blue-50 text-blue-700 ring-blue-200 dark:bg-blue-500/15 dark:text-blue-200 dark:ring-blue-400/30',
  },
  coze: {
    key: 'coze',
    label: 'Coze',
    iconKey: 'coze',
    iconColor: '#5436F5',
    badgeClass: 'bg-purple-100 text-purple-900 ring-purple-200 dark:bg-purple-500/20 dark:text-purple-50 dark:ring-purple-400/30',
    iconWrapClass: 'bg-purple-50 text-purple-700 ring-purple-200 dark:bg-purple-500/15 dark:text-purple-200 dark:ring-purple-400/30',
  },
  cloudflare: {
    key: 'cloudflare',
    label: 'Cloudflare',
    iconKey: 'cloudflare',
    iconColor: '#F38020',
    badgeClass: 'bg-orange-100 text-orange-900 ring-orange-200 dark:bg-orange-500/20 dark:text-orange-50 dark:ring-orange-400/30',
    iconWrapClass: 'bg-orange-50 text-orange-700 ring-orange-200 dark:bg-orange-500/15 dark:text-orange-200 dark:ring-orange-400/30',
  },
  jina: {
    key: 'jina',
    label: 'Jina',
    iconKey: 'jina',
    iconColor: '#111827',
    badgeClass: 'bg-slate-100 text-slate-900 ring-slate-200 dark:bg-slate-500/20 dark:text-slate-50 dark:ring-slate-400/30',
    iconWrapClass: 'bg-slate-50 text-slate-800 ring-slate-200 dark:bg-slate-500/15 dark:text-slate-200 dark:ring-slate-400/30',
  },
  unknown: {
    key: 'unknown',
    label: '未知供应商',
    iconKey: null,
    iconColor: '#64748B',
    badgeClass: 'bg-slate-100 text-slate-800 ring-slate-200 dark:bg-slate-500/20 dark:text-slate-100 dark:ring-slate-400/30',
    iconWrapClass: 'bg-slate-50 text-slate-600 ring-slate-200 dark:bg-slate-500/15 dark:text-slate-200 dark:ring-slate-400/30',
  },
}

const defaultProviderBrandKeys: ProviderBrandKey[] = [
  'anthropic',
  'deepseek',
  'google',
  'moonshot',
  'openai',
  'zhipu',
  'alibaba',
  'xai',
  'midjourney',
  'mistral',
  'meta',
  'cohere',
  'perplexity',
  'openrouter',
  'ollama',
  'baidu',
  'iflytek',
  'tencent',
  'ai360',
  'zeroone',
  'doubao',
  'xiaomi',
  'minimax',
  'suno',
  'dify',
  'coze',
  'cloudflare',
  'jina',
  'unknown',
]

const providerBrandAliases: Record<ProviderBrandKey, string[]> = {
  anthropic: ['anthropic', 'claude'],
  deepseek: ['deepseek', 'deep seek'],
  google: ['google', 'gemini', 'gemma', 'vertex', 'google ai studio'],
  moonshot: ['moonshot', 'kimi'],
  openai: ['openai', 'open ai', 'chatgpt'],
  zhipu: ['智谱', 'zhipu', 'glm', 'chatglm'],
  alibaba: ['阿里巴巴', '阿里', 'alibaba', 'aliyun', '通义', '通义千问', 'tongyi', 'qwen', 'qwq'],
  xai: ['xai', 'x.ai', 'grok'],
  midjourney: ['midjourney', 'mj'],
  mistral: ['mistral', 'mixtral', 'codestral', 'pixtral', 'voxtral', 'magistral'],
  meta: ['meta', 'llama', 'llamaindex'],
  cohere: ['cohere', 'command', 'c4ai'],
  perplexity: ['perplexity', 'pplx'],
  openrouter: ['openrouter', 'open router'],
  ollama: ['ollama'],
  baidu: ['百度', '百度千帆', '千帆', 'baidu', 'qianfan', 'wenxin', '文心', 'ernie'],
  iflytek: ['讯飞', '讯飞星火', 'iflytek', 'spark'],
  tencent: ['腾讯', '腾讯混元', 'tencent', 'hunyuan', '混元'],
  ai360: ['360', 'ai360'],
  zeroone: ['零一万物', '01', 'zeroone', '01ai', 'yi'],
  doubao: ['豆包', 'doubao', '字节', 'bytedance', 'volcengine', '火山引擎'],
  xiaomi: ['小米', 'xiaomi', 'mi', 'mimo', 'mi mo', 'xiaomimimo'],
  minimax: ['minimax', 'abab'],
  suno: ['suno'],
  dify: ['dify'],
  coze: ['coze', '扣子'],
  cloudflare: ['cloudflare', 'workers ai', '@cf'],
  jina: ['jina'],
  unknown: ['未知供应商', '未知', 'unknown'],
}

const providerBrandAliasEntries = Object.entries(providerBrandAliases).flatMap(([key, aliases]) =>
  aliases.map((alias) => ({
    key: key as ProviderBrandKey,
    alias: normalizeProviderBrand(alias),
  })),
)

const providerBrandAliasMap = new Map(
  providerBrandAliasEntries.map(({ alias, key }) => [alias, key]),
)

function canUseProviderBrandFuzzyAlias(alias: string): boolean {
  return alias.length >= 4 || /[\u4e00-\u9fff]/.test(alias) || alias === '@cf'
}

function providerBrandTokens(value: string): string[] {
  return value
    .toLocaleLowerCase()
    .split(/[\s_/|,;:()[\]{}-]+/)
    .map(normalizeProviderBrand)
    .filter(Boolean)
}

export const defaultProviderBrandOptions: ProviderBrandOption[] = defaultProviderBrandKeys.map((key) => ({
  value: providerBrands[key].label,
  label: providerBrands[key].label,
}))

export function normalizeProviderBrand(value: string): string {
  return value.trim().toLocaleLowerCase().replace(/[\s_-]+/g, '')
}

export function resolveProviderBrandKey(value?: string | null): ProviderBrandKey | null {
  const normalized = normalizeProviderBrand(value ?? '')
  if (!normalized) {
    return null
  }

  const exactKey = providerBrandAliasMap.get(normalized)
  if (exactKey) {
    return exactKey
  }

  for (const token of providerBrandTokens(value ?? '')) {
    const tokenKey = providerBrandAliasMap.get(token)
    if (tokenKey) {
      return tokenKey
    }
  }

  return providerBrandAliasEntries.find(({ alias }) => canUseProviderBrandFuzzyAlias(alias) && normalized.includes(alias))?.key ?? null
}

export function resolveProviderBrand(value?: string | null): ProviderBrand {
  const key = resolveProviderBrandKey(value)
  return key ? providerBrands[key] : providerBrands.unknown
}

export function providerBrandDisplayName(value?: string | null): string {
  const source = value?.trim()
  if (!source) {
    return providerBrands.unknown.label
  }

  const key = resolveProviderBrandKey(source)
  return key ? providerBrands[key].label : source
}

export function providerBrandFilterKey(value?: string | null): string {
  const source = value?.trim()
  if (!source) {
    return `brand:${providerBrands.unknown.key}`
  }

  const key = resolveProviderBrandKey(source)
  return key ? `brand:${key}` : `custom:${normalizeProviderBrand(source)}`
}
