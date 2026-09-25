<template>
  <div
    class="border-t border-gray-200 pt-4 dark:border-dark-600"
    data-testid="upstream-usage-config"
  >
    <div class="flex items-start justify-between gap-4">
      <div class="min-w-0">
        <label class="input-label mb-0">{{ t('admin.accounts.upstreamUsage.title') }}</label>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
          {{ t('admin.accounts.upstreamUsage.hint') }}
        </p>
      </div>
      <label class="flex shrink-0 cursor-pointer items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
        <input
          v-model="enabledModel"
          type="checkbox"
          class="h-4 w-4 rounded-compact border-gray-300 text-primary-600 focus:ring-primary-500"
          data-testid="upstream-usage-enabled"
        />
        {{ t('admin.accounts.upstreamUsage.enabled') }}
      </label>
    </div>

    <div v-if="enabledModel && !automaticAdapter" class="mt-3 space-y-3">
      <div>
        <label class="input-label">{{ t('admin.accounts.upstreamUsage.adapter') }}</label>
        <Select
          v-model="adapterModel"
          :options="adapterOptions"
          data-testid="upstream-usage-adapter"
        />
      </div>
      <div>
        <label class="input-label">{{ t('admin.accounts.upstreamUsage.baseUrl') }}</label>
        <input
          v-model="baseUrlModel"
          type="text"
          class="input"
          :placeholder="t('admin.accounts.upstreamUsage.baseUrlPlaceholder')"
          data-testid="upstream-usage-base-url"
        />
        <p class="input-hint">{{ t('admin.accounts.upstreamUsage.baseUrlHint') }}</p>
      </div>
      <template v-if="adapterModel === 'new_api'">
        <div>
          <label class="input-label">{{ t('admin.accounts.upstreamUsage.walletAccessToken') }}</label>
          <input
            v-model="walletAccessTokenModel"
            type="password"
            class="input font-mono"
            autocomplete="new-password"
            :placeholder="t('admin.accounts.upstreamUsage.walletAccessTokenPlaceholder')"
            data-testid="upstream-usage-wallet-access-token"
          />
          <p class="input-hint">{{ t('admin.accounts.upstreamUsage.walletAccessTokenHint') }}</p>
        </div>
        <div>
          <label class="input-label">{{ t('admin.accounts.upstreamUsage.walletUserId') }}</label>
          <input
            v-model="walletUserIdModel"
            type="text"
            inputmode="numeric"
            class="input"
            :placeholder="t('admin.accounts.upstreamUsage.walletUserIdPlaceholder')"
            data-testid="upstream-usage-wallet-user-id"
          />
          <p class="input-hint">{{ t('admin.accounts.upstreamUsage.walletUserIdHint') }}</p>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import type { UpstreamUsageAdapter } from '@/types'

const props = withDefaults(defineProps<{
  enabled: boolean
  adapter: UpstreamUsageAdapter
  baseUrl: string
  walletAccessToken?: string
  walletUserId?: string
  automaticAdapter?: boolean
}>(), {
  enabled: true,
  adapter: 'sub2api',
  baseUrl: '',
  walletAccessToken: '',
  walletUserId: '',
  automaticAdapter: false
})

const emit = defineEmits<{
  'update:enabled': [value: boolean]
  'update:adapter': [value: UpstreamUsageAdapter]
  'update:baseUrl': [value: string]
  'update:walletAccessToken': [value: string]
  'update:walletUserId': [value: string]
}>()

const { t } = useI18n()

// 前端只展示后端已注册的固定适配器，不接受任意请求模板或脚本配置。
const adapterOptions = computed<SelectOption[]>(() => [
  { value: 'sub2api', label: t('admin.accounts.upstreamUsage.adapters.sub2api') },
  { value: 'new_api', label: t('admin.accounts.upstreamUsage.adapters.newApi') },
  { value: 'zivv', label: t('admin.accounts.upstreamUsage.adapters.zivv') }
])

const enabledModel = computed({
  get: () => props.enabled,
  set: (value: boolean) => emit('update:enabled', value)
})

const adapterModel = computed({
  get: () => props.adapter,
  set: (value: string | number | boolean | null) => {
    if (value === 'sub2api' || value === 'new_api' || value === 'zivv') emit('update:adapter', value)
  }
})

const baseUrlModel = computed({
  get: () => props.baseUrl,
  set: (value: string) => emit('update:baseUrl', value)
})

const walletAccessTokenModel = computed({
  get: () => props.walletAccessToken,
  set: (value: string) => emit('update:walletAccessToken', value)
})

const walletUserIdModel = computed({
  get: () => props.walletUserId,
  set: (value: string) => emit('update:walletUserId', value)
})
</script>
