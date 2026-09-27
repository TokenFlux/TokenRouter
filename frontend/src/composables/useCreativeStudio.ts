/**
 * 创作台任务与素材状态
 * 管理模型目录和参数选择，保存并恢复参数，创建 run（支持幂等重试）。
 * 轮询任务输出，将历史记录关联到本地素材，并调用视图提供的画布操作。
 * 点击生成时，视图从画布收集源图和 mask（选中的图片与画笔 mask）。
 * 轮询定时器在 composable 内注册 onBeforeUnmount 清理。
 */

import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  CREATIVE_RUN_TERMINAL_STATUSES,
  createCreativeRun,
  getCreativeActiveRuns,
  getCreativeCapabilities,
  getCreativeModels,
  getCreativeRunOutputContent,
  getCreativeRuns,
  ackCreativeRunOutput,
  type CreativeModelOption,
  type CreativeCapabilities,
  type CreativeOperation,
  type CreativeRun,
} from '@/api/creative'
import {
  LocalStoreError,
  LocalStoreQuotaError,
  CREATIVE_WORKSPACE_STORAGE_KEY,
  clearAll,
  getCreativeWorkspaceId,
  listAssets,
  loadSetting,
  outputAssetKey,
  rotateCreativeWorkspaceId,
  saveAsset,
  saveSetting,
  type LocalAsset,
} from '@/utils/creativeLocalStore'
import { loadRunInputSnapshot, saveRunInputSnapshot } from '@/utils/creativeRunInputs'

// 生成参数来源：视图在点击生成时从画布收集（选中的源图 + 画笔 mask）
export interface CreativeExportInput {
  sourceBlobs: Blob[]
  maskBlob: Blob | null
}

// 上次选择持久化的结构
interface CreativeSelectionSettings {
  optionKey: string
  operation: string
  imageSize: string
  aspectRatio: string
  quality: string
  background: string
  thinkingLevel: string
  prompt: string
}

const SETTINGS_KEY = 'creative:selection'
const DEFAULT_CREATIVE_CAPABILITIES: CreativeCapabilities = {
  max_prompt_chars: 8000,
  max_asset_bytes: 33554432,
  max_total_input_bytes: 67108864,
  max_mask_bytes: 4194304,
  allowed_mime_types: ['image/png', 'image/jpeg', 'image/webp'],
}
const SETTINGS_SAVE_DEBOUNCE = 300

function truncatePrompt(value: string, maxChars = DEFAULT_CREATIVE_CAPABILITIES.max_prompt_chars): string {
  return Array.from(value).slice(0, maxChars).join('')
}

// 所有进行中任务共享一次列表轮询，生图通常耗时 1 到 3 分钟。
const POLL_INTERVAL = 3000

// 画布桥接：视图注册后，收割成功的输出自动放上画布，历史里的输出可一键导入画布
export interface CreativeCanvasBridge {
  // 收割成功（save + ack 后）把输出图片放到画布
  // 返回 Promise 时，等待当前图片放上画布后再处理下一张，按顺序分配画布位置。
  placeOutput(asset: { blob: Blob; runId: string; outputIndex: number }): void | Promise<void>
  // 把历史里的本地输出素材放到画布（与自动上板同一入口）
  importToCanvas(blob: Blob, runId: string, outputIndex: number): void | Promise<void>
}

// 画布上当前操作缺少的输入：局部重绘未选图、图生图未选参考图、局部重绘尚未涂抹。
// 画布计算这个值，输入框状态行按它显示引导文案。
export type CreativeCanvasGuide = 'inpaintPick' | 'editPick' | 'maskPaint'

// group + model 合成选项 key
export function creativeOptionKey(option: Pick<CreativeModelOption, 'group_id' | 'model'>): string {
  return `${option.group_id}::${option.model}`
}

export function useCreativeStudio() {
  const { t } = useI18n()

  // ==================== 状态 ====================

  const models = ref<CreativeModelOption[]>([])
  const capabilities = ref<CreativeCapabilities>({ ...DEFAULT_CREATIVE_CAPABILITIES })
  const loadingModels = ref(false)
  const selectedOptionKey = ref('')
  const operation = ref<CreativeOperation>('generate')
  const prompt = ref('')
  const imageSize = ref('')
  const aspectRatio = ref('auto')
  // 模型级画质档位，支持时默认使用 medium。
  const quality = ref('medium')
  const background = ref('auto')
  const thinkingLevel = ref('minimal')
  const currentRun = ref<CreativeRun | null>(null)
  const runHistory = ref<CreativeRun[]>([])
  const loadingHistory = ref(false)
  const polling = ref(false)
  const busy = ref(false)
  const error = ref('')
  // 服务端 succeeded 但本地缺失 blob 的输出 key 集合（runId:index 不展示素材）
  const missingOutputKeys = ref<Set<string>>(new Set())
  // 本地输出素材索引：outputAssetKey → asset
  const outputAssetMap = ref<Map<string, LocalAsset>>(new Map())
  // 当前表单提交意图的幂等键：失败后重试复用，成功后重置
  const activeIdempotencyKey = ref('')
  // 画布桥接实例（视图在挂载时注册，卸载时可传 null 解绑）
  let canvasBridge: CreativeCanvasBridge | null = null
  // 浏览器工作区代际：清空或其它标签页旋转工作区后，旧异步请求不得回写状态。
  let workspaceGeneration = 0
  let workspaceId: string | null = null
  // 设置恢复完成后才写入默认值，已有记录先从存储恢复。
  let settingsHydrated = false
  let settingsSaveTimer: ReturnType<typeof setTimeout> | null = null
  let settingsRevision = 0
  let settingsDirty = false
  // 设置写入串行化，保证快速输入时最后一次快照不会被旧写入覆盖
  let settingsWriteChain: Promise<void> = Promise.resolve()

  // 本地身份不可用时，清除当前工作区的页面状态并停止轮询。
  function resetWorkspaceState(): void {
    stopPolling()
    clearPollingTimer()
    currentRun.value = null
    runHistory.value = []
    outputAssetMap.value = new Map()
    missingOutputKeys.value = new Set()
    activeIdempotencyKey.value = ''
  }

  // ==================== 计算属性 ====================

  const selectedOption = computed(
    () => models.value.find((m) => creativeOptionKey(m) === selectedOptionKey.value) ?? null,
  )

  const operationOptions = computed(() => selectedOption.value?.operations ?? [])

  const imageSizeOptions = computed(() => selectedOption.value?.image_sizes ?? [])

  const aspectRatioOptions = computed(() => selectedOption.value?.aspect_ratios ?? [])

  // 可选画质档位（模型不支持时为空，参数面板隐藏画质行）
  const qualityOptions = computed(() => selectedOption.value?.qualities ?? [])

  const backgroundOptions = computed(() => selectedOption.value?.background_options ?? [])

  const thinkingLevelOptions = computed(() => selectedOption.value?.thinking_levels ?? [])

  const maxReferenceImages = computed(() => Math.max(1, selectedOption.value?.max_reference_images ?? 1))

  // 估算费用使用模型目录返回的档位价格，与模型广场相同。
  const estimatedCost = computed(() => {
    const option = selectedOption.value
    if (!option) return null
    let unitPrice: number
    switch (imageSize.value) {
      case '512':
        unitPrice = option.price_512 ?? option.price_1k
        break
      case '2K':
        unitPrice = option.price_2k
        break
      case '4K':
        unitPrice = option.price_4k
        break
      default:
        unitPrice = option.price_1k
    }
    return unitPrice
  })

  const canGenerate = computed(() => {
    if (!selectedOption.value || busy.value) return false
    if (!operationOptions.value.includes(operation.value)) return false
    // 提示词为空（纯空白）时禁止提交
    if (prompt.value.trim().length === 0) return false
    if (Array.from(prompt.value).length > capabilities.value.max_prompt_chars) return false
    // 源图 / mask 由画布在点击生成时即时收集，这里不做前置拦截
    return true
  })

  // ==================== 模型目录与选择恢复 ====================

  async function loadModels(): Promise<void> {
    if (loadingModels.value) return
    loadingModels.value = true
    try {
      models.value = await getCreativeModels()
      if (typeof getCreativeCapabilities === 'function') {
        try {
          capabilities.value = await getCreativeCapabilities()
        } catch (capabilityError) {
          console.error('Failed to load creative capabilities:', capabilityError)
        }
      }
      await restoreSettings()
    } catch (e) {
      console.error('Failed to load creative models:', e)
      error.value = t('creative.error.loadModelsFailed')
    } finally {
      loadingModels.value = false
    }
  }

  // 从 settings 恢复上次选择；模型已下线时静默回退默认
  async function restoreSettings(): Promise<void> {
    try {
      const saved = await loadSetting<CreativeSelectionSettings>(SETTINGS_KEY)
      if (!saved) return
      if (models.value.some((m) => creativeOptionKey(m) === saved.optionKey)) {
        selectedOptionKey.value = saved.optionKey
      }
      if (saved.operation) operation.value = saved.operation
      if (saved.imageSize) imageSize.value = saved.imageSize
      if (saved.aspectRatio) aspectRatio.value = saved.aspectRatio
      if (typeof saved.quality === 'string') quality.value = saved.quality
      if (typeof saved.background === 'string') background.value = saved.background
      if (typeof saved.thinkingLevel === 'string') thinkingLevel.value = saved.thinkingLevel
      if (typeof saved.prompt === 'string') prompt.value = truncatePrompt(saved.prompt, capabilities.value.max_prompt_chars)
      normalizeSelection()
    } catch (e) {
      console.error('Failed to restore creative settings:', e)
    } finally {
      settingsHydrated = true
      scheduleSelectionSettingsSave()
    }
  }

  // 选择变更后兜底：所有参数必须落在服务端返回的模型能力范围内。
  function normalizeSelection(): void {
    const option = selectedOption.value
    if (!option) return
    if (!option.operations.includes(operation.value)) {
      operation.value = option.operations[0] ?? 'generate'
    }
    if (!option.image_sizes.includes(imageSize.value)) {
      imageSize.value = option.image_sizes.includes('1K') ? '1K' : (option.image_sizes[0] ?? '')
    }
    const aspectRatios = option.aspect_ratios ?? []
    if (!aspectRatios.includes(aspectRatio.value)) {
      aspectRatio.value = aspectRatios.includes('auto') ? 'auto' : (aspectRatios[0] ?? '')
    }
    const qualities = option.qualities ?? []
    if (qualities.length === 0) {
      quality.value = ''
    } else if (!qualities.includes(quality.value)) {
      quality.value = qualities.includes('medium') ? 'medium' : (qualities[0] ?? '')
    }
    const backgrounds = backgroundOptions.value
    if (backgrounds.length === 0) {
      background.value = ''
    } else if (!backgrounds.includes(background.value)) {
      background.value = backgrounds.includes('auto') ? 'auto' : (backgrounds[0] ?? '')
    }
    const thinkingLevels = option.thinking_levels ?? []
    if (thinkingLevels.length === 0) {
      thinkingLevel.value = ''
    } else if (!thinkingLevels.includes(thinkingLevel.value)) {
      thinkingLevel.value = thinkingLevels.includes('minimal') ? 'minimal' : (thinkingLevels[0] ?? '')
    }
  }

  function selectOption(key: string): void {
    selectedOptionKey.value = key
    normalizeSelection()
  }

  function selectionSettingsSnapshot(): CreativeSelectionSettings {
    return {
      optionKey: selectedOptionKey.value,
      operation: operation.value,
      imageSize: imageSize.value,
      aspectRatio: aspectRatio.value,
      quality: quality.value,
      background: background.value,
      thinkingLevel: thinkingLevel.value,
      prompt: truncatePrompt(prompt.value, capabilities.value.max_prompt_chars),
    }
  }

  // 设置变化后防抖保存，将连续输入提示词产生的 IndexedDB 写入合并。
  function scheduleSelectionSettingsSave(): void {
    if (!settingsHydrated) return
    settingsDirty = true
    settingsRevision++
    if (settingsSaveTimer) clearTimeout(settingsSaveTimer)
    settingsSaveTimer = setTimeout(() => {
      settingsSaveTimer = null
      void persistSelectionSettings()
    }, SETTINGS_SAVE_DEBOUNCE)
  }

  function flushSelectionSettingsSave(): void {
    if (settingsSaveTimer) clearTimeout(settingsSaveTimer)
    settingsSaveTimer = null
    void persistSelectionSettings()
  }

  function persistSelectionSettings(): Promise<void> {
    if (!settingsHydrated || !settingsDirty) return settingsWriteChain
    const snapshot = selectionSettingsSnapshot()
    const revision = settingsRevision
    settingsWriteChain = settingsWriteChain
      .catch(() => undefined)
      .then(() => saveSetting(SETTINGS_KEY, snapshot))
      .then(() => {
        if (settingsRevision === revision) settingsDirty = false
      })
      .catch((error) => {
        console.error('Failed to persist creative settings:', error)
      })
    return settingsWriteChain
  }

  // 参数变化持久化，下次进入恢复
  watch(
    [selectedOptionKey, operation, imageSize, aspectRatio, quality, background, thinkingLevel, prompt],
    scheduleSelectionSettingsSave,
    // 使用同步 watcher 先记录脏状态，确保 pagehide 触发时能拿到最新提示词。
    { flush: 'sync' },
  )

  // ==================== 画布桥接 ====================

  // 视图挂载画布后注册桥接；传入 null 解绑（组件卸载时）
  function registerCanvasBridge(bridge: CreativeCanvasBridge | null): void {
    canvasBridge = bridge
  }

  // 读取当前浏览器工作区；工作区变化时立即丢弃旧页面状态。
  function readWorkspaceId(): string {
    const next = getCreativeWorkspaceId()
    if (workspaceId && workspaceId !== next) {
      workspaceGeneration++
      // 工作区变化时使进行中的历史请求失效，以新工作区的查询结果更新状态。
      historyRefreshGeneration++
      resetWorkspaceState()
    }
    workspaceId = next
    return next
  }

  function isWorkspaceCurrent(id: string, generation: number): boolean {
    return workspaceId === id && workspaceGeneration === generation
  }

  // 同源标签页清空数据后会通过 storage 事件切换到新工作区。
  function onWorkspaceStorage(event: StorageEvent): void {
    // key 为 null 表示 storage.clear()，同样需要重新建立工作区。
    if (event.key !== CREATIVE_WORKSPACE_STORAGE_KEY && event.key !== null) return
    const previous = workspaceId
    try {
      const next = readWorkspaceId()
      if (next === previous) return
      void refreshHistory()
    } catch (cause) {
      if (cause instanceof LocalStoreError && cause.type === 'unavailable') {
        workspaceId = null
        resetWorkspaceState()
      }
      error.value = creativeErrorMessage(cause, 'creative.error.historyFailed')
    }
  }

  if (typeof window !== 'undefined') {
    window.addEventListener('storage', onWorkspaceStorage)
    window.addEventListener('pagehide', onPageHide)
    document.addEventListener('visibilitychange', onVisibilityChange)
  }

  // 页面进入后台时刷新当前表单草稿，降低直接关闭浏览器造成的最后一次输入丢失概率。
  function onPageHide(): void {
    flushSelectionSettingsSave()
  }

  function onVisibilityChange(): void {
    if (document.visibilityState === 'hidden') flushSelectionSettingsSave()
  }

  // 历史里的输出导入画布：取当前工作区的本地素材调用画布桥接；素材缺失或画布未就绪时返回 false
  function importOutputToCanvas(runId: string, outputIndex: number): boolean {
    const asset = outputAssetMap.value.get(outputAssetKey(runId, outputIndex))
    if (!asset || !canvasBridge) return false
    try {
      canvasBridge.importToCanvas(asset.blob, runId, outputIndex)
      return true
    } catch (e) {
      console.error('Failed to import creative output to canvas:', e)
      return false
    }
  }

  // ==================== 创建 run ====================

  async function createRun(exported: CreativeExportInput): Promise<boolean> {
    if (busy.value) return false
    const option = selectedOption.value
    if (!option) {
      error.value = t('creative.error.noModel')
      return false
    }
    if (!option.operations.includes(operation.value)) {
      error.value = t('creative.error.operationNotSupported')
      return false
    }
    if (Array.from(prompt.value).length > capabilities.value.max_prompt_chars) {
      error.value = t('creative.error.promptTooLong')
      return false
    }
    if ((operation.value === 'edit' || operation.value === 'inpaint') && exported.sourceBlobs.length === 0) {
      error.value = t('creative.error.sourceRequired')
      return false
    }
    if (operation.value === 'inpaint' && !exported.maskBlob) {
      error.value = t('creative.error.maskRequired')
      return false
    }
    if (exported.sourceBlobs.length > maxReferenceImages.value) {
      error.value = t('creative.error.referenceLimit', { max: maxReferenceImages.value })
      return false
    }
    const allowedMIMEs = new Set(capabilities.value.allowed_mime_types)
    const totalInputBytes = exported.sourceBlobs.reduce((sum, blob) => sum + blob.size, 0) + (exported.maskBlob?.size ?? 0)
    if (totalInputBytes > capabilities.value.max_total_input_bytes) {
      error.value = t('creative.error.assetTooLarge')
      return false
    }
    for (const blob of exported.sourceBlobs) {
      if (blob.size > capabilities.value.max_asset_bytes || (blob.type && !allowedMIMEs.has(blob.type))) {
        error.value = t('creative.error.assetTooLarge')
        return false
      }
    }
    if (exported.maskBlob && (exported.maskBlob.size > capabilities.value.max_mask_bytes || exported.maskBlob.type !== 'image/png')) {
      error.value = t('creative.error.assetTooLarge')
      return false
    }

    busy.value = true
    error.value = ''
    try {
      const requestWorkspaceId = readWorkspaceId()
      const requestGeneration = workspaceGeneration
      // 同一表单提交意图复用同一幂等键，直到成功
      if (!activeIdempotencyKey.value) {
        activeIdempotencyKey.value = crypto.randomUUID()
      }
      const form = new FormData()
      form.append('group_id', option.group_id)
      form.append('model', option.model)
      form.append('operation', operation.value)
      form.append('prompt', prompt.value)
      form.append('image_size', imageSize.value)
      form.append('aspect_ratio', aspectRatio.value)
      if (quality.value) {
        form.append('quality', quality.value)
      }
      if (background.value) {
        form.append('background', background.value)
      }
      if (thinkingLevel.value) {
        form.append('thinking_level', thinkingLevel.value)
      }
      exported.sourceBlobs.forEach((blob, index) => {
        form.append('source_images[]', blob, `source-${index}.png`)
      })
      if (exported.maskBlob) {
        form.append('mask', exported.maskBlob, 'mask.png')
      }

      const run = await createCreativeRun(form, requestWorkspaceId, activeIdempotencyKey.value)
      // 提交成功，重置幂等键；失败重试时保留
      activeIdempotencyKey.value = ''
      if (!isWorkspaceCurrent(requestWorkspaceId, requestGeneration)) return true
      currentRun.value = run
      upsertRunInHistory(run)
      // 服务端只保存 prompt 的 sha256，失败重试必须靠本地快照取回提示词与参数。
      void saveRunInputSnapshot({
        runId: run.id,
        prompt: prompt.value,
        operation: operation.value,
        model: option.model,
        groupId: option.group_id,
        imageSize: imageSize.value,
        aspectRatio: aspectRatio.value,
        quality: quality.value,
        background: background.value,
        thinkingLevel: thinkingLevel.value,
        createdAt: Date.now(),
      })
      startPolling(run.id, { placeOnCanvas: true })
      return true
    } catch (e) {
      error.value = creativeErrorMessage(e, 'creative.error.submitFailed')
      return false
    } finally {
      busy.value = false
    }
  }

  // 用本地快照还原失败任务的输入区（提示词 + 参数）；返回 false 表示本机没有该次提交记录。
  // 源图 / mask 仍由画布在点击生成时重新采集，因此恢复后可直接重试。
  async function restoreRunInput(runId: string): Promise<boolean> {
    const snapshot = await loadRunInputSnapshot(runId)
    if (!snapshot) {
      error.value = t('creative.error.retryUnavailable')
      return false
    }
    const option = models.value.find(
      (item) => creativeOptionKey(item) === `${snapshot.groupId}::${snapshot.model}`,
    )
    if (!option) {
      error.value = t('creative.error.retryModelUnavailable')
      return false
    }
    selectedOptionKey.value = creativeOptionKey(option)
    operation.value = snapshot.operation as CreativeOperation
    imageSize.value = snapshot.imageSize
    aspectRatio.value = snapshot.aspectRatio
    quality.value = snapshot.quality
    background.value = snapshot.background
    thinkingLevel.value = snapshot.thinkingLevel
    prompt.value = truncatePrompt(snapshot.prompt, capabilities.value.max_prompt_chars)
    error.value = ''
    return true
  }

  // ==================== 轮询与输出收割 ====================

  interface PollState {
    // 由当前页面创建的任务完成后自动上板；历史恢复的任务只收割到本地。
    placeOnCanvas: boolean
  }

  // 只记录需要追踪的 run 与上板意图，实际状态由同一次列表请求批量同步。
  const pollStates = new Map<string, PollState>()
  let pollTimer: ReturnType<typeof setTimeout> | null = null
  // 所有输出收集流程共用队列，按顺序下载并更新本地索引。
  let harvestQueue: Promise<void> = Promise.resolve()
  // 历史刷新使用最新请求的结果更新任务与本地素材索引。
  let historyRefreshGeneration = 0

  function updatePollingState(): void {
    polling.value = pollStates.size > 0
  }

  function stopPolling(runId?: string): void {
    if (runId) {
      pollStates.delete(runId)
    } else {
      pollStates.clear()
    }
    updatePollingState()
  }

  function clearPollingTimer(): void {
    if (!pollTimer) return
    clearTimeout(pollTimer)
    pollTimer = null
  }

  function schedulePollingRefresh(): void {
    if (pollTimer || pollStates.size === 0) return
    pollTimer = setTimeout(() => {
      pollTimer = null
      if (pollStates.size === 0) return
      void refreshHistory()
    }, POLL_INTERVAL)
  }

  function startPolling(runId: string, options: { placeOnCanvas?: boolean } = {}): void {
    const existing = pollStates.get(runId)
    pollStates.set(runId, {
      placeOnCanvas: Boolean(existing?.placeOnCanvas || options.placeOnCanvas),
    })
    updatePollingState()
    schedulePollingRefresh()
  }

  interface HarvestOptions {
    // 当前任务完成时自动放上画布，历史恢复时保存到本地。
    placeOnCanvas?: boolean
    // 历史刷新在工作副本中整理数据，请求完成后再合并索引。
    assets?: Map<string, LocalAsset>
    missing?: Set<string>
    isCurrent?: () => boolean
    workspaceId?: string
    workspaceGeneration?: number
  }

  // 终态 succeeded：逐个取回未 ack 的输出 → 存本地 → ack → 可选放上画布。
  // 历史刷新也复用该流程，因此页面重新进入时仍能收割尚未 ack 的 transient 输出。
  function harvestOutputs(run: CreativeRun, options: HarvestOptions = {}): Promise<void> {
    const task = harvestQueue.then(() => harvestOutputsNow(run, options))
    // 队列本身不能被单个任务失败阻塞，具体错误已在 harvestOutputsNow 内按输出隔离。
    harvestQueue = task.catch(() => undefined)
    return task
  }

  async function fetchOutputWithRetry(runId: string, outputIndex: number, workspaceId: string): Promise<Blob> {
    const maxAttempts = 3
    for (let attempt = 1; attempt <= maxAttempts; attempt++) {
      try {
        return await getCreativeRunOutputContent(runId, outputIndex, workspaceId)
      } catch (error) {
        const status = Number((error as { status?: unknown })?.status)
        // 404/410 等明确的结果丢失无需重试；网络和 5xx 才使用有限退避。
        if ((Number.isFinite(status) && status >= 400 && status < 500) || attempt === maxAttempts) throw error
        await new Promise<void>((resolve) => setTimeout(resolve, 250 * 2 ** (attempt - 1)))
      }
    }
    throw new Error('creative output download failed')
  }

  async function harvestOutputsNow(run: CreativeRun, options: HarvestOptions = {}): Promise<void> {
    const outputs = Array.isArray(run.outputs) ? run.outputs : []
    const activeWorkspaceId = options.workspaceId ?? readWorkspaceId()
    const activeWorkspaceGeneration = options.workspaceGeneration ?? workspaceGeneration
    const assets = options.assets ?? new Map(outputAssetMap.value)
    const missing = options.missing ?? new Set(missingOutputKeys.value)
    for (const output of outputs) {
      if (options.isCurrent && !options.isCurrent()) return
      if (!isWorkspaceCurrent(activeWorkspaceId, activeWorkspaceGeneration)) return
      if (output.status !== 'succeeded') continue
      const key = outputAssetKey(run.id, output.output_index)
      if (output.acked_at && !assets.has(key)) {
        // ack 后 transient 已被服务端删除，当前浏览器没有副本时无法恢复。
        missing.add(key)
        continue
      }
      try {
        // 历史刷新拿到的工作副本可能早于另一个收割流程，优先复用已合并到全局的素材。
        let asset = assets.get(key) ?? outputAssetMap.value.get(key)
        if (asset) assets.set(key, asset)
        if (!asset) {
          const blob = await fetchOutputWithRetry(run.id, output.output_index, activeWorkspaceId)
          if (!isWorkspaceCurrent(activeWorkspaceId, activeWorkspaceGeneration)) return
          asset = {
            key,
            kind: 'output',
            blob,
            runId: run.id,
            outputIndex: output.output_index,
            createdAt: Date.now(),
          }
          await saveAsset(asset)
          if (!isWorkspaceCurrent(activeWorkspaceId, activeWorkspaceGeneration)) return
          assets.set(key, asset)
          // 保存成功后立即合并更新，不能等下一次历史刷新才显示图片。
          if (!options.isCurrent || options.isCurrent()) {
            const nextMap = new Map(outputAssetMap.value)
            nextMap.set(key, asset)
            outputAssetMap.value = nextMap
          }
        }

        // 本地已有素材时也重试 ack，但 ack 失败不能抹掉可用的本地图片。
        if (!output.acked_at) {
          try {
            if (!isWorkspaceCurrent(activeWorkspaceId, activeWorkspaceGeneration)) return
            await ackCreativeRunOutput(run.id, output.output_index, activeWorkspaceId)
          } catch (e) {
            console.error(`Failed to ack creative output ${key}:`, e)
          }
        }

        if (options.placeOnCanvas) {
          // 本地保存 + ack（或 ack 失败但本地已保存）后再上画布；画布异常不影响收割结果。
          try {
            // 画布上板是异步的，必须等待当前输出完成，确保图片已经可见。
            await canvasBridge?.placeOutput({ blob: asset.blob, runId: run.id, outputIndex: output.output_index })
          } catch (e) {
            console.error(`Failed to place creative output ${key} on canvas:`, e)
          }
        }
        missing.delete(key)
      } catch (e) {
        // 单个输出 transient 取回或本地保存失败时标记 missing，然后继续处理其它输出。
        console.error(`Failed to harvest creative output ${key}:`, e)
        if (e instanceof LocalStoreQuotaError) {
          error.value = t('creative.error.quotaExceeded')
        }
        missing.add(key)
      }
    }
    if (!options.assets) {
      // 合并本地索引，使并发任务收集到的素材都能保存。
      const mergedAssets = new Map(outputAssetMap.value)
      for (const [key, asset] of assets) mergedAssets.set(key, asset)
      outputAssetMap.value = mergedAssets
      const mergedMissing = new Set(missingOutputKeys.value)
      for (const key of missing) {
        if (mergedAssets.has(key)) mergedMissing.delete(key)
        else mergedMissing.add(key)
      }
      for (const key of mergedAssets.keys()) mergedMissing.delete(key)
      missingOutputKeys.value = mergedMissing
    }
  }

  // ==================== 历史与本地素材关联 ====================

  function upsertRunInHistory(run: CreativeRun): void {
    const index = runHistory.value.findIndex((r) => r.id === run.id)
    if (index >= 0) runHistory.value.splice(index, 1, run)
    else runHistory.value.unshift(run)
  }

  // 拉取服务端历史 + 本地输出素材索引；服务端标记成功但本地无 blob 的输出记为 missing。
  // 历史列表由服务端按当前浏览器工作区返回；图片仍只从当前浏览器本地素材索引关联。
  async function refreshHistory(): Promise<void> {
    // 手动刷新或定时刷新开始时取消旧定时器，完成后再按固定间隔续排。
    clearPollingTimer()
    const generation = ++historyRefreshGeneration
    loadingHistory.value = true
    let requestWorkspaceId = ''
    let requestWorkspaceGeneration = 0
    try {
      requestWorkspaceId = readWorkspaceId()
      requestWorkspaceGeneration = workspaceGeneration
      const page = await getCreativeRuns(requestWorkspaceId, 1, 20)
      // 活动接口返回全部 queued/running/settlement 任务，历史页的最近 20 条记录可能缺少仍在进行的任务。
      // 旧版测试替身或旧后端没有该接口时仍保留历史接口行为。
      const activeByID = new Map<string, CreativeRun>()
      let activeFetchComplete = false
      if (typeof getCreativeActiveRuns === 'function') {
        let cursor: string | undefined
        try {
          do {
            const activePage = await getCreativeActiveRuns(requestWorkspaceId, cursor, 100)
            for (const run of activePage.items) activeByID.set(run.id, run)
            cursor = activePage.has_more && activePage.next_cursor ? activePage.next_cursor : undefined
          } while (cursor)
          activeFetchComplete = true
        } catch (activeError) {
          // 活动接口短暂不可用时仍保留历史快照；下一轮轮询会继续尝试接管 active run。
          console.error('Failed to refresh creative active runs:', activeError)
        }
      }
      const items = [...page.items]
      for (const activeRun of activeByID.values()) {
        const index = items.findIndex((run) => run.id === activeRun.id)
        if (index >= 0) items[index] = activeRun
        else items.push(activeRun)
      }
      if (generation !== historyRefreshGeneration || !isWorkspaceCurrent(requestWorkspaceId, requestWorkspaceGeneration)) return
      // 列表接口偶尔会返回旧快照，已观测到的终态不能被其它快照改写。
      const knownRuns = new Map(runHistory.value.map((run) => [run.id, run]))
      const mergedItems = items.map((run) => {
        const known = knownRuns.get(run.id)
        if (
          known &&
          CREATIVE_RUN_TERMINAL_STATUSES.includes(known.status) &&
          known.status !== run.status
        ) {
          return known
        }
        return run
      })
      runHistory.value = mergedItems
      // 历史接口已返回终态时立即停止对应轮询，并沿用创建任务时的自动上板策略。
      const terminalHarvestedRunIds = new Set<string>()
      for (const run of mergedItems) {
        const state = pollStates.get(run.id)
        if (state && CREATIVE_RUN_TERMINAL_STATUSES.includes(run.status)) {
          stopPolling(run.id)
          if (run.status === 'succeeded') {
            terminalHarvestedRunIds.add(run.id)
            await harvestOutputs(run, {
              placeOnCanvas: state.placeOnCanvas,
              isCurrent: () => generation === historyRefreshGeneration,
              workspaceId: requestWorkspaceId,
              workspaceGeneration: requestWorkspaceGeneration,
            })
            if (generation !== historyRefreshGeneration || !isWorkspaceCurrent(requestWorkspaceId, requestWorkspaceGeneration)) return
          }
        }
      }
      // 页面刷新后接管仍在进行中的任务；后续由同一个列表轮询统一更新。
      for (const run of mergedItems) {
        if (!CREATIVE_RUN_TERMINAL_STATUSES.includes(run.status) && !pollStates.has(run.id)) {
          startPolling(run.id)
        }
      }
      // active 接口不返回终态；历史快照若确认终态，确保旧 pollState 不会永久增长。
      for (const runID of [...pollStates.keys()]) {
        const known = mergedItems.find((run) => run.id === runID)
        if (known && CREATIVE_RUN_TERMINAL_STATUSES.includes(known.status)) stopPolling(runID)
        else if (!known && activeFetchComplete) stopPolling(runID)
      }
      // 只需输出素材索引（missing 判定用）；源图 / mask 素材已由画布自行管理
      const outputs = await listAssets('output')
      const map = new Map(outputs.map((a) => [a.key, a]))
      if (generation !== historyRefreshGeneration || !isWorkspaceCurrent(requestWorkspaceId, requestWorkspaceGeneration)) return
      const missing = new Set<string>()
      for (const run of mergedItems) {
        if (!terminalHarvestedRunIds.has(run.id)) {
          await harvestOutputs(run, {
            assets: map,
            missing,
            placeOnCanvas: false,
            isCurrent: () => generation === historyRefreshGeneration,
            workspaceId: requestWorkspaceId,
            workspaceGeneration: requestWorkspaceGeneration,
          })
          if (generation !== historyRefreshGeneration || !isWorkspaceCurrent(requestWorkspaceId, requestWorkspaceGeneration)) return
        }
        for (const output of run.outputs ?? []) {
          if (output.status !== 'succeeded') continue
          // 收割失败或已 ack 且本地没有副本时保留缺失占位。
          const key = outputAssetKey(run.id, output.output_index)
          if (!map.has(key)) {
            missing.add(key)
          }
        }
      }
      if (generation !== historyRefreshGeneration || !isWorkspaceCurrent(requestWorkspaceId, requestWorkspaceGeneration)) return
      // 合并历史快照时一并加入任务结束后刚保存的素材，内存索引保留这些新条目。
      for (const [key, asset] of outputAssetMap.value) {
        if (!map.has(key)) map.set(key, asset)
      }
      for (const key of missing) {
        if (map.has(key)) missing.delete(key)
      }
      outputAssetMap.value = map
      missingOutputKeys.value = missing
      if (currentRun.value) {
        const fresh = mergedItems.find((r) => r.id === currentRun.value?.id)
        if (fresh) currentRun.value = fresh
      }
    } catch (e) {
      console.error('Failed to refresh creative history:', e)
      if (e instanceof LocalStoreError && e.type === 'unavailable') {
        workspaceId = null
        resetWorkspaceState()
      }
      error.value = creativeErrorMessage(e, 'creative.error.historyFailed')
    } finally {
      if (generation === historyRefreshGeneration) schedulePollingRefresh()
      if (generation === historyRefreshGeneration) {
        loadingHistory.value = false
      }
    }
  }

  // ==================== 本地数据 ====================

  // 清空本机创作数据（素材 + 场景 + 设置）并重置内存状态；
  // 清空成功后旋转工作区，使旧任务在当前浏览器立即不可见。
  async function clearLocalData(): Promise<void> {
    stopPolling()
    clearPollingTimer()
    // 清空时使进行中的历史请求失效，素材列表保持清空状态。
    historyRefreshGeneration++
    workspaceGeneration++
    try {
      await clearAll()
      workspaceId = rotateCreativeWorkspaceId()
    } catch (e) {
      error.value = creativeErrorMessage(e, 'creative.error.clearFailed')
      throw e
    }
    resetWorkspaceState()
  }

  // ==================== 工具 ====================

  function extractErrorMessage(e: unknown): string {
    const message = (e as { message?: unknown })?.message
    return typeof message === 'string' ? message : ''
  }

  // 本地存储不可用时明确提示并保持 fail-close，不回退到共享历史。
  function creativeErrorMessage(e: unknown, fallbackKey: string): string {
    if (e instanceof LocalStoreError && e.type === 'unavailable') {
      return t('creative.error.workspaceUnavailable')
    }
    return extractErrorMessage(e) || t(fallbackKey)
  }

  // 组件卸载时清理轮询定时器，结束周期性回调。
  onBeforeUnmount(() => {
    stopPolling()
    clearPollingTimer()
    historyRefreshGeneration++
    if (typeof window !== 'undefined') {
      window.removeEventListener('storage', onWorkspaceStorage)
      window.removeEventListener('pagehide', onPageHide)
      document.removeEventListener('visibilitychange', onVisibilityChange)
    }
    flushSelectionSettingsSave()
  })

  return {
    // 状态
    models,
    capabilities,
    loadingModels,
    selectedOptionKey,
    selectedOption,
    operation,
    prompt,
    imageSize,
    aspectRatio,
    quality,
    background,
    thinkingLevel,
    currentRun,
    runHistory,
    loadingHistory,
    polling,
    busy,
    error,
    missingOutputKeys,
    outputAssetMap,
    // 计算
    operationOptions,
    imageSizeOptions,
    aspectRatioOptions,
    qualityOptions,
    backgroundOptions,
    thinkingLevelOptions,
    maxReferenceImages,
    estimatedCost,
    canGenerate,
    // 方法
    loadModels,
    selectOption,
    createRun,
    restoreRunInput,
    refreshHistory,
    clearLocalData,
    registerCanvasBridge,
    importOutputToCanvas,
  }
}
