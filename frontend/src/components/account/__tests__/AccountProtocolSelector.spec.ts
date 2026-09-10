import { useProtocolCatalogFixture } from '@/__tests__/helpers/protocolCatalog'
import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import AccountProtocolSelector from '../AccountProtocolSelector.vue'
vi.mock('vue-i18n', async () => ({ ...await vi.importActual('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))

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

useProtocolCatalogFixture()

it('账号与分组选项共享错误恢复，保留显式关闭的账号集合', async () => {
  const { protocolCatalog } = await import('@/api/admin/protocolCapabilities')
  const { default: client } = await import('@/api/client')
  const { default: fixture } = await import('@/__tests__/fixtures/protocol-catalog.json')
  const { default: GroupSelector } = await import('@/components/admin/group/GroupClientProtocolSelector.vue')
  protocolCatalog.value = null
  const request = vi.spyOn(client, 'get').mockRejectedValue(new Error('offline'))
  const account = mount(AccountProtocolSelector, { props: { platform: 'openai', type: 'apikey', modelValue: [] } })
  const group = mount(GroupSelector, { props: { platform: 'openai', modelValue: [] } })
  try {
    await flushPromises()
    expect(request).toHaveBeenCalledTimes(1)
    expect(account.find('[role="alert"]').exists()).toBe(true)
    expect(group.find('[role="alert"]').exists()).toBe(true)
    request.mockResolvedValue({ data: structuredClone(fixture) })
    await account.get('[data-testid="protocol-catalog-retry"]').trigger('click')
    await flushPromises()
    expect(request).toHaveBeenCalledTimes(2)
    expect(account.find('[role="alert"]').exists()).toBe(false)
    expect(group.find('[role="alert"]').exists()).toBe(false)
    expect(account.find('[data-native-protocol="openai_responses"]').exists()).toBe(true)
    expect(group.find('[data-protocol="openai_responses"]').exists()).toBe(true)
    expect(account.emitted('update:modelValue')).toBeUndefined()
    expect(group.emitted('update:modelValue')).toBeUndefined()
  } finally {
    request.mockRestore()
    account.unmount()
    group.unmount()
  }
})
