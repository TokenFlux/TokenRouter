import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import AccountProtocolSelector from '../AccountProtocolSelector.vue'
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

// 原生能力和客户端转换入口必须分开，特别覆盖认证方式与空集合。
describe('AccountProtocolSelector', () => {
  it.each([
    ['openai', 'apikey', '', ['openai_responses', 'openai_chat_completions', 'openai_embeddings'], ['anthropic_messages']],
    ['openai', 'oauth', 'personalAccessToken', ['openai_responses', 'openai_responses_websocket'], ['anthropic_messages','openai_chat_completions','openai_images_generations','openai_alpha_search','openai_live']],
    ['grok', 'oauth', '', ['openai_responses','grok_tts','grok_voice_realtime'], ['openai_responses_websocket','grok_web_search','openai_responses_compact']],
    ['zhipu', 'apikey', '', ['anthropic_messages','openai_chat_completions'], ['openai_responses']],
    ['gemini', 'service_account', '', ['gemini_generate_content','vertex_batch_prediction'], ['gemini_batch_generate_content','openai_images_generations']],
  ] as const)('%s/%s/%s 仅显示原生项', (platform, type, authMode, present, absent) => {
    const wrapper = mount(AccountProtocolSelector, { props: { platform, type, authMode, modelValue: [] } })
    for (const id of present) expect(wrapper.find(`[data-native-protocol="${id}"]`).exists()).toBe(true)
    for (const id of absent) expect(wrapper.find(`[data-native-protocol="${id}"]`).exists()).toBe(false)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })
  it('认证方式切换时收窄原生选项', async () => {
    const wrapper = mount(AccountProtocolSelector, { props: { platform: 'openai', type: 'oauth', modelValue: ['openai_live'] } })
    await wrapper.setProps({ authMode: 'personalAccessToken' })
    await flushPromises()
    expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toEqual(['openai_responses','openai_responses_websocket','openai_responses_compact'])
  })
})
