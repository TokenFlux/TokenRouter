import { describe, expect, it } from 'vitest'
import { CONCRETE_PLATFORM_OPTIONS } from '@/constants/platforms'

const concretePlatforms = [
  'anthropic',
  'openai',
  'gemini',
  'antigravity',
  'qoder',
  'grok',
  'kimi',
  'zhipu',
  'deepseek',
  'jev'
]

describe('platform option catalogs', () => {
  it('exposes every concrete provider platform', () => {
    expect(CONCRETE_PLATFORM_OPTIONS.map((option) => option.value)).toEqual(concretePlatforms)
  })


})
