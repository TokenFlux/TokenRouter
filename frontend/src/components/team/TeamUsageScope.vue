<template>
  <div class="space-y-6">
    <div v-if="loading" class="flex justify-center py-16"><LoadingSpinner /></div>
    <div v-else-if="noTeam" class="card py-14 text-center">
      <Icon name="users" size="xl" class="mx-auto text-gray-400" />
      <p class="mt-4 font-medium text-gray-900 dark:text-white">{{ t('team.noTeam') }}</p>
      <router-link to="/team" class="btn btn-primary mt-5">{{ t('team.title') }}</router-link>
    </div>
    <template v-else>
      <div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <div v-for="metric in metrics" :key="metric.label" class="card min-w-0 p-5">
          <p class="text-sm text-gray-500 dark:text-gray-400">{{ metric.label }}</p>
          <p class="mt-2 truncate text-xl font-semibold text-gray-900 dark:text-white">
            <BalanceAmount v-if="metric.amount !== undefined" :amount="metric.amount" :fraction-digits="4" icon-size="md" />
            <template v-else>{{ metric.value }}</template>
          </p>
        </div>
      </div>

      <div v-if="isOwner" class="card p-6">
        <div class="flex flex-wrap items-end gap-4">
          <div class="w-full sm:w-56">
            <label class="input-label">{{ t('team.keyOwner') }}</label>
            <Select v-model="memberID" :options="memberOptions" @change="reload" />
          </div>
          <div class="w-full sm:w-56">
            <label class="input-label">{{ t('team.keys') }}</label>
            <Select v-model="keyID" :options="keyOptions" @change="reload" />
          </div>
          <div class="ml-auto flex gap-3">
            <button type="button" class="btn btn-secondary" :disabled="logsLoading" @click="loadUsage">{{ t('common.refresh') }}</button>
            <button type="button" class="btn btn-secondary" @click="resetFilters">{{ t('common.reset') }}</button>
          </div>
        </div>
      </div>

      <div v-if="mode === 'dashboard'" class="card p-6">
        <h2 class="text-base font-semibold text-gray-900 dark:text-white">{{ t('team.totalCost') }}</h2>
        <div v-if="daily.length === 0" class="py-12 text-center text-sm text-gray-500">{{ t('team.noUsage') }}</div>
        <div v-else class="mt-5 space-y-3">
          <div v-for="point in daily" :key="point.date" class="grid grid-cols-[6rem_minmax(0,1fr)_5rem] items-center gap-3 text-sm">
            <span class="text-gray-500">{{ point.date.slice(5) }}</span>
            <div class="h-2 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700"><div class="h-full rounded-full bg-primary-500" :style="{ width: `${Math.max(2, (point.actual_cost / maxDailyCost) * 100)}%` }" /></div>
            <span class="text-right font-medium text-gray-900 dark:text-white"><BalanceAmount :amount="point.actual_cost" :fraction-digits="3" /></span>
          </div>
        </div>
      </div>

      <template v-if="mode === 'usage'">
        <div class="card overflow-hidden">
          <DataTable :columns="usageColumns" :data="logs" :loading="logsLoading" row-key="id">
            <template #cell-actor_email="{ value }">
              <span class="font-medium text-gray-900 dark:text-white">{{ value }}</span>
            </template>
            <template #cell-api_key_name="{ value }">
              <span class="text-gray-700 dark:text-gray-300">{{ value }}</span>
            </template>
            <template #cell-model="{ value }">
              <span class="font-medium text-gray-900 dark:text-white">{{ value }}</span>
            </template>
            <template #cell-tokens="{ row }">
              <div class="flex items-center gap-3 text-sm">
                <span class="inline-flex items-center gap-1">
                  <Icon name="arrowDown" size="sm" class="text-emerald-500" />
                  <span class="font-medium text-gray-900 dark:text-white">{{ Number(row.input_tokens || 0).toLocaleString() }}</span>
                </span>
                <span class="inline-flex items-center gap-1">
                  <Icon name="arrowUp" size="sm" class="text-violet-500" />
                  <span class="font-medium text-gray-900 dark:text-white">{{ Number(row.output_tokens || 0).toLocaleString() }}</span>
                </span>
              </div>
            </template>
            <template #cell-actual_cost="{ value }">
              <BalanceAmount :amount="value" :fraction-digits="4" class="font-medium text-green-600 dark:text-green-400" />
            </template>
            <template #cell-created_at="{ value }">
              <span class="whitespace-nowrap text-sm text-gray-600 dark:text-gray-400">{{ formatDateTime(value) }}</span>
            </template>
            <template #empty>
              <div class="py-6 text-sm text-gray-500 dark:text-gray-400">{{ t('team.noUsage') }}</div>
            </template>
          </DataTable>
        </div>
        <Pagination
          v-if="total > 0"
          :page="page"
          :total="total"
          :page-size="pageSize"
          @update:page="handlePageChange"
          @update:pageSize="handlePageSizeChange"
        />
      </template>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import BalanceAmount from '@/components/common/BalanceAmount.vue'
import { teamAPI, type TeamAPIKey, type TeamContext, type TeamMembership, type TeamUsageLog, type TeamUsageSummary } from '@/api/team'
import { useAppStore } from '@/stores/app'
import { formatDateTime } from '@/utils/format'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import type { Column } from '@/components/common/types'

const { mode = 'usage' } = defineProps<{ mode?: 'dashboard' | 'usage' }>()
const { t } = useI18n()
const appStore = useAppStore()
const loading = ref(true)
const logsLoading = ref(false)
const noTeam = ref(false)
const context = ref<TeamContext | null>(null)
const members = ref<TeamMembership[]>([])
const keys = ref<TeamAPIKey[]>([])
const summary = ref<TeamUsageSummary | null>(null)
const logs = ref<TeamUsageLog[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(getPersistedPageSize())
const memberID = ref<number | null>(null)
const keyID = ref<number | null>(null)
const isOwner = computed(() => context.value?.membership.role === 'owner')
const memberOptions = computed<SelectOption[]>(() => [{ value: null, label: t('team.allMembers') }, ...members.value.map((member) => ({ value: member.user_id, label: member.username || member.email }))])
const keyOptions = computed<SelectOption[]>(() => [{ value: null, label: t('team.allKeys') }, ...keys.value.map((key) => ({ value: key.id, label: key.name }))])
const metrics = computed(() => [
  { label: t('team.totalCost'), amount: Number(summary.value?.actual_cost || 0) },
  { label: t('team.requests'), value: Number(summary.value?.request_count || 0).toLocaleString() },
  { label: t('team.inputTokens'), value: Number(summary.value?.input_tokens || 0).toLocaleString() },
  { label: t('team.outputTokens'), value: Number(summary.value?.output_tokens || 0).toLocaleString() }
])
const daily = computed(() => summary.value?.daily || [])
const maxDailyCost = computed(() => Math.max(0.000001, ...daily.value.map((point) => point.actual_cost)))
const usageColumns = computed<Column[]>(() => [
  { key: 'actor_email', label: t('team.keyOwner') },
  { key: 'api_key_name', label: t('team.keys') },
  { key: 'model', label: t('team.model') },
  { key: 'tokens', label: t('team.tokens') },
  { key: 'actual_cost', label: t('team.cost') },
  { key: 'created_at', label: t('team.time') }
])

const query = () => ({ member_id: memberID.value || undefined, api_key_id: keyID.value || undefined, limit: pageSize.value, offset: (page.value - 1) * pageSize.value })
const reload = async () => { page.value = 1; await loadUsage() }
const resetFilters = async () => { memberID.value = null; keyID.value = null; await reload() }
const loadUsage = async () => {
  if (mode === 'dashboard') {
    summary.value = await teamAPI.usage(query())
    return
  }
  logsLoading.value = true
  try { const [nextSummary, page] = await Promise.all([teamAPI.usage(query()), teamAPI.usageLogs(query())]); summary.value = nextSummary; logs.value = page.items; total.value = page.total } finally { logsLoading.value = false }
}
const handlePageChange = async (nextPage: number) => { page.value = nextPage; await loadUsage() }
const handlePageSizeChange = async (nextPageSize: number) => { pageSize.value = nextPageSize; page.value = 1; await loadUsage() }

// 团队模式独立加载团队上下文，避免读取或展示 Owner 的个人资产详情。
onMounted(async () => {
  try {
    context.value = await teamAPI.current()
    if (context.value.membership.role === 'owner') {
      const [nextMembers, nextKeys] = await Promise.all([teamAPI.members(), teamAPI.keys()])
      members.value = nextMembers
      keys.value = nextKeys
    } else {
      keys.value = await teamAPI.keys()
    }
    await loadUsage()
  } catch (error: any) {
    if (error?.reason === 'TEAM_NOT_FOUND' || error?.reason === 'TEAM_MEMBERSHIP_REQUIRED' || error?.response?.status === 404) noTeam.value = true
    else appStore.showError(error?.message || t('team.loadFailed'))
  } finally { loading.value = false }
})
</script>
