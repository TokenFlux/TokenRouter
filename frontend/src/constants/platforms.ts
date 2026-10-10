import type { ProviderPlatform } from '@/types'

export interface PlatformOption<T extends string = string> {
  value: T
  label: string
  [key: string]: unknown
}

// 提供商与请求路由支持的具体平台目录；各管理筛选器统一从此处派生。
export const CONCRETE_PLATFORM_OPTIONS = [
  { value: 'anthropic', label: 'Anthropic' },
  { value: 'openai', label: 'OpenAI' },
  { value: 'gemini', label: 'Gemini' },
  { value: 'antigravity', label: 'Antigravity' },
  { value: 'qoder', label: 'Qoder' },
  { value: 'grok', label: 'Grok' },
  { value: 'kimi', label: 'Kimi' },
  { value: 'zhipu', label: 'Zhipu' },
  { value: 'deepseek', label: 'DeepSeek' },
  { value: 'jev', label: 'Jev' }
] as const satisfies readonly PlatformOption<ProviderPlatform>[]
