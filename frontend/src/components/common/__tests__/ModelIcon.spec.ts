import { mount } from '@vue/test-utils'
import ModelIcon from '../ModelIcon.vue'
import ProviderIcon from '../ProviderIcon.vue'

/**
 * ModelIcon.spec.ts checks model and provider brand graphics for known names and Nano
 * Banana aliases. Parameterized cases inspect SVG presence, dimensions, and path
 * colors to cover theme inheritance and Google brand resolution across both icon
 * components.
 */

describe('ModelIcon', () => {
  /**
   * Known model names from several providers are rendered at a requested icon size.
   * Each supplied case keeps its SVG graphic and inherited text color.
   */
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

  /**
   * Nano Banana model aliases are supplied to the model icon resolver. Each alias
   * renders a known SVG graphic and does not fall through to an initial.
   */
  it.each(['nano-banana-2', 'nano-banana-pro'])('%s 显示 Google Gemini 图标', (model) => {
    const wrapper = mount(ModelIcon, {
      props: { model },
    })

    expect(wrapper.find('svg').exists()).toBe(true)
    expect(wrapper.find('.model-icon-fallback').exists()).toBe(false)
  })
})

describe('ProviderIcon', () => {
  /**
   * Provider display names identify brands whose marks inherit the surrounding text
   * color. Each supplied case renders its path with currentColor for theme compatible
   * display.
   */
  it.each([
    ['OpenAI', 'OpenAI'],
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

  /**
   * The provider icon receives the Google name or a Nano Banana alias. All
   * inputs resolve to the four-color Google mark at the requested size.
   */
  it.each(['Google', 'nano-banana-2', 'nano-banana-pro'])('%s 使用彩色 Google 徽标', (brand) => {
    const wrapper = mount(ProviderIcon, {
      props: { brand, size: '22px' },
    })

    expect(wrapper.get('svg').attributes('width')).toBe('22px')
    expect(wrapper.findAll('path').map((path) => path.attributes('fill'))).toEqual([
      '#4285F4',
      '#34A853',
      '#FBBC05',
      '#EA4335',
    ])
  })

  /**
   * A caller explicitly requests a monochrome Google mark using currentColor.
   * The requested color applies to every path so the graphic follows the surrounding text.
   */
  it('Google 徽标保留显式指定的颜色', () => {
    const wrapper = mount(ProviderIcon, {
      props: { brand: 'Google', color: 'currentColor' },
    })

    expect(wrapper.findAll('path').map((path) => path.attributes('fill'))).toEqual(
      Array(4).fill('currentColor'),
    )
  })
})
