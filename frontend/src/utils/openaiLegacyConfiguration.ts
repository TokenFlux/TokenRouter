import type { OpenAICompactMode } from '@/types'

// 历史数据仅在导入或编辑边界转换，控件内部只接受明确开关。
export function normalizeOpenAICompactMode(value: unknown): OpenAICompactMode {
  return typeof value === 'string' && value.trim().toLowerCase() === 'force_off'
    ? 'force_off'
    : 'force_on'
}

// 旧服务或导入文件的探测字段不能通过编辑器和高级 JSON 再次提交。
export function normalizeLegacyOpenAIExtra(extra: Record<string, unknown>): Record<string, unknown> {
  const normalized = { ...extra }
  for (const key of [
    'openai_responses_probe_status', 'openai_responses_supported',
    'openai_compact_supported', 'openai_compact_checked_at',
    'openai_compact_last_status', 'openai_compact_last_error',
    'openai_native_compaction_v2_supported', 'openai_native_compaction_v2_checked_at',
    'openai_native_compaction_v2_last_status', 'openai_native_compaction_v2_last_error'
  ]) {
    delete normalized[key]
  }
  for (const key of ['openai_compact_mode', 'openai_native_compaction_v2_mode']) {
    if (Object.prototype.hasOwnProperty.call(normalized, key)) {
      normalized[key] = normalizeOpenAICompactMode(normalized[key])
    }
  }
  return normalized
}
