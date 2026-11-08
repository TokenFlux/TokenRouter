import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
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
  loadRunParamsMap,
  loadSetting,
  outputAssetKey,
  rotateCreativeWorkspaceId,
  saveAsset,
  saveRunParams,
  saveSetting,
  type LocalAsset,
  type LocalRunParams,
} from '@/utils/creativeLocalStore'

/**
 * useCreativeStudio.ts manages generation settings, task polling, and
 * browser-local output recovery. Submissions retain an idempotency key until
 * acceptance, while each refresh merges recent history with active tasks that
 * can fall outside the latest page. Workspace and refresh generations decide
 * which asynchronous results remain relevant to the mounted studio.
 *
 * Output recovery shares an ordered queue that saves images locally before
 * acknowledging their transient server copies. The view supplies canvas
 * placement through a bridge. Settings writes and task-triggered account
 * refreshes each run in order, keeping later snapshots behind earlier requests
 * while allowing balance updates to proceed independently of image downloads.
 * Failed acknowledgements keep local images usable and can be retried on later
 * recovery.
 */

/**
 * CreativeExportInput contains canvas-exported source images and an optional
 * inpaint mask.
 */
export interface CreativeExportInput {
  sourceBlobs: Blob[] // original canvas reference images
  /**
   * The mask is PNG data for inpaint requests; other operations may submit a null
   * value.
   */
  maskBlob: Blob | null
}

/**
 * CreativeSelectionSettings stores the form values restored after catalog
 * loading. quality, background, and thinkingLevel use empty strings when
 * unsupported, allowing submission to omit those optional parameters.
 */
interface CreativeSelectionSettings {
  outputFormat?: string // Submitted output format name.
  frameCount?: number // Submitted animation frame count.
  frameDurationMS?: number // Submitted frame delay, milliseconds.
  optionKey: string // selected group-model key
  operation: string // selected image operation
  imageSize: string // selected image tier
  aspectRatio: string // selected aspect ratio
  quality: string // selected quality option
  background: string // selected background mode
  thinkingLevel: string // selected reasoning level
  prompt: string // saved prompt text
}

/**
 * SETTINGS_KEY identifies the persisted form selection restored after the model
 * catalog becomes available.
 */
const SETTINGS_KEY = 'creative:selection'
/**
 * DEFAULT_CREATIVE_CAPABILITIES supplies prompt and upload limits until the
 * server capability request succeeds for this studio instance.
 */
const DEFAULT_CREATIVE_CAPABILITIES: CreativeCapabilities = {
  max_prompt_chars: 8000,
  max_asset_bytes: 33554432,
  max_total_input_bytes: 67108864,
  max_mask_bytes: 4194304,
  allowed_mime_types: ['image/png', 'image/jpeg', 'image/webp'],
}
/**
 * SETTINGS_SAVE_DEBOUNCE coalesces consecutive prompt and parameter edits
 * before writing their latest browser-local snapshot.
 */
const SETTINGS_SAVE_DEBOUNCE = 300

/**
 * truncatePrompt limits prompt length by Unicode code points rather than UTF-16
 * units. It uses the default capability limit when none is supplied, preserving
 * complete supplementary characters at the cutoff.
 */
function truncatePrompt(
  value: string,
  maxChars = DEFAULT_CREATIVE_CAPABILITIES.max_prompt_chars,
): string {
  return Array.from(value).slice(0, maxChars).join('')
}

/**
 * POLL_INTERVAL delays the next shared history refresh after the preceding
 * request completes while tasks remain active.
 */
const POLL_INTERVAL = 3000

/**
 * CreativeCanvasBridge supplies view-owned image placement operations to
 * recovery and history import.
 */
export interface CreativeCanvasBridge {
  /**
   * placeOutput places a recovered output after local saving and attempted server
   * acknowledgement. An asynchronous implementation returns its placement promise
   * so harvesting can wait before advancing to the next output.
   */
  placeOutput(asset: {
    blob: Blob // recovered output bytes
    runId: string // server run identifier
    outputIndex: number // zero-based output index
  }): void | Promise<void>
  /**
   * importToCanvas imports an existing local output through the registered
   * canvas. Implementations may return a promise, although importOutputToCanvas
   * reports synchronous dispatch success and does not await that promise.
   */
  importToCanvas(blob: Blob, runId: string, outputIndex: number): void | Promise<void>
}

/**
 * CreativeCanvasGuide identifies the missing canvas input displayed by the
 * composer.
 */
export type CreativeCanvasGuide = 'inpaintPick' | 'editPick' | 'maskPaint'

/**
 * creativeOptionKey combines group identity and model name into a stable
 * selector value.
 */
export function creativeOptionKey(option: Pick<CreativeModelOption, 'group_id' | 'model'>): string {
  return `${option.group_id}::${option.model}`
}

/**
 * useCreativeStudio provides reactive generation controls, task history, and
 * browser-local output recovery for a mounted view. Call it during component
 * setup so watchers and registered cleanup follow the view lifecycle. The view
 * supplies source images at submission and registers a canvas bridge for
 * recovered outputs.
 *
 * Workspace generations and history refresh generations determine which
 * asynchronous snapshots can update current state. Output harvesting and
 * settings persistence each use ordered promise chains, while task identity and
 * status changes queue account refreshes independently of downloads. Unmount
 * stops timers and invalidates history publication, then starts a best-effort
 * settings flush.
 */
export function useCreativeStudio() {
  /**
   * t and authStore provide localized feedback and account refreshes for this
   * mounted studio instance.
   */
  const { t } = useI18n()
  const authStore = useAuthStore()

  /**
   * models, capabilities, and loadingModels hold the available catalog and limits
   * used by controls and submission validation.
   */
  const models = ref<CreativeModelOption[]>([])
  const capabilities = ref<CreativeCapabilities>({ ...DEFAULT_CREATIVE_CAPABILITIES })
  const loadingModels = ref(false)
  /**
   * selectedOptionKey, operation, prompt, imageSize, and aspectRatio are shared
   * form inputs restored from local settings after catalog loading.
   */
  const selectedOptionKey = ref('')
  const operation = ref<CreativeOperation>('generate')
  const prompt = ref('')
  const imageSize = ref('')
  const aspectRatio = ref('auto')
  /** Animation controls preserve upstream defaults until a GIF route is selected. */
  const outputFormat = ref('png')
  const frameCount = ref(9)
  const frameDurationMS = ref(200)
  /**
   * outputFormatOptions follows the selected operation's routes, keeping inpaint
   * static. Only servers without an operation map use the legacy combined list;
   * a missing operation in a supplied map offers no formats.
   */
  const outputFormatOptions = computed(() => {
    if (operation.value === 'inpaint') return ['png']
    const option = selectedOption.value
    if (option?.output_formats_by_operation) {
      return option.output_formats_by_operation[operation.value] ?? []
    }
    return option?.output_formats?.length ? option.output_formats : ['png']
  })
  /**
   * quality, background, and thinkingLevel are optional model parameters
   * normalized to empty strings when the selected model lacks those capabilities.
   */
  const quality = ref('medium')
  const background = ref('auto')
  const thinkingLevel = ref('minimal')
  /**
   * currentRun and runHistory retain accepted and refreshed task records for
   * composer status and history rendering.
   */
  const currentRun = ref<CreativeRun | null>(null)
  const runHistory = ref<CreativeRun[]>([])
  /**
   * loadingHistory, polling, busy, and error expose current refresh, tracking,
   * submission, and failure feedback to the studio views.
   */
  const loadingHistory = ref(false)
  const polling = ref(false)
  const busy = ref(false)
  const error = ref('')
  /**
   * missingOutputKeys records successful server outputs whose image bytes are
   * unavailable locally after attempted recovery.
   */
  const missingOutputKeys = ref<Set<string>>(new Set())
  /**
   * outputAssetMap indexes recovered local output records by asset key and merges
   * entries as concurrent tasks complete.
   */
  const outputAssetMap = ref<Map<string, LocalAsset>>(new Map())
  /**
   * runParamsMap keeps submitted prompt and parameter snapshots available before
   * their browser storage writes complete.
   */
  const runParamsMap = ref<Map<string, LocalRunParams>>(new Map())
  /**
   * activeIdempotencyKey survives request failures and resets after acceptance,
   * allowing the next submission attempt to reuse its server identity.
   */
  const activeIdempotencyKey = ref('')
  /**
   * canvasBridge holds view-owned placement callbacks and can be cleared when the
   * view releases its canvas.
   */
  let canvasBridge: CreativeCanvasBridge | null = null
  /**
   * workspaceGeneration and workspaceId identify the browser workspace captured
   * by asynchronous submissions, history queries, and recovery operations.
   */
  let workspaceGeneration = 0
  let workspaceId: string | null = null
  /**
   * settingsHydrated prevents startup defaults from overwriting saved inputs
   * before restoreSettings completes its read or recovery path.
   */
  let settingsHydrated = false
  /**
   * settingsSaveTimer delays pending form persistence and is cancelled before
   * background or unmount flushes.
   */
  let settingsSaveTimer: ReturnType<typeof setTimeout> | null = null
  /**
   * settingsRevision and settingsDirty track edits so only a write matching the
   * latest revision can clear dirty state.
   */
  let settingsRevision = 0
  let settingsDirty = false
  /**
   * settingsWriteChain orders form writes so an older asynchronous save cannot
   * finish after a newer queued snapshot.
   */
  let settingsWriteChain: Promise<void> = Promise.resolve()

  /**
   * userRefreshQueue orders account reads after task identity or status changes,
   * independently of potentially slow output recovery.
   */
  let userRefreshQueue: Promise<void> = Promise.resolve()

  /**
   * Track task identities and statuses separately from output downloads so
   * reservation and settlement account reads can begin while image recovery
   * remains pending.
   */
  watch(
    () =>
      runHistory.value
        .map((run) => `${run.id}:${run.status}`)
        .sort()
        .join(','),
    () => {
      /**
       * Serialize account reads for successive task changes so a later settlement
       * refresh starts after any earlier reservation refresh finishes.
       */
      userRefreshQueue = userRefreshQueue
        .then(() => authStore.refreshUser())
        .then(() => undefined)
        .catch((cause) => {
          /**
           * Account-read failures leave generation and image recovery running; later task
           * changes and the auth store's periodic refresh can update displayed funds.
           */
          console.error('Failed to refresh creative balance:', cause)
        })
    },
  )

  /**
   * resetWorkspaceState stops polling and clears task, asset, and submission
   * identity state. Callers invalidate their own workspace or history generations
   * before using this reset to reject outstanding asynchronous responses.
   */
  function resetWorkspaceState(): void {
    stopPolling()
    clearPollingTimer()
    currentRun.value = null
    runHistory.value = []
    outputAssetMap.value = new Map()
    runParamsMap.value = new Map()
    missingOutputKeys.value = new Set()
    activeIdempotencyKey.value = ''
  }

  /**
   * selectedOption resolves the group-model key against current catalog entries
   * and supplies the capabilities used by dependent controls.
   */
  const selectedOption = computed(
    () => models.value.find((m) => creativeOptionKey(m) === selectedOptionKey.value) ?? null,
  )

  /**
   * Operation, image-size, aspect-ratio, quality, background, and thinking-level
   * choices derive from the selected model; absent lists hide optional controls.
   */
  const operationOptions = computed(() => selectedOption.value?.operations ?? [])

  const imageSizeOptions = computed(() => selectedOption.value?.image_sizes ?? [])

  const aspectRatioOptions = computed(() => selectedOption.value?.aspect_ratios ?? [])

  const qualityOptions = computed(() => selectedOption.value?.qualities ?? [])

  const backgroundOptions = computed(() => selectedOption.value?.background_options ?? [])

  const thinkingLevelOptions = computed(() => selectedOption.value?.thinking_levels ?? [])

  /**
   * maxReferenceImages uses the selected model limit and retains a minimum of one
   * permitted reference image.
   */
  const maxReferenceImages = computed(() =>
    Math.max(1, selectedOption.value?.max_reference_images ?? 1),
  )

  /**
   * estimatedCost selects the model price for the chosen size tier, with the
   * one-kilopixel price used for default sizing. It returns null while the model
   * selection has no matching catalog entry.
   */
  const estimatedCost = computed(() => {
    const option = selectedOption.value
    if (!option) return null
    let unitPrice: number
    switch (imageSize.value) {
      case '512':
        // Use the one-kilopixel price when the catalog omits a 512-pixel price,
        // matching the catalog's default image tier.
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
    return unitPrice * (outputFormat.value === 'gif' ? frameCount.value : 1)
  })

  /**
   * canGenerate checks model, operation, busy state, and prompt text; canvas
   * source and mask requirements are checked at submission.
   */
  const canGenerate = computed(() => {
    if (!selectedOption.value || busy.value) return false
    if (!operationOptions.value.includes(operation.value)) return false
    if (!outputFormatOptions.value.includes(outputFormat.value)) return false
    if (
      outputFormat.value === 'gif' &&
      (!Number.isInteger(frameCount.value) ||
        frameCount.value < 2 ||
        frameCount.value > 16 ||
        !Number.isInteger(frameDurationMS.value) ||
        frameDurationMS.value < 20 ||
        frameDurationMS.value > 2000)
    )
      return false
    if (prompt.value.trim().length === 0) return false
    if (Array.from(prompt.value).length > capabilities.value.max_prompt_chars) return false
    return true
  })

  /**
   * loadModels loads available models, attempts capability refresh, and restores
   * saved form settings. Concurrent model loads are skipped; capability failures
   * retain defaults, while catalog failures publish the localized model-loading
   * error.
   */
  async function loadModels(): Promise<void> {
    if (loadingModels.value) return
    loadingModels.value = true
    try {
      models.value = await getCreativeModels()
      if (typeof getCreativeCapabilities === 'function') {
        try {
          capabilities.value = await getCreativeCapabilities()
        } catch (capabilityError) {
          /**
           * Keep the initial upload limits when capability retrieval fails so catalog
           * restoration and available generation controls can still load.
           */
          console.error('Failed to load creative capabilities:', capabilityError)
        }
      }
      await restoreSettings()
    } catch (e) {
      /**
       * Catalog loading failures publish a localized error while finally releases the
       * loading flag, allowing a later load attempt to retry.
       */
      console.error('Failed to load creative models:', e)
      error.value = t('creative.error.loadModelsFailed')
    } finally {
      loadingModels.value = false
    }
  }

  /**
   * restoreSettings restores stored form values against the available model
   * catalog. It selects a saved model only when that group-model key still
   * exists, then normalizes saved parameters to supported values. Hydration is
   * marked complete in finally even after a storage failure, allowing later edits
   * to persist. The following scheduled save records the normalized selection and
   * prevents initial setup defaults from replacing saved values before the read
   * completes.
   */
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
      outputFormat.value = saved.outputFormat ?? 'png'
      frameCount.value = saved.frameCount ?? 9
      frameDurationMS.value = saved.frameDurationMS ?? 200
      if (typeof saved.quality === 'string') quality.value = saved.quality
      if (typeof saved.background === 'string') background.value = saved.background
      if (typeof saved.thinkingLevel === 'string') thinkingLevel.value = saved.thinkingLevel
      if (typeof saved.prompt === 'string')
        prompt.value = truncatePrompt(saved.prompt, capabilities.value.max_prompt_chars)
      normalizeSelection()
    } catch (e) {
      /**
       * A failed stored-settings read leaves the current form usable; finally enables
       * subsequent edits to persist through the normal debounced path.
       */
      console.error('Failed to restore creative settings:', e)
    } finally {
      settingsHydrated = true
      scheduleSelectionSettingsSave()
    }
  }

  /**
   * normalizeSelection repairs form values against the selected model's
   * advertised choices. Unsupported required options use preferred or first
   * choices, with generate and empty size available for empty lists; unsupported
   * optional groups become empty strings omitted during submission.
   */
  function normalizeSelection(): void {
    const option = selectedOption.value
    if (!option) return
    if (!outputFormatOptions.value.includes(outputFormat.value)) outputFormat.value = 'png'
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
      thinkingLevel.value = thinkingLevels.includes('minimal')
        ? 'minimal'
        : (thinkingLevels[0] ?? '')
    }
  }

  /**
   * selectOption stores a group-model selector and normalizes its dependent form
   * values.
   */
  function selectOption(key: string): void {
    selectedOptionKey.value = key
    normalizeSelection()
  }

  /**
   * selectionSettingsSnapshot copies current generation controls into a browser
   * settings record. Prompt text is truncated to the capability limit by code
   * point; the returned primitive values remain stable as reactive inputs change.
   */
  function selectionSettingsSnapshot(): CreativeSelectionSettings {
    return {
      optionKey: selectedOptionKey.value,
      operation: operation.value,
      imageSize: imageSize.value,
      aspectRatio: aspectRatio.value,
      outputFormat: outputFormat.value,
      frameCount: frameCount.value,
      frameDurationMS: frameDurationMS.value,
      quality: quality.value,
      background: background.value,
      thinkingLevel: thinkingLevel.value,
      prompt: truncatePrompt(prompt.value, capabilities.value.max_prompt_chars),
    }
  }

  /**
   * scheduleSelectionSettingsSave records a hydrated form edit and restarts the
   * settings debounce. Before hydration completes it leaves stored selection
   * recovery in control, preventing startup values from immediately becoming
   * writes. Every later edit increments the revision and marks the form dirty
   * before replacing the pending timer. The callback persists the current
   * snapshot, and write completion clears dirty state only when its captured
   * revision still matches the latest edit.
   */
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

  /**
   * flushSelectionSettingsSave cancels the debounce and starts a pending settings
   * write. It returns immediately, letting pagehide and unmount initiate
   * best-effort persistence without waiting for browser storage completion.
   */
  function flushSelectionSettingsSave(): void {
    if (settingsSaveTimer) clearTimeout(settingsSaveTimer)
    settingsSaveTimer = null
    void persistSelectionSettings()
  }

  /**
   * persistSelectionSettings queues a snapshot of the latest dirty form after
   * earlier writes. Unhydrated or clean settings return the existing write chain.
   * Each queued write captures the revision and clears dirty state only when that
   * revision remains current after success. Prior rejections are consumed to keep
   * later saves available, and a failed write is logged while leaving dirty state
   * for a future scheduled or explicit flush.
   */
  function persistSelectionSettings(): Promise<void> {
    if (!settingsHydrated || !settingsDirty) return settingsWriteChain
    const snapshot = selectionSettingsSnapshot()
    const revision = settingsRevision
    /**
     * Serialize captured snapshots and compare their revisions on completion so an
     * older successful save cannot clear a newer unsaved edit.
     */
    settingsWriteChain = settingsWriteChain
      .catch(() => undefined)
      .then(() => saveSetting(SETTINGS_KEY, snapshot))
      .then(() => {
        if (settingsRevision === revision) settingsDirty = false
      })
      .catch((error) => {
        /**
         * Keep dirty state after a failed write so a later edit or background flush can
         * attempt the current form again.
         */
        console.error('Failed to persist creative settings:', error)
      })
    return settingsWriteChain
  }

  // Operation changes can remove GIF support before submission. Reset only the
  // output format, retaining animation settings for the next compatible selection.
  watch(outputFormatOptions, (formats) => {
    if (!formats.includes(outputFormat.value)) outputFormat.value = 'png'
  })

  watch(
    [
      selectedOptionKey,
      operation,
      imageSize,
      aspectRatio,
      quality,
      outputFormat,
      frameCount,
      frameDurationMS,
      background,
      thinkingLevel,
      prompt,
    ],
    scheduleSelectionSettingsSave,
    /**
     * Record dirty state synchronously with the input change so an immediately
     * following pagehide event can capture the latest prompt value.
     */
    { flush: 'sync' },
  )

  /**
   * registerCanvasBridge replaces the canvas callbacks used by recovery and
   * history imports. The view registers its mounted canvas and passes null during
   * teardown so future dispatches cannot target that bridge.
   */
  function registerCanvasBridge(bridge: CreativeCanvasBridge | null): void {
    canvasBridge = bridge
  }

  /**
   * readWorkspaceId reads the persisted browser identity and returns it for
   * server requests. When an existing identity changes, it advances workspace and
   * history generations before resetting visible task state and stopping polling.
   * Outstanding operations compare their captured identities and generations to
   * decide whether they can still publish. Storage failures propagate to the
   * caller, which selects its own error message and unavailable-workspace reset
   * behavior.
   */
  function readWorkspaceId(): string {
    const next = getCreativeWorkspaceId()
    if (workspaceId && workspaceId !== next) {
      workspaceGeneration++
      /**
       * Invalidate pending history publication before clearing indexes for a changed
       * workspace, keeping old requests from restoring the previous task list.
       */
      historyRefreshGeneration++
      resetWorkspaceState()
    }
    workspaceId = next
    return next
  }

  /**
   * isWorkspaceCurrent reports whether both a captured workspace identifier and
   * generation still match. Callers use the result after asynchronous work to
   * decide whether they can update this studio's current state.
   */
  function isWorkspaceCurrent(id: string, generation: number): boolean {
    return workspaceId === id && workspaceGeneration === generation
  }

  /**
   * onWorkspaceStorage reacts to another tab replacing or clearing the browser
   * workspace identity. Changed identities trigger history refresh, and
   * unavailable local storage clears visible state before presenting the
   * workspace error.
   */
  function onWorkspaceStorage(event: StorageEvent): void {
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
    /**
     * Listen for cross-tab identity changes and page lifecycle flushes during
     * setup; the unmount hook removes these same handlers before finishing.
     */
    window.addEventListener('storage', onWorkspaceStorage)
    window.addEventListener('pagehide', onPageHide)
    document.addEventListener('visibilitychange', onVisibilityChange)
  }

  /**
   * onPageHide starts a best-effort flush of the current form draft. The write
   * remains asynchronous, so this event reduces the debounce delay without
   * guaranteeing completion before browser shutdown.
   */
  function onPageHide(): void {
    flushSelectionSettingsSave()
  }

  /**
   * onVisibilityChange flushes pending form settings when the document becomes
   * hidden. Foreground transitions leave the existing schedule in place, while
   * hidden transitions use the pagehide persistence path.
   */
  function onVisibilityChange(): void {
    if (document.visibilityState === 'hidden') flushSelectionSettingsSave()
  }

  /**
   * importOutputToCanvas dispatches a stored output to the current canvas bridge.
   * Missing bytes or bridge returns false, and synchronous errors are logged;
   * true reports dispatch rather than completion of an asynchronous placement.
   */
  function importOutputToCanvas(runId: string, outputIndex: number): boolean {
    const asset = outputAssetMap.value.get(outputAssetKey(runId, outputIndex))
    if (!asset || !canvasBridge) return false
    try {
      canvasBridge.importToCanvas(asset.blob, runId, outputIndex)
      return true
    } catch (e) {
      /**
       * A synchronous bridge failure reports unsuccessful dispatch while keeping the
       * stored asset available for another history import attempt.
       */
      console.error('Failed to import creative output to canvas:', e)
      return false
    }
  }

  /**
   * rememberRunParams publishes submitted parameters in memory before attempting
   * local persistence. Storage failures are logged while the live history retains
   * those parameters, allowing a submitted task to continue independently.
   */
  async function rememberRunParams(params: LocalRunParams): Promise<void> {
    const nextMap = new Map(runParamsMap.value)
    nextMap.set(params.runId, params)
    /**
     * Publish submitted parameters before storage completes so the history panel
     * can immediately reuse the accepted request's exact input snapshot.
     */
    runParamsMap.value = nextMap
    try {
      await saveRunParams(params)
    } catch (e) {
      /**
       * A failed parameter write leaves the accepted task and its in-memory snapshot
       * available, allowing generation to continue despite local storage failure.
       */
      console.error(`Failed to save creative run params ${params.runId}:`, e)
    }
  }

  /**
   * applyRunParams restores a local task's prompt and generation settings into
   * the form. Missing records return false; unavailable saved models keep the
   * current selection, and normalization repairs settings against its current
   * capabilities.
   */
  function applyRunParams(runId: string): boolean {
    const params = runParamsMap.value.get(runId)
    if (!params) return false
    if (models.value.some((m) => creativeOptionKey(m) === params.optionKey)) {
      selectedOptionKey.value = params.optionKey
    }
    operation.value = params.operation
    imageSize.value = params.imageSize
    aspectRatio.value = params.aspectRatio
    outputFormat.value = params.outputFormat ?? 'png'
    frameCount.value = params.frameCount ?? 9
    frameDurationMS.value = params.frameDurationMS ?? 200
    quality.value = params.quality
    background.value = params.background
    thinkingLevel.value = params.thinkingLevel
    prompt.value = truncatePrompt(params.prompt, capabilities.value.max_prompt_chars)
    normalizeSelection()
    return true
  }

  /**
   * createRun validates supplied image inputs and submits the current generation
   * form. It rejects overlapping submissions, unsupported operations, excessive
   * prompt length, missing required sources or masks, and capability-limit
   * violations before creating a request. The idempotency key survives failed
   * requests and resets after server acceptance.
   *
   * Form parameters are copied before awaiting the request so edits made during
   * submission do not rewrite the saved history snapshot. A captured workspace
   * generation controls publication of the accepted task; current tasks enter
   * shared polling with automatic placement enabled, while stale successful
   * submissions still report acceptance.
   */
  async function createRun(exported: CreativeExportInput): Promise<boolean> {
    if (busy.value) return false
    const option = selectedOption.value
    if (!option) {
      error.value = t('creative.error.noModel')
      return false
    }
    if (
      !option.operations.includes(operation.value) ||
      !outputFormatOptions.value.includes(outputFormat.value)
    ) {
      error.value = t('creative.error.operationNotSupported')
      return false
    }
    if (Array.from(prompt.value).length > capabilities.value.max_prompt_chars) {
      error.value = t('creative.error.promptTooLong')
      return false
    }
    if (
      (operation.value === 'edit' || operation.value === 'inpaint') &&
      exported.sourceBlobs.length === 0
    ) {
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
    const totalInputBytes =
      exported.sourceBlobs.reduce((sum, blob) => sum + blob.size, 0) +
      (exported.maskBlob?.size ?? 0)
    if (totalInputBytes > capabilities.value.max_total_input_bytes) {
      error.value = t('creative.error.assetTooLarge')
      return false
    }
    for (const blob of exported.sourceBlobs) {
      if (
        blob.size > capabilities.value.max_asset_bytes ||
        (blob.type && !allowedMIMEs.has(blob.type))
      ) {
        error.value = t('creative.error.assetTooLarge')
        return false
      }
    }
    if (
      exported.maskBlob &&
      (exported.maskBlob.size > capabilities.value.max_mask_bytes ||
        exported.maskBlob.type !== 'image/png')
    ) {
      error.value = t('creative.error.assetTooLarge')
      return false
    }

    busy.value = true
    error.value = ''
    try {
      const requestWorkspaceId = readWorkspaceId()
      const requestGeneration = workspaceGeneration
      /**
       * Retain the same idempotency key across failed submission attempts so an
       * accepted-but-unobserved server request can be recognized on retry.
       */
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
      form.append('output_format', outputFormat.value)
      if (outputFormat.value === 'gif') {
        form.append('frame_count', String(frameCount.value))
        form.append('frame_duration_ms', String(frameDurationMS.value))
      }
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

      /**
       * Capture primitive form values before awaiting task creation because the user
       * can continue editing the shared prompt during the network request.
       */
      const submitted: Omit<LocalRunParams, 'runId' | 'createdAt'> = {
        optionKey: creativeOptionKey(option),
        groupName: option.group_name,
        operation: operation.value,
        prompt: prompt.value,
        imageSize: imageSize.value,
        aspectRatio: aspectRatio.value,
        outputFormat: outputFormat.value,
        frameCount: frameCount.value,
        frameDurationMS: frameDurationMS.value,
        quality: quality.value,
        background: background.value,
        thinkingLevel: thinkingLevel.value,
        referenceCount: exported.sourceBlobs.length,
      }

      const run = await createCreativeRun(form, requestWorkspaceId, activeIdempotencyKey.value)
      /**
       * Acceptance ends this submission identity; publish the task only when its
       * captured workspace still matches the current browser generation.
       */
      activeIdempotencyKey.value = ''
      if (!isWorkspaceCurrent(requestWorkspaceId, requestGeneration)) return true
      void rememberRunParams({ ...submitted, runId: run.id, createdAt: Date.now() })
      currentRun.value = run
      upsertRunInHistory(run)
      startPolling(run.id, { placeOnCanvas: true })
      return true
    } catch (e) {
      error.value = creativeErrorMessage(e, 'creative.error.submitFailed')
      return false
    } finally {
      busy.value = false
    }
  }

  /**
   * PollState retains automatic placement intent for a run tracked by shared
   * polling.
   */
  interface PollState {
    /**
     * Automatic placement stays enabled once a submitted task requests it; history
     * recovery normally stores outputs locally.
     */
    placeOnCanvas: boolean
  }

  /**
   * pollStates retains run identities and placement intent while one shared list
   * timer refreshes their task status.
   */
  const pollStates = new Map<string, PollState>()
  /**
   * pollTimer holds the next shared history refresh and clears before each
   * scheduled or manual refresh begins.
   */
  let pollTimer: ReturnType<typeof setTimeout> | null = null
  /**
   * harvestQueue orders downloads and local index updates across all runs while
   * allowing subsequent recovery after task failure.
   */
  let harvestQueue: Promise<void> = Promise.resolve()
  /**
   * historyRefreshGeneration selects the latest history request permitted to
   * publish indexes, loading state, and the next polling timer.
   */
  let historyRefreshGeneration = 0

  /**
   * updatePollingState publishes whether any run remains in the shared tracking
   * map.
   */
  function updatePollingState(): void {
    polling.value = pollStates.size > 0
  }

  /**
   * stopPolling removes one tracked run or clears the complete tracking map. It
   * updates reactive polling state; callers separately clear the scheduled timer
   * when stopping the whole polling lifecycle.
   */
  function stopPolling(runId?: string): void {
    if (runId) {
      pollStates.delete(runId)
    } else {
      pollStates.clear()
    }
    updatePollingState()
  }

  /**
   * clearPollingTimer cancels the scheduled shared history refresh and clears its
   * handle. It is safe before every refresh or teardown, including when no poll
   * callback is currently scheduled.
   */
  function clearPollingTimer(): void {
    if (!pollTimer) return
    clearTimeout(pollTimer)
    pollTimer = null
  }

  /**
   * schedulePollingRefresh schedules one list refresh for all tracked runs. It
   * reuses a pending timer and creates no work when the tracking map is empty,
   * preventing each run registration from adding another poll. The callback
   * clears its handle before checking tracking again and launching
   * refreshHistory. That refresh owns scheduling the following interval after
   * completion, while stop and teardown paths can cancel the pending timer
   * through clearPollingTimer.
   */
  function schedulePollingRefresh(): void {
    if (pollTimer || pollStates.size === 0) return
    pollTimer = setTimeout(() => {
      pollTimer = null
      if (pollStates.size === 0) return
      void refreshHistory()
    }, POLL_INTERVAL)
  }

  /**
   * startPolling records a run in the shared tracking map and schedules history
   * polling. Existing automatic-placement intent is combined with the supplied
   * request, so a later history registration cannot disable placement requested
   * by submission. All tracked runs share one timer and list refresh rather than
   * creating per-run requests. Updating reactive polling state after the map
   * change keeps the composer's active-work indicator consistent with the tracked
   * set.
   */
  function startPolling(
    runId: string,
    options: {
      /**
       * Automatic placement stays enabled once a submitted task requests it; history
       * recovery normally stores outputs locally.
       */
      placeOnCanvas?: boolean
    } = {},
  ): void {
    const existing = pollStates.get(runId)
    pollStates.set(runId, {
      placeOnCanvas: Boolean(existing?.placeOnCanvas || options.placeOnCanvas),
    })
    updatePollingState()
    schedulePollingRefresh()
  }

  /**
   * HarvestOptions supplies per-refresh recovery state and ownership checks.
   * assets and missing are caller-owned working indexes, while workspaceId,
   * workspaceGeneration, and isCurrent determine whether asynchronous results may
   * still be published.
   */
  interface HarvestOptions {
    /**
     * Automatic placement stays enabled once a submitted task requests it; history
     * recovery normally stores outputs locally.
     */
    placeOnCanvas?: boolean
    /**
     * When supplied, this working map receives recovered assets and its caller owns
     * final reactive publication.
     */
    assets?: Map<string, LocalAsset>
    /**
     * The supplied set accumulates unavailable output keys for the history refresh
     * that owns this working snapshot.
     */
    missing?: Set<string>
    /**
     * The caller uses this predicate to stop obsolete history work before
     * processing another output or publishing recovered bytes.
     */
    isCurrent?: () => boolean
    workspaceId?: string // Captured browser workspace identity
    workspaceGeneration?: number // Captured workspace invalidation generation
  }

  /**
   * harvestOutputs queues one run's recovery after earlier harvesting tasks. The
   * shared chain orders output downloads and local index updates across
   * independently completing server runs, while harvestOutputsNow owns per-output
   * error handling and workspace checks. A caught continuation becomes the queue
   * tail so an unexpected rejection cannot block subsequent runs. The returned
   * task retains that rejection for its caller instead of replacing the caller's
   * result with the swallowed queue continuation.
   */
  function harvestOutputs(run: CreativeRun, options: HarvestOptions = {}): Promise<void> {
    const task = harvestQueue.then(() => harvestOutputsNow(run, options))
    /**
     * Consume rejection on the shared queue continuation so one failed recovery
     * cannot permanently block outputs belonging to later runs.
     */
    harvestQueue = task.catch(() => undefined)
    return task
  }

  /**
   * fetchOutputWithRetry downloads an output with at most three attempts. Client
   * HTTP failures stop immediately; remaining failures wait with bounded
   * exponential delay between attempts and propagate the final error to
   * harvesting.
   */
  async function fetchOutputWithRetry(
    runId: string,
    outputIndex: number,
    workspaceId: string,
  ): Promise<Blob> {
    const maxAttempts = 3
    for (let attempt = 1; attempt <= maxAttempts; attempt++) {
      try {
        return await getCreativeRunOutputContent(runId, outputIndex, workspaceId)
      } catch (error) {
        const status = Number(
          (
            error as {
              status?: unknown // optional HTTP status code
            }
          )?.status,
        )
        /**
         * Client-status errors describe unavailable or invalid requests that should
         * stop immediately; other failures use bounded retries before reporting this
         * output missing.
         */
        if ((Number.isFinite(status) && status >= 400 && status < 500) || attempt === maxAttempts)
          throw error
        /**
         * Wait with exponential backoff between transient failures, limiting attempts
         * so one unavailable output does not hold the shared recovery queue
         * indefinitely.
         */
        await new Promise<void>((resolve) => setTimeout(resolve, 250 * 2 ** (attempt - 1)))
      }
    }
    throw new Error('creative output download failed')
  }

  /**
   * harvestOutputsNow recovers successful outputs in order and saves their bytes
   * before server acknowledgement. Captured workspace identity prevents obsolete
   * recovery from updating a different browser workspace after awaited downloads
   * or writes. Acknowledged outputs missing from the supplied local index are
   * marked unavailable because the transient server copy may be gone.
   *
   * Existing local assets are reused and acknowledgement failures retain those
   * usable bytes. Canvas placement is awaited when requested, but its failure is
   * logged separately from storage recovery. Per-output failures mark missing
   * entries and allow later outputs to continue; final index merging retains
   * assets published by other completed recovery work.
   */
  async function harvestOutputsNow(run: CreativeRun, options: HarvestOptions = {}): Promise<void> {
    const outputs = Array.isArray(run.outputs) ? run.outputs : []
    const activeWorkspaceId = options.workspaceId ?? readWorkspaceId()
    const activeWorkspaceGeneration = options.workspaceGeneration ?? workspaceGeneration
    const assets = options.assets ?? new Map(outputAssetMap.value)
    const missing = options.missing ?? new Set(missingOutputKeys.value)
    for (const output of outputs) {
      /**
       * Check history ownership before each output, then verify workspace identity
       * independently so stale requests cannot begin recovery for a replacement
       * browser workspace.
       */
      if (options.isCurrent && !options.isCurrent()) return
      if (!isWorkspaceCurrent(activeWorkspaceId, activeWorkspaceGeneration)) return
      if (output.status !== 'succeeded') continue
      const key = outputAssetKey(run.id, output.output_index)
      /**
       * An acknowledged server output may have lost its transient bytes, so absence
       * from the local working index becomes an unavailable-image placeholder.
       */
      if (output.acked_at && !assets.has(key)) {
        missing.add(key)
        continue
      }
      try {
        /**
         * A history working copy can predate completed recovery; reuse the published
         * asset index before downloading the same output again.
         */
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
          /**
           * Save image bytes before acknowledging their transient server copy, then
           * recheck workspace ownership before adding the recovered record to current
           * indexes.
           */
          await saveAsset(asset)
          if (!isWorkspaceCurrent(activeWorkspaceId, activeWorkspaceGeneration)) return
          assets.set(key, asset)
          /**
           * Publish a newly saved image immediately for the current refresh so the user
           * can see it before the remaining history work completes.
           */
          if (!options.isCurrent || options.isCurrent()) {
            const nextMap = new Map(outputAssetMap.value)
            nextMap.set(key, asset)
            outputAssetMap.value = nextMap
          }
        }

        if (!output.acked_at) {
          try {
            if (!isWorkspaceCurrent(activeWorkspaceId, activeWorkspaceGeneration)) return
            await ackCreativeRunOutput(run.id, output.output_index, activeWorkspaceId)
          } catch (e) {
            /**
             * Acknowledgement failure leaves the local image usable and permits a later
             * history refresh to retry acknowledgement without downloading the bytes again.
             */
            console.error(`Failed to ack creative output ${key}:`, e)
          }
        }

        if (options.placeOnCanvas) {
          try {
            /**
             * Wait for each bridge placement before moving to the next output so automatic
             * layout can observe the previous image's completed insertion.
             */
            await canvasBridge?.placeOutput({
              blob: asset.blob,
              runId: run.id,
              outputIndex: output.output_index,
            })
          } catch (e) {
            /**
             * Canvas placement failure does not invalidate saved image bytes or a
             * successful acknowledgement, so history can still offer the recovered output.
             */
            console.error(`Failed to place creative output ${key} on canvas:`, e)
          }
        }
        missing.delete(key)
      } catch (e) {
        /**
         * A download or save failure marks this output unavailable while preserving
         * other recovered assets and allowing the loop to continue.
         */
        console.error(`Failed to harvest creative output ${key}:`, e)
        if (e instanceof LocalStoreQuotaError) {
          error.value = t('creative.error.quotaExceeded')
        }
        missing.add(key)
      }
    }
    if (!options.assets) {
      /**
       * Merge with published assets after recovery rather than replacing them with an
       * older working map that could omit another completed run.
       */
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

  /**
   * upsertRunInHistory updates the shared history entry matching an accepted task
   * identifier. Existing tasks keep their list position, while newly accepted
   * tasks are prepended for immediate visibility in the composer and sidebar.
   */
  function upsertRunInHistory(run: CreativeRun): void {
    const index = runHistory.value.findIndex((r) => r.id === run.id)
    if (index >= 0) runHistory.value.splice(index, 1, run)
    else runHistory.value.unshift(run)
  }

  /**
   * refreshHistory merges recent tasks, all available active tasks, and
   * browser-local outputs for the current workspace. Each refresh captures a
   * generation so newer requests or workspace changes can invalidate its
   * asynchronous publication. Previously observed terminal states take precedence
   * over conflicting snapshots, and tracked successful tasks retain their
   * automatic placement intent.
   *
   * Local parameter reads preserve newly submitted in-memory records, and harvest
   * results merge with assets published during refresh. A complete active query
   * can retire absent tracking entries; an incomplete query retains them for
   * another attempt. The latest refresh alone schedules the next poll and clears
   * loading state.
   */
  async function refreshHistory(): Promise<void> {
    /**
     * Cancel the pending timer before starting any manual or scheduled refresh; the
     * latest completing request owns scheduling the next shared poll.
     */
    clearPollingTimer()
    const generation = ++historyRefreshGeneration
    loadingHistory.value = true
    try {
      const requestWorkspaceId = readWorkspaceId()
      const requestWorkspaceGeneration = workspaceGeneration
      const page = await getCreativeRuns(requestWorkspaceId, 1, 20)
      const activeByID = new Map<string, CreativeRun>()
      let activeFetchComplete = false
      if (typeof getCreativeActiveRuns === 'function') {
        let cursor: string | undefined
        try {
          do {
            const activePage = await getCreativeActiveRuns(requestWorkspaceId, cursor, 100)
            for (const run of activePage.items) activeByID.set(run.id, run)
            cursor =
              activePage.has_more && activePage.next_cursor ? activePage.next_cursor : undefined
          } while (cursor)
          activeFetchComplete = true
        } catch (activeError) {
          /**
           * A partial active-list failure retains the recent history snapshot and
           * existing tracking so another poll can retry discovering older active tasks.
           */
          console.error('Failed to refresh creative active runs:', activeError)
        }
      }
      const items = [...page.items]
      for (const activeRun of activeByID.values()) {
        const index = items.findIndex((run) => run.id === activeRun.id)
        if (index >= 0) items[index] = activeRun
        else items.push(activeRun)
      }
      if (
        generation !== historyRefreshGeneration ||
        !isWorkspaceCurrent(requestWorkspaceId, requestWorkspaceGeneration)
      )
        return
      /**
       * Previously observed terminal statuses take precedence over conflicting list
       * snapshots so a stale response cannot restart a completed task's visible
       * progress.
       */
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
      /**
       * Stop tracked terminal tasks before recovery and retain their placement
       * intent, preventing another polling cycle from duplicating automatic output
       * placement.
       */
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
            if (
              generation !== historyRefreshGeneration ||
              !isWorkspaceCurrent(requestWorkspaceId, requestWorkspaceGeneration)
            )
              return
          }
        }
      }
      for (const run of mergedItems) {
        if (!CREATIVE_RUN_TERMINAL_STATUSES.includes(run.status) && !pollStates.has(run.id)) {
          startPolling(run.id)
        }
      }
      /**
       * Only a complete active listing can retire absent tracked tasks; partial
       * listings retain them so interrupted pagination does not silently stop their
       * polling.
       */
      for (const runID of [...pollStates.keys()]) {
        const known = mergedItems.find((run) => run.id === runID)
        if (known && CREATIVE_RUN_TERMINAL_STATUSES.includes(known.status)) stopPolling(runID)
        else if (!known && activeFetchComplete) stopPolling(runID)
      }
      const outputs = await listAssets('output')
      const map = new Map(outputs.map((a) => [a.key, a]))
      const storedRunParams = await loadStoredRunParams()
      if (
        generation !== historyRefreshGeneration ||
        !isWorkspaceCurrent(requestWorkspaceId, requestWorkspaceGeneration)
      )
        return
      if (storedRunParams) {
        /**
         * Preserve newly submitted in-memory parameters when their asynchronous writes
         * have not yet appeared in the local snapshot read for this refresh.
         */
        for (const [runId, params] of runParamsMap.value) {
          if (!storedRunParams.has(runId)) storedRunParams.set(runId, params)
        }
        runParamsMap.value = storedRunParams
      }
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
          if (
            generation !== historyRefreshGeneration ||
            !isWorkspaceCurrent(requestWorkspaceId, requestWorkspaceGeneration)
          )
            return
        }
        for (const output of run.outputs ?? []) {
          if (output.status !== 'succeeded') continue
          const key = outputAssetKey(run.id, output.output_index)
          if (!map.has(key)) {
            missing.add(key)
          }
        }
      }
      if (
        generation !== historyRefreshGeneration ||
        !isWorkspaceCurrent(requestWorkspaceId, requestWorkspaceGeneration)
      )
        return
      /**
       * Include images published during this history refresh before assigning its
       * final index, then remove missing markers for every available asset.
       */
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
      /**
       * History failures retain the error for display; unavailable workspace storage
       * additionally clears visible task state and stops shared polling.
       */
      console.error('Failed to refresh creative history:', e)
      if (e instanceof LocalStoreError && e.type === 'unavailable') {
        workspaceId = null
        resetWorkspaceState()
      }
      error.value = creativeErrorMessage(e, 'creative.error.historyFailed')
    } finally {
      /**
       * Only the latest refresh may clear loading and schedule another poll,
       * preventing older completions from replacing the current request's lifecycle
       * decisions.
       */
      if (generation === historyRefreshGeneration) schedulePollingRefresh()
      if (generation === historyRefreshGeneration) {
        loadingHistory.value = false
      }
    }
  }

  /**
   * loadStoredRunParams returns the local parameter index or null when reading
   * fails. Errors are logged so history can still display server metadata and
   * retain parameters already available in memory.
   */
  async function loadStoredRunParams(): Promise<Map<string, LocalRunParams> | null> {
    try {
      return await loadRunParamsMap()
    } catch (e) {
      /**
       * Parameter-read failure is isolated from task history so server metadata
       * remains visible and existing in-memory submission details are retained.
       */
      console.error('Failed to load creative run params:', e)
      return null
    }
  }

  /**
   * clearLocalData stops polling and invalidates outstanding history and
   * workspace generations before clearing browser records. Local assets, scene
   * snapshots, and settings are cleared through the storage helper, then a newly
   * stored workspace identifier hides previous server history from this browser.
   * Successful clearing resets the visible indexes. Storage or identity rotation
   * failures publish an error and rethrow, allowing the view to report failure
   * even when some earlier clearing steps already succeeded.
   */
  async function clearLocalData(): Promise<void> {
    stopPolling()
    clearPollingTimer()
    /**
     * Invalidate outstanding history and recovery before clearing local records so
     * delayed downloads cannot repopulate indexes for the previous workspace.
     */
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

  /**
   * extractErrorMessage returns a string message from an unknown error or empty
   * text.
   */
  function extractErrorMessage(e: unknown): string {
    const message = (
      e as {
        message?: unknown // optional failure message text
      }
    )?.message
    return typeof message === 'string' ? message : ''
  }

  /**
   * creativeErrorMessage chooses a workspace-specific message for unavailable
   * local storage. Other failures use their string message when present,
   * otherwise the supplied translation key supplies the operation-specific user
   * feedback.
   */
  function creativeErrorMessage(e: unknown, fallbackKey: string): string {
    if (e instanceof LocalStoreError && e.type === 'unavailable') {
      return t('creative.error.workspaceUnavailable')
    }
    return extractErrorMessage(e) || t(fallbackKey)
  }

  /**
   * Stop shared polling, invalidate history publication, and detach browser
   * listeners on unmount; the final form flush remains a best-effort asynchronous
   * write.
   */
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
    outputFormat,
    frameCount,
    frameDurationMS,
    outputFormatOptions,
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
    runParamsMap,
    operationOptions,
    imageSizeOptions,
    aspectRatioOptions,
    qualityOptions,
    backgroundOptions,
    thinkingLevelOptions,
    maxReferenceImages,
    estimatedCost,
    canGenerate,
    loadModels,
    selectOption,
    createRun,
    refreshHistory,
    clearLocalData,
    registerCanvasBridge,
    importOutputToCanvas,
    applyRunParams,
  }
}
