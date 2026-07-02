<template>
  <div v-if="visible" class="space-y-1">
    <div class="flex flex-wrap items-center gap-1.5">
      <slot name="pre-actions" />

      <!-- 只保留查询/次数展示，避免在账号列表里误触真实上游重置。 -->
      <button
        type="button"
        class="inline-flex min-w-[54px] items-center justify-center gap-0.5 rounded px-1.5 py-0.5 text-[10px] font-medium text-blue-600 transition-colors hover:bg-blue-50 disabled:cursor-not-allowed disabled:opacity-50 dark:text-blue-400 dark:hover:bg-blue-900/30"
        :disabled="loading"
        :title="countButtonTitle"
        @click="handleQuery"
      >
        <Icon
          name="refresh"
          size="xs"
          :class="{ 'animate-spin': loading }"
          :stroke-width="2"
        />
        <span>{{ t('admin.accounts.openaiQuotaReset.count') }}</span>
        <span v-if="data">{{ availableResetCount }}</span>
      </button>

      <slot />
    </div>

    <div
      v-if="error"
      class="max-w-[180px] truncate text-[10px] text-red-600 dark:text-red-400"
      :title="error"
    >
      {{ truncatedError }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Account } from '@/types'
import {
  queryOpenAIQuota,
  type OpenAIQuotaUsage
} from '@/api/admin/accounts'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{
  account: Account
}>()

const { t } = useI18n()

const visible = computed(() => props.account.platform === 'openai' && props.account.type === 'oauth')
const loading = ref(false)
const error = ref<string | null>(null)
const data = ref<OpenAIQuotaUsage | null>(null)

const availableResetCount = computed(() => data.value?.rate_limit_reset_credits?.available_count ?? 0)

// 「次数」按钮同时负责上游查询和数量展示；未加载时提示查询，已加载后提示刷新。
const countButtonTitle = computed(() => {
  if (!data.value) return t('admin.accounts.openaiQuotaReset.countTooltipLoad')
  return t('admin.accounts.openaiQuotaReset.countTooltipRefresh')
})

const truncatedError = computed(() => {
  if (!error.value) return ''
  return error.value.length > 80 ? `${error.value.slice(0, 80)}...` : error.value
})

const extractErrorMessage = (e: unknown): string => {
  const err = e as {
    message?: string
    reason?: string
    response?: { data?: { message?: string; error?: string } }
  }
  return (
    err?.message ||
    err?.reason ||
    err?.response?.data?.message ||
    err?.response?.data?.error ||
    t('common.error')
  )
}

const handleQuery = async () => {
  if (loading.value) return
  loading.value = true
  error.value = null
  try {
    data.value = await queryOpenAIQuota(props.account.id)
  } catch (e) {
    error.value = extractErrorMessage(e)
  } finally {
    loading.value = false
  }
}

watch(
  () => props.account.id,
  () => {
    data.value = null
    error.value = null
    loading.value = false
  }
)
</script>
