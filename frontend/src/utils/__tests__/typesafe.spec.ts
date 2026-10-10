import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { defaultProviderBrandOptions, providerBrandFilterKey, resolveProviderBrandKey } from '../providerBrand'
import { isUpstreamUsageQueryEnabled } from '../upstreamUsage'
import { providerTestProtocolPlan } from '@/components/admin/provider/providerTestProtocols'
import ModelIcon from '@/components/common/ModelIcon.vue'
import ProviderIcon from '@/components/common/ProviderIcon.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import type { Provider } from '@/types'

describe('TypeSafe 品牌与 Jev 平台', () => {
  it('品牌别名和型号共用 TypeSafe 筛选键', () => {
    for (const alias of ['typesafe', 'TypeSafe AI', 'jev', 'jev-latest', 'typesafe/jev-latest']) {
      expect(resolveProviderBrandKey(alias)).toBe('typesafe')
      expect(providerBrandFilterKey(alias)).toBe('brand:typesafe')
    }
    expect(resolveProviderBrandKey('unrelated-model')).toBeNull()
    expect(defaultProviderBrandOptions).toContainEqual({ value: 'TypeSafe', label: 'TypeSafe' })
  })

  it('平台和模型使用本地 TypeSafe 蒙版图标', () => {
    const wrappers = [
      mount(ModelIcon, { props: { model: 'typesafe/jev-latest' } }),
      mount(ProviderIcon, { props: { brand: 'TypeSafe' } }),
      mount(PlatformIcon, { props: { platform: 'jev' } }),
    ]
    for (const wrapper of wrappers) {
      expect(wrapper.html()).toContain('typesafe.png')
      expect(wrapper.html()).toContain('currentcolor')
      wrapper.unmount()
    }
  })

  it('默认关闭余额查询，允许管理员选用兼容适配器', () => {
    const provider = { platform: 'jev', type: 'apikey', credentials: {}, extra: {} } as Provider
    expect(isUpstreamUsageQueryEnabled(provider)).toBe(false)
    provider.extra = { upstream_usage_query: { enabled: true, adapter: 'sub2api' } }
    expect(isUpstreamUsageQueryEnabled(provider)).toBe(true)
    expect(providerTestProtocolPlan(provider).options).toEqual([{ value: 'native', label: 'SystemOne', path: '/v1/systemone' }])
  })
})
