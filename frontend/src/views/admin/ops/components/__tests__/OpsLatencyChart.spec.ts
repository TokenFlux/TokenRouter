import { describe, expect, it, vi } from 'vitest'
import { defineComponent, nextTick } from 'vue'
import { mount } from '@vue/test-utils'
import OpsLatencyChart from '../OpsLatencyChart.vue'

// OpsLatencyChart.spec.ts checks histogram boundary editing through the mounted
// settings controls. Chart rendering and dialog chrome are stubbed, while real
// validation decides whether five edited millisecond boundaries can be emitted
// or reset to defaults.

vi.mock('chart.js', () => ({
  Chart: { register: vi.fn() },
  ArcElement: {},
  Legend: {},
  Tooltip: {},
}))

vi.mock('vue-chartjs', async () => {
  const { defineComponent } = await import('vue')
  return {
    Doughnut: defineComponent({
      name: 'DoughnutChartStub',
      props: ['data', 'options'],
      template: '<div class="doughnut-chart-stub" />',
    }),
  }
})

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  }
})

// BaseDialogStub exposes editor content and footer while honoring visibility,
// allowing tests to inspect validation and emitted boundaries.
const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: {
    show: { type: Boolean, default: false }, // dialog fixture visibility flag
    title: { type: String, default: '' }, // displayed stub heading text
  },
  emits: ['close'],
  template: '<div v-if="show" class="dialog-stub"><slot /><slot name="footer" /></div>',
})

// mountChart provides standard boundaries and no histogram data for editor
// tests. Chart and dialog chrome are stubbed, while the returned wrapper exposes
// real draft validation and apply actions.
function mountChart() {
  return mount(OpsLatencyChart, {
    props: {
      latencyData: null,
      loading: false,
      bucketBoundaries: [100, 200, 500, 1000, 2000],
    },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        EmptyState: true,
        HelpTooltip: true,
        Icon: true,
      },
    },
  })
}

describe('OpsLatencyChart', () => {
  // The user enters five ascending custom duration boundaries in the settings
  // dialog. Applying the draft must emit the numeric sequence and close the
  // editor afterward.
  it('校验并应用五个自定义时长边界', async () => {
    const wrapper = mountChart()
    await wrapper.get('[data-test="latency-bucket-settings"]').trigger('click')

    const values = ['1000', '2000', '5000', '10000', '20000']
    for (let index = 0; index < values.length; index += 1) {
      await wrapper.get(`[data-test="latency-boundary-${index}"]`).setValue(values[index])
    }

    await wrapper.get('[data-test="latency-boundary-apply"]').trigger('click')
    expect(wrapper.emitted('update:bucketBoundaries')).toEqual([[[1000, 2000, 5000, 10000, 20000]]])
    expect(wrapper.find('.dialog-stub').exists()).toBe(false)
  })

  // The editor contains a repeated boundary that violates strict ordering. Apply
  // must be disabled until reset restores standard values, which can then be
  // emitted successfully.
  it('阻止非法边界并支持恢复默认值', async () => {
    const wrapper = mountChart()
    await wrapper.get('[data-test="latency-bucket-settings"]').trigger('click')
    await wrapper.get('[data-test="latency-boundary-2"]').setValue('200')

    const applyButton = wrapper.get<HTMLButtonElement>('[data-test="latency-boundary-apply"]')
    expect(applyButton.element.disabled).toBe(true)
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)

    await wrapper.get('[data-test="latency-boundary-reset"]').trigger('click')
    await nextTick()
    const inputValues = wrapper
      .findAll<HTMLInputElement>('input')
      .map((input) => input.element.value)
    expect(inputValues).toEqual(['100', '200', '500', '1000', '2000'])

    await wrapper.get('[data-test="latency-boundary-apply"]').trigger('click')
    expect(wrapper.emitted('update:bucketBoundaries')).toEqual([[[100, 200, 500, 1000, 2000]]])
  })
})
