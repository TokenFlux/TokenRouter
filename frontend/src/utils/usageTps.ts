import type { UsageLog } from '@/types'

type UsageTpsInput = Partial<Pick<UsageLog, 'output_tokens' | 'duration_ms' | 'first_token_ms'>>

/** calculateUsageTps 用首字返回后的耗时计算每秒输出 Token 数，返回一位小数或 null。 */
export function calculateUsageTps(row: UsageTpsInput): number | null {
  const { output_tokens: outputTokens, duration_ms: durationMs, first_token_ms: firstTokenMs } = row
  if (
    typeof outputTokens !== 'number' || !Number.isFinite(outputTokens) || outputTokens <= 0 ||
    typeof durationMs !== 'number' || !Number.isFinite(durationMs) ||
    typeof firstTokenMs !== 'number' || !Number.isFinite(firstTokenMs) || firstTokenMs < 0 ||
    durationMs <= firstTokenMs
  ) {
    return null
  }

  const tps = outputTokens * 1000 / (durationMs - firstTokenMs)
  // 页面、CSV 和 Excel 共用舍入后的数值，Excel 的单元格格式负责补齐末尾的零。
  return Number.isFinite(tps) ? Number(tps.toFixed(1)) : null
}

/** formatUsageTps 为表格生成带单位的 TPS，数据不足时显示占位符。 */
export function formatUsageTps(row: UsageTpsInput): string {
  const tps = calculateUsageTps(row)
  return tps === null ? '-' : `${tps.toFixed(1)} tok/s`
}
