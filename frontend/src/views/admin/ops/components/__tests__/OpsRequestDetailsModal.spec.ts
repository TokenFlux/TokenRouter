import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import OpsRequestDetailsModal from '../OpsRequestDetailsModal.vue'
import Pagination from '@/components/common/Pagination.vue'
import { formatDateTime } from '../../utils/opsFormatters'

const listRequestDetails = vi.hoisted(() => vi.fn())
vi.mock('@/api/admin/ops', () => ({ opsAPI: { listRequestDetails } }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showError: vi.fn() }) }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: vi.fn() }) }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({
    t: (key: string, params?: Record<string, string | number>) => `${key} ${Object.values(params || {}).join(' ~ ')}`.trim()
  })
}))

const start = '2026-08-25T00:00:00Z'
const end = '2026-08-28T00:00:00Z'
const now = '2026-09-09T00:00:00Z'

async function openModal(props: Record<string, unknown> = {}) {
  const wrapper = mount(OpsRequestDetailsModal, {
    props: { modelValue: false, timeRange: 'custom', preset: { title: '请求明细' }, ...props },
    global: { stubs: { BaseDialog: { template: '<div><slot /></div>' }, Pagination: true } }
  })
  await wrapper.setProps({ modelValue: true })
  await flushPromises()
  return wrapper
}

describe('请求明细时间窗口', () => {
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date(now))
    listRequestDetails.mockReset().mockResolvedValue({ items: [], total: 0 })
  })

  afterEach(() => vi.useRealTimers())

  it('自定义标签与请求使用相同起止时间，刷新和翻页不回退最近一小时', async () => {
    listRequestDetails.mockResolvedValue({ items: [{ kind: 'success', created_at: start, request_id: 'test-request' }], total: 30 })
    const wrapper = await openModal({ customStartTime: start, customEndTime: end, platform: 'openai', groupId: 3 })
    expect(wrapper.text()).toContain(`admin.ops.requestDetails.rangeCustom ${formatDateTime(start)} ~ ${formatDateTime(end)}`)
    expect(wrapper.text()).not.toContain('admin.ops.requestDetails.rangeHours')
    expect(listRequestDetails).toHaveBeenLastCalledWith(expect.objectContaining({ start_time: start, end_time: end, platform: 'openai', group_id: 3, page: 1 }))
    await wrapper.get('button').trigger('click')
    wrapper.getComponent(Pagination).vm.$emit('update:page', 2)
    await flushPromises()
    expect(listRequestDetails).toHaveBeenCalledTimes(3)
    expect(listRequestDetails).toHaveBeenLastCalledWith(expect.objectContaining({ start_time: start, end_time: end, page: 2 }))
    wrapper.unmount()
  })

  it('保持 custom 模式调整边界时重置页码并刷新查询与标签', async () => {
    const wrapper = await openModal({ customStartTime: start, customEndTime: end })
    const nextStart = '2026-08-26T10:00:00Z'
    const nextEnd = '2026-08-27T11:00:00Z'
    await wrapper.setProps({ customStartTime: nextStart, customEndTime: nextEnd })
    await flushPromises()
    expect(listRequestDetails).toHaveBeenCalledTimes(2)
    expect(listRequestDetails).toHaveBeenLastCalledWith(expect.objectContaining({ start_time: nextStart, end_time: nextEnd, page: 1 }))
    expect(wrapper.text()).toContain(formatDateTime(nextStart))
    expect(wrapper.text()).toContain(formatDateTime(nextEnd))
    wrapper.unmount()
  })

  it.each([
    { customStartTime: start }, { customEndTime: end }, {}
  ])('缺少边界时标签与查询同时回退 1h：%j', async (props) => {
    const wrapper = await openModal(props)
    expect(wrapper.text()).toContain('admin.ops.requestDetails.rangeHours 1')
    expect(listRequestDetails).toHaveBeenLastCalledWith(expect.objectContaining({ start_time: '2026-09-08T23:00:00.000Z', end_time: '2026-09-09T00:00:00.000Z' }))
    wrapper.unmount()
  })

  it.each([
    ['5m', '2026-09-08T23:55:00.000Z', 'rangeMinutes 5'],
    ['30m', '2026-09-08T23:30:00.000Z', 'rangeMinutes 30'],
    ['1h', '2026-09-08T23:00:00.000Z', 'rangeHours 1'],
    ['6h', '2026-09-08T18:00:00.000Z', 'rangeHours 6'],
    ['24h', '2026-09-08T00:00:00.000Z', 'rangeHours 24'],
  ])('预设 %s 保持相对时间窗口并忽略旧自定义边界', async (timeRange, expectedStart, label) => {
    const wrapper = await openModal({ timeRange, customStartTime: start, customEndTime: end })
    expect(wrapper.text()).toContain(`admin.ops.requestDetails.${label}`)
    expect(listRequestDetails).toHaveBeenLastCalledWith(expect.objectContaining({ start_time: expectedStart, end_time: '2026-09-09T00:00:00.000Z' }))
    wrapper.unmount()
  })
})
