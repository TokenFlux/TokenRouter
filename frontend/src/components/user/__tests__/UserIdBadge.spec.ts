import { mount, enableAutoUnmount } from '@vue/test-utils'
import { reactive, nextTick } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import UserIdBadge from '../UserIdBadge.vue'

// UserIdBadge.spec.ts checks account identifier visibility and clipboard actions
// against changing public settings and account props. Clipboard writes use a
// recorder so the suite verifies the exact copied value without browser permissions.

// state records clipboard calls and supplies public settings for each mounted account control.
const state = vi.hoisted(() => ({
  copy: vi.fn(), // Clipboard operation recorder.
  settings: {} as { show_user_id?: boolean }, // Public display-switch fixture.
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ cachedPublicSettings: state.settings }),
}))
vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard: state.copy }),
}))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (_key: string, params: { id: number }) => `Copy user ID #${params.id}`,
  }),
}))

enableAutoUnmount(afterEach)
beforeEach(() => {
  state.settings = reactive({})
  state.copy.mockClear()
})

describe('UserIdBadge', () => {
  // Older settings omit the display switch and installations can disable it.
  // Neither state should render a copy action for an existing account.
  it.each([undefined, false])('hides identifiers when the switch is %s', (enabled) => {
    state.settings.show_user_id = enabled
    expect(
      mount(UserIdBadge, { props: { userId: 1233 } })
        .find('button')
        .exists(),
    ).toBe(false)
  })

  // An enabled installation exposes a labeled control for the current account.
  // Copying includes the hash prefix requested for the visible identifier.
  it('displays and copies the hash-prefixed account ID', async () => {
    state.settings.show_user_id = true
    const wrapper = mount(UserIdBadge, { props: { userId: 1233 } })
    const button = wrapper.get('button')
    expect(button.text()).toBe('#1233')
    expect(button.attributes('aria-label')).toBe('Copy user ID #1233')
    await button.trigger('click')
    expect(state.copy).toHaveBeenCalledExactlyOnceWith('#1233')
  })

  // Public settings and account identity can change while the shell remains mounted.
  // The control follows both changes and disappears when the account becomes absent.
  it('follows switch changes and copies the current account', async () => {
    const wrapper = mount(UserIdBadge, { props: { userId: 1233 } })
    state.settings.show_user_id = true
    await nextTick()
    await wrapper.setProps({ userId: 42 })
    await wrapper.get('button').trigger('click')
    expect(state.copy).toHaveBeenLastCalledWith('#42')
    state.settings.show_user_id = false
    await nextTick()
    expect(wrapper.find('button').exists()).toBe(false)
    state.settings.show_user_id = true
    await wrapper.setProps({ userId: undefined })
    expect(wrapper.find('button').exists()).toBe(false)
  })
})
