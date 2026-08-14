import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import GrokFreeIcon from '../GrokFreeIcon.vue'
import PlatformTypeBadge from '../PlatformTypeBadge.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

describe('PlatformTypeBadge', () => {
  it('renders Qoder COSY accounts as Qoder instead of Gemini', () => {
    const wrapper = mount(PlatformTypeBadge, {
      props: {
        platform: 'qoder',
        type: 'cosy'
      },
      global: {
        stubs: {
          PlatformIcon: true,
          Icon: true
        }
      }
    })

    expect(wrapper.text()).toContain('Qoder')
    expect(wrapper.text()).toContain('COSY')
    expect(wrapper.text()).not.toContain('Gemini')
  })

  it('distinguishes Agent Identity, PAT, and OAuth accounts', async () => {
    const wrapper = mount(PlatformTypeBadge, {
      props: {
        platform: 'openai',
        type: 'oauth',
        authMode: 'agentIdentity'
      },
      global: {
        stubs: {
          PlatformIcon: true,
          Icon: true
        }
      }
    })

    expect(wrapper.text()).toContain('Agent Identity')

    await wrapper.setProps({ authMode: 'personalAccessToken' })
    expect(wrapper.text()).toContain('PAT')
    expect(wrapper.text()).not.toContain('Agent Identity')

    await wrapper.setProps({ authMode: undefined })
    expect(wrapper.text()).toContain('OAuth')
  })

  it('renders Grok free aliases with the dedicated icon and without expiration', async () => {
    const wrapper = mount(PlatformTypeBadge, {
      props: {
        platform: 'grok',
        type: 'oauth',
        planType: 'BASIC',
        subscriptionExpiresAt: '2027-01-01T00:00:00Z'
      }
    })

    expect(wrapper.text()).toContain('Grok Free')
    expect(wrapper.findComponent(GrokFreeIcon).exists()).toBe(true)
    expect(wrapper.find('[data-testid="grok-free-plan-icon"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="grok-plan-icon"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('2027-01-01')

    await wrapper.setProps({ planType: 'X_BASIC' })
    expect(wrapper.text()).toContain('X Basic')
    expect(wrapper.findComponent(GrokFreeIcon).exists()).toBe(true)
  })

  it('normalizes and colors paid Grok tiers', async () => {
    const wrapper = mount(PlatformTypeBadge, {
      props: { platform: 'grok', type: 'oauth', planType: 'SuperGrok Heavy' }
    })

    expect(wrapper.text()).toContain('SuperGrok Heavy')
    expect(wrapper.html()).toContain('bg-purple-100')
    expect(wrapper.find('[data-testid="grok-plan-icon"]').exists()).toBe(true)

    await wrapper.setProps({ planType: 'supergrok_lite' })
    expect(wrapper.text()).toContain('SuperGrok Lite')
    expect(wrapper.html()).toContain('bg-cyan-100')

    await wrapper.setProps({ platform: 'openai', planType: 'free' })
    expect(wrapper.text()).toContain('Free')
    expect(wrapper.text()).not.toContain('Grok Free')
    expect(wrapper.find('[data-testid="grok-plan-icon"]').exists()).toBe(false)
  })

  it('uses a compact currentColor Grok free mark', () => {
    const wrapper = mount(GrokFreeIcon)

    expect(wrapper.element.tagName.toLowerCase()).toBe('svg')
    expect(wrapper.attributes('fill')).toBe('currentColor')
    expect(wrapper.classes()).toEqual(expect.arrayContaining(['h-3', 'w-3']))
    expect(wrapper.findAll('path')).toHaveLength(2)
  })
})
