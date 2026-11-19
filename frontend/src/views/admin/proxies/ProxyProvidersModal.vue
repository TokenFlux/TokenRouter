<template>
  <BaseDialog
    :show="show"
    :title="t('admin.proxies.providersTitle', { name: proxyName || '' })"
    width="normal"
    @close="emit('close')"
  >
    <div v-if="!loading && providers.length === 0" class="py-6 text-center text-sm text-gray-500">
      {{ t('admin.proxies.providersEmpty') }}
    </div>
    <div v-else class="max-h-80 overflow-auto">
      <table
        :aria-busy="loading"
        class="min-w-full divide-y divide-gray-200 text-sm dark:divide-dark-700"
      >
        <thead class="bg-gray-50 text-xs text-gray-500 dark:bg-dark-800 dark:text-dark-400">
          <tr>
            <th class="px-4 py-2 text-left">{{ t('admin.proxies.providerName') }}</th>
            <th class="px-4 py-2 text-left">{{ t('admin.providers.columns.platformType') }}</th>
            <th class="px-4 py-2 text-left">{{ t('admin.proxies.providerNotes') }}</th>
          </tr>
        </thead>
        <TableSkeletonBody v-if="loading" :columns="3" cell-class="px-3 py-2" />
        <tbody
          v-else
          class="divide-y divide-gray-200 bg-white dark:divide-dark-700 dark:bg-dark-900"
        >
          <tr v-for="provider in providers" :key="provider.id">
            <td class="px-4 py-2 font-medium text-gray-900 dark:text-white">
              {{ provider.name }}
            </td>
            <td class="px-4 py-2">
              <PlatformTypeBadge :platform="provider.platform" :type="provider.type" />
            </td>
            <td class="px-4 py-2 text-gray-600 dark:text-gray-300">
              {{ provider.notes || '-' }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <template #footer>
      <div class="flex justify-end">
        <button @click="emit('close')" class="btn btn-secondary">
          {{ t('common.close') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import PlatformTypeBadge from '@/components/common/PlatformTypeBadge.vue'
import TableSkeletonBody from '@/components/common/TableSkeletonBody.vue'
import type { ProxyProviderSummary } from '@/api/admin/proxies.types'

/**
 * ProxyProvidersModal.vue renders the read-only list of upstream provider
 * accounts currently bound to a selected proxy, showing skeleton rows while the
 * summary query is in flight.
 */

defineProps<{
  show: boolean // Dialog visibility flag.
  proxyName?: string | null // Selected proxy display name.
  loading: boolean // Whether the bound-provider query is pending.
  providers: ProxyProviderSummary[] // Bound provider summaries returned by the server.
}>()

const emit = defineEmits<{
  close: [] // Dismisses the proxy providers dialog.
}>()

const { t } = useI18n()
</script>
