import { describe, expect, it } from 'vitest'
import { supportsImageGenerationPlatform } from '../groupsImagePricing'

// 移除价格面板后，生成权限仍按平台能力开放。
describe('image generation permissions', () => {
  it.each(['openai', 'grok', 'gemini', 'antigravity'])('supports %s', platform => {
    expect(supportsImageGenerationPlatform(platform)).toBe(true)
  })
  it('excludes text-only platforms', () => {
    expect(supportsImageGenerationPlatform('anthropic')).toBe(false)
  })
})
