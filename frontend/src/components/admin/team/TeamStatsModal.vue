<template>
  <BaseDialog
    :show="show"
    :title="t('team.statisticsTitle', { name: team?.name || '' })"
    width="extra-wide"
    @close="emit('close')"
  >
    <div class="space-y-6">
      <div
        v-if="team"
        class="flex flex-wrap items-center justify-between gap-3 rounded-control border border-gray-200 bg-gray-50 px-4 py-3 dark:border-dark-700 dark:bg-dark-800"
      >
        <div class="min-w-0">
          <p class="truncate font-semibold text-gray-900 dark:text-white">{{ team.name }}</p>
          <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
            {{ t('team.last30DaysStatistics') }} · {{ t('team.memberCount', { count: team.member_count }) }}
          </p>
        </div>
        <span :class="['badge', team.status === 'active' ? 'badge-success' : 'badge-danger']">
          {{ team.status === 'active' ? t('team.statusActive') : t('team.statusSuspended') }}
        </span>
      </div>

      <div v-if="loading && !summary" class="flex justify-center py-16">
        <LoadingSpinner />
      </div>

      <template v-else>
        <div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
          <div class="metric-card">
            <p class="metric-label">{{ t('team.totalCost') }}</p>
            <BalanceAmount :amount="summary?.actual_cost || 0" :fraction-digits="4" icon-size="md" class="mt-2 text-xl font-semibold" />
          </div>
          <div class="metric-card">
            <p class="metric-label">{{ t('team.requests') }}</p>
            <p class="metric-value">{{ formatNumber(summary?.request_count || 0) }}</p>
          </div>
          <div class="metric-card">
            <p class="metric-label">{{ t('team.inputTokens') }}</p>
            <p class="metric-value">{{ formatNumber(summary?.input_tokens || 0) }}</p>
          </div>
          <div class="metric-card">
            <p class="metric-label">{{ t('team.outputTokens') }}</p>
            <p class="metric-value">{{ formatNumber(summary?.output_tokens || 0) }}</p>
          </div>
        </div>

        <TeamMemberUsageCharts :series="memberSeries" :loading="loading" />
      </template>
    </div>

    <template #footer>
      <div class="flex justify-end">
        <button class="btn btn-secondary" @click="emit('close')">{{ t('common.close') }}</button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { AdminTeam } from '@/api/admin/teams'
import type { TeamUsageSummary } from '@/api/team'
import BaseDialog from '@/components/common/BaseDialog.vue'
import BalanceAmount from '@/components/common/BalanceAmount.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import TeamMemberUsageCharts from '@/components/charts/TeamMemberUsageCharts.vue'
import { useAppStore } from '@/stores/app'

const props = defineProps<{
  show: boolean
  team: AdminTeam | null
}>()

const emit = defineEmits<{
  (event: 'close'): void
}>()

const { t } = useI18n()
const appStore = useAppStore()
const loading = ref(false)
const summary = ref<TeamUsageSummary | null>(null)
const memberSeries = ref<Array<{ userID: number; label: string; summary: TeamUsageSummary }>>([])
let requestSequence = 0

const formatNumber = (value: number) => Number(value || 0).toLocaleString()

// 管理员统计与团队所有者看到的图表使用同一套成员口径和展示组件。
const loadStatistics = async () => {
  const team = props.team
  if (!team) return
  const sequence = ++requestSequence
  loading.value = true
  summary.value = null
  memberSeries.value = []
  try {
    const [members, teamSummary] = await Promise.all([
      adminAPI.teams.members(team.id),
      adminAPI.teams.usage(team.id),
    ])
    const memberSummaries = await Promise.all(
      members.map((member) => adminAPI.teams.usage(team.id, { member_id: member.user_id })),
    )
    if (sequence !== requestSequence) return
    summary.value = teamSummary
    memberSeries.value = members.map((member, index) => ({
      userID: member.user_id,
      label: member.username || member.email,
      summary: memberSummaries[index],
    }))
  } catch (error: any) {
    if (sequence !== requestSequence) return
    appStore.showError(error?.message || t('team.loadFailed'))
  } finally {
    if (sequence === requestSequence) loading.value = false
  }
}

watch(
  () => [props.show, props.team?.id] as const,
  ([visible]) => {
    if (visible && props.team) void loadStatistics()
    else {
      requestSequence++
      summary.value = null
      memberSeries.value = []
    }
  },
  { immediate: true },
)
</script>

<style scoped>
.metric-card {
  @apply rounded-surface border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-900;
}

.metric-label {
  @apply text-xs font-medium text-gray-500 dark:text-gray-400;
}

.metric-value {
  @apply mt-2 text-xl font-semibold text-gray-900 dark:text-white;
}
</style>
