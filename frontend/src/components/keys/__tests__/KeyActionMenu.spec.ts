import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import KeyActionMenu from '../KeyActionMenu.vue'
import type { ApiKey } from '@/types'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

const apiKey: ApiKey = {
  id: 7,
  user_id: 3,
  team_id: 2,
  scope: 'team',
  key: 'sk-test',
  name: 'team-key',
  group_id: 1,
  status: 'active',
  fast_mode_policy: 'follow_request',
  ip_whitelist: [],
  ip_blacklist: [],
  last_used_at: null,
  last_used_ip: null,
  quota: 0,
  quota_used: 0,
  expires_at: null,
  created_at: '2026-07-01T00:00:00Z',
  updated_at: '2026-07-01T00:00:00Z',
  current_concurrency: 0,
  rate_limit_5h: 0,
  rate_limit_1d: 0,
  rate_limit_7d: 0,
  usage_5h: 0,
  usage_1d: 0,
  usage_7d: 0,
  window_5h_start: null,
  window_1d_start: null,
  window_7d_start: null,
  reset_5h_at: null,
  reset_1d_at: null,
  reset_7d_at: null,
}

describe('KeyActionMenu', () => {
  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('将使用、tf/CCS 导入和删除收纳到更多菜单', async () => {
    const wrapper = mount(KeyActionMenu, {
      props: {
        show: true,
        apiKey,
        position: { top: 80, left: 100 },
        allowImport: true,
      },
      attachTo: document.body,
    })

    const menu = document.body.querySelector('[role="menu"]')
    expect(menu?.id).toBe('key-action-menu-7')
    expect(menu?.querySelectorAll('[role="menuitem"]')).toHaveLength(4)
    expect(document.body.textContent).toContain('keys.useKey')
    expect(document.body.textContent).toContain('keys.importToTf')
    expect(document.body.textContent).toContain('keys.importToCcSwitch')
    expect(document.body.textContent).toContain('common.delete')

    const tfButton = Array.from(document.body.querySelectorAll('button'))
      .find((button) => button.textContent?.includes('keys.importToTf'))
    const ccsButton = Array.from(document.body.querySelectorAll('button'))
      .find((button) => button.textContent?.includes('keys.importToCcSwitch'))
    expect(tfButton?.querySelector('path')?.getAttribute('d'))
      .toBe(ccsButton?.querySelector('path')?.getAttribute('d'))
    expect(tfButton?.querySelector('svg')?.classList.contains('text-blue-500')).toBe(true)
    tfButton?.click()
    await wrapper.vm.$nextTick()

    expect(wrapper.emitted('import-tf')?.[0]).toEqual([apiKey])
    expect(wrapper.emitted('close')).toHaveLength(1)

    await wrapper.setProps({ show: true })
    const deleteButton = Array.from(document.body.querySelectorAll('button'))
      .find((button) => button.textContent?.includes('common.delete'))
    deleteButton?.click()
    await wrapper.vm.$nextTick()

    expect(wrapper.emitted('delete')?.[0]).toEqual([apiKey])
    expect(wrapper.emitted('close')).toHaveLength(2)
    wrapper.unmount()
  })

  it('按设置隐藏 CCS 导入入口', () => {
    const wrapper = mount(KeyActionMenu, {
      props: {
        show: true,
        apiKey,
        position: { top: 80, left: 100 },
        allowImport: false,
      },
      attachTo: document.body,
    })

    expect(document.body.textContent).not.toContain('keys.importToCcSwitch')
    expect(document.body.textContent).toContain('keys.importToTf')
    wrapper.unmount()
  })
})
