import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import GroupRoutingPolicyFields from '../GroupRoutingPolicyFields.vue'
import { cloneRoutingPolicy, defaultRoutingPolicy } from '../routingPolicy'
import type { GroupRoutingPolicy } from '@/types'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
const stubs = { Select: true, Toggle: true, ModelTagInput: true, Icon: true }

describe('分组独立路由策略', () => {
  it('数据库中的空映射可以正常打开，显式关闭仍被保留', () => {
    const raw = { ...defaultRoutingPolicy(), enabled: false, model_mapping: null, allowed_models: null, features_config: null } as unknown as GroupRoutingPolicy
    const wrapper = mount(GroupRoutingPolicyFields, { props: { modelValue: raw, platform: 'openai' }, global: { stubs } })
    expect(wrapper.findComponent({ name: 'Toggle' }).props('modelValue')).toBe(false)
    expect(cloneRoutingPolicy(raw).model_mapping).toEqual({})
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
