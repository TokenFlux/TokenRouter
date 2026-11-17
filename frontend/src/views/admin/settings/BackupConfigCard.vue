<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import SettingsCard from '@/components/common/settings/SettingsCard.vue'
import SettingsSection from '@/components/common/settings/SettingsSection.vue'
import Skeleton from '@/components/common/Skeleton.vue'

// BackupConfigCard.vue keeps each backup configuration card in place while its
// endpoint loads. Storage, content, and schedule have different control shapes.
// A failed first read offers a retry in the same card before mounting its editor.

// The section identifies the configuration endpoint and selects its heading and placeholder.
withDefaults(
  defineProps<{
    section: 'storage' | 'content' | 'schedule' // Backup configuration group.
    status?: 'pending' | 'ready' | 'failed' // Initial read state.
  }>(),
  { status: 'pending' },
)

// The retry event lets the owning form repeat the read for this section.
const emit = defineEmits<{ retry: [] }>()

// The translator supplies the configuration headings, loading label, and retry action.
const { t } = useI18n()
</script>

<template>
  <SettingsCard
    :title="t(`admin.backup.${section}.title`)"
    :description="t(`admin.backup.${section}.description`)"
    :aria-busy="status === 'pending'"
  >
    <template #actions>
      <slot v-if="status === 'ready'" name="actions" />
      <Skeleton
        v-else-if="status === 'pending' && section === 'storage'"
        :width="128"
        :height="36"
      />
    </template>

    <div
      v-if="status === 'pending'"
      role="status"
      :aria-label="t('common.loading')"
      data-loading-skeleton
    >
      <SettingsSection v-if="section === 'storage'" aria-hidden="true">
        <div class="flex max-w-full gap-2">
          <Skeleton :width="80" :height="32" />
          <Skeleton :width="80" :height="32" />
        </div>
        <div class="space-y-2">
          <Skeleton :width="96" :height="14" />
          <Skeleton :height="36" />
          <Skeleton :width="240" :height="12" class="max-w-full" />
        </div>
      </SettingsSection>
      <SettingsSection v-else aria-hidden="true">
        <div
          v-for="row in section === 'content' ? 4 : 1"
          :key="row"
          class="flex items-start justify-between gap-4"
        >
          <div class="min-w-0 flex-1 space-y-2">
            <Skeleton :width="148" :height="16" class="max-w-full" />
            <Skeleton v-if="section === 'content'" :width="420" :height="14" class="max-w-full" />
          </div>
          <Skeleton :width="44" :height="24" class="shrink-0 rounded-full" />
        </div>
        <Skeleton v-if="section === 'content'" :width="236" :height="12" class="max-w-full" />
      </SettingsSection>
    </div>
    <div
      v-else-if="status === 'failed'"
      class="flex flex-wrap items-center justify-between gap-3"
      role="status"
    >
      <p class="text-sm text-gray-500 dark:text-dark-400">{{
        t('admin.backup.configurationUnavailable')
      }}</p>
      <button type="button" class="btn btn-secondary" @click="emit('retry')">{{
        t('common.retry')
      }}</button>
    </div>
    <slot v-else />
  </SettingsCard>
</template>
