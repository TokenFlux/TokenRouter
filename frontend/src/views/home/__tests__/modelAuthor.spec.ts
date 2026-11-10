import { describe, expect, it } from 'vitest'
import { modelAuthor } from '../modelAuthor'

// modelAuthor.spec.ts checks developer attribution for public model identifiers.
// Cases cover both native and hosted identifiers, where the serving service can
// differ from the developer, as well as custom aliases that should omit the byline.

describe('modelAuthor', () => {
  // Known public models retain the same developer even when their identifier
  // contains a hosting prefix or the deployment uses uppercase letters.
  it.each([
    ['claude-opus-4-7', 'Anthropic'],
    ['google-vertex/claude-sonnet-4-6', 'Anthropic'],
    ['azure/GPT-IMAGE-2', 'OpenAI'],
    ['codex-auto-review', 'OpenAI'],
    ['gemini-4-argon', 'Google'],
    ['nano-banana-pro', 'Google'],
    ['deepseek-v4-pro', 'DeepSeek'],
    ['grok-imagine-image-2.0', 'xAI'],
    ['bailian/GLM-5.3', 'Z.AI'],
    ['self-deployed/kimi-k3', 'Moonshot AI'],
    ['qwen3.8-max', 'Alibaba'],
    ['jev-preview', 'TypeSafe AI'],
  ])('attributes %s to %s', (modelId, author) => {
    expect(modelAuthor(modelId)).toBe(author)
  })

  // Unknown aliases lack enough model identity to attribute them. A provider
  // prefix or incidental family substring alone cannot produce a developer byline.
  it.each(['openai/custom-alias', 'local-gpt-6', 'geminify', ''])(
    'omits the developer for %j',
    (modelId) => {
      expect(modelAuthor(modelId)).toBeNull()
    },
  )
})
