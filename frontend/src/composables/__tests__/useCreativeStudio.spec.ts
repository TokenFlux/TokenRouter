import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'

/**
 * useCreativeStudio.spec.ts exercises catalog selection, pricing, submission,
 * local recovery, and polling through mocked browser storage and HTTP APIs.
 * Tests retain independent network and persistence observations so delayed
 * operations cannot silently overwrite newer workspace state.
 */

/**
 * refreshUser records account reads separately from task APIs so tests can
 * delay or reject balance refreshes during generation.
 */
const { refreshUser } = vi.hoisted(() => ({ refreshUser: vi.fn() }))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ refreshUser }),
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key }),
}))

vi.mock('@/api/creative', () => ({
  CREATIVE_RUN_TERMINAL_STATUSES: ['succeeded', 'failed', 'cancelled', 'result_lost'],
  getCreativeModels: vi.fn(),
  getCreativeCapabilities: vi.fn(),
  getCreativeActiveRuns: vi.fn(),
  createCreativeRun: vi.fn(),
  getCreativeRuns: vi.fn(),
  getCreativeRun: vi.fn(),
  getCreativeRunOutputContent: vi.fn(),
  ackCreativeRunOutput: vi.fn(),
}))

/**
 * Retain real key builders and error classes while replacing storage effects so
 * recovery assertions use production identity rules with controlled request
 * outcomes.
 */
vi.mock('@/utils/creativeLocalStore', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/utils/creativeLocalStore')>()
  return {
    ...actual,
    saveAsset: vi.fn(),
    deleteAsset: vi.fn(),
    listAssets: vi.fn(),
    loadAsset: vi.fn(),
    loadSetting: vi.fn(),
    saveSetting: vi.fn(),
    saveRunParams: vi.fn(),
    loadRunParamsMap: vi.fn(),
    clearAll: vi.fn(),
  }
})

import * as creativeApi from '@/api/creative'
import * as localStore from '@/utils/creativeLocalStore'
import { useCreativeStudio, creativeOptionKey } from '@/composables/useCreativeStudio'
import type { CreativeRun } from '@/api/creative'

/**
 * useCreativeStudio.spec.ts exercises generation validation, local settings,
 * shared polling, output recovery, and workspace invalidation through the
 * mounted composable. API and persistent-store effects are mocked, while real
 * key construction and storage error classes keep identity and failure handling
 * representative. Fake timers control polling and debounce intervals, and
 * deferred promises expose ordering between recovery, clearing, placement, and
 * account refreshes. The tests inspect observable state and request arguments
 * without contacting generation providers.
 */

/**
 * mockedApi and mockedStore provide typed access to request and persistence
 * substitutes used across studio scenarios.
 */
const mockedApi = vi.mocked(creativeApi)
const mockedStore = vi.mocked(localStore)

/**
 * currentWorkspaceId reads the browser identity established by the production
 * workspace helper.
 */
function currentWorkspaceId(): string {
  return localStorage.getItem(localStore.CREATIVE_WORKSPACE_STORAGE_KEY) ?? ''
}

/**
 * MODEL supplies one selectable image model with multiple sizes and optional
 * controls for validation, normalization, and pricing assertions.
 */
const MODEL = {
  group_id: 'g1',
  group_name: 'Group A',
  model: 'model-x',
  operations: ['generate', 'edit', 'inpaint'],
  image_sizes: ['512', '1K', '2K'],
  aspect_ratios: ['1:1', '4:3', '16:9', 'auto'],
  qualities: ['low', 'medium', 'high', 'auto'],
  output_formats: [],
  output_compression: null,
  background_options: ['auto', 'opaque', 'transparent'],
  thinking_levels: ['minimal', 'high'],
  max_output_count: 1,
  max_reference_images: 16,
  price_512: 1.5,
  price_1k: 2,
  price_2k: 3,
  price_4k: 4,
}

/**
 * makeRun builds a server task fixture with caller-supplied identity and
 * status. Defaults supply the shared model and empty outputs, while overrides
 * define the specific polling or recovery scenario.
 */
function makeRun(partial: Partial<CreativeRun> & Pick<CreativeRun, 'id' | 'status'>): CreativeRun {
  return {
    operation: 'generate',
    model: MODEL.model,
    group_id: MODEL.group_id,
    requested_output_count: 1,
    outputs: [],
    ...partial,
  }
}

/**
 * mountStudio creates the composable inside a real mounted component setup. The
 * returned wrapper lets callers trigger lifecycle cleanup, while the studio
 * exposes reactive controls for direct scenario assertions.
 */
function mountStudio() {
  let studio!: ReturnType<typeof useCreativeStudio>
  const wrapper = mount(
    defineComponent({
      /**
       * setup initializes the studio in component context and returns a minimal
       * render function.
       */
      setup() {
        studio = useCreativeStudio()
        return () => h('div')
      },
    }),
  )
  return { studio, wrapper }
}

describe('useCreativeStudio', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.useFakeTimers()
    localStorage.clear()
    refreshUser.mockResolvedValue(undefined)

    mockedApi.getCreativeModels.mockResolvedValue([{ ...MODEL }])
    mockedApi.getCreativeCapabilities.mockResolvedValue({
      max_prompt_chars: 8000,
      max_asset_bytes: 33554432,
      max_total_input_bytes: 67108864,
      max_mask_bytes: 4194304,
      allowed_mime_types: ['image/png', 'image/jpeg', 'image/webp'],
    })
    mockedApi.getCreativeActiveRuns.mockResolvedValue({
      items: [],
      next_cursor: '',
      has_more: false,
    })
    mockedApi.createCreativeRun.mockResolvedValue(makeRun({ id: 'run-1', status: 'queued' }))
    mockedApi.getCreativeRuns.mockResolvedValue({ items: [], total: 0 })
    mockedApi.getCreativeRun.mockResolvedValue(makeRun({ id: 'run-1', status: 'succeeded' }))
    mockedApi.getCreativeRunOutputContent.mockResolvedValue(
      new Blob(['img'], { type: 'image/png' }),
    )
    mockedApi.ackCreativeRunOutput.mockResolvedValue(undefined)
    mockedStore.saveAsset.mockResolvedValue({} as localStore.LocalAsset)
    mockedStore.deleteAsset.mockResolvedValue(undefined)
    mockedStore.loadAsset.mockResolvedValue(null)
    mockedStore.loadSetting.mockResolvedValue(null)
    mockedStore.saveSetting.mockResolvedValue(undefined)
    mockedStore.clearAll.mockResolvedValue(undefined)
    mockedStore.saveRunParams.mockResolvedValue(undefined)
    mockedStore.loadRunParamsMap.mockResolvedValue(new Map())
    mockedStore.listAssets.mockImplementation((kind: string) =>
      Promise.resolve(kind === 'output' || kind === 'source' || kind === 'mask' ? [] : []),
    )
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
    vi.clearAllMocks()
  })

  /**
   * setupStudio mounts the studio, loads the mocked catalog, and selects its sole
   * model. Callers receive the wrapper for cleanup and a normalized form ready
   * for their scenario.
   */
  async function setupStudio() {
    const { studio, wrapper } = mountStudio()
    await studio.loadModels()
    studio.selectOption(creativeOptionKey(MODEL))
    return { studio, wrapper }
  }

  /**
   * A model advertises preferred ratio, quality, background, and thinking
   * choices. Selecting it must normalize the form to the intended defaults
   * supported by that catalog entry.
   */
  it('模型支持时为比例、画质、背景和思考强度选择固定默认值', async () => {
    const { studio, wrapper } = await setupStudio()

    expect(studio.aspectRatio.value).toBe('auto')
    expect(studio.quality.value).toBe('medium')
    expect(studio.background.value).toBe('auto')
    expect(studio.thinkingLevel.value).toBe('minimal')
    wrapper.unmount()
  })

  describe('创作参数本地持久化', () => {
    /**
     * Stored settings contain a complete model selection and prompt from an earlier
     * visit. Catalog loading must restore those values before later form
     * persistence begins.
     */
    it('恢复完整的模型参数与提示词设置', async () => {
      mockedStore.loadSetting.mockResolvedValueOnce({
        optionKey: creativeOptionKey(MODEL),
        operation: 'edit',
        imageSize: '2K',
        aspectRatio: '4:3',
        quality: 'high',
        background: 'transparent',
        thinkingLevel: '',
        prompt: '恢复的提示词',
      })
      const { studio, wrapper } = mountStudio()

      await studio.loadModels()

      expect(studio.prompt.value).toBe('恢复的提示词')
      expect(studio.operation.value).toBe('edit')
      expect(studio.imageSize.value).toBe('2K')
      expect(studio.aspectRatio.value).toBe('4:3')
      expect(studio.quality.value).toBe('high')
      expect(studio.background.value).toBe('transparent')
      wrapper.unmount()
    })

    /**
     * Prompt edits occur both during normal activity and immediately before
     * pagehide. Writes must respect the debounce during editing and flush the
     * latest text when hiding.
     */
    it('提示词输入使用防抖写入，并在页面隐藏时立即刷新', async () => {
      const { studio, wrapper } = await setupStudio()
      await vi.advanceTimersByTimeAsync(300)
      /**
       * Discard hydration writes before testing prompt debounce so the observed calls
       * correspond to edits made by this scenario.
       */
      mockedStore.saveSetting.mockClear()

      studio.prompt.value = '正在编辑的提示词'
      await Promise.resolve()
      expect(mockedStore.saveSetting).not.toHaveBeenCalled()

      await vi.advanceTimersByTimeAsync(299)
      expect(mockedStore.saveSetting).not.toHaveBeenCalled()
      await vi.advanceTimersByTimeAsync(1)
      await Promise.resolve()
      expect(mockedStore.saveSetting).toHaveBeenCalledWith(
        'creative:selection',
        expect.objectContaining({ prompt: '正在编辑的提示词' }),
      )

      mockedStore.saveSetting.mockClear()
      studio.prompt.value = '关闭前的最新提示词'
      window.dispatchEvent(new Event('pagehide'))
      await vi.advanceTimersByTimeAsync(0)
      await Promise.resolve()
      expect(mockedStore.saveSetting).toHaveBeenCalledWith(
        'creative:selection',
        expect.objectContaining({ prompt: '关闭前的最新提示词' }),
      )
      wrapper.unmount()
    })
  })

  describe('createRun 校验', () => {
    /**
     * An edit request supplies no reference image to the submission entry point.
     * Validation must reject it locally with source-required feedback before any
     * API call.
     */
    it('edit 无源图时直接报错且不发起请求', async () => {
      const { studio } = await setupStudio()
      studio.operation.value = 'edit'

      const ok = await studio.createRun({ sourceBlobs: [], maskBlob: null })

      expect(ok).toBe(false)
      expect(studio.error.value).toBe('creative.error.sourceRequired')
      expect(mockedApi.createCreativeRun).not.toHaveBeenCalled()
    })

    /**
     * An inpaint request supplies source bytes but omits its required mask.
     * Validation must publish mask-required feedback and avoid calling the server
     * creation endpoint.
     */
    it('inpaint 有源图但无 mask 时直接报错且不发起请求', async () => {
      const { studio } = await setupStudio()
      studio.operation.value = 'inpaint'

      const ok = await studio.createRun({ sourceBlobs: [new Blob(['a'])], maskBlob: null })

      expect(ok).toBe(false)
      expect(studio.error.value).toBe('creative.error.maskRequired')
      expect(mockedApi.createCreativeRun).not.toHaveBeenCalled()
    })

    /**
     * The selected operation is absent from the model's supported operation list.
     * Submission must fail with the unsupported-operation message before sending
     * any creation request.
     */
    it('操作不在模型支持列表时拒绝提交', async () => {
      const { studio } = await setupStudio()
      studio.operation.value = 'video'

      const ok = await studio.createRun({ sourceBlobs: [], maskBlob: null })

      expect(ok).toBe(false)
      expect(studio.error.value).toBe('creative.error.operationNotSupported')
      expect(mockedApi.createCreativeRun).not.toHaveBeenCalled()
    })
  })

  describe('canGenerate 表单门禁', () => {
    /**
     * Generation supports GIF while editing only supports PNG. Switching operations
     * resets the invalid selection and its estimate before a static edit is submitted.
     */
    it('uses operation-specific formats and resets GIF when switching to static editing', async () => {
      const { studio, wrapper } = await setupStudio()
      studio.models.value[0].output_formats = ['png', 'gif']
      studio.models.value[0].output_formats_by_operation = {
        generate: ['png', 'gif'],
        edit: ['png'],
        inpaint: ['png'],
      }
      studio.prompt.value = 'A blinking star'
      studio.outputFormat.value = 'gif'
      studio.frameCount.value = 4
      expect(studio.outputFormatOptions.value).toEqual(['png', 'gif'])
      expect(studio.estimatedCost.value).toBe(8)

      studio.operation.value = 'edit'
      await flushPromises()
      expect(studio.outputFormatOptions.value).toEqual(['png'])
      expect(studio.outputFormat.value).toBe('png')
      expect(studio.estimatedCost.value).toBe(2)
      expect(studio.canGenerate.value).toBe(true)
      expect(
        await studio.createRun({
          sourceBlobs: [new Blob(['source'], { type: 'image/png' })],
          maskBlob: null,
        }),
      ).toBe(true)
      const form = mockedApi.createCreativeRun.mock.calls[0][0] as FormData
      expect(form.get('operation')).toBe('edit')
      expect(form.get('output_format')).toBe('png')
      expect(form.has('frame_count')).toBe(false)
      wrapper.unmount()
    })

    /**
     * A stale restored format conflicts with the current editing route. Submission
     * rejects GIF immediately, even before the reactive format-reset watcher runs.
     */
    it('blocks a stale GIF selection before sending an unsupported edit', async () => {
      const { studio, wrapper } = await setupStudio()
      studio.models.value[0].output_formats = ['png', 'gif']
      studio.models.value[0].output_formats_by_operation = {
        generate: ['png', 'gif'],
        edit: ['png'],
      }
      studio.operation.value = 'edit'
      await flushPromises()
      studio.prompt.value = 'A blinking star'
      studio.outputFormat.value = 'gif'
      expect(studio.canGenerate.value).toBe(false)
      expect(await studio.createRun({ sourceBlobs: [new Blob(['source'])], maskBlob: null })).toBe(
        false,
      )
      expect(mockedApi.createCreativeRun).not.toHaveBeenCalled()
      wrapper.unmount()
    })

    /**
     * The server publishes a GIF-capable editing route separately from static generation.
     * A missing entry in that map must not inherit the combined GIF list.
     */
    it('exposes GIF editing independently and rejects missing operation entries', async () => {
      const { studio, wrapper } = await setupStudio()
      studio.models.value[0].output_formats = ['png', 'gif']
      studio.models.value[0].output_formats_by_operation = {
        generate: ['png'],
        edit: ['png', 'gif'],
      }
      expect(studio.outputFormatOptions.value).toEqual(['png'])
      studio.operation.value = 'edit'
      expect(studio.outputFormatOptions.value).toEqual(['png', 'gif'])
      delete studio.models.value[0].output_formats_by_operation.edit
      expect(studio.outputFormatOptions.value).toEqual([])
      studio.prompt.value = 'A blinking star'
      expect(studio.canGenerate.value).toBe(false)
      wrapper.unmount()
    })

    /**
     * The prompt changes from empty text to whitespace and then meaningful content.
     * The reactive generation gate must remain closed until a nonblank prompt is
     * present.
     */
    it('提示词为空或纯空白时禁止生成', async () => {
      const { studio } = await setupStudio()

      studio.prompt.value = ''
      expect(studio.canGenerate.value).toBe(false)

      studio.prompt.value = '   '
      expect(studio.canGenerate.value).toBe(false)

      studio.prompt.value = '一只猫'
      expect(studio.canGenerate.value).toBe(true)
    })
  })

  describe('estimatedCost 定价', () => {
    /**
     * A GIF-capable route receives explicit frame controls and a per-frame quote.
     * Submission must preserve those controls while PNG returns to one output unit.
     */
    it('prices GIF frames and sends animation parameters', async () => {
      const { studio, wrapper } = await setupStudio()
      studio.models.value[0].output_formats = ['png', 'gif']
      studio.outputFormat.value = 'gif'
      studio.frameCount.value = 4
      studio.frameDurationMS.value = 300
      studio.prompt.value = 'A blinking star'
      expect(studio.estimatedCost.value).toBe(8)
      mockedApi.createCreativeRun.mockResolvedValue(makeRun({ id: 'gif-run', status: 'queued' }))
      expect(await studio.createRun({ sourceBlobs: [], maskBlob: null })).toBe(true)
      const form = mockedApi.createCreativeRun.mock.calls[0][0] as FormData
      expect(form.get('output_format')).toBe('gif')
      expect(form.get('frame_count')).toBe('4')
      expect(form.get('frame_duration_ms')).toBe('300')
      studio.outputFormat.value = 'png'
      expect(studio.estimatedCost.value).toBe(2)
      wrapper.unmount()
    })

    /**
     * The selected image size moves through multiple catalog pricing tiers. The
     * estimate must follow each tier's supplied price, including the separately
     * configured 512 option.
     */
    it('按所选尺寸读取模型目录的实际档位价格', async () => {
      const { studio } = await setupStudio()

      studio.imageSize.value = '1K'
      expect(studio.estimatedCost.value).toBe(2)
      studio.imageSize.value = '2K'
      expect(studio.estimatedCost.value).toBe(3)
      studio.imageSize.value = '4K'
      expect(studio.estimatedCost.value).toBe(4)
      studio.imageSize.value = '512'
      expect(studio.estimatedCost.value).toBe(1.5)

      studio.imageSize.value = '2K'
      expect(studio.estimatedCost.value).toBe(3)
    })
  })

  describe('createRun 提交', () => {
    /**
     * A valid submission includes prompt, model options, and a reference image. Its
     * FormData must carry supported fields and start shared list polling after
     * acceptance.
     */
    it('成功路径：FormData 字段齐全并启动轮询', async () => {
      const { studio } = await setupStudio()
      studio.prompt.value = '一只猫'
      studio.quality.value = 'high'
      studio.background.value = 'opaque'
      await Promise.resolve()
      const sourceBlob = new Blob(['src'], { type: 'image/png' })
      mockedApi.createCreativeRun.mockResolvedValue(makeRun({ id: 'run-9', status: 'queued' }))
      mockedApi.getCreativeRuns.mockResolvedValue({
        items: [makeRun({ id: 'run-9', status: 'succeeded' })],
        total: 1,
      })

      const ok = await studio.createRun({ sourceBlobs: [sourceBlob], maskBlob: null })

      expect(ok).toBe(true)
      expect(studio.error.value).toBe('')
      expect(mockedApi.createCreativeRun).toHaveBeenCalledTimes(1)

      const form = mockedApi.createCreativeRun.mock.calls[0][0] as FormData
      expect(form.get('group_id')).toBe('g1')
      expect(form.get('model')).toBe('model-x')
      expect(form.get('operation')).toBe('generate')
      expect(form.get('prompt')).toBe('一只猫')
      expect(form.get('image_size')).toBe('1K')
      expect(form.get('aspect_ratio')).toBe('auto')
      expect(form.get('quality')).toBe('high')
      expect(form.get('output_format')).toBe('png')
      expect(form.get('output_compression')).toBeNull()
      expect(form.get('background')).toBe('opaque')
      expect(form.get('thinking_level')).toBe('minimal')
      expect(form.get('response_mime_type')).toBeNull()
      expect(form.getAll('source_images[]')).toHaveLength(1)

      expect(studio.polling.value).toBe(true)
      await vi.advanceTimersByTimeAsync(3000)
      expect(mockedApi.getCreativeRuns).toHaveBeenCalledTimes(1)
      expect(mockedApi.getCreativeRun).not.toHaveBeenCalled()
    })

    /**
     * The user edits the prompt while task creation is awaiting its response.
     * Remembered run parameters must retain the pre-request snapshot in memory and
     * local storage.
     */
    it('提交成功后记录发请求前的提示词和参数', async () => {
      const { studio } = await setupStudio()
      studio.prompt.value = '一只猫'
      studio.quality.value = 'high'
      await Promise.resolve()
      let resolveRun!: (run: CreativeRun) => void
      mockedApi.createCreativeRun.mockReturnValue(
        new Promise((resolve) => {
          resolveRun = resolve
        }),
      )

      const pending = studio.createRun({ sourceBlobs: [], maskBlob: null })
      studio.prompt.value = '提交后继续编辑'
      resolveRun(makeRun({ id: 'run-7', status: 'queued' }))
      await pending

      const expected = expect.objectContaining({
        runId: 'run-7',
        optionKey: creativeOptionKey(MODEL),
        groupName: 'Group A',
        operation: 'generate',
        prompt: '一只猫',
        imageSize: '1K',
        aspectRatio: 'auto',
        quality: 'high',
        background: 'auto',
        thinkingLevel: 'minimal',
        referenceCount: 0,
      })
      expect(mockedStore.saveRunParams).toHaveBeenCalledWith(expected)
      expect(studio.runParamsMap.value.get('run-7')).toEqual(expected)
    })

    /**
     * Task creation succeeds while saving its local parameter snapshot reports a
     * quota failure. The submission must remain accepted and its in-memory history
     * parameters remain available.
     */
    it('任务参数写入失败时任务照常提交', async () => {
      const { studio } = await setupStudio()
      studio.prompt.value = '一只猫'
      await Promise.resolve()
      mockedStore.saveRunParams.mockRejectedValue(new localStore.LocalStoreQuotaError())
      vi.spyOn(console, 'error').mockImplementation(() => undefined)

      const ok = await studio.createRun({ sourceBlobs: [], maskBlob: null })
      await Promise.resolve()

      expect(ok).toBe(true)
      expect(studio.error.value).toBe('')
      expect(studio.runParamsMap.value.has('run-1')).toBe(true)
    })

    /**
     * A saved run contains one size unsupported by the current model catalog. Reuse
     * must restore its prompt and supported options while normalizing that size to
     * the default.
     */
    it('applyRunParams 把历史参数和提示词填回输入框，超出模型能力的参数回到默认值', async () => {
      mockedStore.loadRunParamsMap.mockResolvedValue(
        new Map([
          [
            'run-3',
            {
              runId: 'run-3',
              optionKey: creativeOptionKey(MODEL),
              groupName: 'Group A',
              operation: 'edit',
              prompt: '历史提示词',
              imageSize: '4K',
              aspectRatio: '16:9',
              quality: 'low',
              background: 'transparent',
              thinkingLevel: 'high',
              referenceCount: 2,
              createdAt: 1,
            },
          ],
        ]),
      )
      const { studio } = await setupStudio()
      await studio.refreshHistory()

      expect(studio.applyRunParams('missing-run')).toBe(false)
      expect(studio.applyRunParams('run-3')).toBe(true)

      expect(studio.prompt.value).toBe('历史提示词')
      expect(studio.operation.value).toBe('edit')
      expect(studio.imageSize.value).toBe('1K')
      expect(studio.aspectRatio.value).toBe('16:9')
      expect(studio.quality.value).toBe('low')
      expect(studio.background.value).toBe('transparent')
      expect(studio.thinkingLevel.value).toBe('high')
    })

    /**
     * Loading browser-local run parameters fails while server history remains
     * available. History refresh must still publish the task list without replacing
     * it with a storage error.
     */
    it('任务参数读取失败时历史照常刷新', async () => {
      mockedStore.loadRunParamsMap.mockRejectedValue(
        new localStore.LocalStoreError('unavailable', 'blocked'),
      )
      mockedApi.getCreativeRuns.mockResolvedValue({
        items: [makeRun({ id: 'run-5', status: 'failed' })],
        total: 1,
      })
      vi.spyOn(console, 'error').mockImplementation(() => undefined)
      const { studio } = await setupStudio()

      await studio.refreshHistory()

      expect(studio.error.value).toBe('')
      expect(studio.runHistory.value.map((run) => run.id)).toEqual(['run-5'])
    })

    /**
     * A valid inpaint submission includes both source and PNG mask bytes. The
     * request must include the named mask file, current workspace identity, and an
     * idempotency key.
     */
    it('inpaint 时附加 mask 文件与幂等键', async () => {
      const { studio } = await setupStudio()
      studio.operation.value = 'inpaint'

      const ok = await studio.createRun({
        sourceBlobs: [new Blob(['src'], { type: 'image/png' })],
        maskBlob: new Blob(['mask'], { type: 'image/png' }),
      })

      expect(ok).toBe(true)
      const [, workspaceId, key] = mockedApi.createCreativeRun.mock.calls[0]
      expect(workspaceId).toBe(currentWorkspaceId())
      expect(typeof key).toBe('string')
      expect(key).toHaveLength(36)
      const form = mockedApi.createCreativeRun.mock.calls[0][0] as FormData
      const mask = form.get('mask') as File
      expect(mask).toBeInstanceOf(Blob)
      expect(mask.name).toBe('mask.png')
    })

    /**
     * Creation fails twice before succeeding and then receives another new
     * submission. Retries through acceptance must share the same key, while the
     * subsequent submission receives a different one.
     */
    it('失败重试复用同一幂等键，成功后重置', async () => {
      const { studio } = await setupStudio()
      mockedApi.createCreativeRun.mockRejectedValue(new Error('network'))

      const first = await studio.createRun({ sourceBlobs: [], maskBlob: null })
      const retry = await studio.createRun({ sourceBlobs: [], maskBlob: null })

      expect(first).toBe(false)
      expect(retry).toBe(false)
      const keyAfterFail = mockedApi.createCreativeRun.mock.calls[1][2]
      expect(mockedApi.createCreativeRun.mock.calls[0][2]).toBe(keyAfterFail)

      mockedApi.createCreativeRun.mockResolvedValue(makeRun({ id: 'run-2', status: 'queued' }))
      const success = await studio.createRun({ sourceBlobs: [], maskBlob: null })
      expect(success).toBe(true)
      expect(mockedApi.createCreativeRun.mock.calls[2][2]).toBe(keyAfterFail)

      mockedApi.createCreativeRun.mockRejectedValue(new Error('network'))
      await studio.createRun({ sourceBlobs: [], maskBlob: null })
      const newKey = mockedApi.createCreativeRun.mock.calls[3][2]
      expect(newKey).not.toBe(keyAfterFail)
      expect(typeof newKey).toBe('string')
    })
  })

  describe('轮询节奏', () => {
    /**
     * Two accepted runs progress through shared list snapshots and complete
     * together. They must share each scheduled poll while both outputs are
     * recovered and placed through the bridge.
     */
    it('多个 run 共享一次列表轮询并分别收割输出', async () => {
      const { studio } = await setupStudio()
      const run1 = makeRun({
        id: 'run-1',
        status: 'queued',
        outputs: [{ output_index: 0, status: 'succeeded' }],
      })
      const run2 = makeRun({
        id: 'run-2',
        status: 'queued',
        outputs: [{ output_index: 0, status: 'succeeded' }],
      })
      mockedApi.createCreativeRun.mockResolvedValueOnce(run1).mockResolvedValueOnce(run2)
      let pollCount = 0
      mockedApi.getCreativeRuns.mockImplementation(() => {
        pollCount += 1
        const status = pollCount === 1 ? 'running' : 'succeeded'
        const outputs =
          status === 'succeeded' ? [{ output_index: 0, status: 'succeeded' as const }] : []
        return Promise.resolve({
          items: [
            makeRun({ id: 'run-2', status, outputs }),
            makeRun({ id: 'run-1', status, outputs }),
          ],
          total: 2,
        })
      })
      const placed: string[] = []
      studio.registerCanvasBridge({
        placeOutput: ({ runId }) => {
          placed.push(runId)
        },
        importToCanvas: vi.fn(),
      })

      await studio.createRun({ sourceBlobs: [], maskBlob: null })
      await studio.createRun({ sourceBlobs: [], maskBlob: null })
      await vi.advanceTimersByTimeAsync(3000)

      expect(mockedApi.getCreativeRuns).toHaveBeenCalledTimes(1)
      expect(mockedApi.getCreativeRun).not.toHaveBeenCalled()
      await vi.advanceTimersByTimeAsync(3000)

      expect(mockedApi.getCreativeRuns).toHaveBeenCalledTimes(2)
      expect(new Set(placed)).toEqual(new Set(['run-1', 'run-2']))
      expect(studio.outputAssetMap.value.has('output:run-1:0')).toBe(true)
      expect(studio.outputAssetMap.value.has('output:run-2:0')).toBe(true)
      expect(studio.polling.value).toBe(false)
    })

    /**
     * A history refresh discovers a queued task created outside the current
     * submission flow. The next shared poll must publish its terminal state and
     * stop tracking it.
     */
    it('批量轮询会同步外部任务终态并停止追踪', async () => {
      const { studio } = await setupStudio()
      const queuedRun = makeRun({ id: 'run-external', status: 'queued' })
      const succeededRun = makeRun({ id: 'run-external', status: 'succeeded' })
      mockedApi.getCreativeRuns
        .mockResolvedValueOnce({ items: [queuedRun], total: 1 })
        .mockResolvedValueOnce({ items: [succeededRun], total: 1 })

      await studio.refreshHistory()
      expect(studio.runHistory.value[0]?.status).toBe('queued')

      await vi.advanceTimersByTimeAsync(3000)
      expect(studio.runHistory.value[0]?.status).toBe('succeeded')
      expect(mockedApi.getCreativeRuns).toHaveBeenCalledTimes(2)
      expect(studio.polling.value).toBe(false)
    })

    /**
     * A list response reports queued status after success was already observed for
     * that run. The known terminal task must remain successful in current and
     * history state.
     */
    it('历史接口短暂返回旧状态时不会降级已完成任务', async () => {
      const { studio } = await setupStudio()
      const succeededRun = makeRun({ id: 'run-stale-list', status: 'succeeded' })
      mockedApi.createCreativeRun.mockResolvedValue(succeededRun)
      mockedApi.getCreativeRuns.mockResolvedValue({
        items: [makeRun({ id: 'run-stale-list', status: 'queued' })],
        total: 1,
      })

      await studio.createRun({ sourceBlobs: [], maskBlob: null })
      await vi.advanceTimersByTimeAsync(3000)

      expect(studio.currentRun.value?.status).toBe('succeeded')
      expect(studio.runHistory.value[0]?.status).toBe('succeeded')
      expect(studio.polling.value).toBe(false)
    })

    /**
     * A submitted task stays running through two list polls before succeeding.
     * Requests must follow the three-second schedule and cease once the terminal
     * snapshot is observed.
     */
    it('固定每 3s 批量查询，终态后停止', async () => {
      const { studio } = await setupStudio()
      let calls = 0
      mockedApi.getCreativeRuns.mockImplementation(() => {
        calls += 1
        return Promise.resolve({
          items: [makeRun({ id: 'run-1', status: calls <= 2 ? 'running' : 'succeeded' })],
          total: 1,
        })
      })

      await studio.createRun({ sourceBlobs: [], maskBlob: null })
      expect(mockedApi.getCreativeRuns).not.toHaveBeenCalled()

      await vi.advanceTimersByTimeAsync(2999)
      expect(mockedApi.getCreativeRuns).not.toHaveBeenCalled()
      await vi.advanceTimersByTimeAsync(1)
      expect(mockedApi.getCreativeRuns).toHaveBeenCalledTimes(1)

      await vi.advanceTimersByTimeAsync(3000)
      expect(mockedApi.getCreativeRuns).toHaveBeenCalledTimes(2)
      await vi.advanceTimersByTimeAsync(3000)
      expect(mockedApi.getCreativeRuns).toHaveBeenCalledTimes(3)

      expect(studio.polling.value).toBe(false)
      expect(studio.currentRun.value?.status).toBe('succeeded')
      const countAfterTerminal = mockedApi.getCreativeRuns.mock.calls.length
      await vi.advanceTimersByTimeAsync(10000)
      expect(mockedApi.getCreativeRuns).toHaveBeenCalledTimes(countAfterTerminal)
      expect(mockedApi.getCreativeRun).not.toHaveBeenCalled()
    })

    /**
     * A tracked run enters result_lost without usable output content. Polling must
     * stop and the studio must avoid attempting a content download for that
     * terminal task.
     */
    it('result_lost 终态直接停止，不尝试取回内容', async () => {
      const { studio } = await setupStudio()
      mockedApi.getCreativeRuns.mockResolvedValue({
        items: [makeRun({ id: 'run-1', status: 'result_lost' })],
        total: 1,
      })

      await studio.createRun({ sourceBlobs: [], maskBlob: null })
      await vi.advanceTimersByTimeAsync(3000)

      expect(studio.polling.value).toBe(false)
      expect(studio.currentRun.value?.status).toBe('result_lost')
      expect(mockedApi.getCreativeRunOutputContent).not.toHaveBeenCalled()
    })

    /**
     * The mounted studio is removed while a task remains active after one poll.
     * Advancing the clock afterward must produce no further scheduled history
     * requests.
     */
    it('组件卸载后定时器清理，不再轮询', async () => {
      const { studio, wrapper } = await setupStudio()
      mockedApi.getCreativeRuns.mockResolvedValue({
        items: [makeRun({ id: 'run-1', status: 'running' })],
        total: 1,
      })

      await studio.createRun({ sourceBlobs: [], maskBlob: null })
      await vi.advanceTimersByTimeAsync(3000)
      expect(mockedApi.getCreativeRuns).toHaveBeenCalledTimes(1)

      wrapper.unmount()
      await vi.advanceTimersByTimeAsync(10000)
      expect(mockedApi.getCreativeRuns).toHaveBeenCalledTimes(1)
    })
  })

  describe('输出收割', () => {
    /**
     * succeededRunWithOutputs builds a successful task fixture with two
     * unacknowledged outputs.
     */
    function succeededRunWithOutputs() {
      return makeRun({
        id: 'run-1',
        status: 'succeeded',
        outputs: [
          { output_index: 0, status: 'succeeded' },
          { output_index: 1, status: 'succeeded' },
        ],
      })
    }

    /**
     * A successful run exposes two unacknowledged image outputs in index order.
     * Recovery must download, save, and acknowledge both identities while leaving
     * the missing-output set empty.
     */
    it('succeeded 后逐输出取回 → 存本地 → ack', async () => {
      const { studio } = await setupStudio()
      mockedApi.getCreativeRuns.mockResolvedValue({ items: [succeededRunWithOutputs()], total: 1 })

      await studio.createRun({ sourceBlobs: [], maskBlob: null })
      await vi.advanceTimersByTimeAsync(3000)

      expect(mockedApi.getCreativeRunOutputContent).toHaveBeenCalledTimes(2)
      expect(mockedApi.getCreativeRunOutputContent).toHaveBeenNthCalledWith(
        1,
        'run-1',
        0,
        currentWorkspaceId(),
      )
      expect(mockedApi.getCreativeRunOutputContent).toHaveBeenNthCalledWith(
        2,
        'run-1',
        1,
        currentWorkspaceId(),
      )

      expect(mockedStore.saveAsset).toHaveBeenCalledTimes(2)
      const savedKeys = mockedStore.saveAsset.mock.calls.map(([asset]) => asset.key)
      expect(savedKeys).toEqual(['output:run-1:0', 'output:run-1:1'])
      for (const [asset] of mockedStore.saveAsset.mock.calls) {
        expect(asset.kind).toBe('output')
        expect(asset.runId).toBe('run-1')
      }

      expect(mockedApi.ackCreativeRunOutput).toHaveBeenCalledTimes(2)
      expect(mockedApi.ackCreativeRunOutput).toHaveBeenNthCalledWith(
        1,
        'run-1',
        0,
        currentWorkspaceId(),
      )
      expect(mockedApi.ackCreativeRunOutput).toHaveBeenNthCalledWith(
        2,
        'run-1',
        1,
        currentWorkspaceId(),
      )
      expect(studio.missingOutputKeys.value.size).toBe(0)
    })

    /**
     * The first successful output is permanently unavailable while a second remains
     * recoverable. Recovery must mark only the missing output and continue saving
     * and acknowledging the available image.
     */
    it('单个输出取回失败仅标 missing，不影响其它输出', async () => {
      const { studio } = await setupStudio()
      mockedApi.getCreativeRunOutputContent.mockImplementation((_runId: string, index: number) => {
        if (index === 0) {
          return Promise.reject(Object.assign(new Error('gone'), { status: 410 }))
        }
        return Promise.resolve(new Blob(['img-1'], { type: 'image/png' }))
      })
      mockedApi.getCreativeRuns.mockResolvedValue({
        items: [succeededRunWithOutputs()],
        total: 1,
      })
      mockedStore.listAssets.mockImplementation((kind: string) =>
        Promise.resolve(
          kind === 'output'
            ? [
                {
                  key: 'output:run-1:1',
                  kind: 'output' as const,
                  blob: new Blob(['img']),
                  runId: 'run-1',
                  outputIndex: 1,
                  createdAt: 1,
                },
              ]
            : [],
        ),
      )

      await studio.createRun({ sourceBlobs: [], maskBlob: null })
      await vi.advanceTimersByTimeAsync(3000)

      expect(studio.missingOutputKeys.value.has('output:run-1:0')).toBe(true)
      expect(studio.missingOutputKeys.value.has('output:run-1:1')).toBe(false)
      expect(mockedStore.saveAsset).toHaveBeenCalledTimes(1)
      expect(mockedStore.saveAsset.mock.calls[0][0].outputIndex).toBe(1)
      expect(mockedApi.ackCreativeRunOutput).toHaveBeenCalledWith('run-1', 1, currentWorkspaceId())
    })

    /**
     * A successful output already carries a server acknowledgement timestamp.
     * Recovery must skip both content download and acknowledgement when the local
     * index has no usable copy.
     */
    it('已 ack 的输出跳过取回', async () => {
      const { studio } = await setupStudio()
      mockedApi.getCreativeRuns.mockResolvedValue({
        items: [
          makeRun({
            id: 'run-1',
            status: 'succeeded',
            outputs: [{ output_index: 0, status: 'succeeded', acked_at: 1725000000 }],
          }),
        ],
        total: 1,
      })

      await studio.createRun({ sourceBlobs: [], maskBlob: null })
      await vi.advanceTimersByTimeAsync(3000)

      expect(mockedApi.getCreativeRunOutputContent).not.toHaveBeenCalled()
      expect(mockedApi.ackCreativeRunOutput).not.toHaveBeenCalled()
    })
  })

  describe('画布桥接', () => {
    /**
     * succeededRunWithTwoOutputs supplies two successful outputs for canvas bridge
     * ordering tests.
     */
    function succeededRunWithTwoOutputs() {
      return makeRun({
        id: 'run-1',
        status: 'succeeded',
        outputs: [
          { output_index: 0, status: 'succeeded' },
          { output_index: 1, status: 'succeeded' },
        ],
      })
    }

    /**
     * A completed submitted run returns two distinct image Blobs through recovery.
     * The canvas bridge must receive both matching run and output identities after
     * their acknowledgement attempts.
     */
    it('收割成功（save + ack 后）经桥接把输出放上画布', async () => {
      const { studio } = await setupStudio()
      const blob0 = new Blob(['img-0'], { type: 'image/png' })
      const blob1 = new Blob(['img-1'], { type: 'image/png' })
      mockedApi.getCreativeRuns.mockResolvedValue({
        items: [succeededRunWithTwoOutputs()],
        total: 1,
      })
      mockedApi.getCreativeRunOutputContent.mockImplementation((_runId: string, index: number) =>
        Promise.resolve(index === 0 ? blob0 : blob1),
      )
      const bridge = { placeOutput: vi.fn(), importToCanvas: vi.fn() }
      studio.registerCanvasBridge(bridge)

      await studio.createRun({ sourceBlobs: [], maskBlob: null })
      await vi.advanceTimersByTimeAsync(3000)

      expect(mockedApi.ackCreativeRunOutput).toHaveBeenCalledTimes(2)
      expect(bridge.placeOutput).toHaveBeenCalledTimes(2)
      expect(bridge.placeOutput).toHaveBeenNthCalledWith(1, {
        blob: blob0,
        runId: 'run-1',
        outputIndex: 0,
      })
      expect(bridge.placeOutput).toHaveBeenNthCalledWith(2, {
        blob: blob1,
        runId: 'run-1',
        outputIndex: 1,
      })
      expect(studio.missingOutputKeys.value.size).toBe(0)
    })

    /**
     * The first asynchronous canvas placement is deliberately held pending.
     * Recovery must wait for its completion before invoking placement for the
     * second output in the same run.
     */
    it('多输出上板按顺序等待前一张完成，避免并发覆盖画布状态', async () => {
      const { studio } = await setupStudio()
      mockedApi.getCreativeRuns.mockResolvedValue({
        items: [succeededRunWithTwoOutputs()],
        total: 1,
      })

      let resolveFirst!: () => void
      /**
       * Hold the first bridge promise open so the assertion observes whether recovery
       * incorrectly starts the next output placement before its predecessor
       * completes.
       */
      const firstPlacementDone = new Promise<void>((resolve) => {
        resolveFirst = resolve
      })
      const started: number[] = []
      const bridge = {
        placeOutput: vi.fn(
          ({
            outputIndex,
          }: {
            outputIndex: number // zero-based output index
          }) => {
            started.push(outputIndex)
            return outputIndex === 0 ? firstPlacementDone : Promise.resolve()
          },
        ),
        importToCanvas: vi.fn(),
      }
      studio.registerCanvasBridge(bridge)

      await studio.createRun({ sourceBlobs: [], maskBlob: null })
      await vi.advanceTimersByTimeAsync(3000)

      expect(started).toEqual([0])
      resolveFirst()
      await vi.advanceTimersByTimeAsync(0)

      expect(started).toEqual([0, 1])
    })

    /**
     * The canvas bridge throws during output placement after bytes are saved.
     * Recovery must still acknowledge both outputs and leave their available assets
     * out of missing state.
     */
    it('桥接 placeOutput 抛异常不影响收割与 ack', async () => {
      const { studio } = await setupStudio()
      mockedApi.getCreativeRuns.mockResolvedValue({
        items: [succeededRunWithTwoOutputs()],
        total: 1,
      })
      const bridge = {
        placeOutput: vi.fn(() => {
          throw new Error('canvas not ready')
        }),
        importToCanvas: vi.fn(),
      }
      studio.registerCanvasBridge(bridge)

      await studio.createRun({ sourceBlobs: [], maskBlob: null })
      await vi.advanceTimersByTimeAsync(3000)

      expect(mockedApi.ackCreativeRunOutput).toHaveBeenCalledTimes(2)
      expect(studio.missingOutputKeys.value.size).toBe(0)
    })

    /**
     * History import is attempted with absent bytes, present bytes, and a throwing
     * bridge. Its boolean must reflect immediate dispatch availability and
     * synchronous failure for each case.
     */
    it('importOutputToCanvas 取本地素材委托桥接；素材缺失或桥接异常时返回 false', async () => {
      const { studio } = await setupStudio()

      const bridge = { placeOutput: vi.fn(), importToCanvas: vi.fn() }
      studio.registerCanvasBridge(bridge)
      expect(studio.importOutputToCanvas('run-9', 0)).toBe(false)
      expect(bridge.importToCanvas).not.toHaveBeenCalled()

      const blob = new Blob(['img'], { type: 'image/png' })
      studio.outputAssetMap.value = new Map([
        ['output:run-9:0', { key: 'output:run-9:0', kind: 'output', blob, createdAt: 1 }],
      ])
      expect(studio.importOutputToCanvas('run-9', 0)).toBe(true)
      expect(bridge.importToCanvas).toHaveBeenCalledWith(blob, 'run-9', 0)

      studio.registerCanvasBridge({
        placeOutput: vi.fn(),
        importToCanvas: () => {
          throw new Error('boom')
        },
      })
      expect(studio.importOutputToCanvas('run-9', 0)).toBe(false)
    })
  })

  describe('本地数据', () => {
    /**
     * Local clearing rejects with a disk error from the storage helper. The studio
     * must publish that message and rethrow so its view can report failure.
     */
    it('clearLocalData 失败时写入错误文案并向上抛出', async () => {
      const { studio } = await setupStudio()
      mockedStore.clearAll.mockRejectedValue(new Error('disk full'))

      await expect(studio.clearLocalData()).rejects.toThrow('disk full')
      expect(studio.error.value).toBe('disk full')
    })

    /**
     * The studio holds current and historical task state before local clearing
     * succeeds. It must clear those indexes, call storage clearing, and persist a
     * fresh workspace identity.
     */
    it('clearLocalData 调 clearAll 并重置内存状态', async () => {
      const { studio } = await setupStudio()
      studio.currentRun.value = makeRun({ id: 'run-1', status: 'succeeded' })
      studio.runHistory.value = [makeRun({ id: 'run-1', status: 'succeeded' })]
      const previousWorkspaceId = currentWorkspaceId()

      await studio.clearLocalData()

      expect(mockedStore.clearAll).toHaveBeenCalledTimes(1)
      expect(currentWorkspaceId()).toMatch(/^[0-9a-f-]{36}$/)
      expect(currentWorkspaceId()).not.toBe(previousWorkspaceId)
      expect(studio.currentRun.value).toBeNull()
      expect(studio.runHistory.value).toEqual([])
      expect(studio.missingOutputKeys.value.size).toBe(0)
    })

    /**
     * An output download completes after local data has been cleared and the
     * workspace invalidated. Its obsolete recovery must not save bytes or
     * repopulate cleared history.
     */
    it('清空期间完成的旧收割不会把素材重新写回', async () => {
      const { studio } = await setupStudio()
      const run = makeRun({
        id: 'run-clearing',
        status: 'succeeded',
        outputs: [{ output_index: 0, status: 'succeeded' }],
      })
      let resolveContent!: (blob: Blob) => void
      mockedApi.getCreativeRuns.mockResolvedValue({ items: [run], total: 1 })
      mockedApi.getCreativeRunOutputContent.mockReturnValue(
        new Promise((resolve) => {
          resolveContent = resolve
        }),
      )

      const refresh = studio.refreshHistory()
      await Promise.resolve()
      await Promise.resolve()
      await studio.clearLocalData()
      /**
       * Release the delayed image after clearing to reproduce obsolete recovery
       * resuming against a changed workspace generation and empty current history.
       */
      resolveContent(new Blob(['stale']))
      await refresh

      expect(mockedStore.saveAsset).not.toHaveBeenCalled()
      expect(studio.runHistory.value).toEqual([])
    })

    /**
     * Another same-origin tab stores a new workspace identifier and emits its
     * storage event. The studio must query that workspace and replace the visible
     * task list.
     */
    it('同源标签页收到工作区变化后重置并刷新历史', async () => {
      const { studio } = await setupStudio()
      await studio.refreshHistory()
      const previousWorkspaceId = currentWorkspaceId()
      const nextWorkspaceId = '22222222-2222-4222-8222-222222222222'
      mockedApi.getCreativeRuns.mockResolvedValue({
        items: [makeRun({ id: 'new-workspace-run', status: 'queued' })],
        total: 1,
      })

      localStorage.setItem(localStore.CREATIVE_WORKSPACE_STORAGE_KEY, nextWorkspaceId)
      window.dispatchEvent(
        new StorageEvent('storage', {
          key: localStore.CREATIVE_WORKSPACE_STORAGE_KEY,
          oldValue: previousWorkspaceId,
          newValue: nextWorkspaceId,
          storageArea: localStorage,
        }),
      )
      await Promise.resolve()
      await Promise.resolve()

      expect(studio.runHistory.value.map((run) => run.id)).toEqual(['new-workspace-run'])
      expect(mockedApi.getCreativeRuns).toHaveBeenLastCalledWith(nextWorkspaceId, 1, 20)
    })
  })

  describe('balance refresh', () => {
    /**
     * An accepted submission changes the tracked task list before polling begins.
     * The shared account refresh must run so the page header can update its
     * reserved funds.
     */
    it('refreshes the user after a successful submission', async () => {
      const { studio, wrapper } = await setupStudio()
      studio.prompt.value = 'An illustrated city'

      expect(await studio.createRun({ sourceBlobs: [], maskBlob: null })).toBe(true)
      await flushPromises()

      expect(refreshUser).toHaveBeenCalledTimes(1)
      wrapper.unmount()
    })

    /**
     * A tracked task changes from running to each supported terminal status.
     * Account refresh must run once for the change and stay unchanged on repeated
     * identical snapshots.
     */
    it.each(['succeeded', 'failed', 'cancelled', 'result_lost'] as const)(
      'refreshes the user when a tracked task becomes %s',
      async (status) => {
        const { studio, wrapper } = await setupStudio()
        mockedApi.getCreativeRuns.mockResolvedValue({
          items: [makeRun({ id: 'balance-run', status: 'running' })],
          total: 1,
        })
        await studio.refreshHistory()
        await flushPromises()
        refreshUser.mockClear()

        mockedApi.getCreativeRuns.mockResolvedValue({
          items: [makeRun({ id: 'balance-run', status })],
          total: 1,
        })
        await studio.refreshHistory()
        await flushPromises()
        expect(refreshUser).toHaveBeenCalledTimes(1)

        await studio.refreshHistory()
        await flushPromises()
        expect(refreshUser).toHaveBeenCalledTimes(1)
        wrapper.unmount()
      },
    )

    /**
     * An older active task disappears from the complete active listing outside
     * recent history. Its removal must refresh the shared account and leave polling
     * stopped.
     */
    it('refreshes when an older active task disappears', async () => {
      const { studio, wrapper } = await setupStudio()
      mockedApi.getCreativeActiveRuns.mockResolvedValueOnce({
        items: [makeRun({ id: 'older-run', status: 'release_pending' })],
        next_cursor: '',
        has_more: false,
      })
      await studio.refreshHistory()
      await flushPromises()
      refreshUser.mockClear()

      await studio.refreshHistory()
      await flushPromises()

      expect(refreshUser).toHaveBeenCalledTimes(1)
      expect(studio.polling.value).toBe(false)
      wrapper.unmount()
    })

    /**
     * A settlement status arrives while the reservation-triggered account read
     * remains pending. The second account refresh must wait until the first
     * finishes before starting its own read.
     */
    it('serializes a settlement refresh behind a pending reservation refresh', async () => {
      const { studio, wrapper } = await setupStudio()
      let resolveReservation!: () => void
      refreshUser.mockImplementationOnce(
        () =>
          new Promise<void>((resolve) => {
            resolveReservation = resolve
          }),
      )
      studio.prompt.value = 'An illustrated city'
      await studio.createRun({ sourceBlobs: [], maskBlob: null })
      await flushPromises()
      mockedApi.getCreativeRuns.mockResolvedValue({
        items: [makeRun({ id: 'run-1', status: 'succeeded' })],
        total: 1,
      })

      await studio.refreshHistory()
      await flushPromises()
      expect(refreshUser).toHaveBeenCalledTimes(1)
      resolveReservation()
      await flushPromises()

      expect(refreshUser).toHaveBeenCalledTimes(2)
      wrapper.unmount()
    })

    /**
     * A terminal task's image download is delayed after its status becomes visible.
     * The shared account refresh must start before bytes are saved, allowing
     * funding updates independently of recovery.
     */
    it('refreshes settled funds before the output download completes', async () => {
      const { studio, wrapper } = await setupStudio()
      let resolveOutput!: (blob: Blob) => void
      mockedApi.getCreativeRunOutputContent.mockImplementationOnce(
        () =>
          new Promise<Blob>((resolve) => {
            resolveOutput = resolve
          }),
      )
      mockedApi.getCreativeRuns.mockResolvedValue({
        items: [
          makeRun({
            id: 'download-run',
            status: 'succeeded',
            outputs: [{ output_index: 0, status: 'succeeded' }],
          }),
        ],
        total: 1,
      })

      const historyRefresh = studio.refreshHistory()
      await flushPromises()

      expect(refreshUser).toHaveBeenCalledTimes(1)
      expect(mockedStore.saveAsset).not.toHaveBeenCalled()
      resolveOutput(new Blob(['image'], { type: 'image/png' }))
      await historyRefresh
      expect(mockedApi.ackCreativeRunOutput).toHaveBeenCalled()
      wrapper.unmount()
    })

    /**
     * The account read fails after task creation succeeds. Generation must remain
     * accepted, log the account error, and permit a later terminal change to
     * trigger another refresh.
     */
    it('continues generation and later refreshes after an account read fails', async () => {
      const { studio, wrapper } = await setupStudio()
      const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {})
      refreshUser.mockRejectedValueOnce(new Error('Network unavailable'))
      studio.prompt.value = 'An illustrated city'

      expect(await studio.createRun({ sourceBlobs: [], maskBlob: null })).toBe(true)
      await flushPromises()
      expect(studio.error.value).toBe('')
      expect(consoleError).toHaveBeenCalledWith(
        'Failed to refresh creative balance:',
        expect.any(Error),
      )

      mockedApi.getCreativeRuns.mockResolvedValue({
        items: [makeRun({ id: 'run-1', status: 'failed' })],
        total: 1,
      })
      await studio.refreshHistory()
      await flushPromises()

      expect(refreshUser).toHaveBeenCalledTimes(2)
      expect(studio.polling.value).toBe(false)
      wrapper.unmount()
    })
  })

  describe('refreshHistory', () => {
    /**
     * The recent history page omits an older task still awaiting settlement.
     * Active-list cursor traversal must include that task and keep shared polling
     * active after merging both sources.
     */
    it('通过 active runs 游标接管不在最近历史页的任务', async () => {
      const { studio } = await setupStudio()
      const recent = makeRun({ id: 'recent-run', status: 'succeeded' })
      const olderActive = makeRun({ id: 'older-active', status: 'settlement_pending', outputs: [] })
      mockedApi.getCreativeRuns.mockResolvedValue({ items: [recent], total: 21 })
      mockedApi.getCreativeActiveRuns
        .mockResolvedValueOnce({ items: [olderActive], next_cursor: 'next', has_more: true })
        .mockResolvedValueOnce({ items: [], next_cursor: '', has_more: false })

      await studio.refreshHistory()

      expect(studio.runHistory.value.map((run) => run.id)).toEqual(['recent-run', 'older-active'])
      expect(studio.polling.value).toBe(true)
      expect(mockedApi.getCreativeActiveRuns).toHaveBeenNthCalledWith(
        1,
        currentWorkspaceId(),
        undefined,
        100,
      )
      expect(mockedApi.getCreativeActiveRuns).toHaveBeenNthCalledWith(
        2,
        currentWorkspaceId(),
        'next',
        100,
      )
    })

    /**
     * Workspace localStorage access throws before history can be queried. The
     * studio must keep the list empty, report workspace-unavailable feedback, and
     * avoid the server history request.
     */
    it('工作区本地存储不可用时不回退到共享历史', async () => {
      const { studio } = await setupStudio()
      vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
        throw new DOMException('blocked', 'SecurityError')
      })

      await studio.refreshHistory()

      expect(mockedApi.getCreativeRuns).not.toHaveBeenCalled()
      expect(studio.runHistory.value).toEqual([])
      expect(studio.error.value).toBe('creative.error.workspaceUnavailable')
    })

    /**
     * Server history includes tasks with very old creation timestamps. The studio
     * must publish both records for its current workspace without applying a
     * browser-time cutoff.
     */
    it('不再按本机时间水位线过滤服务端历史', async () => {
      const { studio } = await setupStudio()
      mockedApi.getCreativeRuns.mockResolvedValue({
        items: [
          makeRun({ id: 'old-run', status: 'succeeded', created_at: 1 }),
          makeRun({ id: 'new-run', status: 'succeeded', created_at: 2 }),
        ],
        total: 2,
      })

      await studio.refreshHistory()

      expect(studio.runHistory.value.map((r) => r.id)).toEqual(['old-run', 'new-run'])
      expect(mockedApi.getCreativeRuns).toHaveBeenCalledWith(currentWorkspaceId(), 1, 20)
    })

    /**
     * History exposes two unacknowledged outputs while one already exists locally.
     * Refresh must reuse the local image, download the other, and publish both
     * without missing placeholders.
     */
    it('历史刷新自动恢复未 ack 的服务端输出并关联本地素材', async () => {
      const { studio } = await setupStudio()
      const run = makeRun({
        id: 'run-7',
        status: 'succeeded',
        outputs: [
          { output_index: 0, status: 'succeeded' },
          { output_index: 1, status: 'succeeded' },
        ],
      })
      mockedApi.getCreativeRuns.mockResolvedValue({ items: [run], total: 1 })
      const localOutput = {
        key: 'output:run-7:0',
        kind: 'output' as const,
        blob: new Blob(['img']),
        runId: 'run-7',
        outputIndex: 0,
        createdAt: 1,
      }
      mockedStore.listAssets.mockImplementation((kind: string) =>
        Promise.resolve(kind === 'output' ? [localOutput] : []),
      )

      await studio.refreshHistory()

      expect(studio.runHistory.value.map((r) => r.id)).toEqual(['run-7'])
      expect(studio.outputAssetMap.value.get('output:run-7:0')).toEqual(localOutput)
      expect(studio.outputAssetMap.value.has('output:run-7:1')).toBe(true)
      expect(studio.missingOutputKeys.value.has('output:run-7:1')).toBe(false)
      expect(studio.missingOutputKeys.value.has('output:run-7:0')).toBe(false)
      expect(mockedApi.getCreativeRunOutputContent).toHaveBeenCalledWith(
        'run-7',
        1,
        currentWorkspaceId(),
      )
      expect(mockedApi.ackCreativeRunOutput).toHaveBeenCalledWith('run-7', 1, currentWorkspaceId())
    })

    /**
     * A successful historical task has one unacknowledged transient output and no
     * local bytes. Refresh must download and save that image, acknowledge it, and
     * expose it locally.
     */
    it('历史刷新自动收割尚未 ack 的服务端输出', async () => {
      const { studio } = await setupStudio()
      const run = makeRun({
        id: 'run-history-recover',
        status: 'succeeded',
        outputs: [{ output_index: 0, status: 'succeeded' }],
      })
      const blob = new Blob(['history-image'], { type: 'image/png' })
      mockedApi.getCreativeRuns.mockResolvedValue({ items: [run], total: 1 })
      mockedApi.getCreativeRunOutputContent.mockResolvedValue(blob)

      await studio.refreshHistory()

      expect(mockedApi.getCreativeRunOutputContent).toHaveBeenCalledWith(
        'run-history-recover',
        0,
        currentWorkspaceId(),
      )
      expect(mockedStore.saveAsset).toHaveBeenCalledWith(
        expect.objectContaining({
          key: 'output:run-history-recover:0',
          kind: 'output',
          blob,
        }),
      )
      expect(mockedApi.ackCreativeRunOutput).toHaveBeenCalledWith(
        'run-history-recover',
        0,
        currentWorkspaceId(),
      )
      expect(studio.outputAssetMap.value.get('output:run-history-recover:0')?.blob).toBe(blob)
      expect(studio.missingOutputKeys.value.has('output:run-history-recover:0')).toBe(false)
    })

    /**
     * A historical output exists in the local asset store but remains
     * unacknowledged. Refresh must retain its bytes and retry acknowledgement
     * without making another download request.
     */
    it('本地已有未 ack 素材时只重试 ack，不重复下载', async () => {
      const { studio } = await setupStudio()
      const run = makeRun({
        id: 'run-local-recover',
        status: 'succeeded',
        outputs: [{ output_index: 0, status: 'succeeded' }],
      })
      const localOutput = {
        key: 'output:run-local-recover:0',
        kind: 'output' as const,
        blob: new Blob(['local-image']),
        runId: 'run-local-recover',
        outputIndex: 0,
        createdAt: 1,
      }
      mockedApi.getCreativeRuns.mockResolvedValue({ items: [run], total: 1 })
      mockedStore.listAssets.mockImplementation((kind: string) =>
        Promise.resolve(kind === 'output' ? [localOutput] : []),
      )

      await studio.refreshHistory()

      expect(mockedApi.getCreativeRunOutputContent).not.toHaveBeenCalled()
      expect(mockedApi.ackCreativeRunOutput).toHaveBeenCalledWith(
        'run-local-recover',
        0,
        currentWorkspaceId(),
      )
      expect(studio.outputAssetMap.value.get(localOutput.key)).toEqual(localOutput)
      expect(studio.missingOutputKeys.value.has(localOutput.key)).toBe(false)
    })

    /**
     * Local saving succeeds before the output acknowledgement request fails. The
     * recovered asset must remain visible and must not enter the missing-output set
     * because acknowledgement failed.
     */
    it('本地保存成功但 ack 失败时仍保留素材，不标记 missing', async () => {
      const { studio } = await setupStudio()
      const run = makeRun({
        id: 'run-ack-retry',
        status: 'succeeded',
        outputs: [{ output_index: 0, status: 'succeeded' }],
      })
      mockedApi.getCreativeRuns.mockResolvedValue({ items: [run], total: 1 })
      mockedApi.ackCreativeRunOutput.mockRejectedValue(new Error('ack network error'))

      await studio.refreshHistory()

      expect(mockedStore.saveAsset).toHaveBeenCalledTimes(1)
      expect(studio.outputAssetMap.value.has('output:run-ack-retry:0')).toBe(true)
      expect(studio.missingOutputKeys.value.has('output:run-ack-retry:0')).toBe(false)
    })

    /**
     * A successful output is acknowledged but its browser-local copy is absent.
     * Refresh must mark it missing without retrying either content download or
     * server acknowledgement.
     */
    it('已 ack 且本地无素材时不尝试从服务端恢复', async () => {
      const { studio } = await setupStudio()
      const run = makeRun({
        id: 'run-acked-missing',
        status: 'succeeded',
        outputs: [{ output_index: 0, status: 'succeeded', acked_at: 1725000000 }],
      })
      mockedApi.getCreativeRuns.mockResolvedValue({ items: [run], total: 1 })

      await studio.refreshHistory()

      expect(mockedApi.getCreativeRunOutputContent).not.toHaveBeenCalled()
      expect(mockedApi.ackCreativeRunOutput).not.toHaveBeenCalled()
      expect(studio.missingOutputKeys.value.has('output:run-acked-missing:0')).toBe(true)
    })

    /**
     * Two history requests overlap and the older response arrives last. Only the
     * newer task list and its recovered asset may remain published after both
     * requests finish.
     */
    it('并发历史刷新只接受最新请求，旧响应不能覆盖新列表', async () => {
      const { studio } = await setupStudio()
      const oldRun = makeRun({ id: 'run-old', status: 'succeeded' })
      const newRun = makeRun({
        id: 'run-new',
        status: 'succeeded',
        outputs: [{ output_index: 0, status: 'succeeded' }],
      })
      let resolveOld!: (page: {
        items: CreativeRun[] // returned server task records
        total: number // server task record count
      }) => void
      mockedApi.getCreativeRuns
        .mockImplementationOnce(
          () =>
            new Promise((resolve) => {
              resolveOld = resolve
            }),
        )
        .mockResolvedValueOnce({ items: [newRun], total: 1 })

      const oldRefresh = studio.refreshHistory()
      await Promise.resolve()
      const newRefresh = studio.refreshHistory()
      await newRefresh

      /**
       * Complete the older request last so generation checks must preserve the newer
       * published list and recovered asset despite reversed response order.
       */
      resolveOld({ items: [oldRun], total: 1 })
      await oldRefresh

      expect(studio.runHistory.value.map((run) => run.id)).toEqual(['run-new'])
      expect(studio.outputAssetMap.value.has('output:run-new:0')).toBe(true)
    })
  })
})
