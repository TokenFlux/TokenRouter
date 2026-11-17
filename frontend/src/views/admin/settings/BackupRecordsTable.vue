<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import TableSkeletonBody from '@/components/common/TableSkeletonBody.vue'

// BackupRecordsTable.vue keeps backup history headers in place before records
// are available. Its owner renders record actions through the default slot.
// The table body distinguishes a first read, an empty result, and a failed read.

// The state controls the table body while the headers stay visible across reads.
withDefaults(
  defineProps<{ state?: 'loading' | 'empty' | 'failed' | 'ready' /* Visible table state. */ }>(),
  { state: 'loading' },
)

// The translator supplies localized table headings and the empty and failed messages.
const { t } = useI18n()
</script>

<template>
  <div class="overflow-x-auto">
    <table class="w-full min-w-[800px] text-sm" :aria-busy="state === 'loading'">
      <thead>
        <tr
          class="border-b border-gray-200 text-left text-xs tracking-wide text-gray-500 dark:border-dark-700 dark:text-dark-400"
        >
          <th class="py-2 pr-4">ID</th>
          <th class="py-2 pr-4">{{ t('admin.backup.columns.status') }}</th>
          <th class="py-2 pr-4">{{ t('admin.backup.columns.storage') }}</th>
          <th class="py-2 pr-4">{{ t('admin.backup.columns.fileName') }}</th>
          <th class="py-2 pr-4">{{ t('admin.backup.columns.size') }}</th>
          <th class="py-2 pr-4">{{ t('admin.backup.columns.parts') }}</th>
          <th class="py-2 pr-4">{{ t('admin.backup.columns.expiresAt') }}</th>
          <th class="py-2 pr-4">{{ t('admin.backup.columns.triggeredBy') }}</th>
          <th class="py-2 pr-4">{{ t('admin.backup.columns.startedAt') }}</th>
          <th class="py-2">{{ t('admin.backup.columns.actions') }}</th>
        </tr>
      </thead>
      <TableSkeletonBody
        v-if="state === 'loading'"
        :columns="10"
        :rows="4"
        cell-class="py-3 pr-4"
      />
      <tbody v-else>
        <slot v-if="state === 'ready'" />
        <tr v-else>
          <td colspan="10" class="py-6 text-center text-sm text-gray-500 dark:text-dark-400">
            <span role="status">{{
              t(state === 'failed' ? 'admin.backup.recordsUnavailable' : 'admin.backup.empty')
            }}</span>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
