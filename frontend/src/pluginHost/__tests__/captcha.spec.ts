import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent, h, reactive } from 'vue'
import TrCaptcha from '../TrCaptcha.vue'

// captcha.spec.ts models native challenge completion and public-setting failures.
// Synthetic proof values exercise provider selection, Tencent serialization, and
// lifetime rejection without loading a third-party challenge SDK or solving a
// CAPTCHA. A failed configuration read must never become a disabled-site success.

// Mutable settings and deferred native proof let tests reproduce a provider switch
// while the preceding challenge is still open; wrappers are always unmounted afterward.
const fixture = vi.hoisted(() => ({
  settings: null as unknown as { cachedPublicSettings: Record<string, unknown> | null },
  fetch: vi.fn(),
  verify: vi.fn(),
  reset: vi.fn(),
}))
const wrappers: VueWrapper[] = []
vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key, locale: { value: 'en' } }),
}))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    get cachedPublicSettings() {
      return fixture.settings.cachedPublicSettings
    },
    fetchPublicSettings: fixture.fetch,
  }),
}))
vi.mock('@/components/auth/CaptchaChallenge.vue', () => ({
  default: defineComponent({
    name: 'CaptchaChallenge',
    setup(_, { expose }) {
      expose({ verifyAction: fixture.verify, reset: fixture.reset })
      return () => h('div', { 'data-test': 'challenge' })
    },
  }),
}))

beforeEach(() => {
  fixture.settings = reactive({ cachedPublicSettings: null })
  vi.resetAllMocks()
})
afterEach(() => {
  wrappers.splice(0).forEach((wrapper) => wrapper.unmount())
})

describe('plugin captcha provider boundary', () => {
  // Configuration initially fails before any provider has been selected.
  // Only a successful retry confirming disabled verification emits an empty proof to the plugin page.
  it('waits for confirmed disabled settings and retains retry after failure', async () => {
    fixture.fetch.mockResolvedValueOnce(null).mockImplementationOnce(async () => {
      fixture.settings.cachedPublicSettings = {}
      return {}
    })
    const wrapper = mount(TrCaptcha)
    wrappers.push(wrapper)
    await flushPromises()
    expect(wrapper.emitted('verified')).toBeUndefined()
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.emitted('verified')).toEqual([['']])
  })

  // Tencent returns two independently required proof fields from its action challenge.
  // The wrapper serializes both without truncation so the native host verifier receives the complete proof.
  it('encodes Tencent ticket and random proof in the opaque token', async () => {
    fixture.settings.cachedPublicSettings = {
      tencent_captcha_enabled: true,
      tencent_captcha_app_id: '123',
    }
    fixture.fetch.mockResolvedValue(fixture.settings.cachedPublicSettings)
    fixture.verify.mockResolvedValue({ token: 'ticket', randstr: 'random' })
    const wrapper = mount(TrCaptcha)
    wrappers.push(wrapper)
    await flushPromises()
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.emitted('verified')).toEqual([
      [JSON.stringify({ ticket: 'ticket', randstr: 'random' })],
    ])
  })

  // The selected provider changes before its outstanding action returns a proof.
  // Completion from the old generation cannot emit a token under the replacement provider's configuration.
  it('ignores stale proof after the provider changes', async () => {
    fixture.settings.cachedPublicSettings = {
      tencent_captcha_enabled: true,
      tencent_captcha_app_id: '123',
    }
    fixture.fetch.mockResolvedValue(fixture.settings.cachedPublicSettings)
    let finish!: (proof: { token: string; randstr: string }) => void
    fixture.verify.mockReturnValue(
      new Promise((resolve) => {
        finish = resolve
      }),
    )
    const wrapper = mount(TrCaptcha)
    wrappers.push(wrapper)
    await flushPromises()
    await wrapper.get('button').trigger('click')
    fixture.settings.cachedPublicSettings = { turnstile_enabled: true, turnstile_site_key: 'new' }
    await flushPromises()
    finish({ token: 'old', randstr: 'old' })
    await flushPromises()
    expect(wrapper.emitted('verified')).toBeUndefined()
    expect(wrapper.emitted('expired')).toBeDefined()
  })
})
