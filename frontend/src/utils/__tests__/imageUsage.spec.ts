import { describe, expect, it } from 'vitest'

import {
  hasImageInputCost,
  hasImageInputTokens,
  textInputTokens,
  uncachedImageInputTokens,
} from '@/utils/imageUsage'

/**
 * imageUsage.spec.ts tests separation of image and text input usage. Positive
 * counts and charges enable image sections, zero values leave them absent, and
 * inconsistent image totals clamp the remaining text count to zero.
 */

describe('imageUsage input helpers', () => {
  /**
   * Cached image input overlaps the upstream image total but belongs to the
   * separate cache bucket. Displayed uncached image and text counts must match pricing.
   */
  it('separates explicitly reported cached image input', () => {
    const row = { input_tokens: 100, image_input_tokens: 80, image_cache_read_tokens: 30 }
    expect(uncachedImageInputTokens(row)).toBe(50)
    expect(textInputTokens(row)).toBe(50)
    expect(hasImageInputTokens({ ...row, image_cache_read_tokens: 80 })).toBe(false)
  })

  /**
   * Input rows contain positive and zero image token counts and charges. The predicates
   * enable image sections for positive observations and leave zero-valued observations
   * disabled.
   */
  it('detects image input tokens and costs', () => {
    expect(hasImageInputTokens({ input_tokens: 371, image_input_tokens: 352 })).toBe(true)
    expect(hasImageInputTokens({ input_tokens: 19, image_input_tokens: 0 })).toBe(false)
    expect(hasImageInputCost({ image_input_cost: 0.002816 })).toBe(true)
    expect(hasImageInputCost({ image_input_cost: 0 })).toBe(false)
  })

  /**
   * A combined input total includes a separately recorded image-token portion.
   * Subtraction returns the remaining text count so image tokens are not displayed
   * twice.
   */
  it('returns the text-only portion of total input tokens', () => {
    expect(textInputTokens({ input_tokens: 371, image_input_tokens: 352 })).toBe(19)
  })

  /**
   * A malformed row records more image tokens than total input tokens. The text
   * remainder clamps to zero, protecting consumers from a negative displayed text
   * count.
   */
  it('clamps malformed image input totals to zero text tokens', () => {
    expect(textInputTokens({ input_tokens: 10, image_input_tokens: 50 })).toBe(0)
  })
})
