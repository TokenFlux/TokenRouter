import { nextMotionFrame } from '@/__tests__/helpers/motion'
const ipGeoMocks = vi.hoisted(() => ({
  getEntry: vi.fn(() => ({ status: 'idle' as const })),
  fetchOne: vi.fn(),
  fetchBatch: vi.fn(),
}))

const clipboardMocks = vi.hoisted(() => ({
  copyToClipboard: vi.fn(),
}))

vi.mock('@/utils/ipGeoLookup', () => ipGeoMocks)
vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => clipboardMocks,
}))

vi.mock('@/composables/useBalanceDisplay', () => ({
  useBalanceDisplay: () => ({
    balanceUnitSymbol: { value: '$' },
    usdUnitSymbol: '$',
    formatBalanceAmount: (value: number | null | undefined) => `$${(value ?? 0).toFixed(6)}`,
    formatUsdAmount: (value: number | null | undefined) => `$${(value ?? 0).toFixed(6)}`,
  }),
}))

import { describe, expect, it, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'

import UsageTable from '../UsageTable.vue'

const messages: Record<string, string> = {
  'admin.usage.userDeletedBadge': 'Deleted',
  'usage.costDetails': 'Cost Breakdown',
  'admin.usage.inputCost': 'Input Cost',
  'admin.usage.outputCost': 'Output Cost',
  'admin.usage.cacheCreationCost': 'Cache Creation Cost',
  'admin.usage.cacheReadCost': 'Cache Read Cost',
  'usage.inputTokenPrice': 'Input price',
  'usage.outputTokenPrice': 'Output price',
  'usage.perMillionTokens': '/ 1M tokens',
  'usage.serviceTier': 'Service tier',
  'usage.serviceTierPriority': 'Fast',
  'usage.serviceTierUltrafast': 'Ultrafast',
  'usage.serviceTierFlex': 'Flex',
  'usage.serviceTierStandard': 'Standard',
  'usage.rate': 'Rate',
  'usage.providerMultiplier': 'Provider rate',
  'usage.original': 'Original',
  'usage.userBilled': 'User billed',
  'usage.providerBilled': 'Provider billed',
  'usage.imageUnit': ' images',
  'usage.imageCount': 'Image count',
  'usage.imageBillingSize': 'Billing size',
  'usage.imageInputSize': 'Input size',
  'usage.imageOutputSize': 'Output size',
  'usage.imageSizeSource': 'Size source',
  'usage.imageSizeBreakdown': 'Size breakdown',
  'usage.imageSizeSourceOutput': 'Upstream output',
  'usage.imageSizeSourceInput': 'Request input',
  'usage.imageSizeSourceDefault': 'Default billing tier',
  'usage.imageSizeSourceLegacy': 'Legacy record',
  'usage.imageSizeSourceMissing': 'Not recorded',
  'usage.imageSizeNotRecorded': 'not recorded',
  'usage.imageSizeLegacyUnstandardized': 'legacy unstandardized',
  'usage.imageSizeUnknown': 'unknown',
  'usage.imageUnitPrice': 'Per-image price',
  'usage.imageTotalPrice': 'Image total price',
  'admin.usage.billingModeToken': 'Token',
  'admin.usage.billingModePerRequest': 'Per request',
  'admin.usage.billingModeImage': 'Image',
  'admin.usage.upstreamRequestIdCopied': 'Upstream ID copied',
  'keys.copied': 'Copied',
  'keys.copyToClipboard': 'Copy to clipboard',
  'usage.detailedTiming': 'Detailed Timing',
  'usage.timingRequestSize': 'Request size',
  'usage.timingSlot': 'Slot',
  'usage.timingGetConn': 'Get conn',
  'usage.timingGotConn': 'Got conn',
  'usage.timingWriteRequest': 'Write request',
  'usage.timingFirstByte': 'First byte',
  'usage.timingFirstSSE': 'First SSE',
  'usage.timingVisible': 'Visible',
  'usage.timingFlush': 'First flush',
  'usage.timingAttempts': 'Attempts',
  'usage.timingReused': 'Reused conn',
  'usage.timingWriteError': 'Write error',
  'usage.timingUnavailable': 'Not collected',
  'common.copyFailed': 'Copy failed',
  'common.copied': 'Copied',
  'requests.copy': 'Copy request ID',
  'requests.copied': 'Request ID copied',
}

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => messages[key] ?? key,
    }),
  }
})

const DataTableStub = {
  props: ['data'],
  template: `
    <div>
      <div v-for="row in data" :key="row.request_id">
        <slot name="cell-model" :row="row" :value="row.model" />
        <slot name="cell-billing_mode" :row="row" />
        <slot name="cell-tokens" :row="row" />
        <slot name="cell-cost" :row="row" />
        <slot name="cell-latency" :row="row" />
        <div class="tps-cell"><slot name="cell-tps" :row="row" /></div>
        <slot name="cell-request_id" :row="row" />
        <slot name="cell-upstream_request_id" :row="row" />
      </div>
    </div>
  `,
}

const baseImageRow = {
  request_id: 'req-admin-image',
  model: 'gpt-image-2',
  actual_cost: 0.4,
  total_cost: 0.4,
  provider_rate_multiplier: 1,
  rate_multiplier: 1,
  service_tier: null,
  input_cost: 0,
  output_cost: 0,
  cache_creation_cost: 0,
  cache_read_cost: 0,
  input_tokens: 0,
  output_tokens: 0,
  cache_creation_tokens: 0,
  cache_read_tokens: 0,
  cache_creation_5m_tokens: 0,
  cache_creation_1h_tokens: 0,
  cache_ttl_overridden: false,
  billing_mode: 'image',
  image_count: 2,
  image_size: '2K',
  image_input_size: null,
  image_output_size: null,
  image_size_source: null,
  image_size_breakdown: null,
}

it('用量分组徽章优先显示译文，缺少展示名称时使用业务名称', async () => {
  const wrapper = mount(UsageTable, {
    props: {
      data: [
        { ...baseImageRow, request_id: 'translated', group: { id: 1, name: 'business-name', display_name: 'English group' } },
        { ...baseImageRow, request_id: 'original', group: { id: 2, name: 'Original group' } },
      ],
      loading: false,
      columns: [{ key: 'group', label: 'Group' }],
      showProviderBilling: false,
    },
    global: {
      stubs: {
        DataTable: { props: ['data'], template: '<div><slot v-for="row in data" name="cell-group" :row="row" /></div>' },
        GroupBadge: { props: ['name'], template: '<span data-test="group-name">{{ name }}</span>' },
        EmptyState: true,
        Icon: true,
        Teleport: true,
      },
    },
  })
  expect(wrapper.findAll('[data-test="group-name"]').map(item => item.text())).toEqual(['English group', 'Original group'])
  await wrapper.setProps({ data: [{ ...baseImageRow, group: { id: 1, name: 'business-name', display_name: '中文分组' } }] })
  expect(wrapper.get('[data-test="group-name"]').text()).toBe('中文分组')
  wrapper.unmount()
})

describe('admin UsageTable request ID column', () => {
  beforeEach(() => {
    clipboardMocks.copyToClipboard.mockReset().mockResolvedValue(true)
  })

  it('renders and copies the complete request ID', async () => {
    const wrapper = mount(UsageTable, {
      props: {
        data: [{ ...baseImageRow, request_id: 'req-admin-visible-id' }],
        loading: false,
        columns: [{ key: 'request_id', label: 'Request ID' }],
      },
      global: {
        stubs: { DataTable: DataTableStub, EmptyState: true, Icon: true, Teleport: true },
      },
    })

    expect(wrapper.text()).toContain('req-admin-visible-id')
    await wrapper.get('button[title="Copy request ID"]').trigger('click')

    expect(clipboardMocks.copyToClipboard).toHaveBeenCalledWith('req-admin-visible-id', 'Request ID copied')
    expect(wrapper.get('button').attributes('title')).toBe('Copied')
  })
})

describe('使用记录 TPS 列', () => {
  it('展示 TPS 和公式提示，历史记录缺少耗时时显示占位符', () => {
    const wrapper = mount(UsageTable, {
      props: {
        data: [
          { ...baseImageRow, request_id: 'valid', output_tokens: 100, duration_ms: 5000, first_token_ms: 1000 },
          { ...baseImageRow, request_id: 'missing', output_tokens: 100, duration_ms: 5000, first_token_ms: null },
          { ...baseImageRow, request_id: 'rounding', output_tokens: 23, duration_ms: 21000, first_token_ms: 1000 },
        ],
        columns: [{ key: 'tps', label: 'TPS' }],
      },
      global: {
        stubs: { DataTable: DataTableStub, EmptyState: true, Icon: true, Teleport: true },
      },
    })
    expect(wrapper.findAll('.tps-cell').map(cell => cell.text())).toEqual(['25.0 tok/s', '-', '1.1 tok/s'])
    expect(wrapper.get('.tps-cell span').attributes('title')).toBe('usage.tpsHint')
    wrapper.unmount()
  })
})

describe('admin UsageTable detailed timing tooltip', () => {
  it('renders the request stage timings from the latency info button', async () => {
    const wrapper = mount(UsageTable, {
      props: {
        data: [{
          ...baseImageRow,
          detailed_timing: {
            request_content_length: 8360000,
            provider_slot_acquired_ms: 120,
            upstream_got_conn_ms: 200,
            upstream_get_conn_ms: 180,
            upstream_wrote_request_ms: 420,
            upstream_first_response_byte_ms: 980,
            upstream_first_sse_data_ms: 1020,
            first_visible_output_ms: 1060,
            first_downstream_flush_ms: 1080,
            upstream_attempt_count: 2,
          },
        }],
        loading: false,
        columns: [{ key: 'latency', label: 'Latency' }],
      },
      global: {
        stubs: { DataTable: DataTableStub, EmptyState: true, Icon: true, Teleport: true },
      },
    })

    const timingButton = wrapper.get('button[title="Detailed Timing"]')
    await timingButton.trigger('mouseenter')
    await nextTick()

    expect(wrapper.text()).toContain('Slot')
    expect(wrapper.text()).toContain('7.97MB')
    expect(wrapper.text()).toContain('120ms')
    expect(wrapper.text()).toContain('First SSE')
    expect(wrapper.text()).toContain('2')
  })

  it('keeps the timing tooltip open after a mobile-style click until toggled again', async () => {
    const wrapper = mount(UsageTable, {
      props: {
        data: [{
          ...baseImageRow,
          detailed_timing: { provider_slot_acquired_ms: 120 },
        }],
        loading: false,
        columns: [{ key: 'latency', label: 'Latency' }],
      },
      global: {
        stubs: { DataTable: DataTableStub, EmptyState: true, Icon: true, Teleport: true },
      },
    })

    const timingButton = wrapper.get('button[title="Detailed Timing"]')
    await timingButton.trigger('click')
    await nextTick()
    expect(wrapper.find('[data-testid="timing-detail-tooltip"]').exists()).toBe(true)

    await timingButton.trigger('mouseleave')
    expect(wrapper.find('[data-testid="timing-detail-tooltip"]').exists()).toBe(true)

    await timingButton.trigger('click')
    await nextTick()
    await nextMotionFrame()
    expect(wrapper.find('[data-testid="timing-detail-tooltip"]').exists()).toBe(false)
  })
})

describe('admin UsageTable tooltip', () => {
  beforeEach(() => {
    vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({
      x: 0,
      y: 0,
      top: 20,
      left: 20,
      right: 120,
      bottom: 40,
      width: 100,
      height: 20,
      toJSON: () => ({}),
    } as DOMRect)
  })

  it('marks only usage rows that actually applied long-context billing', () => {
    const wrapper = mount(UsageTable, {
      props: {
        data: [
          {
            ...baseImageRow,
            request_id: 'req-long-context-enabled',
            long_context_billing_applied: true,
          },
          {
            ...baseImageRow,
            request_id: 'req-long-context-disabled',
            long_context_billing_applied: false,
          },
        ],
        loading: false,
        columns: [],
      },
      global: {
        stubs: {
          DataTable: DataTableStub,
          EmptyState: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    expect(wrapper.findAll('[data-testid="long-context-billing-marker"]')).toHaveLength(1)
    expect(wrapper.get('[data-testid="long-context-billing-marker"]').text()).toBe('L')
  })

  it('shows service tier and billing breakdown in cost tooltip', async () => {
    const row = {
      request_id: 'req-admin-1',
      actual_cost: 0.092883,
      total_cost: 0.092883,
      provider_rate_multiplier: 1,
      rate_multiplier: 1,
      service_tier: 'priority',
      input_cost: 0.020285,
      output_cost: 0.00303,
      cache_creation_cost: 0,
      cache_read_cost: 0.069568,
      input_tokens: 4057,
      output_tokens: 101,
    }

    const wrapper = mount(UsageTable, {
      props: {
        data: [row],
        loading: false,
        columns: [],
      },
      global: {
        stubs: {
          DataTable: DataTableStub,
          EmptyState: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    const tooltipTriggers = wrapper.findAll('.group.relative')
    await tooltipTriggers[tooltipTriggers.length - 1].trigger('mouseenter')
    await nextTick()

    const text = wrapper.text()
    expect(text).toContain('Service tier')
    expect(text).toContain('Fast')
    expect(text).toContain('Rate')
    expect(text).toContain('1.00x')
    expect(text).toContain('Provider rate')
    expect(text).toContain('User billed')
    expect(text).toContain('Provider billed')
    expect(text).toContain('$0.092883')
    expect(text).toContain('$5.0000 / 1M tokens')
    expect(text).toContain('$30.0000 / 1M tokens')
    expect(text).toContain('$0.069568')
  })

  it.each([
    { triggerIndex: 0, tooltipTestId: 'token-detail-tooltip', tooltipWidth: 260, tooltipHeight: 180 },
    { triggerIndex: 1, tooltipTestId: 'cost-detail-tooltip', tooltipWidth: 296, tooltipHeight: 340 },
  ])('keeps $tooltipTestId inside a narrow viewport', async ({ triggerIndex, tooltipTestId, tooltipWidth, tooltipHeight }) => {
    const viewportWidthSpy = vi.spyOn(window, 'innerWidth', 'get').mockReturnValue(320)
    const viewportHeightSpy = vi.spyOn(window, 'innerHeight', 'get').mockReturnValue(640)
    const rectSpy = vi.mocked(HTMLElement.prototype.getBoundingClientRect)
    rectSpy.mockImplementation(function (this: HTMLElement) {
      if (this.dataset.testid === tooltipTestId) {
        return {
          x: 0,
          y: 0,
          top: 0,
          left: 0,
          right: tooltipWidth,
          bottom: tooltipHeight,
          width: tooltipWidth,
          height: tooltipHeight,
          toJSON: () => ({}),
        } as DOMRect
      }

      return {
        x: 280,
        y: 300,
        top: 300,
        left: 280,
        right: 304,
        bottom: 320,
        width: 24,
        height: 20,
        toJSON: () => ({}),
      } as DOMRect
    })

    const wrapper = mount(UsageTable, {
      props: {
        data: [{
          request_id: 'req-mobile-tooltip',
          model: 'gpt-5.4',
          actual_cost: 0.092883,
          total_cost: 0.092883,
          rate_multiplier: 1,
          input_cost: 0.020285,
          output_cost: 0.00303,
          cache_creation_cost: 0,
          cache_read_cost: 0.069568,
          input_tokens: 4057,
          output_tokens: 101,
        }],
        loading: false,
        columns: [],
      },
      global: {
        stubs: {
          DataTable: DataTableStub,
          EmptyState: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    try {
      await wrapper.findAll('.group.relative')[triggerIndex].trigger('mouseenter')
      await nextTick()
      await nextTick()

      const tooltip = wrapper.get(`[data-testid="${tooltipTestId}"]`)
      const left = Number.parseFloat((tooltip.element as HTMLElement).style.left)
      expect(left).toBeGreaterThanOrEqual(12)
      expect(left + tooltipWidth).toBeLessThanOrEqual(308)
      expect(tooltip.classes()).not.toContain('invisible')
    } finally {
      wrapper.unmount()
      viewportWidthSpy.mockRestore()
      viewportHeightSpy.mockRestore()
    }
  })

  // 后端确认上游模型与出站模型不同后展示声明，未知模型保持未知。
  it.each([
    { mismatch: true, response: 'runtime-model-' + 'v'.repeat(160), visible: true },
    { mismatch: false, response: 'sent-model', visible: false },
    { mismatch: null, response: null, visible: false },
    { mismatch: undefined, response: undefined, visible: false },
    { mismatch: true, response: '', visible: false },
  ])('renders the response model only on a known mismatch: $mismatch/$visible', ({ mismatch, response, visible }) => {
    const wrapper = mount(UsageTable, {
      props: {
        data: [{ ...baseImageRow, model: 'public-model', upstream_model: 'sent-model',
          model_mapping_chain: 'public-model→sent-model',
          upstream_response_model: response, upstream_model_mismatch: mismatch }],
        loading: false,
        columns: [],
      },
      global: { stubs: { DataTable: DataTableStub, EmptyState: true, Icon: true, Teleport: true } },
    })
    const model = wrapper.find('[data-testid="upstream-response-model"]')
    expect(model.exists()).toBe(visible)
    expect(wrapper.text()).toContain('public-model')
    expect(wrapper.text()).toContain('sent-model')
    if (visible) {
      expect(model.text()).toContain('↳')
      expect(model.text()).toContain(response)
      expect(model.attributes('title')).toContain(`sent-model → ${response}`)
      expect(model.classes()).toEqual(expect.arrayContaining(['text-xs', 'break-all', 'text-orange-600', 'dark:text-orange-400']))
    }
    wrapper.unmount()
  })

  it('shows requested and upstream models separately for admin rows', () => {
    const row = {
      request_id: 'req-admin-model-1',
      model: 'claude-sonnet-4',
      upstream_model: 'claude-sonnet-4-20250514',
      actual_cost: 0,
      total_cost: 0,
      provider_rate_multiplier: 1,
      rate_multiplier: 1,
      input_cost: 0,
      output_cost: 0,
      cache_creation_cost: 0,
      cache_read_cost: 0,
      input_tokens: 0,
      output_tokens: 0,
    }

    const wrapper = mount(UsageTable, {
      props: {
        data: [row],
        loading: false,
        columns: [],
      },
      global: {
        stubs: {
          DataTable: DataTableStub,
          EmptyState: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    const text = wrapper.text()
    expect(text).toContain('claude-sonnet-4')
    expect(text).toContain('claude-sonnet-4-20250514')
  })

  it.each([
    {
      name: 'defaulted row',
      row: {
        ...baseImageRow,
        request_id: 'req-admin-default-image',
        image_size: '2K',
        image_input_size: 'auto',
        image_output_size: null,
        image_size_source: 'default',
      },
      expected: ['2K', 'Default billing tier', 'auto', 'unknown'],
    },
    {
      name: 'output-sourced row',
      row: {
        ...baseImageRow,
        request_id: 'req-admin-output-image',
        image_size: '4K',
        image_input_size: '1024x1024',
        image_output_size: '3840x2160',
        image_size_source: 'output',
        image_size_breakdown: { '4K': 1 },
      },
      expected: ['4K', 'Upstream output', '1024x1024', '3840x2160', '4K x 1'],
    },
    {
      name: 'input-sourced row',
      row: {
        ...baseImageRow,
        request_id: 'req-admin-input-image',
        image_size: '1K',
        image_input_size: '1024x1024',
        image_output_size: null,
        image_size_source: 'input',
      },
      expected: ['1K', 'Request input', '1024x1024', 'unknown'],
    },
    {
      name: 'legacy unstandardized row',
      row: {
        ...baseImageRow,
        request_id: 'req-admin-legacy-unstandardized-image',
        image_size: '512x512',
        image_input_size: null,
        image_output_size: null,
        image_size_source: null,
      },
      expected: ['legacy unstandardized: 512x512', 'Legacy record', 'unknown'],
    },
  ])('shows image usage metadata for $name', async ({ row, expected }) => {
    const wrapper = mount(UsageTable, {
      props: {
        data: [row],
        loading: false,
        columns: [],
      },
      global: {
        stubs: {
          DataTable: DataTableStub,
          EmptyState: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    await wrapper.find('.group.relative').trigger('mouseenter')
    await nextTick()

    const text = wrapper.text()
    expect(text).toContain('Image count')
    expect(text).toContain('Billing size')
    expect(text).toContain('Size source')
    expect(text).toContain('Input size')
    expect(text).toContain('Output size')
    expect(text).toContain('Per-image price')
    expect(text).toContain('Image total price')
    for (const value of expected) {
      expect(text).toContain(value)
    }
  })

  it('displays historical image rows with missing billing_mode as image usage without a 2K fallback', async () => {
    const wrapper = mount(UsageTable, {
      props: {
        data: [
          {
            ...baseImageRow,
            request_id: 'req-admin-legacy-missing-image',
            billing_mode: null,
            image_size: null,
            image_input_size: null,
            image_output_size: null,
            image_size_source: null,
            image_size_breakdown: null,
          },
        ],
        loading: false,
        columns: [],
      },
      global: {
        stubs: {
          DataTable: DataTableStub,
          EmptyState: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    await wrapper.find('.group.relative').trigger('mouseenter')
    await nextTick()

    const text = wrapper.text()
    expect(text).toContain('Image')
    expect(text).toContain('Image count')
    expect(text).toContain('Per-image price')
    expect(text).toContain('not recorded')
    expect(text).not.toContain('(2K)')
  })
})

describe('admin UsageTable request ID column', () => {
  beforeEach(() => {
    clipboardMocks.copyToClipboard.mockReset().mockResolvedValue(true)
  })

  it('renders and copies the request ID', async () => {
    const wrapper = mount(UsageTable, {
      props: {
        data: [{ ...baseImageRow, request_id: 'req-admin-visible-id' }],
        loading: false,
        columns: [{ key: 'request_id', label: 'Request ID' }],
      },
      global: {
        stubs: {
          DataTable: DataTableStub,
          EmptyState: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    expect(wrapper.text()).toContain('req-admin-visible-id')
    await wrapper.get('button[title="Copy request ID"]').trigger('click')

    expect(clipboardMocks.copyToClipboard).toHaveBeenCalledWith('req-admin-visible-id', 'Request ID copied')
  })

  it('renders and copies the upstream ID', async () => {
    const wrapper = mount(UsageTable, {
      props: {
        data: [{ ...baseImageRow, request_id: '', upstream_request_id: '20260903082826779695' }],
        loading: false,
        columns: [{ key: 'upstream_request_id', label: 'Upstream ID' }],
      },
      global: {
        stubs: {
          DataTable: DataTableStub,
          EmptyState: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    expect(wrapper.text()).toContain('20260903082826779695')
    const copyButtons = wrapper.findAll('button[title="Copy to clipboard"]')
    expect(copyButtons).toHaveLength(1)
    await copyButtons[0].trigger('click')

    expect(clipboardMocks.copyToClipboard).toHaveBeenCalledWith('20260903082826779695', 'Upstream ID copied')
  })
})
describe('admin UsageTable IP geolocation batch toolbar', () => {
  const DataTableStubWithIp = {
    props: ['data'],
    template: `
      <div>
        <div v-for="row in data" :key="row.request_id">
          <slot name="cell-ip_address" :row="row" />
        </div>
      </div>
    `,
  }

  beforeEach(() => {
    ipGeoMocks.getEntry.mockReset()
    ipGeoMocks.fetchOne.mockReset()
    ipGeoMocks.fetchBatch.mockReset()
    ipGeoMocks.getEntry.mockReturnValue({ status: 'idle' })
  })

  it('does not render the batch toolbar when the ip_address column is not visible', () => {
    const wrapper = mount(UsageTable, {
      props: {
        data: [{ request_id: 'r1', ip_address: '8.8.8.8' }],
        loading: false,
        columns: [],
      },
      global: { stubs: { DataTable: DataTableStubWithIp, EmptyState: true, Teleport: true } },
    })
    expect(wrapper.text()).not.toContain('usage.ipGeo.batchFetch')
  })

  it('renders the batch toolbar with a pending count when the ip_address column is visible', () => {
    const wrapper = mount(UsageTable, {
      props: {
        data: [
          { request_id: 'r1', ip_address: '8.8.8.8' },
          { request_id: 'r2', ip_address: '8.8.8.8' },
          { request_id: 'r3', ip_address: '1.1.1.1' },
        ],
        loading: false,
        columns: [{ key: 'ip_address', label: 'IP' }],
      },
      global: { stubs: { DataTable: DataTableStubWithIp, EmptyState: true, Teleport: true } },
    })
    expect(wrapper.text()).toContain('usage.ipGeo.pending')
    const button = wrapper.find('button')
    expect(button.exists()).toBe(true)
    expect((button.element as HTMLButtonElement).disabled).toBe(false)
  })

  it('fetches deduplicated IPs from the current page when the batch button is clicked', async () => {
    ipGeoMocks.fetchBatch.mockResolvedValue(true)
    const wrapper = mount(UsageTable, {
      props: {
        data: [
          { request_id: 'r1', ip_address: '8.8.8.8' },
          { request_id: 'r2', ip_address: '8.8.8.8' },
          { request_id: 'r3', ip_address: '1.1.1.1' },
        ],
        loading: false,
        columns: [{ key: 'ip_address', label: 'IP' }],
      },
      global: { stubs: { DataTable: DataTableStubWithIp, EmptyState: true, Teleport: true } },
    })
    await wrapper.find('button').trigger('click')
    expect(ipGeoMocks.fetchBatch).toHaveBeenCalledWith(['8.8.8.8', '1.1.1.1'])
    expect(wrapper.emitted('ipGeoBatchFailed')).toBeUndefined()
  })

  it('emits ipGeoBatchFailed when the batch request reports a network-level failure', async () => {
    ipGeoMocks.fetchBatch.mockResolvedValue(false)
    const wrapper = mount(UsageTable, {
      props: {
        data: [{ request_id: 'r1', ip_address: '8.8.8.8' }],
        loading: false,
        columns: [{ key: 'ip_address', label: 'IP' }],
      },
      global: { stubs: { DataTable: DataTableStubWithIp, EmptyState: true, Teleport: true } },
    })
    await wrapper.find('button').trigger('click')
    expect(wrapper.emitted('ipGeoBatchFailed')).toHaveLength(1)
  })

  it('renders IpGeoCell content for ip_address cells', () => {
    ipGeoMocks.getEntry.mockReturnValue({ status: 'success', label: 'CN · Guangdong · Shenzhen', detail: {} })
    const wrapper = mount(UsageTable, {
      props: {
        data: [{ request_id: 'r1', ip_address: '121.35.47.43' }],
        loading: false,
        columns: [{ key: 'ip_address', label: 'IP' }],
      },
      global: { stubs: { DataTable: DataTableStubWithIp, EmptyState: true, Teleport: true } },
    })
    expect(wrapper.text()).toContain('121.35.47.43')
    expect(wrapper.text()).toContain('CN · Guangdong · Shenzhen')
  })
})

// 这个 DataTable stub 会渲染 cell-user，便于断言已删除徽标。
const DataTableStubWithUser = {
  props: ['data'],
  template: `
    <div>
      <div v-for="row in data" :key="row.request_id">
        <slot name="cell-user" :row="row" />
        <slot name="cell-model" :row="row" :value="row.model" />
        <slot name="cell-billing_mode" :row="row" />
        <slot name="cell-tokens" :row="row" />
        <slot name="cell-cost" :row="row" />
      </div>
    </div>
  `,
}

describe('admin UsageTable deleted-user badge', () => {
  it('right-aligns compact members on mobile and constrains long emails on desktop', () => {
    const email = 'member.with.a.very.long.address@tokenrouter.example.com'
    const wrapper = mount(UsageTable, {
      props: {
        data: [{ request_id: 'req-long-email', user_id: 3559, user: { id: 3559, email } }],
        loading: false,
        columns: [{ key: 'user', label: 'User' }],
        userClickable: false,
        compactUserColumn: true,
      },
      global: {
        stubs: {
          DataTable: DataTableStubWithUser,
          EmptyState: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    expect(wrapper.get('[data-test="usage-user-cell"]').classes()).toEqual(expect.arrayContaining([
      'justify-end',
      'md:w-32',
      'md:justify-start',
    ]))
    expect(wrapper.get('[data-test="usage-user-cell"]').classes()).not.toContain('w-32')
    expect(wrapper.get('[data-test="usage-user-email"]').classes()).toContain('truncate')
    expect(wrapper.get('[data-test="usage-user-email"]').text()).toBe('m***s')
    expect(wrapper.get('[data-test="usage-user-email"]').attributes('title')).toBe(email)
    expect(wrapper.text()).toContain('#3559')
  })

  it('uses the same first-and-last masking rule for a one-character email local part', () => {
    const wrapper = mount(UsageTable, {
      props: {
        data: [{ request_id: 'req-short-email', user_id: 8, user: { id: 8, email: 'a@example.com' } }],
        loading: false,
        columns: [{ key: 'user', label: 'User' }],
        userClickable: false,
        compactUserColumn: true,
      },
      global: {
        stubs: { DataTable: DataTableStubWithUser, EmptyState: true, Icon: true, Teleport: true },
      },
    })

    expect(wrapper.get('[data-test="usage-user-email"]').text()).toBe('a***a')
    expect(wrapper.get('[data-test="usage-user-email"]').attributes('title')).toBe('a@example.com')
  })

  it('prefers a custom username while preserving the email in the title', () => {
    const wrapper = mount(UsageTable, {
      props: {
        data: [{ request_id: 'req-username', user_id: 7, user: { id: 7, username: 'Ada', email: 'ada@example.com' } }],
        loading: false,
        columns: [{ key: 'user', label: 'User' }],
        userClickable: false,
        compactUserColumn: true,
      },
      global: {
        stubs: { DataTable: DataTableStubWithUser, EmptyState: true, Icon: true, Teleport: true },
      },
    })

    expect(wrapper.get('[data-test="usage-user-email"]').text()).toBe('Ada')
    expect(wrapper.get('[data-test="usage-user-email"]').attributes('title')).toBe('Ada (ada@example.com)')
  })

  it('renders deleted badge for a soft-deleted user row', () => {
    const row = {
      request_id: 'req-deleted-user-1',
      model: 'claude-3',
      user_id: 2,
      user: { id: 2, email: 'd@test.com', deleted_at: '2026-05-28T00:00:00Z' },
      actual_cost: 0,
      total_cost: 0,
      input_cost: 0,
      output_cost: 0,
      rate_multiplier: 1,
      input_tokens: 1,
      output_tokens: 1,
    }

    const wrapper = mount(UsageTable, {
      props: {
        data: [row],
        loading: false,
        columns: [{ key: 'user', label: 'User' }],
      },
      global: {
        stubs: {
          DataTable: DataTableStubWithUser,
          EmptyState: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    expect(wrapper.text()).toContain('Deleted')
    expect(wrapper.text()).toContain('d***d')
  })

  it('does NOT render deleted badge for an active user row', () => {
    const row = {
      request_id: 'req-active-user-1',
      model: 'claude-3',
      user_id: 3,
      user: { id: 3, email: 'active@test.com', deleted_at: null },
      actual_cost: 0,
      total_cost: 0,
      input_cost: 0,
      output_cost: 0,
      rate_multiplier: 1,
      input_tokens: 1,
      output_tokens: 1,
    }

    const wrapper = mount(UsageTable, {
      props: {
        data: [row],
        loading: false,
        columns: [{ key: 'user', label: 'User' }],
      },
      global: {
        stubs: {
          DataTable: DataTableStubWithUser,
          EmptyState: true,
          Icon: true,
          Teleport: true,
        },
      },
    })

    expect(wrapper.text()).not.toContain('Deleted')
    expect(wrapper.text()).toContain('a***e')
  })
})
