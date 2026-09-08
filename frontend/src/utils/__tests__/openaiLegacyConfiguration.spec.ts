import { describe, expect, it } from 'vitest'
import { normalizeLegacyOpenAIExtra, normalizeOpenAICompactMode } from '../openaiLegacyConfiguration'

describe('OpenAI 历史配置边界', () => {
  it('将旧自动值转换为明确开关，保留显式关闭', () => {
    expect(normalizeOpenAICompactMode('auto')).toBe('force_on')
    expect(normalizeOpenAICompactMode(undefined)).toBe('force_on')
    expect(normalizeOpenAICompactMode(' FORCE_OFF ')).toBe('force_off')
  })

  it('丢弃任意类型的旧状态，不修改原对象或补写缺省开关', () => {
    const input = {
      openai_responses_supported: false,
      openai_responses_probe_status: { invalid: true },
      openai_compact_supported: [],
      openai_native_compaction_v2_last_error: 'old error',
      openai_compact_mode: 'auto',
      keep: { enabled: false }
    }
    expect(normalizeLegacyOpenAIExtra(input)).toEqual({ openai_compact_mode: 'force_on', keep: { enabled: false } })
    expect(input.openai_compact_mode).toBe('auto')
    expect(normalizeLegacyOpenAIExtra({})).toEqual({})
  })
})
