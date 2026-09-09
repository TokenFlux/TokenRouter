<template>
  <button
    v-if="queryEnabled"
    type="button"
    class="inline-flex items-center gap-0.5 rounded px-1.5 py-0.5 text-[10px] font-medium text-blue-600 transition-colors hover:bg-blue-50 disabled:cursor-not-allowed disabled:opacity-50 dark:text-blue-400 dark:hover:bg-blue-900/30"
    :disabled="loading"
    :title="t('admin.accounts.upstreamUsage.query')"
    :aria-label="t('admin.accounts.upstreamUsage.query')"
    @click="query"
  >
    <Icon
      name="refresh"
      size="xs"
      :class="{ 'animate-spin': loading }"
      :stroke-width="2"
    />
    {{ t('admin.accounts.usageWindow.activeQuery') }}
  </button>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Account } from '@/types'
import Icon from '@/components/icons/Icon.vue'
import { isUpstreamUsageQueryEnabled } from '@/utils/upstreamUsage'

const props = withDefaults(defineProps<{
  account: Account
  loading?: boolean
  request?: ((account: Account, options?: { force?: boolean }) => void) | null
}>(), {
  loading: false,
  request: null
})

const { t } = useI18n()

// 查询按钮只负责管理员显式操作；配置关闭时由内容组件显示关闭状态。
const queryEnabled = computed(() => isUpstreamUsageQueryEnabled(props.account))

const query = () => {
  props.request?.(props.account, { force: true })
}
</script>
