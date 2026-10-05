import { nextTick, ref } from 'vue'
import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import CreativeRunHistory from '@/components/creative/CreativeRunHistory.vue'
import { CREATIVE_OUTPUT_DRAG_MIME } from '@/utils/creativeDrag'
import { outputAssetKey, type LocalAsset } from '@/utils/creativeLocalStore'

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

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copied: { value: false }, copyToClipboard: vi.fn() }),
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() }),
}))

vi.mock('@/utils/creativeRunInputs', () => ({
  loadRunInputSnapshot: vi.fn(),
}))

const run = {
  id: 'crun_0123456789abcdef',
  status: 'succeeded' as const,
  model: 'gpt-image-2',
  created_at: Date.now(),
  outputs: [{ output_index: 0, status: 'succeeded' as const, mime_type: 'image/png' }],
}

function createStudio(asset: LocalAsset) {
  return {
    runHistory: ref([run]),
    currentRun: ref(null),
    loadingHistory: ref(false),
    outputAssetMap: ref(new Map([[asset.key, asset]])),
    refreshHistory: vi.fn(),
    importOutputToCanvas: vi.fn(),
  }
}

describe('CreativeRunHistory 拖放', () => {
  beforeEach(() => {
    Object.defineProperty(URL, 'createObjectURL', {
      configurable: true,
      value: vi.fn(() => 'blob:history-test'),
    })
    Object.defineProperty(URL, 'revokeObjectURL', {
      configurable: true,
      value: vi.fn(),
    })
  })

  it('为本地历史缩略图写入受校验的拖放载荷', async () => {
    const asset: LocalAsset = {
      key: outputAssetKey(run.id, 0),
      kind: 'output',
      blob: new Blob(['image'], { type: 'image/png' }),
      runId: run.id,
      outputIndex: 0,
      createdAt: Date.now(),
    }
    const wrapper = mount(CreativeRunHistory, {
      props: { studio: createStudio(asset) },
      global: { stubs: { Icon: true } },
    })

    await wrapper.get('button[aria-expanded="false"]').trigger('click')
    const rowButton = wrapper
      .findAll('button')
      .find((button) => button.text().includes('creative.status.succeeded'))
    expect(rowButton).toBeDefined()
    await rowButton!.trigger('click')

    const transfer = { effectAllowed: '', setData: vi.fn() }
    const event = new Event('dragstart', { bubbles: true, cancelable: true })
    Object.defineProperty(event, 'dataTransfer', { value: transfer })
    wrapper.get('img').element.dispatchEvent(event)

    expect(transfer.effectAllowed).toBe('copy')
    expect(transfer.setData).toHaveBeenCalledWith(
      CREATIVE_OUTPUT_DRAG_MIME,
      JSON.stringify({ runId: run.id, outputIndex: 0 }),
    )
    wrapper.unmount()
  })

  it('在历史入口显示活动任务数量', () => {
    const asset: LocalAsset = {
      key: outputAssetKey(run.id, 0),
      kind: 'output',
      blob: new Blob(['image'], { type: 'image/png' }),
      runId: run.id,
      outputIndex: 0,
      createdAt: Date.now(),
    }
    const studio = createStudio(asset)
    const wrapper = mount(CreativeRunHistory, {
      props: { studio, activeRunCount: 1 },
      global: { stubs: { Icon: true } },
    })

    const badge = wrapper.find('button[aria-expanded="false"] span')
    expect(badge.exists()).toBe(true)
    expect(badge.text()).toBe('1')
    wrapper.unmount()
  })

  it('在活动任务详情中显示并更新生成耗时', async () => {
    vi.useFakeTimers()
    const now = new Date('2026-08-31T00:01:05.000Z')
    vi.setSystemTime(now)
    const startedAt = now.getTime() - 65_000
    const asset: LocalAsset = {
      key: outputAssetKey(run.id, 0),
      kind: 'output',
      blob: new Blob(['image'], { type: 'image/png' }),
      runId: run.id,
      outputIndex: 0,
      createdAt: startedAt,
    }
    const activeRun = {
      ...run,
      id: 'crun_active_timer',
      status: 'running' as const,
      created_at: startedAt,
      started_at: startedAt,
      outputs: [],
    }
    const studio = createStudio(asset)
    studio.runHistory.value = [activeRun]
    const wrapper = mount(CreativeRunHistory, {
      props: { studio },
      global: { stubs: { Icon: true } },
    })

    await wrapper.get('button[aria-expanded="false"]').trigger('click')
    const rowButton = wrapper
      .findAll('button')
      .find((button) => button.text().includes('creative.status.running'))
    expect(rowButton).toBeDefined()
    expect(rowButton!.text()).toContain('01:05')
    await rowButton!.trigger('click')

    expect(wrapper.get('[data-testid="creative-run-elapsed"]').text()).toBe('01:05')
    vi.advanceTimersByTime(2_000)
    await nextTick()
    expect(wrapper.get('[data-testid="creative-run-elapsed"]').text()).toBe('01:07')
    expect(rowButton!.text()).toContain('01:07')

    wrapper.unmount()
    vi.useRealTimers()
  })
})
