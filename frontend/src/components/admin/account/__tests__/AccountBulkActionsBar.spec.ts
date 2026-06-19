import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import AccountBulkActionsBar from '../AccountBulkActionsBar.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

describe('AccountBulkActionsBar', () => {
  it('emits refresh-token for selected accounts', async () => {
    const wrapper = mount(AccountBulkActionsBar, {
      props: {
        selectedIds: [44]
      }
    })

    await wrapper.findAll('button').find((button) => button.text() === 'admin.accounts.bulkActions.refreshToken')?.trigger('click')

    expect(wrapper.emitted('refresh-token')).toHaveLength(1)
  })
})
