import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import OAuthAuthorizationFlow from '../OAuthAuthorizationFlow.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copied: false,
    copyToClipboard: vi.fn()
  })
}))

describe('OAuthAuthorizationFlow', () => {
  it('emits Codex PAT token for OpenAI PAT auth mode', async () => {
    const wrapper = mount(OAuthAuthorizationFlow, {
      props: {
        addMethod: 'oauth',
        platform: 'openai',
        showCookieOption: false,
        showRefreshTokenOption: false,
        showCodexPatOption: true
      },
      global: {
        stubs: {
          Icon: true
        }
      }
    })

    const patRadio = wrapper.find('input[value="codex_pat"]')
    expect(patRadio.exists()).toBe(true)

    await patRadio.setValue(true)
    const tokenInput = wrapper.find('input[type="password"]')
    await tokenInput.setValue(' at-test-token ')
    await wrapper.find('button.btn-primary').trigger('click')

    expect(wrapper.emitted('update:inputMethod')?.at(-1)).toEqual(['codex_pat'])
    expect(wrapper.emitted('import-codex-pat')).toEqual([['at-test-token']])
  })
})
