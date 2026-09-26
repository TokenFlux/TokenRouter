import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import GroupRoutingPolicyFields from '../GroupRoutingPolicyFields.vue'
import { cloneRoutingPolicy, defaultRoutingPolicy } from '../routingPolicy'
import type { GroupRoutingPolicy } from '@/types'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
const stubs = { Select: true, Toggle: true, ModelTagInput: true, Icon: true }

describe('分组独立路由策略', () => {
  it('空映射可以正常打开，表单不再显示策略总开关', () => {
    const raw = { ...defaultRoutingPolicy(), enabled: false, model_mapping: null, allowed_models: null, features_config: null } as unknown as GroupRoutingPolicy
    const wrapper = mount(GroupRoutingPolicyFields, { props: { modelValue: raw, platform: 'openai' }, global: { stubs } })
    expect(wrapper.findComponent({ name: 'Toggle' }).props('modelValue')).toBe(false)
    expect(wrapper.findAllComponents({ name: 'Toggle' })).toHaveLength(1)
    expect(wrapper.text()).not.toContain('admin.groups.routingPolicy.enabled')
    expect(wrapper.text()).not.toContain('admin.groups.routingPolicy.features')
    expect(wrapper.text()).not.toContain('admin.groups.routingPolicy.imageBridge')
    expect(cloneRoutingPolicy(raw).model_mapping).toEqual({})
    expect(cloneRoutingPolicy(raw).enabled).toBe(true)
    expect(raw.enabled).toBe(false)
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })

  it('编辑功能设置时直接启用策略并保留未展示的历史文字', () => {
    const policy = { ...defaultRoutingPolicy(), enabled: false, features: '旧展示文字' }
    const wrapper = mount(GroupRoutingPolicyFields, { props: { modelValue: policy, platform: 'anthropic' }, global: { stubs } })
    wrapper.findAllComponents({ name: 'Toggle' })[1].vm.$emit('update:modelValue', true)
    expect(wrapper.emitted('update:modelValue')?.[0]?.[0]).toMatchObject({
      enabled: true,
      features: '旧展示文字',
      features_config: { web_search_emulation: { anthropic: true } },
    })
    expect(policy.enabled).toBe(false)
  })

  it('复制策略后编辑映射、白名单和特性不会污染原分组', () => {
    const source = defaultRoutingPolicy()
    source.model_mapping.openai = { alias: 'gpt-original' }
    source.allowed_models.openai = ['gpt-original']
    source.features_config.codex_image_generation_bridge = { openai: false }
    const copy = cloneRoutingPolicy(source)
    copy.model_mapping.openai.alias = 'changed'
    copy.allowed_models.openai.push('changed')
    ;(copy.features_config.codex_image_generation_bridge as Record<string, boolean>).openai = true
    expect(source.model_mapping.openai.alias).toBe('gpt-original')
    expect(source.allowed_models.openai).toEqual(['gpt-original'])
    expect(source.features_config.codex_image_generation_bridge).toEqual({ openai: false })
  })
})
