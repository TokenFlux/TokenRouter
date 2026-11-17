<template>
  <div class="space-y-4">
    <!-- Storage connection draft -->
    <BackupConfigCard section="storage" :status="configStatus.storage" @retry="loadStorageConfig">
      <template #actions>
        <button
          type="button"
          class="btn btn-secondary btn-sm h-9"
          :disabled="testingStorage"
          @click="testStorage"
        >
          {{ testingStorage ? t('common.loading') : t('admin.backup.storage.testConnection') }}
        </button>
      </template>
      <SettingsSection>
        <SettingsSegmented
          v-model="storageForm.type"
          :options="storageTypeOptions"
          :ariaLabel="t('admin.backup.storage.title')"
        />

        <div v-if="storageForm.type === 'local'">
          <label for="backup-local-path" class="input-label">{{
            t('admin.backup.storage.localPath')
          }}</label>
          <input
            id="backup-local-path"
            :value="storageForm.local_path || '-'"
            class="input font-mono text-sm"
            readonly
          />
          <p class="input-hint">{{ t('admin.backup.storage.localHint') }}</p>
        </div>

        <template v-else>
          <p class="input-hint mt-0">
            {{ t('admin.backup.s3.descriptionPrefix') }}
            <button
              type="button"
              class="text-primary-600 underline hover:text-primary-700 dark:text-primary-400 dark:hover:text-primary-300"
              @click="showR2Guide = true"
              >Cloudflare R2</button
            >
            {{ t('admin.backup.s3.descriptionSuffix') }}
          </p>
          <div class="grid gap-4 md:grid-cols-2">
            <div>
              <label for="backup-s3-endpoint" class="input-label">{{
                t('admin.backup.s3.endpoint')
              }}</label>
              <input
                id="backup-s3-endpoint"
                v-model="s3Form.endpoint"
                class="input font-mono text-sm"
                placeholder="https://<account_id>.r2.cloudflarestorage.com"
              />
            </div>
            <div>
              <label for="backup-s3-region" class="input-label">{{
                t('admin.backup.s3.region')
              }}</label>
              <input
                id="backup-s3-region"
                v-model="s3Form.region"
                class="input"
                placeholder="auto"
              />
            </div>
            <div>
              <label for="backup-s3-bucket" class="input-label">{{
                t('admin.backup.s3.bucket')
              }}</label>
              <input id="backup-s3-bucket" v-model="s3Form.bucket" class="input" />
            </div>
            <div>
              <label for="backup-s3-prefix" class="input-label">{{
                t('admin.backup.s3.prefix')
              }}</label>
              <input
                id="backup-s3-prefix"
                v-model="s3Form.prefix"
                class="input"
                placeholder="backups/"
              />
            </div>
            <div>
              <label for="backup-s3-access-key-id" class="input-label">{{
                t('admin.backup.s3.accessKeyId')
              }}</label>
              <input
                id="backup-s3-access-key-id"
                v-model="s3Form.access_key_id"
                class="input font-mono text-sm"
              />
            </div>
            <div>
              <label for="backup-s3-secret-access-key" class="input-label">{{
                t('admin.backup.s3.secretAccessKey')
              }}</label>
              <input
                id="backup-s3-secret-access-key"
                v-model="s3Form.secret_access_key"
                type="password"
                class="input font-mono text-sm"
                :placeholder="s3SecretConfigured ? t('admin.backup.s3.secretConfigured') : ''"
              />
            </div>
          </div>
          <SettingToggleRow
            id="backup-s3-force-path-style"
            v-model="s3Form.force_path_style"
            :label="t('admin.backup.s3.forcePathStyle')"
          />
          <div>
            <p class="input-label">{{ t('admin.backup.s3.uploadMode') }}</p>
            <SettingsSegmented
              v-model="s3Form.upload_mode"
              :options="uploadModeOptions"
              :ariaLabel="t('admin.backup.s3.uploadMode')"
            />
            <p class="input-hint">{{ t('admin.backup.s3.uploadModeHint') }}</p>
          </div>
        </template>
      </SettingsSection>
    </BackupConfigCard>

    <!-- Backup content inclusion draft -->
    <BackupConfigCard section="content" :status="configStatus.content" @retry="loadContentConfig">
      <SettingsSection>
        <SettingToggleRow
          v-for="option in contentOptions"
          :id="`backup-content-${option.key}`"
          :key="option.key"
          v-model="contentForm[option.key]"
          :label="option.title"
          :hint="option.description"
        />
        <p class="input-hint">
          {{ t('admin.backup.content.excludedCount', { count: contentExcludedCount }) }}
        </p>
      </SettingsSection>
    </BackupConfigCard>

    <!-- Scheduled backup retention draft -->
    <BackupConfigCard section="schedule" :status="configStatus.schedule" @retry="loadSchedule">
      <SettingsSection>
        <SettingToggleRow
          id="backup-schedule-enabled"
          v-model="scheduleForm.enabled"
          :label="t('admin.backup.schedule.enabled')"
        />
        <Collapse :open="scheduleForm.enabled">
          <SettingsSubpanel>
            <SettingRow
              id="backup-schedule-cron"
              field
              label-for="backup-schedule-cron"
              :label="t('admin.backup.schedule.cronExpr')"
              :hint="t('admin.backup.schedule.cronHint')"
            >
              <input
                id="backup-schedule-cron"
                v-model="scheduleForm.cron_expr"
                class="input font-mono text-sm"
                placeholder="0 2 * * *"
              />
            </SettingRow>
            <SettingRow
              id="backup-schedule-retain-days"
              field
              label-for="backup-schedule-retain-days"
              :label="t('admin.backup.schedule.retainDays')"
              :hint="t('admin.backup.schedule.retainDaysHint')"
            >
              <input
                id="backup-schedule-retain-days"
                v-model.number="scheduleForm.retain_days"
                type="number"
                min="0"
                class="input"
              />
            </SettingRow>
            <SettingRow
              id="backup-schedule-retain-count"
              field
              label-for="backup-schedule-retain-count"
              :label="t('admin.backup.schedule.retainCount')"
              :hint="t('admin.backup.schedule.retainCountHint')"
            >
              <input
                id="backup-schedule-retain-count"
                v-model.number="scheduleForm.retain_count"
                type="number"
                min="0"
                class="input"
              />
            </SettingRow>
          </SettingsSubpanel>
        </Collapse>
      </SettingsSection>
    </BackupConfigCard>

    <!-- Backup history and record actions -->
    <SettingsCard
      :title="t('admin.backup.operations.title')"
      :description="t('admin.backup.operations.description')"
    >
      <template #actions>
        <div class="flex flex-wrap items-center gap-2">
          <label for="backup-manual-expire-days" class="text-xs text-gray-500 dark:text-dark-400">
            {{ t('admin.backup.operations.expireDays') }}
          </label>
          <input
            id="backup-manual-expire-days"
            v-model.number="manualExpireDays"
            type="number"
            min="0"
            class="input w-20"
          />
          <button
            type="button"
            class="btn btn-primary btn-sm h-9"
            :disabled="creatingBackup"
            @click="createBackup"
          >
            {{
              creatingBackup
                ? t('admin.backup.operations.backing')
                : t('admin.backup.operations.createBackup')
            }}
          </button>
          <button
            type="button"
            class="btn btn-secondary btn-sm h-9"
            :disabled="loadingBackups"
            @click="loadBackups"
          >
            {{ loadingBackups ? t('common.loading') : t('common.refresh') }}
          </button>
        </div>
      </template>

      <BackupRecordsTable :state="backupTableState">
        <tr
          v-for="record in backups"
          :key="record.id"
          class="border-b border-gray-100 align-top dark:border-dark-700"
        >
          <td class="py-3 pr-4 font-mono text-xs">{{ record.id }}</td>
          <td class="py-3 pr-4">
            <span class="rounded-compact px-2 py-0.5 text-xs" :class="statusClass(record.status)">
              {{
                record.status === 'running' && record.progress
                  ? t(`admin.backup.progress.${record.progress}`)
                  : t(`admin.backup.status.${record.status}`)
              }}
            </span>
          </td>
          <td class="py-3 pr-4 text-xs">{{ storageLabel(record) }}</td>
          <td class="py-3 pr-4 text-xs">{{ record.file_name }}</td>
          <td class="py-3 pr-4 text-xs">{{ formatSize(record.size_bytes) }}</td>
          <td class="py-3 pr-4 text-xs">{{ record.parts?.length || 1 }}</td>
          <td class="py-3 pr-4 text-xs">
            {{ record.expires_at ? formatDate(record.expires_at) : t('admin.backup.neverExpire') }}
          </td>
          <td class="py-3 pr-4 text-xs">
            {{
              record.triggered_by === 'scheduled'
                ? t('admin.backup.trigger.scheduled')
                : t('admin.backup.trigger.manual')
            }}
          </td>
          <td class="py-3 pr-4 text-xs">{{ formatDate(record.started_at) }}</td>
          <td class="py-3 text-xs">
            <div class="flex flex-wrap gap-1">
              <button
                v-if="record.status === 'completed'"
                type="button"
                class="btn btn-secondary btn-xs"
                @click="downloadBackup(record.id)"
              >
                {{ t('admin.backup.actions.download') }}
              </button>
              <button
                v-if="record.status === 'completed'"
                type="button"
                class="btn btn-secondary btn-xs"
                :disabled="restoringId === record.id"
                @click="restoreBackup(record.id)"
              >
                {{
                  restoringId === record.id
                    ? t('common.loading')
                    : t('admin.backup.actions.restore')
                }}
              </button>
              <button type="button" class="btn btn-danger btn-xs" @click="removeBackup(record.id)">
                {{ t('common.delete') }}
              </button>
            </div>
          </td>
        </tr>
      </BackupRecordsTable>
    </SettingsCard>
  </div>

  <!-- Cloudflare R2 setup guide dialog -->
  <teleport to="body">
    <MotionTransition name="modal">
      <div
        v-if="showR2Guide"
        class="fixed inset-0 z-50 flex items-center justify-center p-4"
        @mousedown.self="showR2Guide = false"
      >
        <div class="fixed inset-0 bg-[var(--overlay-bg)]" @click="showR2Guide = false"></div>
        <div
          class="relative max-h-[85vh] w-full max-w-2xl overflow-y-auto rounded-surface bg-white p-6 shadow-2xl dark:bg-dark-900 sm:rounded-dialog"
        >
          <button
            type="button"
            class="absolute right-4 top-4 text-gray-400 hover:text-gray-600 dark:hover:text-gray-200"
            @click="showR2Guide = false"
          >
            <Icon name="x" size="sm" />
          </button>

          <h2 class="mb-4 text-lg font-bold text-gray-900 dark:text-white">{{
            t('admin.backup.r2Guide.title')
          }}</h2>
          <p class="mb-4 text-sm text-gray-500 dark:text-gray-400">{{
            t('admin.backup.r2Guide.intro')
          }}</p>

          <!-- Create the storage bucket -->
          <div class="mb-5">
            <h3
              class="mb-2 flex items-center gap-2 text-sm font-semibold text-gray-900 dark:text-white"
            >
              <span
                class="flex h-6 w-6 items-center justify-center rounded-full bg-primary-100 text-xs font-bold text-primary-700 dark:bg-primary-900/40 dark:text-primary-300"
                >1</span
              >
              {{ t('admin.backup.r2Guide.step1.title') }}
            </h3>
            <ol class="ml-8 list-decimal space-y-1 text-sm text-gray-600 dark:text-gray-300">
              <li>{{ t('admin.backup.r2Guide.step1.line1') }}</li>
              <li>{{ t('admin.backup.r2Guide.step1.line2') }}</li>
              <li>{{ t('admin.backup.r2Guide.step1.line3') }}</li>
            </ol>
          </div>

          <!-- Create access credentials -->
          <div class="mb-5">
            <h3
              class="mb-2 flex items-center gap-2 text-sm font-semibold text-gray-900 dark:text-white"
            >
              <span
                class="flex h-6 w-6 items-center justify-center rounded-full bg-primary-100 text-xs font-bold text-primary-700 dark:bg-primary-900/40 dark:text-primary-300"
                >2</span
              >
              {{ t('admin.backup.r2Guide.step2.title') }}
            </h3>
            <ol class="ml-8 list-decimal space-y-1 text-sm text-gray-600 dark:text-gray-300">
              <li>{{ t('admin.backup.r2Guide.step2.line1') }}</li>
              <li>{{ t('admin.backup.r2Guide.step2.line2') }}</li>
              <li>{{ t('admin.backup.r2Guide.step2.line3') }}</li>
              <li>{{ t('admin.backup.r2Guide.step2.line4') }}</li>
            </ol>
            <div
              class="mt-2 rounded-control bg-amber-50 p-3 text-xs text-amber-700 dark:bg-amber-900/20 dark:text-amber-300"
            >
              {{ t('admin.backup.r2Guide.step2.warning') }}
            </div>
          </div>

          <!-- Locate the account endpoint -->
          <div class="mb-5">
            <h3
              class="mb-2 flex items-center gap-2 text-sm font-semibold text-gray-900 dark:text-white"
            >
              <span
                class="flex h-6 w-6 items-center justify-center rounded-full bg-primary-100 text-xs font-bold text-primary-700 dark:bg-primary-900/40 dark:text-primary-300"
                >3</span
              >
              {{ t('admin.backup.r2Guide.step3.title') }}
            </h3>
            <p class="ml-8 text-sm text-gray-600 dark:text-gray-300">{{
              t('admin.backup.r2Guide.step3.desc')
            }}</p>
            <code
              class="ml-8 mt-1 block rounded-compact bg-gray-100 px-3 py-2 text-xs text-gray-800 dark:bg-dark-700 dark:text-gray-200"
              >https://&lt;{{
                t('admin.backup.r2Guide.step3.accountId')
              }}&gt;.r2.cloudflarestorage.com</code
            >
          </div>

          <!-- Enter the connection fields -->
          <div class="mb-5">
            <h3
              class="mb-2 flex items-center gap-2 text-sm font-semibold text-gray-900 dark:text-white"
            >
              <span
                class="flex h-6 w-6 items-center justify-center rounded-full bg-primary-100 text-xs font-bold text-primary-700 dark:bg-primary-900/40 dark:text-primary-300"
                >4</span
              >
              {{ t('admin.backup.r2Guide.step4.title') }}
            </h3>
            <div
              class="ml-8 overflow-hidden rounded-control border border-gray-200 dark:border-dark-600"
            >
              <table class="w-full text-sm">
                <tbody>
                  <tr
                    v-for="(row, i) in r2ConfigRows"
                    :key="i"
                    class="border-b border-gray-100 dark:border-dark-700 last:border-0"
                  >
                    <td
                      class="whitespace-nowrap bg-gray-50 px-3 py-2 font-medium text-gray-700 dark:bg-dark-700 dark:text-gray-300"
                      >{{ row.field }}</td
                    >
                    <td class="px-3 py-2 text-gray-600 dark:text-gray-400"
                      ><code class="text-xs">{{ row.value }}</code></td
                    >
                  </tr>
                </tbody>
              </table>
            </div>
          </div>

          <!-- Cloudflare free-tier information -->
          <div
            class="rounded-control bg-green-50 p-3 text-xs text-green-700 dark:bg-green-900/20 dark:text-green-300"
          >
            {{ t('admin.backup.r2Guide.freeTier') }}
          </div>

          <div class="mt-4 text-right">
            <button type="button" class="btn btn-primary btn-sm" @click="showR2Guide = false">{{
              t('common.close')
            }}</button>
          </div>
        </div>
      </div>
    </MotionTransition>
  </teleport>
  <!-- Separate archive parts retain their individual download links -->
  <teleport to="body">
    <MotionTransition name="modal">
      <div
        v-if="downloadPartsModalOpen"
        class="fixed inset-0 z-50 flex items-center justify-center p-4"
        @mousedown.self="closeDownloadParts"
      >
        <div class="fixed inset-0 bg-[var(--overlay-bg)]" @click="closeDownloadParts"></div>
        <div
          class="relative max-h-[85vh] w-full max-w-lg overflow-y-auto rounded-surface bg-white p-6 shadow-2xl dark:bg-dark-900 sm:rounded-dialog"
        >
          <button
            type="button"
            class="absolute right-4 top-4 text-gray-400 hover:text-gray-600 dark:hover:text-gray-200"
            :aria-label="t('common.close')"
            @click="closeDownloadParts"
          >
            <Icon name="x" size="sm" />
          </button>
          <h2 class="mb-1 text-lg font-bold text-gray-900 dark:text-white">
            {{ t('admin.backup.actions.downloadParts') }}
          </h2>
          <p class="mb-4 text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.backup.actions.downloadPartsHint') }}
          </p>
          <div class="space-y-2">
            <div
              v-for="part in downloadParts"
              :key="part.index"
              class="flex items-center justify-between gap-3 rounded-control border border-gray-200 px-3 py-2 dark:border-dark-600"
            >
              <span class="text-sm text-gray-700 dark:text-gray-300">
                {{ t('admin.backup.actions.partLabel', { index: part.index }) }}
                <span class="ml-2 text-xs text-gray-500 dark:text-gray-400">{{
                  formatSize(part.size_bytes)
                }}</span>
              </span>
              <a :href="part.url" class="btn btn-secondary btn-xs" rel="noopener">
                {{ t('admin.backup.actions.download') }}
              </a>
            </div>
          </div>
        </div>
      </div>
    </MotionTransition>
  </teleport>
  <TotpStepUpDialog :controller="backupStepUp" />
</template>

<script setup lang="ts">
import MotionTransition from '@/components/common/MotionTransition.vue'
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api'
import { useAppStore } from '@/stores'
import type {
  BackupContentConfig,
  BackupDownloadPart,
  BackupS3Config,
  BackupScheduleConfig,
  BackupRecord,
  BackupStorageConfig,
} from '@/api/admin/backup'
import {
  useStepUp,
  isStepUpBlocked,
  isStepUpCancelled,
  stepUpBlockReason,
} from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { useDirtyTracker } from '@/composables/useDirtyTracker'
import { useSettingsSaveTarget } from '@/composables/useSettingsSaveRegistry'
import Collapse from '@/components/common/Collapse.vue'
import SettingRow from '@/components/common/settings/SettingRow.vue'
import SettingToggleRow from '@/components/common/settings/SettingToggleRow.vue'
import SettingsCard from '@/components/common/settings/SettingsCard.vue'
import SettingsSection from '@/components/common/settings/SettingsSection.vue'
import SettingsSegmented, {
  type SettingsSegmentedOption,
} from '@/components/common/settings/SettingsSegmented.vue'
import BackupConfigCard from './BackupConfigCard.vue'
import BackupRecordsTable from './BackupRecordsTable.vue'
import SettingsSubpanel from '@/components/common/settings/SettingsSubpanel.vue'

/**
 * BackupSettings.vue edits backup storage, content, and schedules and operates
 * backup records. Each configuration group registers its own save target,
 * allowing the settings page to submit changed groups independently. Storage
 * writes and sensitive backup actions pass through step-up verification, while
 * download selection distinguishes authenticated local files from remote files
 * and split archives.
 *
 * Creation and restoration have separate interval handles and busy indicators.
 * Browser visibility pauses interval scheduling and triggers a list refresh
 * before resuming observed running tasks; hiding the settings panel leaves the
 * component mounted. Unmount clears current handles and removes the visibility
 * listener, but outstanding requests remain asynchronous and can still settle.
 * Secret loading clears the password draft, while request construction
 * restores newly entered secret text after normalization.
 */

// Translation and notifications support feedback from independently saved
// configuration groups and long-running backup operations in this panel.
const { t } = useI18n()
const appStore = useAppStore()

// Storage selectors restrict draft values to supported backend and upload
// modes while translating their labels for the current locale.
const storageTypeOptions = computed<SettingsSegmentedOption<BackupStorageConfig['type']>[]>(() => [
  { value: 'local', label: t('admin.backup.storage.local') },
  { value: 's3', label: t('admin.backup.storage.remote') },
])
const uploadModeOptions = computed<
  SettingsSegmentedOption<NonNullable<BackupS3Config['upload_mode']>>[]
>(() => [
  { value: 'multipart', label: t('admin.backup.s3.uploadModeMultipart') },
  { value: 'spooled_put', label: t('admin.backup.s3.uploadModeSpooled') },
])
// backupStepUp owns the verification dialog state shared by storage writes
// and sensitive backup operations in this mounted panel.
const backupStepUp = useStepUp()

/**
 * reportStepUpBlocked displays the reason a backup action cannot pass step-up
 * verification. It returns true when it handled a blocked error, allowing
 * callers to suppress a duplicate generic error notification.
 */
function reportStepUpBlocked(error: unknown): boolean {
  if (!isStepUpBlocked(error)) return false
  appStore.showError(
    stepUpBlockReason(error) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN'
      ? t('stepUp.adminApiKeyForbidden')
      : t('stepUp.notEnabled'),
  )
  return true
}

// ConfigReadStatus distinguishes each initial request from an available editor
// or a request that failed before the section received server settings.
type ConfigReadStatus = 'pending' | 'ready' | 'failed'

// configStatus tracks the independent first reads. Later failed refreshes keep
// available drafts mounted so edits and the last read state remain accessible.
const configStatus = reactive<Record<'storage' | 'content' | 'schedule', ConfigReadStatus>>({
  storage: 'pending',
  content: 'pending',
  schedule: 'pending',
})

// Storage drafts retain the selected backend and editable S3 fields
// separately so switching storage type keeps entered remote settings.
const storageForm = ref<BackupStorageConfig>({
  type: 'local',
  local_path: '',
  s3: {
    endpoint: '',
    region: 'auto',
    bucket: '',
    access_key_id: '',
    secret_access_key: '',
    prefix: 'backups/',
    force_path_style: false,
    upload_mode: 'spooled_put',
  },
})
const s3Form = ref<BackupS3Config>({
  endpoint: '',
  region: 'auto',
  bucket: '',
  access_key_id: '',
  secret_access_key: '',
  prefix: 'backups/',
  force_path_style: false,
  upload_mode: 'spooled_put',
})
// s3SecretConfigured is inferred from loaded access-key presence and controls
// the empty password field hint.
const s3SecretConfigured = ref(false)
// testingStorage disables repeat connection tests until the current request
// reports its outcome and releases the indicator.
const testingStorage = ref(false)

// Content inclusion flags drive the backup exclusions shown beside each
// option and are saved through their own registered target.
const contentForm = ref<BackupContentConfig>({
  include_usage_records: false,
  include_ops_logs: false,
  include_audit_logs: false,
  include_runtime_data: false,
  excluded_table_data: [],
})
/**
 * BackupContentOptionKey identifies the four switchable backup content
 * inclusion flags.
 */
type BackupContentOptionKey = keyof Pick<
  BackupContentConfig,
  'include_usage_records' | 'include_ops_logs' | 'include_audit_logs' | 'include_runtime_data'
>
// Content labels and pattern counts translate the four inclusion switches
// into the displayed number of excluded table patterns.
const contentTablePatternCounts: Record<BackupContentOptionKey, number> = {
  include_usage_records: 10,
  include_ops_logs: 9,
  include_audit_logs: 5,
  include_runtime_data: 6,
}
const contentOptions = computed<
  Array<{
    key: BackupContentOptionKey // Draft inclusion field identifier.
    title: string // Translated option display name.
    description: string // Translated included-data explanation.
  }>
>(() => [
  {
    key: 'include_usage_records',
    title: t('admin.backup.content.usageRecords.title'),
    description: t('admin.backup.content.usageRecords.description'),
  },
  {
    key: 'include_ops_logs',
    title: t('admin.backup.content.opsLogs.title'),
    description: t('admin.backup.content.opsLogs.description'),
  },
  {
    key: 'include_audit_logs',
    title: t('admin.backup.content.auditLogs.title'),
    description: t('admin.backup.content.auditLogs.description'),
  },
  {
    key: 'include_runtime_data',
    title: t('admin.backup.content.runtimeData.title'),
    description: t('admin.backup.content.runtimeData.description'),
  },
])
const contentExcludedCount = computed(() =>
  contentOptions.value.reduce(
    (total, option) =>
      contentForm.value[option.key] ? total : total + contentTablePatternCounts[option.key],
    0,
  ),
)

// Schedule fields form an independently saved draft, with retention values
// measured in days and completed backup count.
const scheduleForm = ref<BackupScheduleConfig>({
  enabled: false,
  cron_expr: '0 2 * * *',
  retain_days: 14,
  retain_count: 10,
})

// Record state drives table actions, creation progress, restoration identity,
// and the currently opened split-archive download links.
const backups = ref<BackupRecord[]>([])
const loadingBackups = ref(true)
const backupsLoadFailed = ref(false)

// backupReadSequence identifies the latest history read across manual refresh and visibility changes.
let backupReadSequence = 0

// backupTableState keeps available records visible during refresh and
// distinguishes a first request, a failed read, and a successful empty result.
const backupTableState = computed(() => {
  if (backups.value.length > 0) return 'ready'
  if (loadingBackups.value) return 'loading'
  if (backupsLoadFailed.value) return 'failed'
  return 'empty'
})
const creatingBackup = ref(false)
// restoringId identifies the row under restoration; an empty value releases
// all restore action indicators.
const restoringId = ref('')
const manualExpireDays = ref(14)
const downloadParts = ref<BackupDownloadPart[]>([])
const downloadPartsModalOpen = ref(false)

// Creation and restoration each own one interval handle; stopping scheduling
// leaves outstanding status requests free to settle.
const pollingTimer = ref<ReturnType<typeof setInterval> | null>(null)
const restoringPollingTimer = ref<ReturnType<typeof setInterval> | null>(null)
// MAX_POLL_COUNT bounds two-second status attempts to approximately thirty
// minutes before releasing the local operation indicator.
const MAX_POLL_COUNT = 900

/**
 * updateRecordInList replaces an existing backup row with a fetched record of
 * the same ID. Missing rows are left absent, so polling updates do not append
 * records that a list refresh removed.
 */
function updateRecordInList(updated: BackupRecord) {
  const idx = backups.value.findIndex((r) => r.id === updated.id)
  if (idx >= 0) {
    backups.value[idx] = updated
  }
}

/**
 * startPolling schedules status reads for one created backup and replaces the
 * current creation timer. Each successful read updates an existing row, and
 * terminal status clears the busy indicator, reports the result, and reloads
 * the list.
 *
 * The attempt limit stops scheduling and warns that server work may still be
 * running. Read failures consume attempts but keep polling. Clearing the timer
 * does not cancel outstanding reads, so overlapping responses or a late
 * response after visibility changes can still update component state.
 */
function startPolling(backupId: string) {
  stopPolling()
  let count = 0
  pollingTimer.value = setInterval(async () => {
    // Exhausting UI attempts does not stop the server task; release this
    // indicator and leave a running-operation warning for the administrator.
    if (count++ >= MAX_POLL_COUNT) {
      stopPolling()
      creatingBackup.value = false
      appStore.showWarning(t('admin.backup.operations.backupRunning'))
      return
    }
    try {
      const record = await adminAPI.backup.getBackup(backupId)
      updateRecordInList(record)
      if (record.status === 'completed' || record.status === 'failed') {
        stopPolling()
        creatingBackup.value = false
        if (record.status === 'completed') {
          appStore.showSuccess(t('admin.backup.operations.backupCreated'))
        } else {
          appStore.showError(record.error_message || t('admin.backup.operations.backupFailed'))
        }
        await loadBackups()
      }
    } catch {
      // A failed status read leaves server work unresolved, so retain the
      // interval and try again within the remaining attempt budget.
    }
  }, 2000)
}

/**
 * stopPolling clears the current creation interval and releases its stored
 * handle. It leaves the creation busy indicator and any outstanding request
 * unchanged, allowing visibility handling to resume server work later.
 */
function stopPolling() {
  if (pollingTimer.value) {
    // Clear future ticks while retaining any already-issued status request,
    // which may still update the displayed record after this call.
    clearInterval(pollingTimer.value)
    pollingTimer.value = null
  }
}

/**
 * startRestorePolling schedules status reads for a restoring backup and
 * replaces the current restoration timer. The fetched restore_status
 * determines completion, while the general backup status remains available in
 * the same displayed record.
 *
 * Terminal responses clear the restoring ID, notify the administrator, and
 * refresh the list. Failed reads keep consuming the bounded polling attempts.
 * Stopping the interval only prevents future ticks: outstanding requests can
 * still complete, and the attempt limit releases the UI while server
 * restoration may continue.
 */
function startRestorePolling(backupId: string) {
  stopRestorePolling()
  let count = 0
  restoringPollingTimer.value = setInterval(async () => {
    // Exhausting UI attempts does not stop the server task; release this
    // indicator and leave a running-operation warning for the administrator.
    if (count++ >= MAX_POLL_COUNT) {
      stopRestorePolling()
      restoringId.value = ''
      appStore.showWarning(t('admin.backup.operations.restoreRunning'))
      return
    }
    try {
      const record = await adminAPI.backup.getBackup(backupId)
      updateRecordInList(record)
      if (record.restore_status === 'completed' || record.restore_status === 'failed') {
        stopRestorePolling()
        restoringId.value = ''
        if (record.restore_status === 'completed') {
          appStore.showSuccess(t('admin.backup.actions.restoreSuccess'))
        } else {
          appStore.showError(record.restore_error || t('admin.backup.operations.restoreFailed'))
        }
        await loadBackups()
      }
    } catch {
      // A failed status read leaves server work unresolved, so retain the
      // interval and try again within the remaining attempt budget.
    }
  }, 2000)
}

/**
 * stopRestorePolling clears the current restoration interval and resets its
 * handle. It leaves restoringId and outstanding status requests untouched, so
 * this stop operation controls scheduling rather than server restoration or
 * response publication.
 */
function stopRestorePolling() {
  if (restoringPollingTimer.value) {
    // Clearing this handle stops future ticks; an outstanding restore status
    // request can still settle and publish its result.
    clearInterval(restoringPollingTimer.value)
    restoringPollingTimer.value = null
  }
}

/**
 * handleVisibilityChange pauses both polling intervals while the browser
 * document is hidden. When visibility returns, it refreshes records and
 * restarts polling for the first running creation and the first running
 * restoration found in the resulting list.
 *
 * The component remains mounted when its settings panel is hidden, so panel
 * selection does not trigger this listener. A failed list refresh leaves prior
 * records available for the scan. Existing in-flight reads and visibility
 * refreshes are not cancelled or checked against a component generation.
 */
function handleVisibilityChange() {
  if (document.hidden) {
    stopPolling()
    stopRestorePolling()
  } else {
    // Server work continues while the document is hidden; refresh records
    // before choosing which creation and restoration to poll again.
    loadBackups().then(() => {
      const running = backups.value.find((r) => r.status === 'running')
      if (running) {
        creatingBackup.value = true
        startPolling(running.id)
      }
      const restoring = backups.value.find((r) => r.restore_status === 'running')
      if (restoring) {
        restoringId.value = restoring.id
        startRestorePolling(restoring.id)
      }
    })
  }
}

// R2 guide state controls the setup modal and its translated example
// configuration rows for remote backup storage.
const showR2Guide = ref(false)
const r2ConfigRows = computed(() => [
  { field: t('admin.backup.s3.endpoint'), value: 'https://<account_id>.r2.cloudflarestorage.com' },
  { field: t('admin.backup.s3.region'), value: 'auto' },
  { field: t('admin.backup.s3.bucket'), value: t('admin.backup.r2Guide.step4.bucketValue') },
  { field: t('admin.backup.s3.prefix'), value: 'backups/' },
  { field: 'Access Key ID', value: t('admin.backup.r2Guide.step4.fromStep2') },
  { field: 'Secret Access Key', value: t('admin.backup.r2Guide.step4.fromStep2') },
  { field: t('admin.backup.s3.forcePathStyle'), value: t('admin.backup.r2Guide.step4.unchecked') },
])

/**
 * normalizeS3Form supplies editable S3 defaults and clears the secret field
 * when loading configuration. Missing upload modes use spooled_put;
 * buildStoragePayload restores the current secret afterward when preparing
 * test and save requests.
 */
function normalizeS3Form(cfg?: Partial<BackupS3Config>): BackupS3Config {
  return {
    endpoint: cfg?.endpoint || '',
    region: cfg?.region || 'auto',
    bucket: cfg?.bucket || '',
    access_key_id: cfg?.access_key_id || '',
    secret_access_key: '',
    prefix: cfg?.prefix || 'backups/',
    force_path_style: Boolean(cfg?.force_path_style),
    upload_mode: cfg?.upload_mode === 'multipart' ? 'multipart' : 'spooled_put',
  }
}

/**
 * buildStoragePayload combines the selected storage type and local path with
 * normalized S3 draft fields. It calls the loading normalizer to apply
 * endpoint and upload defaults, then restores the secret entered in the S3
 * form before returning a fresh payload object.
 *
 * Loading configuration deliberately clears secret text, so using that
 * normalized object directly for submission would discard a replacement
 * credential. The explicit reassignment preserves the draft secret for both
 * connection testing and storage saves, including an empty value.
 */
function buildStoragePayload(): BackupStorageConfig {
  // The loading normalizer clears secrets, so restore the current input
  // before tests or saves can submit a replacement credential.
  const s3Payload = normalizeS3Form(s3Form.value)
  s3Payload.secret_access_key = s3Form.value.secret_access_key || ''
  return {
    type: storageForm.value.type,
    local_path: storageForm.value.local_path,
    s3: s3Payload,
  }
}

/**
 * loadStorageConfig fills the storage draft and derives the configured-secret
 * hint from access-key presence. It reports read errors and marks the storage
 * group clean after the next render tick, including when the previous draft
 * survives a failed read.
 */
async function loadStorageConfig() {
  if (configStatus.storage !== 'ready') configStatus.storage = 'pending'
  try {
    const cfg = await adminAPI.backup.getStorageConfig()
    storageForm.value = {
      type: cfg.type || 'local',
      local_path: cfg.local_path || '',
      s3: normalizeS3Form(cfg.s3),
    }
    s3Form.value = normalizeS3Form(cfg.s3)
    s3SecretConfigured.value = Boolean(cfg.s3?.access_key_id)
    configStatus.storage = 'ready'
  } catch (error) {
    if (configStatus.storage !== 'ready') configStatus.storage = 'failed'
    appStore.showError(
      (error as { message?: string /* Optional server error message. */ })?.message ||
        t('errors.networkError'),
    )
  }
  // Establish the snapshot after loading finishes, including handled failures
  // that retain the current draft, so discard completes with a clean group.
  await nextTick()
  markClean('storage')
}

/**
 * saveStorageConfig saves the storage payload through step-up verification and
 * reloads the persisted configuration. The reload clears the entered secret
 * and establishes the clean storage snapshot before this save target returns
 * success.
 *
 * Cancellation or blocked verification returns false for the page save
 * registry, and other write failures show an error. The reload handles its own
 * errors, so a failed readback can still yield true after the write succeeded
 * and can mark the existing draft clean.
 */
async function saveStorageConfig(): Promise<boolean> {
  try {
    await backupStepUp.run(() => adminAPI.backup.updateStorageConfig(buildStoragePayload()))
    // Readback removes entered secret text and updates the clean snapshot
    // after persistence, including the readback helper's handled-error path.
    await loadStorageConfig()
    return true
  } catch (error) {
    if (isStepUpCancelled(error)) return false
    if (reportStepUpBlocked(error)) return false
    appStore.showError(
      (error as { message?: string /* Optional server error message. */ })?.message ||
        t('errors.networkError'),
    )
    return false
  }
}

/**
 * testStorage checks connectivity using the current storage payload, including
 * any entered secret. It owns the testing indicator until the request settles
 * and shows the returned message or the localized result text.
 */
async function testStorage() {
  testingStorage.value = true
  try {
    const result = await adminAPI.backup.testStorageConnection(buildStoragePayload())
    if (result.ok) {
      appStore.showSuccess(result.message || t('admin.backup.storage.testSuccess'))
    } else {
      appStore.showError(result.message || t('admin.backup.storage.testFailed'))
    }
  } catch (error) {
    appStore.showError(
      (error as { message?: string /* Optional server error message. */ })?.message ||
        t('errors.networkError'),
    )
  } finally {
    testingStorage.value = false
  }
}

/**
 * normalizeContentConfig coerces the four inclusion flags and supplies an
 * empty excluded-table list when missing. An existing excluded_table_data
 * array remains shared with the input configuration used by the caller.
 */
function normalizeContentConfig(cfg?: Partial<BackupContentConfig>): BackupContentConfig {
  return {
    include_usage_records: Boolean(cfg?.include_usage_records),
    include_ops_logs: Boolean(cfg?.include_ops_logs),
    include_audit_logs: Boolean(cfg?.include_audit_logs),
    include_runtime_data: Boolean(cfg?.include_runtime_data),
    excluded_table_data: cfg?.excluded_table_data || [],
  }
}

/**
 * loadContentConfig replaces the content draft with normalized server fields
 * and marks it clean after rendering. Read failures display an error, retain
 * the existing draft, and still establish a clean snapshot.
 */
async function loadContentConfig() {
  if (configStatus.content !== 'ready') configStatus.content = 'pending'
  try {
    const cfg = await adminAPI.backup.getContentConfig()
    contentForm.value = normalizeContentConfig(cfg)
    configStatus.content = 'ready'
  } catch (error) {
    if (configStatus.content !== 'ready') configStatus.content = 'failed'
    appStore.showError(
      (error as { message?: string /* Optional server error message. */ })?.message ||
        t('errors.networkError'),
    )
  }
  // A failed read keeps existing inclusion choices; the loader still records
  // those choices as the clean result of this load attempt.
  await nextTick()
  markClean('content')
}

/**
 * saveContentConfig submits normalized inclusion settings and uses the
 * returned configuration as the current draft. After Vue processes that
 * replacement, it records a clean content snapshot and returns true to the
 * page save registry.
 *
 * A rejected update displays an error and returns false, leaving the existing
 * draft available for retry. The clean snapshot therefore follows the server
 * response on success, while this operation leaves storage and schedule
 * snapshots to their separate registered save targets.
 */
async function saveContentConfig(): Promise<boolean> {
  try {
    const cfg = await adminAPI.backup.updateContentConfig(normalizeContentConfig(contentForm.value))
    contentForm.value = normalizeContentConfig(cfg)
    await nextTick()
    // Record the server-returned content after Vue observes the draft
    // replacement so the registry compares against persisted fields on
    // success.
    markClean('content')
    return true
  } catch (error) {
    appStore.showError(
      (error as { message?: string /* Optional server error message. */ })?.message ||
        t('errors.networkError'),
    )
    return false
  }
}

/**
 * loadSchedule loads the schedule with defaults for empty cron and falsy
 * retention values. It reports errors, retains the prior draft on failure, and
 * marks the resulting schedule clean after the render tick.
 */
async function loadSchedule() {
  if (configStatus.schedule !== 'ready') configStatus.schedule = 'pending'
  try {
    const cfg = await adminAPI.backup.getSchedule()
    scheduleForm.value = {
      enabled: cfg.enabled,
      cron_expr: cfg.cron_expr || '0 2 * * *',
      retain_days: cfg.retain_days || 14,
      retain_count: cfg.retain_count || 10,
    }
    configStatus.schedule = 'ready'
  } catch (error) {
    if (configStatus.schedule !== 'ready') configStatus.schedule = 'failed'
    appStore.showError(
      (error as { message?: string /* Optional server error message. */ })?.message ||
        t('errors.networkError'),
    )
  }
  // Loading marks the visible schedule clean after rendering even when the
  // request failed and the previous schedule remains in the form.
  await nextTick()
  markClean('schedule')
}

/**
 * saveSchedule persists the current schedule and marks its snapshot clean
 * after success. It returns a boolean for the page save registry, showing
 * errors and retaining dirty state when the request rejects.
 */
async function saveSchedule(): Promise<boolean> {
  try {
    await adminAPI.backup.updateSchedule(scheduleForm.value)
    // The schedule request has completed successfully, so subsequent registry
    // checks compare the current schedule against this saved snapshot.
    markClean('schedule')
    return true
  } catch (error) {
    appStore.showError(
      (error as { message?: string /* Optional server error message. */ })?.message ||
        t('errors.networkError'),
    )
    return false
  }
}

/**
 * loadBackups refreshes the history table for mounting, polling, and manual
 * refresh. A newer read owns the records, the failure state, and the loading
 * indicator as soon as it begins. A late result from an earlier read cannot
 * invalidate the newer list or display an error against it. A current failure
 * preserves the last visible records and reports an error through the app store.
 */
async function loadBackups() {
  // Claim the history state before dispatch so an earlier read cannot publish
  // records or clear the latest request's pending indicator.
  const sequence = ++backupReadSequence
  loadingBackups.value = true
  try {
    const result = await adminAPI.backup.listBackups()
    if (sequence !== backupReadSequence) return
    backups.value = result.items || []
    backupsLoadFailed.value = false
  } catch (error) {
    if (sequence !== backupReadSequence) return
    backupsLoadFailed.value = true
    appStore.showError(
      (error as { message?: string /* Optional server error message. */ })?.message ||
        t('errors.networkError'),
    )
  } finally {
    if (sequence === backupReadSequence) loadingBackups.value = false
  }
}

/**
 * createBackup starts a backup through step-up verification using the entered
 * expiry in days. A successful record is inserted and polled; cancellation,
 * blocked verification, conflicts, and errors release the creation indicator
 * through their respective feedback paths.
 */
async function createBackup() {
  creatingBackup.value = true
  try {
    const record = await backupStepUp.run(() =>
      adminAPI.backup.createBackup({ expire_days: manualExpireDays.value }),
    )
    // Show the accepted task before polling so subsequent status responses
    // can replace its row by ID in the displayed list.
    backups.value.unshift(record)
    startPolling(record.id)
  } catch (error: any) {
    if (isStepUpCancelled(error)) {
      creatingBackup.value = false
      return
    }
    if (reportStepUpBlocked(error)) {
      creatingBackup.value = false
      return
    }
    if (error?.response?.status === 409) {
      appStore.showWarning(t('admin.backup.operations.alreadyInProgress'))
    } else {
      appStore.showError(error?.message || t('errors.networkError'))
    }
    creatingBackup.value = false
  }
}

/**
 * downloadBackup chooses a download route from the displayed record and passes
 * sensitive access through step-up verification. Local single files use an
 * authenticated Blob request with a temporary object URL, while split archives
 * and remote records ask the server for download URLs.
 *
 * A parts response opens the link list; a remote single file clicks an anchor
 * in the current page. That navigation remains usable after asynchronous step-up verification has consumed transient browser activation. Missing remote
 * URLs report failure, and the local object URL is revoked after its anchor is
 * clicked.
 */
async function downloadBackup(id: string) {
  try {
    const record = backups.value.find((item) => item.id === id)
    if (recordStorageType(record) === 'local' && !record?.parts?.length) {
      const blob = await backupStepUp.run(() => adminAPI.backup.downloadBackupFile(id))
      // The object URL belongs to this one download action; release it after
      // the temporary anchor has initiated the browser download.
      const url = URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = url
      link.download = record?.file_name || `${id}.sql.gz`
      document.body.appendChild(link)
      link.click()
      link.remove()
      URL.revokeObjectURL(url)
      return
    }
    const result = await backupStepUp.run(() => adminAPI.backup.getDownloadURL(id))
    if (result.parts && result.parts.length > 0) {
      downloadParts.value = result.parts
      downloadPartsModalOpen.value = true
      return
    }
    if (!result.url) {
      throw new Error(t('admin.backup.actions.downloadFailed'))
    }
    // The signed URL supplies attachment disposition; same-page anchor
    // navigation still works after step-up awaits have consumed transient
    // activation.
    const link = document.createElement('a')
    link.href = result.url
    link.rel = 'noopener'
    link.click()
  } catch (error) {
    if (isStepUpCancelled(error)) return
    if (reportStepUpBlocked(error)) return
    appStore.showError(
      (error as { message?: string /* Optional server error message. */ })?.message ||
        t('errors.networkError'),
    )
  }
}

/**
 * closeDownloadParts closes the archive link dialog and clears its part list.
 */
function closeDownloadParts() {
  downloadPartsModalOpen.value = false
  downloadParts.value = []
}

/**
 * restoreBackup asks for confirmation and a password before step-up
 * verification starts restoration. Success updates the existing record and
 * begins restore polling; cancelled, blocked, conflicting, or failed requests
 * release the selected restore indicator.
 */
async function restoreBackup(id: string) {
  if (!window.confirm(t('admin.backup.actions.restoreConfirm'))) return
  const password = window.prompt(t('admin.backup.actions.restorePasswordPrompt'))
  if (!password) return
  restoringId.value = id
  try {
    const record = await backupStepUp.run(() => adminAPI.backup.restoreBackup(id, password))
    updateRecordInList(record)
    startRestorePolling(id)
  } catch (error: any) {
    restoringId.value = ''
    if (isStepUpCancelled(error)) return
    if (reportStepUpBlocked(error)) return
    // The API client exposes normalized status at the top level, while the
    // response form remains accepted for alternate error shapes.
    if (error?.status === 409 || error?.response?.status === 409) {
      appStore.showWarning(t('admin.backup.operations.restoreRunning'))
    } else {
      appStore.showError(error?.message || t('errors.networkError'))
    }
  }
}

/**
 * removeBackup deletes the selected backup after browser confirmation and
 * reloads the records. Cancellation returns immediately, while a failed delete
 * or refresh displays the API error through the application store.
 */
async function removeBackup(id: string) {
  if (!window.confirm(t('admin.backup.actions.deleteConfirm'))) return
  try {
    await adminAPI.backup.deleteBackup(id)
    appStore.showSuccess(t('admin.backup.actions.deleted'))
    await loadBackups()
  } catch (error) {
    appStore.showError(
      (error as { message?: string /* Optional server error message. */ })?.message ||
        t('errors.networkError'),
    )
  }
}

/**
 * statusClass returns semantic badge colors for completed, running, or failed
 * backup states. Any other status receives neutral classes, keeping unknown
 * server statuses displayable in both light and dark themes.
 */
function statusClass(status: string): string {
  switch (status) {
    case 'completed':
      return 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-300'
    case 'running':
      return 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300'
    case 'failed':
      return 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300'
    default:
      return 'bg-gray-100 text-gray-700 dark:bg-dark-800 dark:text-gray-300'
  }
}

/**
 * recordStorageType selects the local or remote download family from a
 * possibly older backup record. A parts list takes precedence over storage
 * metadata because split archives require server-provided part links even when
 * storage_type is absent.
 *
 * Otherwise, explicit local storage or a missing storage type together with an
 * empty S3 key selects local download. All remaining records, including a
 * missing record, use the remote URL endpoint. This precedence supports
 * records written before storage_type was included in API responses.
 */
function recordStorageType(record?: BackupRecord): 'local' | 's3' {
  if (!record) return 's3'
  // Part metadata wins over legacy storage hints because the download dialog
  // needs the individual URLs returned by the remote endpoint.
  if (record.parts?.length) return 's3'
  if (record.storage_type === 'local' || (!record.storage_type && !record.s3_key)) return 'local'
  return 's3'
}

/**
 * storageLabel translates the download storage family inferred for a backup
 * record.
 */
function storageLabel(record: BackupRecord): string {
  return recordStorageType(record) === 'local'
    ? t('admin.backup.storage.local')
    : t('admin.backup.storage.remote')
}

/**
 * formatSize displays positive byte counts using B, KB, or MB labels. Missing
 * or nonpositive counts produce a dash, and larger counts use powers of 1024
 * with one decimal place.
 */
function formatSize(bytes: number): string {
  if (!bytes || bytes <= 0) return '-'
  // Binary powers determine the displayed kilobyte and megabyte units.
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

/**
 * formatDate formats a valid timestamp using the browser locale and timezone.
 * Empty values display a dash, while invalid date strings remain visible
 * unchanged so the table retains the supplied record value.
 */
function formatDate(value?: string): string {
  if (!value) return '-'
  const date = new Date(value)
  // Retain malformed server text so the table exposes the supplied timestamp
  // even when the browser cannot interpret it.
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString()
}

// The registry saves storage, content, and schedule independently; each
// snapshot tracks exactly the fields supplied by its getter.
const { isDirty, markClean } = useDirtyTracker({
  storage: () => ({ type: storageForm.value.type, s3: s3Form.value }),
  content: () => contentForm.value,
  schedule: () => scheduleForm.value,
})
useSettingsSaveTarget('backupStorage', {
  dirty: computed(() => isDirty('storage')),
  save: saveStorageConfig,
  discard: loadStorageConfig,
})
useSettingsSaveTarget('backupContent', {
  dirty: computed(() => isDirty('content')),
  save: saveContentConfig,
  discard: loadContentConfig,
})
useSettingsSaveTarget('backupSchedule', {
  dirty: computed(() => isDirty('schedule')),
  save: saveSchedule,
  discard: loadSchedule,
})

// Register visibility handling before loading records, then resume existing
// server tasks after the parallel configuration and record reads finish.
onMounted(async () => {
  document.addEventListener('visibilitychange', handleVisibilityChange)
  await Promise.all([loadStorageConfig(), loadContentConfig(), loadSchedule(), loadBackups()])

  // Loaded records can contain tasks started before mounting; resume the
  // first running creation and restoration using their separate timers.
  const runningBackup = backups.value.find((r) => r.status === 'running')
  if (runningBackup) {
    creatingBackup.value = true
    startPolling(runningBackup.id)
  }
  const restoringBackup = backups.value.find((r) => r.restore_status === 'running')
  if (restoringBackup) {
    restoringId.value = restoringBackup.id
    startRestorePolling(restoringBackup.id)
  }
})

// Release currently stored timers and the document listener on unmount;
// outstanding asynchronous requests are not cancelled by this cleanup.
onBeforeUnmount(() => {
  stopPolling()
  stopRestorePolling()
  document.removeEventListener('visibilitychange', handleVisibilityChange)
})
</script>

<style scoped>
.modal-enter-active,
.modal-leave-active {
  transition: opacity var(--motion-normal) var(--motion-ease);
}
.modal-enter-from,
.modal-leave-to {
  opacity: 0;
}
</style>
