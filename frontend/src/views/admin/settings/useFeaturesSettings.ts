import { ref, computed, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api'
import type {
  CreativeModelCandidate,
  CreativeModelSetting,
  CreativeWorkerStatus,
} from '@/api/admin/settings'
import type { CreativeOperation } from '@/api/creative'
import type { SettingsForm } from './settingsState'
import type { Ref } from 'vue'
import type { SettingsTab } from './useNavigationSettings'

/**
 * useFeaturesSettings.ts manages creative model selection and worker status for the
 * feature settings panel. The shared form owns selected group/model pairs and operations,
 * while the candidate response supplies currently available capabilities. Row choices
 * retain an unavailable current selection for display, and save normalization
 * distinguishes known candidates with removed operations from historical rows missing from
 * the response.
 *
 * Worker polling belongs to the page controller and runs every five seconds while the
 * features tab is active. Leaving the tab or unmounting clears the interval; requests
 * already started may still publish their results. Failed status reads retain the previous
 * snapshot, and usage calculations combine that snapshot with the configured worker count
 * when runtime capacity is unavailable.
 */

/**
 * FeaturesSettingsForm selects feature flags, affiliate values, and creative settings from
 * the page draft.
 */
export type FeaturesSettingsForm = Pick<
  SettingsForm,
  | 'affiliate_admin_recharge_enabled'
  | 'affiliate_enabled'
  | 'affiliate_rebate_duration_days'
  | 'affiliate_rebate_freeze_hours'
  | 'affiliate_rebate_per_invitee_cap'
  | 'affiliate_rebate_rate'
  | 'creative_enabled'
  | 'creative_model_settings'
  | 'creative_worker_count'
  | 'cyber_session_block_enabled'
  | 'cyber_session_block_ttl_seconds'
  | 'risk_control_enabled'
  | 'bstats_enabled'
  | 'bstats_log_errors'
  | 'bstats_log_sent_data'
  | 'bstats_log_response_status_text'
  | 'team_enabled'
>

/**
 * useFeaturesSettings binds creative selection and worker observation to the shared form
 * and active tab. Invoke it during component setup so its immediate watcher starts polling
 * when appropriate and its unmount hook clears the interval. Selected model rows remain in
 * the caller-owned draft while fetched candidates and status snapshots live in this
 * controller.
 *
 * Candidate availability guides option filtering and save normalization, including
 * historical selections absent from current results. Status requests can finish after
 * polling stops; the controller retains their snapshots and does not sequence overlapping
 * responses. Consumers use the returned refs for loading and usage display throughout the
 * page lifetime.
 */
export function useFeaturesSettings(form: FeaturesSettingsForm, activeTab: Ref<SettingsTab>) {
  // Unavailable row labels follow the current interface locale when candidate
  // lookup results no longer contain a configured group and model.
  const { t } = useI18n()

  // This order governs displayed capability buttons and normalized operation
  // arrays, including recognized operations retained for unavailable historical rows.
  const creativeOperationChoices: CreativeOperation[] = ['generate', 'edit', 'inpaint']

  // Candidate data and request indicators belong to this controller; a failed
  // refresh exposes an error while leaving the previous candidates available.
  const creativeModelCandidates = ref<CreativeModelCandidate[]>([])

  const creativeModelCandidatesLoading = ref(false)

  const creativeModelCandidatesError = ref(false)

  // Status snapshots drive the usage display independently from draft edits;
  // the timer exists only while the features tab is active.
  const creativeWorkerStatus = ref<CreativeWorkerStatus | null>(null)

  // A null handle means polling is stopped; clearing it permits tab reentry
  // to install another interval while earlier requests may still complete.
  let creativeWorkerStatusTimer: number | null = null

  // Usage combines running capacity with draft capacity when status is missing
  // or stopped, then bounds busy counts and percentages for the progress bar.
  const creativeWorkerUsageTotal = computed(() => {
    const status = creativeWorkerStatus.value
    if (status?.running && status.worker_count > 0) {
      return status.worker_count
    }
    // Stopped or unavailable runtime capacity uses the draft count for preview;
    // nonnumeric and negative values display zero until save validation reports them.
    return Math.max(0, Math.floor(Number(form.creative_worker_count)) || 0)
  })

  const creativeWorkerUsageBusy = computed(() => {
    const status = creativeWorkerStatus.value
    if (!status?.running) {
      return 0
    }
    // Busy observations may disagree with the chosen total; clamp the display
    // to its current capacity so the usage bar remains representable.
    return Math.min(Math.max(status.busy_workers, 0), creativeWorkerUsageTotal.value)
  })

  const creativeWorkerUsagePercent = computed(() => {
    const total = creativeWorkerUsageTotal.value
    if (total <= 0) {
      return 0
    }
    // The progress bar consumes a whole percentage.
    return Math.min(Math.round((creativeWorkerUsageBusy.value / total) * 100), 100)
  })

  const creativeWorkerUsageText = computed(
    () => `${creativeWorkerUsageBusy.value}/${creativeWorkerUsageTotal.value}`,
  )

  /**
   * loadCreativeWorkerStatus replaces the worker snapshot after a successful status
   * request. Failures retain the previous snapshot silently; requests are independent and
   * may finish after the tab leaves features or the interval stops.
   */
  async function loadCreativeWorkerStatus() {
    try {
      creativeWorkerStatus.value = await adminAPI.settings.getCreativeWorkerStatus()
    } catch {
      // Status reads are observational: keep the last successful snapshot after
      // a polling failure so editing remains available without repeated error toasts.
    }
  }

  /**
   * startCreativeWorkerStatusPolling requests status immediately and installs an
   * interval of five seconds when none exists. Repeated starts reuse the current interval; requests
   * can overlap because the timer does not await completion.
   */
  function startCreativeWorkerStatusPolling() {
    if (creativeWorkerStatusTimer !== null) {
      return
    }
    void loadCreativeWorkerStatus()
    // Five seconds is the refresh cadence for this tab; a tick can start a
    // new observation before an earlier request has returned its snapshot.
    creativeWorkerStatusTimer = window.setInterval(() => {
      void loadCreativeWorkerStatus()
    }, 5000)
  }

  /**
   * stopCreativeWorkerStatusPolling clears the owned interval and resets its handle for
   * later restarts. Calling it while stopped has no effect; an already-started status
   * request can still update the snapshot.
   */
  function stopCreativeWorkerStatusPolling() {
    if (creativeWorkerStatusTimer === null) {
      return
    }
    window.clearInterval(creativeWorkerStatusTimer)
    creativeWorkerStatusTimer = null
  }

  // Hidden settings panels remain mounted, so tab selection controls polling
  // independently from component lifetime and the persisted creative-enabled switch.
  watch(
    activeTab,
    (tab) => {
      if (tab === 'features') {
        startCreativeWorkerStatusPolling()
      } else {
        stopCreativeWorkerStatusPolling()
      }
    },
    { immediate: true },
  )

  // Page teardown stops future timer ticks; already-started requests retain
  // their ordinary completion path because this controller owns no abort handle.
  onUnmounted(() => {
    stopCreativeWorkerStatusPolling()
  })

  /**
   * creativeModelSettingKey identifies a configured model within its numeric group.
   */
  function creativeModelSettingKey(item: Pick<CreativeModelSetting, 'group_id' | 'model'>): string {
    return `${item.group_id}::${item.model}`
  }

  // Candidate choices preserve response group order and insert disabled group
  // headers, while each selectable row carries its capability record for inspection.
  const creativeModelCandidateOptions = computed(() => {
    // value and label identify a selector row; kind and disabled mark group
    // headings. candidate is present for selectable rows and carries fetched capabilities.
    const options: Array<{
      value: string // group/model selection key
      label: string // model or group label
      kind?: string // group header marker
      disabled?: boolean // header selection disabled
      candidate?: CreativeModelCandidate // selectable capability source record
    }> = []
    const groups = new Map<number, CreativeModelCandidate[]>()
    for (const candidate of creativeModelCandidates.value) {
      const items = groups.get(candidate.group_id) ?? []
      items.push(candidate)
      groups.set(candidate.group_id, items)
    }
    for (const [groupID, candidates] of groups) {
      // A missing display name uses the numeric group identity so headers still
      // distinguish candidates belonging to different groups in the selector.
      const groupName = candidates[0]?.group_name || String(groupID)
      options.push({
        value: `__creative_group_${groupID}`,
        label: groupName,
        kind: 'group',
        disabled: true,
      })
      for (const candidate of candidates) {
        options.push({
          value: creativeModelSettingKey(candidate),
          label: `${candidate.model} / ${candidate.platform}`,
          candidate,
        })
      }
    }
    return options
  })

  /**
   * creativeModelOptionsForRow returns grouped candidates suitable for the indexed
   * editable row. It removes candidates used by other rows while retaining the current
   * value and group headings. If the current pair is absent from fetched candidates, a
   * disabled stale choice displays that configured value so the administrator can identify
   * the unavailable selection.
   *
   * The comparison uses the combined group/model key, allowing identical model names in
   * separate groups. This method reads the shared draft and candidate refs without
   * rewriting either, so rendering choices leaves historical selections available for
   * later validation.
   */
  function creativeModelOptionsForRow(index: number) {
    const current = form.creative_model_settings[index]
    const used = new Set(
      form.creative_model_settings
        .filter((_, itemIndex) => itemIndex !== index)
        .map((item) => creativeModelSettingKey(item)),
    )
    const currentKey = current ? creativeModelSettingKey(current) : ''
    const options = creativeModelCandidateOptions.value.filter(
      (option) => option.kind === 'group' || option.value === currentKey || !used.has(option.value),
    )
    if (
      current &&
      !creativeModelCandidates.value.some(
        (candidate) => creativeModelSettingKey(candidate) === currentKey,
      )
    ) {
      // Keep an unavailable current pair visible as a disabled choice so the
      // administrator can identify the stored selection before deciding to replace it.
      options.unshift({
        value: currentKey,
        label: `${current.group_id} / ${current.model} (${t('admin.settings.features.creative.modelSettings.unavailable')})`,
        kind: 'stale',
        disabled: true,
      })
    }
    return options
  }

  /**
   * creativeCandidateForSetting finds current capability data for a configured group/model
   * pair. An absent candidate returns undefined, which consumers use to show an
   * unavailable row or apply historical-row normalization rules.
   */
  function creativeCandidateForSetting(
    item: CreativeModelSetting,
  ): CreativeModelCandidate | undefined {
    const key = creativeModelSettingKey(item)
    return creativeModelCandidates.value.find(
      (candidate) => creativeModelSettingKey(candidate) === key,
    )
  }

  /**
   * creativeOperationSupported reports whether the row's current candidate advertises an
   * operation. Missing rows and unavailable candidates return false, so their operation
   * controls remain disabled until usable capability data exists.
   */
  function creativeOperationSupported(index: number, operation: CreativeOperation): boolean {
    const item = form.creative_model_settings[index]
    if (!item) return false
    return creativeCandidateForSetting(item)?.operations.includes(operation) ?? false
  }

  /**
   * creativeOperationCheckboxDisabled disables unsupported operations and removal of a
   * row's last selected operation. The button layer enforces that minimum; callers
   * invoking toggleCreativeOperation directly still need to respect it themselves.
   */
  function creativeOperationCheckboxDisabled(index: number, operation: CreativeOperation): boolean {
    const item = form.creative_model_settings[index]
    if (!item || !creativeOperationSupported(index, operation)) return true
    return item.operations.length <= 1 && item.operations.includes(operation)
  }

  /**
   * onCreativeModelSelected replaces a row's group, model, and operation selection from a
   * known candidate. Nonstring values, unknown candidate keys, and missing rows are
   * ignored; operations are copied into a separate array.
   */
  function onCreativeModelSelected(index: number, value: string | number | boolean | null) {
    if (typeof value !== 'string') return
    const candidate = creativeModelCandidates.value.find(
      (item) => creativeModelSettingKey(item) === value,
    )
    if (!candidate) return
    const row = form.creative_model_settings[index]
    if (!row) return
    row.group_id = candidate.group_id
    row.model = candidate.model
    row.operations = [...candidate.operations]
  }

  /**
   * addCreativeModelSetting appends the first candidate whose group/model pair is not
   * selected. It copies the candidate's operations into the new draft row and does nothing
   * when every candidate is already used.
   */
  function addCreativeModelSetting() {
    const candidate = creativeModelCandidates.value.find(
      (item) =>
        !form.creative_model_settings.some(
          (setting) => creativeModelSettingKey(setting) === creativeModelSettingKey(item),
        ),
    )
    if (!candidate) return
    form.creative_model_settings.push({
      group_id: candidate.group_id,
      model: candidate.model,
      operations: [...candidate.operations],
    })
  }

  /**
   * removeCreativeModelSetting removes the creative model row at the supplied index.
   */
  function removeCreativeModelSetting(index: number) {
    form.creative_model_settings.splice(index, 1)
  }

  /**
   * toggleCreativeOperation adds or removes a supported operation and orders the result by
   * creativeOperationChoices. Missing rows or unsupported operations are ignored; callers
   * must use the button-disabled rule to prevent removing the final operation.
   */
  function toggleCreativeOperation(index: number, operation: CreativeOperation, checked: boolean) {
    const item = form.creative_model_settings[index]
    if (!item || !creativeOperationSupported(index, operation)) return
    const next = new Set(item.operations)
    if (checked) next.add(operation)
    else next.delete(operation)
    item.operations = creativeOperationChoices.filter((choice) => next.has(choice))
  }

  /**
   * normalizeCreativeModelSettingsForSave returns validated group/model selections with
   * operations in the supported display order. It requires positive safe group IDs and
   * nonempty trimmed model names, and duplicate normalized pairs return null. Known
   * candidates constrain selected operations to current capabilities; rows with no
   * remaining known capability are omitted.
   *
   * Historical rows absent from the candidate response retain selected recognized
   * operations, but an empty recognized set returns null. These distinct outcomes let
   * removed capabilities disappear while still rejecting unusable unknown rows. The
   * returned records and operation arrays are newly constructed, leaving editable rows
   * available for correction when validation fails.
   */
  function normalizeCreativeModelSettingsForSave(): CreativeModelSetting[] | null {
    const seen = new Set<string>()
    const normalized: CreativeModelSetting[] = []
    for (const item of form.creative_model_settings) {
      const groupID = Number(item.group_id)
      const model = String(item.model || '').trim()
      const candidate = creativeModelCandidates.value.find(
        (entry) =>
          creativeModelSettingKey(entry) === creativeModelSettingKey({ group_id: groupID, model }),
      )
      // Current candidates constrain operations to advertised capabilities; absent
      // candidates keep recognized selections so historical configuration can still be saved.
      const operations = creativeOperationChoices.filter(
        (operation) =>
          item.operations.includes(operation) &&
          (candidate?.operations.includes(operation) ?? true),
      )
      if (!Number.isSafeInteger(groupID) || groupID <= 0 || !model) {
        return null
      }
      if (operations.length === 0) {
        // A known row with removed capabilities is omitted; an unknown row
        // with no recognized operation cannot produce a usable request and rejects saving.
        if (candidate) continue
        return null
      }
      const key = creativeModelSettingKey({ group_id: groupID, model })
      if (seen.has(key)) return null
      seen.add(key)
      normalized.push({ group_id: groupID, model, operations })
    }
    return normalized
  }

  /**
   * loadCreativeModelCandidates refreshes the available creative group/model pairs and
   * capability lists. It resets the error indicator before loading; failure sets the
   * indicator while retaining earlier candidates, and loading clears when the request
   * settles.
   */
  async function loadCreativeModelCandidates() {
    creativeModelCandidatesLoading.value = true
    creativeModelCandidatesError.value = false
    try {
      creativeModelCandidates.value = await adminAPI.settings.getCreativeModelCandidates()
    } catch {
      // Retain earlier candidates after refresh failure so current rows remain
      // inspectable, while the warning tells the administrator that loading failed.
      creativeModelCandidatesError.value = true
    } finally {
      creativeModelCandidatesLoading.value = false
    }
  }
  return {
    creativeOperationChoices,
    creativeModelCandidates,
    creativeModelCandidatesLoading,
    creativeModelCandidatesError,
    creativeWorkerStatus,
    creativeWorkerUsageTotal,
    creativeWorkerUsageBusy,
    creativeWorkerUsagePercent,
    creativeWorkerUsageText,
    loadCreativeWorkerStatus,
    startCreativeWorkerStatusPolling,
    stopCreativeWorkerStatusPolling,
    creativeModelSettingKey,
    creativeModelCandidateOptions,
    creativeModelOptionsForRow,
    creativeCandidateForSetting,
    creativeOperationSupported,
    creativeOperationCheckboxDisabled,
    onCreativeModelSelected,
    addCreativeModelSetting,
    removeCreativeModelSetting,
    toggleCreativeOperation,
    normalizeCreativeModelSettingsForSave,
    loadCreativeModelCandidates,
  }
}
