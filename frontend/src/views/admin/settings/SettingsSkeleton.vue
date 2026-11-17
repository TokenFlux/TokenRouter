<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Skeleton from '@/components/common/Skeleton.vue'
import SettingsCard from '@/components/common/settings/SettingsCard.vue'
import SettingsSection from '@/components/common/settings/SettingsSection.vue'
import BackupConfigCard from './BackupConfigCard.vue'
import BackupRecordsTable from './BackupRecordsTable.vue'
import SettingsTabSkeleton from './SettingsTabSkeleton.vue'
import type { GatewaySection, SettingsTab } from './useNavigationSettings'

/**
 * SettingsSkeleton.vue reserves the first settings cards while the bulk request
 * loads. General uses the preaggregation card, Backup shares its configuration
 * cards and history table. Each other tab reserves its own initial sections.
 */
// The navigation state and known public SMTP flag drive the initial card layout.
defineProps<{
  tab: SettingsTab // Selected settings tab.
  gatewaySection: GatewaySection // Selected gateway subsection.
  smtpEnabled: boolean | null // Known public email verification flag.
}>()

/**
 * The translation service supplies the loading status label announced while editable settings panels have not mounted.
 */
const { t } = useI18n()
</script>

<template>
  <div
    class="space-y-4"
    role="status"
    :aria-label="t('common.loading')"
    aria-busy="true"
    data-testid="settings-skeleton"
  >
    <template v-if="tab === 'general'">
      <SettingsCard
        :title="t('admin.settings.preAggregation.title')"
        :description="t('admin.settings.preAggregation.description')"
        aria-hidden="true"
      >
        <template #actions>
          <Skeleton :width="36" :height="36" class="rounded-control" />
        </template>
        <SettingsSection>
          <div class="flex items-center justify-between gap-4">
            <Skeleton :width="136" :height="16" />
            <Skeleton :width="44" :height="24" class="shrink-0 rounded-full" />
          </div>
          <div class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between sm:gap-4">
            <Skeleton :width="112" :height="16" />
            <Skeleton :width="0" :height="36" class="w-full shrink-0 sm:w-56" />
          </div>
          <div class="grid grid-cols-2 gap-x-6 gap-y-4 xl:grid-cols-4">
            <div
              v-for="metric in 5"
              :key="metric"
              class="space-y-2"
              :class="metric === 2 ? 'col-span-2 xl:col-span-1' : ''"
            >
              <Skeleton :width="84" :height="14" class="max-w-full" />
              <Skeleton :width="108" :height="16" class="max-w-full" />
            </div>
          </div>
        </SettingsSection>
        <SettingsSection>
          <div class="flex items-center justify-between gap-4">
            <Skeleton :width="136" :height="16" />
            <Skeleton :width="44" :height="24" class="shrink-0 rounded-full" />
          </div>
          <div class="grid grid-cols-2 gap-x-6 gap-y-4 xl:grid-cols-4">
            <div v-for="metric in 3" :key="metric" class="space-y-2">
              <Skeleton :width="84" :height="14" class="max-w-full" />
              <Skeleton :width="108" :height="16" class="max-w-full" />
            </div>
          </div>
        </SettingsSection>
        <SettingsSection>
          <div class="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
            <Skeleton :width="96" :height="16" />
            <div class="flex gap-2">
              <Skeleton :width="74" :height="36" class="rounded-control" />
              <Skeleton :width="142" :height="36" class="rounded-control" />
            </div>
          </div>
        </SettingsSection>
      </SettingsCard>
      <SettingsCard
        :title="t('admin.settings.usageRanking.title')"
        :description="t('admin.settings.usageRanking.description')"
        aria-hidden="true"
      >
        <div class="flex items-start justify-between gap-4">
          <div class="min-w-0 flex-1 space-y-2">
            <Skeleton :width="124" :height="16" class="max-w-full" />
            <Skeleton :width="280" :height="14" class="max-w-full" />
          </div>
          <Skeleton :width="44" :height="24" class="shrink-0 rounded-full" />
        </div>
      </SettingsCard>
    </template>
    <template v-else-if="tab === 'backup'">
      <BackupConfigCard section="storage" />
      <BackupConfigCard section="content" />
      <BackupConfigCard section="schedule" />
      <SettingsCard
        :title="t('admin.backup.operations.title')"
        :description="t('admin.backup.operations.description')"
      >
        <template #actions>
          <div class="flex flex-wrap items-center gap-2" aria-hidden="true">
            <Skeleton :width="80" :height="36" />
            <Skeleton :width="110" :height="36" />
            <Skeleton :width="78" :height="36" />
          </div>
        </template>
        <BackupRecordsTable />
      </SettingsCard>
    </template>
    <div v-else class="space-y-4" aria-hidden="true">
      <SettingsTabSkeleton
        :tab="tab"
        :gateway-section="gatewaySection"
        :smtp-enabled="smtpEnabled"
      />
    </div>
  </div>
</template>
