import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { defineComponent, h } from 'vue'
import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'

import Pagination from '../Pagination.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

const testDirectory = dirname(fileURLToPath(import.meta.url))
const readSource = (relativePath: string) => readFileSync(resolve(testDirectory, relativePath), 'utf8')

const SelectStub = defineComponent({
  name: 'PaginationSelectStub',
  props: ['modelValue', 'options'],
  setup(props) {
    return () => h('button', { class: 'select-trigger h-9' }, String(props.modelValue))
  }
})

const IconStub = defineComponent({
  name: 'Icon',
  setup() {
    return () => h('span')
  }
})

describe('36px control sizing', () => {
  it('uses the 36px baseline in shared controls', () => {
    const globalStyle = readSource('../../../style.css')
    const selectSource = readSource('../Select.vue')
    const proxySelectorSource = readSource('../ProxySelector.vue')
    const dateRangePickerSource = readSource('../DateRangePicker.vue')
    const paginationSource = readSource('../Pagination.vue')

    expect(globalStyle).toContain('@apply rounded-control px-4 py-1.5 text-sm font-medium;')
    expect(globalStyle).toContain('@apply min-h-9;')
    expect(globalStyle).toContain('@apply inline-flex h-9 w-9 items-center justify-center rounded-control p-0;')
    expect(globalStyle).toContain('@apply w-full rounded-control px-4 py-1.5 text-sm;')
    expect(globalStyle).toContain('@apply flex h-9 items-center gap-3 rounded-control py-1.5;')
    expect(selectSource).toContain('@apply h-9 min-h-9 rounded-control px-4 py-1.5 text-sm;')
    expect(proxySelectorSource).toContain('@apply h-9 min-h-9 rounded-control px-4 py-1.5 text-sm;')
    expect(dateRangePickerSource).toContain('@apply h-9 min-h-9 rounded-control px-4 py-1.5 text-sm;')
    expect(dateRangePickerSource).toContain('@apply inline-flex h-9 min-h-9 items-center justify-center rounded-control px-4 py-1.5 text-sm font-medium;')
    // 分页控件直接由模板里的 h-9 提供 36px 基线,不再有局部高度覆盖。
    expect(paginationSource).toContain('pagination-jump-button btn btn-ghost btn-sm h-9')
    expect(paginationSource).not.toContain('--pagination-control-height')
  })

  it('renders every pagination button on the 36px baseline', () => {
    const wrapper = mount(Pagination, {
      props: {
        total: 30,
        page: 2,
        pageSize: 10
      },
      global: {
        stubs: {
          Select: SelectStub,
          Icon: IconStub
        }
      }
    })

    const buttons = wrapper.findAll('button')
    expect(buttons.length).toBe(8)
    expect(buttons.every((button) => button.classes().includes('h-9'))).toBe(true)
  })

  it('keeps the latest page-specific sizing fixes explicit', () => {
    const accountBulkActionsSource = readSource('../../admin/account/AccountBulkActionsBar.vue')
    const userUsageSource = readSource('../../../views/user/UsageView.vue')
    const adminUsageSource = readSource('../../../views/admin/UsageView.vue')
    const riskControlSource = readSource('../../../views/admin/RiskControlView.vue')
    const settingsSource = readSource('../../../views/admin/SettingsView.vue')
    const emailTemplateSource = readSource('../../../views/admin/settings/EmailTemplateEditor.vue')
    const backupSource = readSource('../../../views/admin/BackupView.vue')
    const providerListSource = readSource('../../payment/PaymentProviderList.vue')
    const adminOrdersSource = readSource('../../../views/admin/orders/AdminOrdersView.vue')
    const adminPaymentPlansSource = readSource('../../../views/admin/orders/AdminPaymentPlansView.vue')

    expect(accountBulkActionsSource).toContain('class="btn btn-primary btn-sm h-[30px]"')
    expect(userUsageSource).toContain('<div class="card p-4">')
    expect(adminUsageSource).toContain('<div class="card p-4">')
    expect(riskControlSource).toContain('class="grid grid-cols-1 items-start gap-4 p-4 xl:grid-cols-[minmax(0,1fr)_minmax(360px,440px)]"')
    expect(riskControlSource).toContain(":class=\"apiKeyRowsExpanded ? 'overflow-visible' : ''\"")
    expect(settingsSource).toContain('class="btn btn-primary btn-sm h-9"')
    expect(settingsSource).toContain('class="btn btn-secondary btn-sm h-9 w-fit"')
    expect(emailTemplateSource).toContain('class="btn btn-primary btn-sm h-9"')
    expect(backupSource).toContain('class="btn btn-primary btn-sm h-9"')
    expect(providerListSource).toContain('class="btn btn-secondary btn-sm h-9 w-9 p-0"')
    expect(adminOrdersSource).toContain('<TablePageLayout>')
    expect(adminOrdersSource).toContain('<template #table>')
    expect(adminPaymentPlansSource).toContain('<TablePageLayout>')
    expect(adminPaymentPlansSource).toContain('<template #table>')
  })
})
