import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { keysAPI } from '@/api/keys'
import { userGroupsAPI } from '@/api/groups'
import { teamAPI, type TeamAPIKey } from '@/api/team'
import { usageAPI, type TrendParams } from '@/api/usage'
import type { SelectOption } from '@/components/common/Select.vue'
import type { ModelStat, UsageRequestType } from '@/api/usage.types'
import { requestTypeToLegacyStream } from '@/utils/usageRequestType'
import { formatQueryTime, type UsageRange } from './usageChartData'
import {
  BILLING_MODE_ICONS,
  BILLING_TYPE_ICONS,
  COMPACTION_ICONS,
  REQUEST_TYPE_ICONS,
  withOptionIcons,
} from '@/components/common/filterOptionIcons'

// useUsageChartFilters.ts manages dashboard usage filters and the available key, group, and
// model choices. Query parameters retain the distinction between an unrestricted null value
// and selected false or zero values. Request types also supply a compatible stream boolean
// when the shared converter can represent them.
//
// Personal and team records are merged by identity, while missing team membership permits
// personal options to load. Model choices use unfiltered usage for the selected interval and
// preserve a selected name that no longer appears in its results. Sequence checks discard
// older model responses, and request failures retain existing choices for the current page.

// UsageChartFilters stores optional dashboard restrictions corresponding to usage-list query
// fields.
export interface UsageChartFilters {
  // All conditions use null for unrestricted queries. billing_type keeps numeric zero as
  // balance billing, and native_compaction_v2 keeps false as a selected legacy-compaction
  // condition.
  api_key_id: number | null // Selected API-key identity.
  model: string | null // Selected request model name.
  group_id: number | null // Selected routing-group identity.
  request_type: UsageRequestType | null // Selected request transport category.
  billing_type: number | null // Balance or subscription category.
  billing_mode: string | null // Selected pricing calculation mode.
  native_compaction_v2: boolean | null // Selected compaction generation flag.
}

// NamedOption carries an API record identity and its displayed selection label.
type NamedOption = {
  id: number // API record identity.
  name: string // Displayed selection label.
  brand?: string // Group badge brand drawn as the filter-row icon.
}

// emptyFilters returns a fresh unrestricted filter object for initialization or reset.
const emptyFilters = (): UsageChartFilters => ({
  api_key_id: null,
  model: null,
  group_id: null,
  request_type: null,
  billing_type: null,
  billing_mode: null,
  native_compaction_v2: null,
})

// isTeamMissing identifies responses that allow filter loading to continue with personal
// keys. Other team failures propagate to the option loader and leave its previous candidates
// available.
const isTeamMissing = (error: any): boolean =>
  error?.reason === 'TEAM_NOT_FOUND' ||
  error?.reason === 'TEAM_MEMBERSHIP_REQUIRED' ||
  error?.response?.status === 404

// useUsageChartFilters creates dashboard query conditions and their localized candidate
// lists during component setup. Personal and team keys are merged by ID, and team-bound
// groups supplement available personal groups. Model candidates come from an unfiltered time
// range and retain the selected model even when it has no returned usage. A separate request
// sequence prevents an older model candidate query from replacing a more recent one.
export function useUsageChartFilters() {
  // Translation, filter values, and candidate collections belong to this dashboard instance
  // and feed the computed dropdown options below.
  const { t } = useI18n()
  const filters = ref<UsageChartFilters>(emptyFilters())
  const apiKeys = ref<NamedOption[]>([])
  const groups = ref<NamedOption[]>([])
  const modelNames = ref<string[]>([])

  // activeCount counts selected non-null conditions, including zero billing type and false
  // compaction, to decide whether range-wide models need a separate request.
  const activeCount = computed(
    () => Object.values(filters.value).filter((value) => value !== null).length,
  )

  // queryParams omits unrestricted values and supplies the legacy stream boolean alongside
  // request_type when that conversion has a boolean representation.
  const queryParams = computed<Partial<TrendParams>>(() => {
    const params: Partial<TrendParams> = {}
    const current = filters.value
    if (current.api_key_id !== null) params.api_key_id = current.api_key_id
    if (current.model) params.model = current.model
    if (current.group_id !== null) params.group_id = current.group_id
    if (current.request_type) {
      params.request_type = current.request_type
      // Compatibility callers still consume the stream boolean alongside request_type;
      // transports without a boolean representation leave that older parameter absent.
      const stream = requestTypeToLegacyStream(current.request_type)
      if (typeof stream === 'boolean') params.stream = stream
    }
    if (current.billing_type !== null) params.billing_type = current.billing_type
    if (current.billing_mode) params.billing_mode = current.billing_mode
    if (current.native_compaction_v2 !== null)
      params.native_compaction_v2 = current.native_compaction_v2
    return params
  })

  // Dropdown options prepend an unrestricted choice and derive labels from the loaded
  // records or the current locale.
  const apiKeyOptions = computed<SelectOption[]>(() => [
    { value: null, label: t('usage.allApiKeys') },
    ...apiKeys.value.map((key) => ({ value: key.id, label: key.name })),
  ])
  const modelOptions = computed<SelectOption[]>(() => [
    { value: null, label: t('admin.usage.allModels') },
    ...modelNames.value.map((name) => ({ value: name, label: name })),
  ])
  const groupOptions = computed<SelectOption[]>(() => [
    { value: null, label: t('admin.usage.allGroups') },
    ...groups.value.map((group) => ({ value: group.id, label: group.name, brand: group.brand })),
  ])
  const requestTypeOptions = computed<SelectOption[]>(() =>
    withOptionIcons(
      [
        { value: null, label: t('admin.usage.allTypes') },
        { value: 'ws_v2', label: t('usage.ws') },
        { value: 'live', label: t('usage.live') },
        { value: 'stream', label: t('usage.stream') },
        { value: 'sync', label: t('usage.sync') },
      ],
      REQUEST_TYPE_ICONS,
    ),
  )
  const billingTypeOptions = computed<SelectOption[]>(() =>
    withOptionIcons(
      [
        { value: null, label: t('admin.usage.allBillingTypes') },
        { value: 0, label: t('admin.usage.billingTypeBalance') },
        { value: 1, label: t('admin.usage.billingTypeSubscription') },
      ],
      BILLING_TYPE_ICONS,
    ),
  )
  const billingModeOptions = computed<SelectOption[]>(() =>
    withOptionIcons(
      [
        { value: null, label: t('admin.usage.allBillingModes') },
        { value: 'token', label: t('admin.usage.billingModeToken') },
        { value: 'per_request', label: t('admin.usage.billingModePerRequest') },
        { value: 'image', label: t('admin.usage.billingModeImage') },
        { value: 'video', label: t('admin.usage.billingModeVideo') },
      ],
      BILLING_MODE_ICONS,
    ),
  )
  const compactionOptions = computed<SelectOption[]>(() =>
    withOptionIcons(
      [
        { value: null, label: t('usage.allCompactions') },
        { value: true, label: t('usage.nativeCompactionV2') },
        { value: false, label: t('usage.legacyCompaction') },
      ],
      COMPACTION_ICONS,
    ),
  )

  // loadKeyAndGroupOptions gathers personal keys, available groups, and optional team keys
  // for the dashboard filters. A missing team allows personal results to proceed, while
  // other request failures retain the existing option arrays. Keys and groups are merged by
  // numeric identity so shared records appear once. Team key group names supplement the
  // group candidates, allowing team-associated usage to remain selectable even when those
  // groups are absent from the personal list.
  const loadKeyAndGroupOptions = async () => {
    try {
      const [personalKeys, availableGroups] = await Promise.all([
        keysAPI.list(1, 100, { scope: 'personal' }),
        userGroupsAPI.getAvailable('personal', undefined, false),
      ])
      let teamKeys: TeamAPIKey[] = []
      try {
        await teamAPI.current()
        teamKeys = await teamAPI.keys()
      } catch (error) {
        // Missing team membership is expected for personal accounts, so personal choices can
        // publish; other failures preserve the previous option collections.
        if (!isTeamMissing(error)) throw error
      }

      const keyMap = new Map<number, NamedOption>()
      for (const key of [...personalKeys.items, ...teamKeys])
        keyMap.set(key.id, { id: key.id, name: key.name })
      apiKeys.value = [...keyMap.values()]

      const groupMap = new Map<number, NamedOption>()
      for (const group of availableGroups)
        groupMap.set(group.id, { id: group.id, name: group.name, brand: group.display_brand })
      for (const key of teamKeys) {
        if (key.group_id && key.group_name)
          groupMap.set(key.group_id, { id: key.group_id, name: key.group_name })
      }
      groups.value = [...groupMap.values()]
    } catch (error) {
      console.error('Failed to load usage filter options:', error)
    }
  }

  // setModelOptions replaces sorted model names from a successful usage response. The
  // currently selected model is retained even if absent from those results so the filter can
  // still display and clear it.
  const setModelOptions = (models: ModelStat[]) => {
    const names = new Set(models.map((item) => item.model).filter(Boolean))
    if (filters.value.model) names.add(filters.value.model)
    modelNames.value = [...names].sort()
  }

  // modelSeq orders unfiltered model-candidate requests independently from the trend queries
  // that use the selected conditions.
  let modelSeq = 0

  // loadModelOptions fetches unfiltered model usage for the supplied time range and updates
  // candidates. Each invocation captures a sequence, and successful responses publish only
  // while that sequence remains current. The query omits all selected filters so one
  // selected model does not narrow its own choices. Failed requests log the error and retain
  // the existing names, including the current selection, until another successful candidate
  // load.
  const loadModelOptions = async (range: UsageRange) => {
    const seq = ++modelSeq
    try {
      const response = await usageAPI.getDashboardModels({
        start_date: formatQueryTime(range.startAt),
        end_date: formatQueryTime(range.endAt),
      })
      // A later interval query owns model candidates now, so reject an older response before
      // it replaces choices or reattaches the selected model to obsolete results.
      if (seq !== modelSeq) return
      setModelOptions(response.models || [])
    } catch (error) {
      // A candidate query failure leaves the previous names available for the filter control,
      // including its selected model, until another successful load can replace them.
      console.error('Failed to load usage model options:', error)
    }
  }

  // reset replaces all selected conditions with a fresh unrestricted filter object.
  const reset = () => {
    filters.value = emptyFilters()
  }

  return {
    filters,
    activeCount,
    queryParams,
    apiKeyOptions,
    modelOptions,
    groupOptions,
    requestTypeOptions,
    billingTypeOptions,
    billingModeOptions,
    compactionOptions,
    loadKeyAndGroupOptions,
    setModelOptions,
    loadModelOptions,
    reset,
  }
}
