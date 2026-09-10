import { useProtocolCatalogFixture } from '@/__tests__/helpers/protocolCatalog'
import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import GroupClientProtocolSelector from '../GroupClientProtocolSelector.vue'

vi.mock('vue-i18n', async () => ({
  ...await vi.importActual('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))

describe('GroupClientProtocolSelector', () => {
  it('renders the protocol rows without an outer frame', () => {
    const wrapper = mount(GroupClientProtocolSelector, {
      props: {
        platform: 'openai',
        modelValue: ['openai_responses', 'openai_chat_completions']
      }
    })

    const list = wrapper.get('[data-testid="client-protocol-list"]')
    expect(list.classes()).toContain('divide-y')
    expect(list.classes()).not.toContain('border')
    expect(list.classes()).not.toContain('rounded-md')
  })

  it('shows the canonical endpoint for each supported protocol', () => {
    const wrapper = mount(GroupClientProtocolSelector, {
      props: {
        platform: 'gemini',
        modelValue: []
      }
    })

    expect(wrapper.get('[data-protocol-endpoint="anthropic_messages"]').text()).toContain(
      '/v1/messages'
    )
    expect(wrapper.get('[data-protocol-endpoint="openai_responses"]').text()).toContain(
      '/v1/responses'
    )
    expect(wrapper.get('[data-protocol-endpoint="openai_chat_completions"]').text()).toContain(
      '/v1/chat/completions'
    )
    expect(wrapper.get('[data-protocol-endpoint="gemini_generate_content"]').text()).toContain(
      '/v1beta/models/{model}:generateContent'
    )
  })

  it('uses the shared enabled switch color', () => {
    const wrapper = mount(GroupClientProtocolSelector, {
      props: {
        platform: 'openai',
        modelValue: ['openai_responses']
      }
    })

    expect(wrapper.get('[data-protocol="openai_responses"]').classes()).toContain(
      'toggle-active'
    )
    expect(wrapper.get('[data-protocol="openai_chat_completions"]').classes()).not.toContain(
      'toggle-active'
    )
  })

  it('allows default protocols to be disabled', async () => {
    const wrapper = mount(GroupClientProtocolSelector, {
      props: {
        platform: 'openai',
        modelValue: ['openai_responses', 'openai_chat_completions']
      }
    })

    expect(wrapper.get('[data-protocol="openai_responses"]').attributes('disabled')).toBeUndefined()
    await wrapper.get('[data-protocol="openai_responses"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toEqual([
      'openai_chat_completions'
    ])
  })

  it('allows any platform to disable its final protocol', async () => {
    const wrapper = mount(GroupClientProtocolSelector, {
      props: {
        platform: 'anthropic',
        modelValue: ['anthropic_messages']
      }
    })

    await wrapper.get('[data-protocol="anthropic_messages"]').trigger('click')
    expect(wrapper.emitted('update:modelValue')?.at(-1)?.[0]).toEqual([])
  })
})

it('转换目标与客户端入口开关独立，并使用项目 Select', async () => {
  const wrapper = mount(GroupClientProtocolSelector, { props: { platform: 'openai', modelValue: ['anthropic_messages'], fallbacks: { anthropic_messages: 'openai_responses' } } })
  const selects = wrapper.findAllComponents({ name: 'Select' })
  expect(selects.length).toBeGreaterThan(0)
  const messages = selects.find(select => select.props('modelValue') === 'openai_responses')!
  expect(messages.props('options')).toEqual(expect.arrayContaining([{ value: 'openai_responses', label: 'OpenAI Responses' }]))
  messages.vm.$emit('update:modelValue', 'openai_chat_completions')
  expect(wrapper.emitted('update:fallbacks')?.at(-1)?.[0]).toEqual({ anthropic_messages: 'openai_chat_completions' })
  expect(wrapper.emitted('update:modelValue')).toBeUndefined()
})

useProtocolCatalogFixture()
