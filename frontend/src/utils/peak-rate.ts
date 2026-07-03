/**
 * 高峰时段倍率的共享展示逻辑。
 *
 * 高峰窗口由后端按服务器全局时区判定，前端展示必须带上服务器时区标注，
 * 避免用户按浏览器本地时间误读计费窗口。
 */

export interface PeakRateFields {
  peak_rate_enabled?: boolean
  peak_start?: string
  peak_end?: string
  peak_rate_multiplier?: number
}

export function hasPeakRate(fields?: PeakRateFields | null): boolean {
  return Boolean(fields?.peak_rate_enabled && fields.peak_start && fields.peak_end)
}

/** 将 "+08:00" 展示为 "UTC+08:00"；旧缓存无该字段时返回空串。 */
export function serverTimezoneLabel(utcOffset?: string | null): string {
  return utcOffset ? `UTC${utcOffset}` : ''
}

export function currentServerTimezoneLabel(): string {
  const settings = typeof window !== 'undefined' ? window.__APP_CONFIG__ : undefined
  return serverTimezoneLabel(settings?.server_utc_offset)
}

/** 生成 "14:00-18:00 ×2 (UTC+08:00)"，无时区标签时省略括号。 */
export function formatPeakRateWindow(fields: PeakRateFields | null | undefined, tzLabel?: string): string {
  if (!hasPeakRate(fields) || !fields) return ''
  const base = `${fields.peak_start}-${fields.peak_end} ×${fields.peak_rate_multiplier ?? 1}`
  return tzLabel ? `${base} (${tzLabel})` : base
}
