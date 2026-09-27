import { nextTick, ref } from 'vue'
import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import CreativeRunHistory from '@/components/creative/CreativeRunHistory.vue'

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  }
})

vi.mock('@/composables/useBalanceDisplay', () => ({
  useBalanceDisplay: () => ({
    formatBalanceAmount: (value: number | null | undefined) => String(value ?? ''),
  }),
}))

function createStudio(runs: unknown[]) {
  return {
    runHistory: ref(runs),
    currentRun: ref(null),
    loadingHistory: ref(false),
    outputAssetMap: ref(new Map()),
    refreshHistory: vi.fn(),
    importOutputToCanvas: vi.fn(),
  }
}

const failedRun = {
  id: 'crun_failed',
  status: 'failed' as const,
  model: 'gemini-3.1-flash-lite-image',
  created_at: Date.now(),
  outputs: [{ output_index: 0, status: 'pending' as const }],
}

const succeededRun = {
  id: 'crun_ok',
  status: 'succeeded' as const,
  model: 'gpt-image-2',
  created_at: Date.now(),
  outputs: [{ output_index: 0, status: 'succeeded' as const, mime_type: 'image/png' }],
}

// 展开历史面板并点开指定模型所在的行。
async function expandRunRow(wrapper: ReturnType<typeof mount>, model: string) {
  await wrapper.get('button').trigger('click')
  await nextTick()
  const rowButton = wrapper.findAll('button').find((item) => item.text().includes(model))
  expect(rowButton).toBeTruthy()
  await rowButton!.trigger('click')
  await nextTick()
}

describe('CreativeRunHistory 失败重试', () => {
  it('失败任务展开后提供重试按钮，点击派发 retry 事件', async () => {
    const wrapper = mount(CreativeRunHistory, {
      props: { studio: createStudio([failedRun]) as never, activeRunCount: 0 },
    })

    await expandRunRow(wrapper, failedRun.model)

    const retry = wrapper.get('[data-testid="creative-run-retry"]')
    await retry.trigger('click')
    expect(wrapper.emitted('retry')?.[0]).toEqual([failedRun.id])
  })

  it('成功任务不提供重试按钮', async () => {
    const wrapper = mount(CreativeRunHistory, {
      props: { studio: createStudio([succeededRun]) as never, activeRunCount: 0 },
    })

    await expandRunRow(wrapper, succeededRun.model)

    expect(wrapper.find('[data-testid="creative-run-retry"]').exists()).toBe(false)
  })
})
