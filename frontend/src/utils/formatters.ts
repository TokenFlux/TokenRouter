/**
 * 格式化缓存 token 数量（1K/1M 缩写）
 */
export function formatCacheTokens(tokens: number): string {
  if (tokens >= 1000000) return `${(tokens / 1000000).toFixed(1)}M`
  if (tokens >= 1000) return `${(tokens / 1000).toFixed(1)}K`
  return tokens.toLocaleString()
}

/**
 * 自适应精度格式化倍率：保留至多 4 位小数并去掉末尾多余的 0，
 * 但至少保留 2 位小数（0.035 -> "0.035"，0.3 -> "0.30"，1 -> "1.00"）
 */
export function formatMultiplier(val: number): string {
  if (val < 0.0001) return val.toPrecision(2)
  return val.toFixed(4).replace(/(\.\d{2}\d*?)0+$/, '$1')
}

/**
 * 格式化紧凑 token 计数（小写 k/m 后缀，如 "272k"、"1.5m"）
 * 模型广场上下文区间切换器与网关默认价格弹窗共用；与 formatTokensK（大写后缀、固定 1 位小数）语义不同，不要混用。
 * @param value token 数量
 * @returns 格式化后的字符串，如 "950", "272k", "1.5m"
 */
export function formatCompactTokenCount(value: number): string {
  const compact = (divided: number): string => new Intl.NumberFormat(undefined, {
    maximumFractionDigits: divided >= 100 ? 0 : 1,
  }).format(divided)
  if (value >= 1_000_000) {
    return `${compact(value / 1_000_000)}m`
  }
  if (value >= 1_000) {
    return `${compact(value / 1_000)}k`
  }
  return new Intl.NumberFormat(undefined, { maximumFractionDigits: 0 }).format(value)
}

/**
 * 格式化紧凑 token 区间（如 "0-272k"、"272k+"）；maxTokens 为空表示无上限。
 * @param minTokens 区间下限
 * @param maxTokens 区间上限，null/undefined 表示无上限
 */
export function formatCompactTokenRange(minTokens: number, maxTokens?: number | null): string {
  if (typeof maxTokens !== 'number') {
    return `${formatCompactTokenCount(minTokens)}+`
  }
  return `${formatCompactTokenCount(minTokens)}-${formatCompactTokenCount(maxTokens)}`
}
