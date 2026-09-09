import { describe, expect, it } from 'vitest'
import { protocolCatalog } from '@/api/admin/protocolCapabilities'
import type { GroupPlatform } from '@/types'
import {
  defaultGroupClientProtocols,
  effectiveGroupClientProtocols,
  setGroupClientProtocol,
  supportedGroupClientProtocols
} from '../groupClientProtocols'

describe('groupClientProtocols', () => {
  it.each<[GroupPlatform, string[], string[]]>([
    ['anthropic', ['anthropic_messages', 'openai_responses', 'openai_chat_completions'], ['anthropic_messages']],
    ['openai', ['anthropic_messages','openai_responses','openai_chat_completions','openai_embeddings','openai_images_generations','openai_images_edits','openai_responses_websocket','openai_live','openai_responses_compact','openai_alpha_search'], ['openai_responses', 'openai_chat_completions']],
    ['gemini', ['anthropic_messages','openai_responses','openai_chat_completions','gemini_generate_content','image_batches'], ['gemini_generate_content']],
    ['antigravity', ['anthropic_messages', 'openai_responses', 'openai_chat_completions', 'gemini_generate_content'], ['anthropic_messages', 'gemini_generate_content']],
    ['qoder', ['anthropic_messages', 'openai_responses', 'openai_chat_completions'], []],
    ['grok', ['anthropic_messages','openai_responses','openai_chat_completions','openai_images_generations','openai_images_edits','grok_videos_generations','grok_videos_edits','grok_videos_extensions','grok_tts','grok_stt','grok_custom_voices','grok_voice_realtime','openai_responses_websocket','openai_responses_compact','grok_web_search','grok_x_search'], ['openai_responses','openai_chat_completions','openai_images_generations','openai_images_edits']],
    ['kimi', ['anthropic_messages', 'openai_responses', 'openai_chat_completions'], ['anthropic_messages', 'openai_responses', 'openai_chat_completions']],
    ['zhipu', ['anthropic_messages', 'openai_responses', 'openai_chat_completions'], ['anthropic_messages', 'openai_responses', 'openai_chat_completions']],
    ['deepseek', ['anthropic_messages', 'openai_responses', 'openai_chat_completions'], ['anthropic_messages', 'openai_responses', 'openai_chat_completions']]
  ])('returns the %s protocol policy', (platform, supported, defaults) => {
    expect(supportedGroupClientProtocols(platform)).toEqual(supported)
    expect(defaultGroupClientProtocols(platform)).toEqual(defaults)
  })

  it('treats missing and explicit empty collections as no enabled protocols', () => {
    expect(effectiveGroupClientProtocols('openai', undefined)).toEqual([])
    expect(effectiveGroupClientProtocols('qoder', [])).toEqual([])
  })

  it('allows every supported protocol to be disabled', () => {
    expect(setGroupClientProtocol('openai', ['openai_responses', 'openai_chat_completions'], 'openai_responses', false)).toEqual([
      'openai_chat_completions'
    ])
    expect(setGroupClientProtocol('openai', ['openai_chat_completions'], 'openai_chat_completions', false)).toEqual([])
  })
})

it('用户侧直接读取后端集合，不依赖管理员目录', () => {
  protocolCatalog.value = null
  expect(effectiveGroupClientProtocols('openai', ['openai_responses','openai_images_edits'])).toEqual(['openai_responses','openai_images_edits'])
})
