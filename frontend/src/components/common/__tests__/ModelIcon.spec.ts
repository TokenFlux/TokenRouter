import { mount } from '@vue/test-utils'
import ModelIcon from '../ModelIcon.vue'
import ProviderIcon from '../ProviderIcon.vue'

describe('ModelIcon', () => {
  it.each([
    ['OpenAI', 'gpt-5.6'],
    ['OpenAI Codex', 'codex-auto-review'],
    ['Grok', 'grok-4.5'],
    ['Kimi', 'kimi-k2.6'],
  ])('%s 模型图标继承主题文字色', (_provider, model) => {
    const wrapper = mount(ModelIcon, {
      props: {
        model,
        size: '28px',
      },
    })

    expect(wrapper.get('svg').attributes('width')).toBe('28px')
    expect(wrapper.get('path').attributes('fill')).toBe('currentColor')
    expect(wrapper.find('.model-icon-fallback').exists()).toBe(false)
  })

  it.each(['nano-banana-2', 'nano-banana-pro'])('%s 显示 Google Gemini 图标', (model) => {
    const wrapper = mount(ModelIcon, {
      props: { model },
    })

    expect(wrapper.find('svg').exists()).toBe(true)
    expect(wrapper.find('.model-icon-fallback').exists()).toBe(false)
  })

  it.each(['glm-4.6', 'zhipu', '智谱'])('%s 显示智谱图标而不是首字母回退', (model) => {
    const wrapper = mount(ModelIcon, {
      props: { model },
    })

    expect(wrapper.find('svg').exists()).toBe(true)
    expect(wrapper.find('.model-icon-fallback').exists()).toBe(false)
  })
})

describe('ProviderIcon', () => {
  it.each([
    ['xAI', 'xAI'],
    ['Moonshot', 'Kimi'],
  ])('%s 徽标继承主题文字色', (_provider, brand) => {
    const wrapper = mount(ProviderIcon, {
      props: {
        brand,
      },
    })

    expect(wrapper.get('path').attributes('fill')).toBe('currentColor')
  })

  it.each(['nano-banana-2', 'nano-banana-pro'])('%s 使用 Google 徽标而不是首字母回退', (model) => {
    const wrapper = mount(ProviderIcon, {
      props: { brand: model },
    })

    expect(wrapper.find('svg').exists()).toBe(true)
    expect(wrapper.find('.provider-icon-fallback').exists()).toBe(false)
    expect(wrapper.get('path').attributes('fill')).toBe('#4285F4')
  })
})
