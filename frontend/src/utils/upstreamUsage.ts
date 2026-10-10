import type { Provider } from '@/types'

type UpstreamUsageProvider = Pick<Provider, 'type' | 'platform' | 'credentials' | 'extra'>

/** 上游用量只支持 API Key；智谱按量付费没有公开余额端点。 */
// @project-doc docs/interfaces/upstream_usage.md#frontend_lifecycle
export function supportsUpstreamUsageQuery(provider: UpstreamUsageProvider): boolean {
  return provider.type === 'apikey' &&
    !(provider.platform === 'zhipu' && provider.credentials?.provider_mode !== 'coding')
}

/** 展示、手动查询和批量查询共用开关，缺少配置时默认启用。 */
export function isUpstreamUsageQueryEnabled(provider: UpstreamUsageProvider): boolean {
  const config = provider.extra?.upstream_usage_query as Record<string, unknown> | undefined
  return supportsUpstreamUsageQuery(provider) && (config?.enabled ?? (provider.platform !== 'jev')) === true
}
