import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import { useAppStore } from '@/stores/app'
import UserDashboardContact from '../UserDashboardContact.vue'

// UserDashboardContact.spec.ts mounts the dashboard contact card against a real app store
// whose contact setting each test controls. Clipboard handling is replaced with a spy, so
// the tests observe rendered rows, link targets, and the value passed to copy actions.

// copyToClipboard records copy requests from text contact rows without writing to the
// system clipboard during component tests.
const copyToClipboard = vi.fn()

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

describe('UserDashboardContact', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  // The card starts without configured contact details and gains them after mount. The
  // test protects the hidden empty state and the reactive appearance once settings load.
  it('renders only when contact info is configured', async () => {
    const appStore = useAppStore()
    const wrapper = mount(UserDashboardContact)

    expect(wrapper.find('[data-testid="dashboard-contact"]').exists()).toBe(false)

    appStore.contactInfo = 'QQ: 123456'
    await nextTick()

    expect(wrapper.find('[data-testid="dashboard-contact"]').exists()).toBe(true)
    wrapper.unmount()
  })

  // A setting with one link and one plain value yields one row of each kind. The test
  // protects the new-tab link target and the exact value handed to the clipboard.
  it('opens links in a new tab and copies plain values', async () => {
    const appStore = useAppStore()
    appStore.contactInfo = 'Telegram: https://t.me/tokenflux, QQ: 123456'
    const wrapper = mount(UserDashboardContact)

    const link = wrapper.get('[data-testid="dashboard-contact-link"]')
    expect(link.attributes('href')).toBe('https://t.me/tokenflux')
    expect(link.attributes('target')).toBe('_blank')
    expect(link.text()).toContain('Telegram')
    expect(link.text()).toContain('t.me/tokenflux')

    await wrapper.get('[data-testid="dashboard-contact-copy"]').trigger('click')
    expect(copyToClipboard).toHaveBeenCalledWith('123456')
    wrapper.unmount()
  })
})
