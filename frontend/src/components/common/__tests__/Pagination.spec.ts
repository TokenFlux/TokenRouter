import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import Pagination from '../Pagination.vue'

// Pagination.spec.ts verifies navigation at empty and populated result boundaries.
// Both responsive control groups use the actual component, while child visuals and
// translation formatting are replaced so assertions focus on available page actions.

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, values?: Record<string, unknown>) =>
      values ? `${key} ${JSON.stringify(values)}` : key,
  }),
}))

describe('pagination result boundaries', () => {
  // Filtering a populated table down to zero rows must disable navigation in both layouts.
  // The mobile label and jump range still describe one valid empty page.
  it('disables both directions after the result set becomes empty', async () => {
    const wrapper = mount(Pagination, {
      props: { total: 21, page: 1, pageSize: 10, showPageSizeSelector: false, showJump: true },
      global: { stubs: { Icon: true, Select: true } },
    })
    await wrapper.setProps({ total: 0 })
    const controls = wrapper
      .findAll('button')
      .filter((button) =>
        ['pagination.previous', 'pagination.next'].includes(
          button.attributes('aria-label') || button.text(),
        ),
      )
    expect(controls).toHaveLength(4)
    for (const button of controls) {
      expect(button.attributes('disabled')).toBeDefined()
      await button.trigger('click')
    }
    expect(wrapper.emitted('update:page')).toBeUndefined()
    expect(wrapper.get('.pagination-mobile').text()).toContain('"total":1')
    expect(wrapper.get('input[type="number"]').attributes('max')).toBe('1')
    wrapper.unmount()
  })

  // A populated middle page keeps both directions available and emits the selected target.
  // Moving to the last page disables forward navigation without blocking a return to earlier records.
  it('retains normal navigation and the last-page boundary', async () => {
    const wrapper = mount(Pagination, {
      props: { total: 21, page: 2, pageSize: 10, showPageSizeSelector: false },
      global: { stubs: { Icon: true, Select: true } },
    })
    await wrapper.get('[aria-label="pagination.previous"]').trigger('click')
    await wrapper.get('[aria-label="pagination.next"]').trigger('click')
    expect(wrapper.emitted('update:page')).toEqual([[1], [3]])
    await wrapper.setProps({ page: 3 })
    expect(wrapper.get('[aria-label="pagination.next"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[aria-label="pagination.previous"]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })
})
