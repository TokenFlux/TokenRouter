import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { keysAPI } from '@/api/keys'
import { userGroupsAPI } from '@/api/groups'
import { teamAPI, type TeamAPIKey } from '@/api/team'
import { usageAPI, type TrendParams } from '@/api/usage'
import type { SelectOption } from '@/components/common/Select.vue'
import type { ModelStat, UsageRequestType } from '@/types'
import { requestTypeToLegacyStream } from '@/utils/usageRequestType'
import { formatQueryTime, type UsageRange } from './usageChartData'

// UsageChartFilters 与使用记录页的筛选条件一一对应，null 表示不限。
export interface UsageChartFilters {
  api_key_id: number | null
  model: string | null
  group_id: number | null
  request_type: UsageRequestType | null
  billing_type: number | null
  billing_mode: string | null
  native_compaction_v2: boolean | null
}

type NamedOption = { id: number; name: string }

const emptyFilters = (): UsageChartFilters => ({
  api_key_id: null,
  model: null,
  group_id: null,
  request_type: null,
  billing_type: null,
  billing_mode: null,
  native_compaction_v2: null,
})

// isTeamMissing 判断当前用户是否没有团队，这种情况只使用个人密钥。
const isTeamMissing = (error: any): boolean => (
  error?.reason === 'TEAM_NOT_FOUND'
  || error?.reason === 'TEAM_MEMBERSHIP_REQUIRED'
  || error?.response?.status === 404
)

// useUsageChartFilters 管理用量图的筛选条件和候选项，候选项来源与使用记录页一致。
export function useUsageChartFilters() {
  const { t } = useI18n()
  const filters = ref<UsageChartFilters>(emptyFilters())
  const apiKeys = ref<NamedOption[]>([])
  const groups = ref<NamedOption[]>([])
  const modelNames = ref<string[]>([])

  const activeCount = computed(() => Object.values(filters.value).filter((value) => value !== null).length)

  // queryParams 只带上已选的条件；请求类型同时换算出旧版 stream 参数，与使用记录页一致。
  const queryParams = computed<Partial<TrendParams>>(() => {
    const params: Partial<TrendParams> = {}
    const current = filters.value
    if (current.api_key_id !== null) params.api_key_id = current.api_key_id
    if (current.model) params.model = current.model
    if (current.group_id !== null) params.group_id = current.group_id
    if (current.request_type) {
      params.request_type = current.request_type
      const stream = requestTypeToLegacyStream(current.request_type)
      if (typeof stream === 'boolean') params.stream = stream
    }
    if (current.billing_type !== null) params.billing_type = current.billing_type
    if (current.billing_mode) params.billing_mode = current.billing_mode
    if (current.native_compaction_v2 !== null) params.native_compaction_v2 = current.native_compaction_v2
    return params
  })

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
    ...groups.value.map((group) => ({ value: group.id, label: group.name })),
  ])
  const requestTypeOptions = computed<SelectOption[]>(() => [
    { value: null, label: t('admin.usage.allTypes') },
    { value: 'ws_v2', label: t('usage.ws') },
    { value: 'live', label: t('usage.live') },
    { value: 'stream', label: t('usage.stream') },
    { value: 'sync', label: t('usage.sync') },
  ])
  const billingTypeOptions = computed<SelectOption[]>(() => [
    { value: null, label: t('admin.usage.allBillingTypes') },
    { value: 0, label: t('admin.usage.billingTypeBalance') },
    { value: 1, label: t('admin.usage.billingTypeSubscription') },
  ])
  const billingModeOptions = computed<SelectOption[]>(() => [
    { value: null, label: t('admin.usage.allBillingModes') },
    { value: 'token', label: t('admin.usage.billingModeToken') },
    { value: 'per_request', label: t('admin.usage.billingModePerRequest') },
    { value: 'image', label: t('admin.usage.billingModeImage') },
    { value: 'video', label: t('admin.usage.billingModeVideo') },
  ])
  const compactionOptions = computed<SelectOption[]>(() => [
    { value: null, label: t('usage.allCompactions') },
    { value: true, label: t('usage.nativeCompactionV2') },
    { value: false, label: t('usage.legacyCompaction') },
  ])

  // loadKeyAndGroupOptions 读取个人与团队密钥，以及可用分组和团队密钥绑定的分组。
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
        if (!isTeamMissing(error)) throw error
      }

      const keyMap = new Map<number, NamedOption>()
      for (const key of [...personalKeys.items, ...teamKeys]) keyMap.set(key.id, { id: key.id, name: key.name })
      apiKeys.value = [...keyMap.values()]

      const groupMap = new Map<number, NamedOption>()
      for (const group of availableGroups) groupMap.set(group.id, { id: group.id, name: group.name })
      for (const key of teamKeys) {
        if (key.group_id && key.group_name) groupMap.set(key.group_id, { id: key.group_id, name: key.group_name })
      }
      groups.value = [...groupMap.values()]
    } catch (error) {
      console.error('Failed to load usage filter options:', error)
    }
  }

  // setModelOptions 根据模型统计刷新候选项，并保留当前选中的模型。
  const setModelOptions = (models: ModelStat[]) => {
    const names = new Set(models.map((item) => item.model).filter(Boolean))
    if (filters.value.model) names.add(filters.value.model)
    modelNames.value = [...names].sort()
  }

  // 用递增序号丢弃过期的模型候选响应。
  let modelSeq = 0

  // loadModelOptions 按时间范围读取用过的模型，其他筛选条件独立于候选查询。
  const loadModelOptions = async (range: UsageRange) => {
    const seq = ++modelSeq
    try {
      const response = await usageAPI.getDashboardModels({
        start_date: formatQueryTime(range.startAt),
        end_date: formatQueryTime(range.endAt),
      })
      if (seq !== modelSeq) return
      setModelOptions(response.models || [])
    } catch (error) {
      console.error('Failed to load usage model options:', error)
    }
  }

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
