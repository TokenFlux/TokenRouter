import type { Account } from '@/types'

type UpstreamUsageAccount = Pick<Account, 'type' | 'platform' | 'credentials' | 'extra'>

/** 上游用量只支持 API Key；智谱按量付费没有公开余额端点。 */
// @project-doc docs/interfaces/upstream_usage.md#frontend_lifecycle
export function supportsUpstreamUsageQuery(account: UpstreamUsageAccount): boolean {
  return account.type === 'apikey' &&
    !(account.platform === 'zhipu' && account.credentials?.account_mode !== 'coding')
}

/** 展示、手动查询和批量查询共用开关，缺少配置时默认启用。 */
export function isUpstreamUsageQueryEnabled(account: UpstreamUsageAccount): boolean {
  const config = account.extra?.upstream_usage_query as Record<string, unknown> | undefined
  return supportsUpstreamUsageQuery(account) && config?.enabled !== false
}
