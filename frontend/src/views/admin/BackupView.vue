<template>
    <div class="space-y-6">
      <!-- 备份存储配置 -->
      <div class="card p-6">
        <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 class="text-base font-semibold text-gray-900 dark:text-white">
              {{ t('admin.backup.storage.title') }}
            </h3>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
              {{ t('admin.backup.storage.description') }}
            </p>
          </div>
        </div>
        <div class="mb-4 inline-flex rounded-lg border border-gray-200 bg-gray-50 p-1 text-sm dark:border-dark-700 dark:bg-dark-900">
          <button
            type="button"
            class="rounded-md px-3 py-1.5 font-medium transition"
            :class="storageForm.type === 'local' ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300' : 'text-gray-600 hover:text-gray-900 dark:text-gray-400 dark:hover:text-gray-200'"
            @click="storageForm.type = 'local'"
          >
            {{ t('admin.backup.storage.local') }}
          </button>
          <button
            type="button"
            class="rounded-md px-3 py-1.5 font-medium transition"
            :class="storageForm.type === 's3' ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300' : 'text-gray-600 hover:text-gray-900 dark:text-gray-400 dark:hover:text-gray-200'"
            @click="storageForm.type = 's3'"
          >
            {{ t('admin.backup.storage.remote') }}
          </button>
        </div>

        <div v-if="storageForm.type === 'local'" class="grid grid-cols-1 gap-3 md:grid-cols-2">
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.storage.localPath') }}</label>
            <input :value="storageForm.local_path || '-'" class="input w-full bg-gray-50 font-mono text-sm dark:bg-dark-900" readonly />
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.backup.storage.localHint') }}</p>
          </div>
        </div>

        <div v-else>
          <p class="mb-3 text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.backup.s3.descriptionPrefix') }}
            <button type="button" class="text-primary-600 underline hover:text-primary-700 dark:text-primary-400 dark:hover:text-primary-300" @click="showR2Guide = true">Cloudflare R2</button>
            {{ t('admin.backup.s3.descriptionSuffix') }}
          </p>
          <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.s3.endpoint') }}</label>
            <input v-model="s3Form.endpoint" class="input w-full" placeholder="https://<account_id>.r2.cloudflarestorage.com" />
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.s3.region') }}</label>
            <input v-model="s3Form.region" class="input w-full" placeholder="auto" />
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.s3.bucket') }}</label>
            <input v-model="s3Form.bucket" class="input w-full" />
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.s3.prefix') }}</label>
            <input v-model="s3Form.prefix" class="input w-full" placeholder="backups/" />
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.s3.accessKeyId') }}</label>
            <input v-model="s3Form.access_key_id" class="input w-full" />
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.s3.secretAccessKey') }}</label>
            <input v-model="s3Form.secret_access_key" type="password" class="input w-full" :placeholder="s3SecretConfigured ? t('admin.backup.s3.secretConfigured') : ''" />
          </div>
          <label class="inline-flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300 md:col-span-2">
            <input v-model="s3Form.force_path_style" type="checkbox" />
            <span>{{ t('admin.backup.s3.forcePathStyle') }}</span>
          </label>
          <div class="md:col-span-2">
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.s3.uploadMode') }}</label>
            <div class="inline-flex border border-gray-200 bg-gray-50 p-1 text-sm dark:border-dark-700 dark:bg-dark-900">
              <button
                type="button"
                class="px-3 py-1.5 font-medium transition"
                :class="s3Form.upload_mode === 'multipart' ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300' : 'text-gray-600 hover:text-gray-900 dark:text-gray-400 dark:hover:text-gray-200'"
                @click="s3Form.upload_mode = 'multipart'"
              >
                {{ t('admin.backup.s3.uploadModeMultipart') }}
              </button>
              <button
                type="button"
                class="px-3 py-1.5 font-medium transition"
                :class="s3Form.upload_mode === 'spooled_put' ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-700 dark:text-primary-300' : 'text-gray-600 hover:text-gray-900 dark:text-gray-400 dark:hover:text-gray-200'"
                @click="s3Form.upload_mode = 'spooled_put'"
              >
                {{ t('admin.backup.s3.uploadModeSpooled') }}
              </button>
            </div>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.backup.s3.uploadModeHint') }}</p>
          </div>
          </div>
        </div>
        <div class="mt-4 flex flex-wrap gap-2">
          <button type="button" class="btn btn-secondary btn-sm" :disabled="testingStorage" @click="testStorage">
            {{ testingStorage ? t('common.loading') : t('admin.backup.storage.testConnection') }}
          </button>
          <button type="button" class="btn btn-primary btn-sm" :disabled="savingStorage" @click="saveStorageConfig">
            {{ savingStorage ? t('common.loading') : t('common.save') }}
          </button>
        </div>
      </div>

      <!-- 备份内容配置 -->
      <div class="card p-6">
        <div class="mb-4">
          <h3 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('admin.backup.content.title') }}
          </h3>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.backup.content.description') }}
          </p>
        </div>
        <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
          <label
            v-for="option in contentOptions"
            :key="option.key"
            class="flex items-start gap-3 rounded-lg border border-gray-200 p-3 text-sm dark:border-dark-700"
          >
            <input v-model="contentForm[option.key]" type="checkbox" class="mt-1" />
            <span>
              <span class="block font-medium text-gray-800 dark:text-gray-200">{{ option.title }}</span>
              <span class="mt-1 block text-xs leading-5 text-gray-500 dark:text-gray-400">{{ option.description }}</span>
            </span>
          </label>
        </div>
        <p class="mt-3 text-xs text-gray-500 dark:text-gray-400">
          {{ t('admin.backup.content.excludedCount', { count: contentExcludedCount }) }}
        </p>
        <div class="mt-4">
          <button type="button" class="btn btn-primary btn-sm" :disabled="savingContent" @click="saveContentConfig">
            {{ savingContent ? t('common.loading') : t('common.save') }}
          </button>
        </div>
      </div>

      <!-- 定时备份配置 -->
      <div class="card p-6">
        <div class="mb-4">
          <h3 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('admin.backup.schedule.title') }}
          </h3>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.backup.schedule.description') }}
          </p>
        </div>
        <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
          <label class="inline-flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300 md:col-span-2">
            <input v-model="scheduleForm.enabled" type="checkbox" />
            <span>{{ t('admin.backup.schedule.enabled') }}</span>
          </label>
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.schedule.cronExpr') }}</label>
            <input v-model="scheduleForm.cron_expr" class="input w-full" placeholder="0 2 * * *" />
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.backup.schedule.cronHint') }}</p>
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.schedule.retainDays') }}</label>
            <input v-model.number="scheduleForm.retain_days" type="number" min="0" class="input w-full" />
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.backup.schedule.retainDaysHint') }}</p>
          </div>
          <div>
            <label class="mb-1 block text-xs font-medium text-gray-600 dark:text-gray-400">{{ t('admin.backup.schedule.retainCount') }}</label>
            <input v-model.number="scheduleForm.retain_count" type="number" min="0" class="input w-full" />
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.backup.schedule.retainCountHint') }}</p>
          </div>
        </div>
        <div class="mt-4">
          <button type="button" class="btn btn-primary btn-sm" :disabled="savingSchedule" @click="saveSchedule">
            {{ savingSchedule ? t('common.loading') : t('common.save') }}
          </button>
        </div>
      </div>

      <!-- 备份操作 -->
      <div class="card p-6">
        <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 class="text-base font-semibold text-gray-900 dark:text-white">
              {{ t('admin.backup.operations.title') }}
            </h3>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
              {{ t('admin.backup.operations.description') }}
            </p>
          </div>
          <div class="flex flex-wrap items-center gap-2">
            <div class="flex items-center gap-1">
              <label class="text-xs text-gray-600 dark:text-gray-400">{{ t('admin.backup.operations.expireDays') }}</label>
              <input v-model.number="manualExpireDays" type="number" min="0" class="input w-20 text-xs" />
            </div>
            <button type="button" class="btn btn-primary btn-sm" :disabled="creatingBackup" @click="createBackup">
              {{ creatingBackup ? t('admin.backup.operations.backing') : t('admin.backup.operations.createBackup') }}
            </button>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="loadingBackups" @click="loadBackups">
              {{ loadingBackups ? t('common.loading') : t('common.refresh') }}
            </button>
          </div>
        </div>

        <div class="overflow-x-auto">
          <table class="w-full min-w-[800px] text-sm">
            <thead>
              <tr class="border-b border-gray-200 text-left text-xs uppercase tracking-wide text-gray-500 dark:border-dark-700 dark:text-gray-400">
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
            <tbody>
              <tr v-for="record in backups" :key="record.id" class="border-b border-gray-100 align-top dark:border-dark-800">
                <td class="py-3 pr-4 font-mono text-xs">{{ record.id }}</td>
                <td class="py-3 pr-4">
                  <span
                    class="rounded px-2 py-0.5 text-xs"
                    :class="statusClass(record.status)"
                  >
                    {{ record.status === 'running' && record.progress
                      ? t(`admin.backup.progress.${record.progress}`)
                      : t(`admin.backup.status.${record.status}`) }}
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
                  {{ record.triggered_by === 'scheduled' ? t('admin.backup.trigger.scheduled') : t('admin.backup.trigger.manual') }}
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
                      {{ restoringId === record.id ? t('common.loading') : t('admin.backup.actions.restore') }}
                    </button>
                    <button
                      type="button"
                      class="btn btn-danger btn-xs"
                      @click="removeBackup(record.id)"
                    >
                      {{ t('common.delete') }}
                    </button>
                  </div>
                </td>
              </tr>
              <tr v-if="backups.length === 0">
                <td colspan="10" class="py-6 text-center text-sm text-gray-500 dark:text-gray-400">
                  {{ t('admin.backup.empty') }}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>

    <!-- Cloudflare R2 配置教程弹窗 -->
    <teleport to="body">
      <transition name="modal">
        <div v-if="showR2Guide" class="fixed inset-0 z-50 flex items-center justify-center p-4" @mousedown.self="showR2Guide = false">
          <div class="fixed inset-0 bg-black/50" @click="showR2Guide = false"></div>
          <div class="relative max-h-[85vh] w-full max-w-2xl overflow-y-auto rounded-xl bg-white p-6 shadow-2xl dark:bg-dark-800">
            <button type="button" class="absolute right-4 top-4 text-gray-400 hover:text-gray-600 dark:hover:text-gray-200" @click="showR2Guide = false">
              <svg class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" /></svg>
            </button>

            <h2 class="mb-4 text-lg font-bold text-gray-900 dark:text-white">{{ t('admin.backup.r2Guide.title') }}</h2>
            <p class="mb-4 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.backup.r2Guide.intro') }}</p>

            <!-- 步骤 1 -->
            <div class="mb-5">
              <h3 class="mb-2 flex items-center gap-2 text-sm font-semibold text-gray-900 dark:text-white">
                <span class="flex h-6 w-6 items-center justify-center rounded-full bg-primary-100 text-xs font-bold text-primary-700 dark:bg-primary-900/40 dark:text-primary-300">1</span>
                {{ t('admin.backup.r2Guide.step1.title') }}
              </h3>
              <ol class="ml-8 list-decimal space-y-1 text-sm text-gray-600 dark:text-gray-300">
                <li>{{ t('admin.backup.r2Guide.step1.line1') }}</li>
                <li>{{ t('admin.backup.r2Guide.step1.line2') }}</li>
                <li>{{ t('admin.backup.r2Guide.step1.line3') }}</li>
              </ol>
            </div>

            <!-- 步骤 2 -->
            <div class="mb-5">
              <h3 class="mb-2 flex items-center gap-2 text-sm font-semibold text-gray-900 dark:text-white">
                <span class="flex h-6 w-6 items-center justify-center rounded-full bg-primary-100 text-xs font-bold text-primary-700 dark:bg-primary-900/40 dark:text-primary-300">2</span>
                {{ t('admin.backup.r2Guide.step2.title') }}
              </h3>
              <ol class="ml-8 list-decimal space-y-1 text-sm text-gray-600 dark:text-gray-300">
                <li>{{ t('admin.backup.r2Guide.step2.line1') }}</li>
                <li>{{ t('admin.backup.r2Guide.step2.line2') }}</li>
                <li>{{ t('admin.backup.r2Guide.step2.line3') }}</li>
                <li>{{ t('admin.backup.r2Guide.step2.line4') }}</li>
              </ol>
              <div class="mt-2 rounded-lg bg-amber-50 p-3 text-xs text-amber-700 dark:bg-amber-900/20 dark:text-amber-300">
                {{ t('admin.backup.r2Guide.step2.warning') }}
              </div>
            </div>

            <!-- 步骤 3 -->
            <div class="mb-5">
              <h3 class="mb-2 flex items-center gap-2 text-sm font-semibold text-gray-900 dark:text-white">
                <span class="flex h-6 w-6 items-center justify-center rounded-full bg-primary-100 text-xs font-bold text-primary-700 dark:bg-primary-900/40 dark:text-primary-300">3</span>
                {{ t('admin.backup.r2Guide.step3.title') }}
              </h3>
              <p class="ml-8 text-sm text-gray-600 dark:text-gray-300">{{ t('admin.backup.r2Guide.step3.desc') }}</p>
              <code class="ml-8 mt-1 block rounded bg-gray-100 px-3 py-2 text-xs text-gray-800 dark:bg-dark-700 dark:text-gray-200">https://&lt;{{ t('admin.backup.r2Guide.step3.accountId') }}&gt;.r2.cloudflarestorage.com</code>
            </div>

            <!-- 步骤 4：填写表单 -->
            <div class="mb-5">
              <h3 class="mb-2 flex items-center gap-2 text-sm font-semibold text-gray-900 dark:text-white">
                <span class="flex h-6 w-6 items-center justify-center rounded-full bg-primary-100 text-xs font-bold text-primary-700 dark:bg-primary-900/40 dark:text-primary-300">4</span>
                {{ t('admin.backup.r2Guide.step4.title') }}
              </h3>
              <div class="ml-8 overflow-hidden rounded-lg border border-gray-200 dark:border-dark-600">
                <table class="w-full text-sm">
                  <tbody>
                    <tr v-for="(row, i) in r2ConfigRows" :key="i" class="border-b border-gray-100 dark:border-dark-700 last:border-0">
                      <td class="whitespace-nowrap bg-gray-50 px-3 py-2 font-medium text-gray-700 dark:bg-dark-700 dark:text-gray-300">{{ row.field }}</td>
                      <td class="px-3 py-2 text-gray-600 dark:text-gray-400"><code class="text-xs">{{ row.value }}</code></td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </div>

            <!-- 免费额度说明 -->
            <div class="rounded-lg bg-green-50 p-3 text-xs text-green-700 dark:bg-green-900/20 dark:text-green-300">
              {{ t('admin.backup.r2Guide.freeTier') }}
            </div>

            <div class="mt-4 text-right">
              <button type="button" class="btn btn-primary btn-sm" @click="showR2Guide = false">{{ t('common.close') }}</button>
            </div>
          </div>
        </div>
      </transition>
    </teleport>
    <!-- 分卷下载链接 -->
    <teleport to="body">
      <transition name="modal">
        <div
          v-if="downloadPartsModalOpen"
          class="fixed inset-0 z-50 flex items-center justify-center p-4"
          @mousedown.self="closeDownloadParts"
        >
          <div class="fixed inset-0 bg-black/50" @click="closeDownloadParts"></div>
          <div class="relative max-h-[85vh] w-full max-w-lg overflow-y-auto rounded-xl bg-white p-6 shadow-2xl dark:bg-dark-800">
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
                class="flex items-center justify-between gap-3 rounded-lg border border-gray-200 px-3 py-2 dark:border-dark-600"
              >
                <span class="text-sm text-gray-700 dark:text-gray-300">
                  {{ t('admin.backup.actions.partLabel', { index: part.index }) }}
                  <span class="ml-2 text-xs text-gray-500 dark:text-gray-400">{{ formatSize(part.size_bytes) }}</span>
                </span>
                <a :href="part.url" class="btn btn-secondary btn-xs" rel="noopener">
                  {{ t('admin.backup.actions.download') }}
                </a>
              </div>
            </div>
          </div>
        </div>
      </transition>
    </teleport>
    <TotpStepUpDialog :controller="backupStepUp" />
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
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
import { useStepUp, isStepUpBlocked, isStepUpCancelled, stepUpBlockReason } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import Icon from '@/components/icons/Icon.vue'

const { t } = useI18n()
const appStore = useAppStore()
const backupStepUp = useStepUp()

// 敏感操作被 2FA 门控拦截时的统一提示。
function reportStepUpBlocked(error: unknown): boolean {
  if (!isStepUpBlocked(error)) return false
  appStore.showError(
    stepUpBlockReason(error) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN'
      ? t('stepUp.adminApiKeyForbidden')
      : t('stepUp.notEnabled')
  )
  return true
}

// 存储配置
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
const s3SecretConfigured = ref(false)
const savingStorage = ref(false)
const testingStorage = ref(false)

// 备份内容配置
const contentForm = ref<BackupContentConfig>({
  include_usage_records: false,
  include_ops_logs: false,
  include_audit_logs: false,
  include_runtime_data: false,
  include_data_share_sessions: false,
  excluded_table_data: [],
})
const savingContent = ref(false)
type BackupContentOptionKey = keyof Pick<BackupContentConfig, 'include_usage_records' | 'include_ops_logs' | 'include_audit_logs' | 'include_runtime_data' | 'include_data_share_sessions'>
const contentTablePatternCounts: Record<BackupContentOptionKey, number> = {
  include_usage_records: 10,
  include_ops_logs: 9,
  include_audit_logs: 5,
  include_runtime_data: 6,
  include_data_share_sessions: 1,
}
const contentOptions = computed<Array<{ key: BackupContentOptionKey, title: string, description: string }>>(() => [
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
  {
    key: 'include_data_share_sessions',
    title: t('admin.backup.content.dataShareSessions.title'),
    description: t('admin.backup.content.dataShareSessions.description'),
  },
])
const contentExcludedCount = computed(() => (
  contentOptions.value.reduce((total, option) => (
    contentForm.value[option.key] ? total : total + contentTablePatternCounts[option.key]
  ), 0)
))

// 定时备份配置
const scheduleForm = ref<BackupScheduleConfig>({
  enabled: false,
  cron_expr: '0 2 * * *',
  retain_days: 14,
  retain_count: 10,
})
const savingSchedule = ref(false)

// 备份记录
const backups = ref<BackupRecord[]>([])
const loadingBackups = ref(false)
const creatingBackup = ref(false)
const restoringId = ref('')
const manualExpireDays = ref(14)
const downloadParts = ref<BackupDownloadPart[]>([])
const downloadPartsModalOpen = ref(false)

// 轮询状态
const pollingTimer = ref<ReturnType<typeof setInterval> | null>(null)
const restoringPollingTimer = ref<ReturnType<typeof setInterval> | null>(null)
const MAX_POLL_COUNT = 900

function updateRecordInList(updated: BackupRecord) {
  const idx = backups.value.findIndex(r => r.id === updated.id)
  if (idx >= 0) {
    backups.value[idx] = updated
  }
}

function startPolling(backupId: string) {
  stopPolling()
  let count = 0
  pollingTimer.value = setInterval(async () => {
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
      // 轮询失败时不中断
    }
  }, 2000)
}

function stopPolling() {
  if (pollingTimer.value) {
    clearInterval(pollingTimer.value)
    pollingTimer.value = null
  }
}

function startRestorePolling(backupId: string) {
  stopRestorePolling()
  let count = 0
  restoringPollingTimer.value = setInterval(async () => {
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
      // 轮询失败时不中断
    }
  }, 2000)
}

function stopRestorePolling() {
  if (restoringPollingTimer.value) {
    clearInterval(restoringPollingTimer.value)
    restoringPollingTimer.value = null
  }
}

function handleVisibilityChange() {
  if (document.hidden) {
    stopPolling()
    stopRestorePolling()
  } else {
    // 标签页恢复时刷新列表，检查是否仍有活跃操作
    loadBackups().then(() => {
      const running = backups.value.find(r => r.status === 'running')
      if (running) {
        creatingBackup.value = true
        startPolling(running.id)
      }
      const restoring = backups.value.find(r => r.restore_status === 'running')
      if (restoring) {
        restoringId.value = restoring.id
        startRestorePolling(restoring.id)
      }
    })
  }
}

// R2 配置教程
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

function buildStoragePayload(): BackupStorageConfig {
  // 提交时必须保留用户在密码框中输入的新 Secret，只有服务端回填表单时才清空。
  const s3Payload = normalizeS3Form(s3Form.value)
  s3Payload.secret_access_key = s3Form.value.secret_access_key || ''
  return {
    type: storageForm.value.type,
    local_path: storageForm.value.local_path,
    s3: s3Payload,
  }
}

async function loadStorageConfig() {
  try {
    const cfg = await adminAPI.backup.getStorageConfig()
    storageForm.value = {
      type: cfg.type || 'local',
      local_path: cfg.local_path || '',
      s3: normalizeS3Form(cfg.s3),
    }
    s3Form.value = normalizeS3Form(cfg.s3)
    s3SecretConfigured.value = Boolean(cfg.s3?.access_key_id)
  } catch (error) {
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  }
}

async function saveStorageConfig() {
  savingStorage.value = true
  try {
    await backupStepUp.run(() => adminAPI.backup.updateStorageConfig(buildStoragePayload()))
    appStore.showSuccess(t('admin.backup.storage.saved'))
    await loadStorageConfig()
  } catch (error) {
    if (isStepUpCancelled(error)) return
    if (reportStepUpBlocked(error)) return
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  } finally {
    savingStorage.value = false
  }
}

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
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  } finally {
    testingStorage.value = false
  }
}

function normalizeContentConfig(cfg?: Partial<BackupContentConfig>): BackupContentConfig {
  return {
    include_usage_records: Boolean(cfg?.include_usage_records),
    include_ops_logs: Boolean(cfg?.include_ops_logs),
    include_audit_logs: Boolean(cfg?.include_audit_logs),
    include_runtime_data: Boolean(cfg?.include_runtime_data),
    include_data_share_sessions: Boolean(cfg?.include_data_share_sessions),
    excluded_table_data: cfg?.excluded_table_data || [],
  }
}

async function loadContentConfig() {
  try {
    const cfg = await adminAPI.backup.getContentConfig()
    contentForm.value = normalizeContentConfig(cfg)
  } catch (error) {
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  }
}

async function saveContentConfig() {
  savingContent.value = true
  try {
    const cfg = await adminAPI.backup.updateContentConfig(normalizeContentConfig(contentForm.value))
    contentForm.value = normalizeContentConfig(cfg)
    appStore.showSuccess(t('admin.backup.content.saved'))
  } catch (error) {
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  } finally {
    savingContent.value = false
  }
}

async function loadSchedule() {
  try {
    const cfg = await adminAPI.backup.getSchedule()
    scheduleForm.value = {
      enabled: cfg.enabled,
      cron_expr: cfg.cron_expr || '0 2 * * *',
      retain_days: cfg.retain_days || 14,
      retain_count: cfg.retain_count || 10,
    }
  } catch (error) {
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  }
}

async function saveSchedule() {
  savingSchedule.value = true
  try {
    await adminAPI.backup.updateSchedule(scheduleForm.value)
    appStore.showSuccess(t('admin.backup.schedule.saved'))
  } catch (error) {
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  } finally {
    savingSchedule.value = false
  }
}

async function loadBackups() {
  loadingBackups.value = true
  try {
    const result = await adminAPI.backup.listBackups()
    backups.value = result.items || []
  } catch (error) {
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  } finally {
    loadingBackups.value = false
  }
}

async function createBackup() {
  creatingBackup.value = true
  try {
    const record = await backupStepUp.run(() => adminAPI.backup.createBackup({ expire_days: manualExpireDays.value }))
    // 插入到列表顶部
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

async function downloadBackup(id: string) {
  try {
    const record = backups.value.find(item => item.id === id)
    if (recordStorageType(record) === 'local' && !record?.parts?.length) {
      const blob = await backupStepUp.run(() => adminAPI.backup.downloadBackupFile(id))
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
    // 预签名 URL 带 attachment disposition，同页 anchor 导航直接触发下载；
    // 不用 window.open：step-up 弹窗 await 会耗尽瞬态用户激活，新标签页会被浏览器拦截。
    const link = document.createElement('a')
    link.href = result.url
    link.rel = 'noopener'
    link.click()
  } catch (error) {
    if (isStepUpCancelled(error)) return
    if (reportStepUpBlocked(error)) return
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  }
}

function closeDownloadParts() {
  downloadPartsModalOpen.value = false
  downloadParts.value = []
}

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
    // apiClient 拦截器把 HTTP 错误归一化为顶层 { status } 平面对象（无 response 字段）
    if (error?.status === 409 || error?.response?.status === 409) {
      appStore.showWarning(t('admin.backup.operations.restoreRunning'))
    } else {
      appStore.showError(error?.message || t('errors.networkError'))
    }
  }
}

async function removeBackup(id: string) {
  if (!window.confirm(t('admin.backup.actions.deleteConfirm'))) return
  try {
    await adminAPI.backup.deleteBackup(id)
    appStore.showSuccess(t('admin.backup.actions.deleted'))
    await loadBackups()
  } catch (error) {
    appStore.showError((error as { message?: string })?.message || t('errors.networkError'))
  }
}

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

function recordStorageType(record?: BackupRecord): 'local' | 's3' {
  if (!record) return 's3'
  if (record.parts?.length) return 's3'
  if (record.storage_type === 'local' || (!record.storage_type && !record.s3_key)) return 'local'
  return 's3'
}

function storageLabel(record: BackupRecord): string {
  return recordStorageType(record) === 'local'
    ? t('admin.backup.storage.local')
    : t('admin.backup.storage.remote')
}

function formatSize(bytes: number): string {
  if (!bytes || bytes <= 0) return '-'
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
}

function formatDate(value?: string): string {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString()
}

onMounted(async () => {
  document.addEventListener('visibilitychange', handleVisibilityChange)
  await Promise.all([
    loadStorageConfig(),
    loadContentConfig(),
    loadSchedule(),
    loadBackups(),
  ])

  // 如果有正在 running 的备份，恢复轮询
  const runningBackup = backups.value.find(r => r.status === 'running')
  if (runningBackup) {
    creatingBackup.value = true
    startPolling(runningBackup.id)
  }
  const restoringBackup = backups.value.find(r => r.restore_status === 'running')
  if (restoringBackup) {
    restoringId.value = restoringBackup.id
    startRestorePolling(restoringBackup.id)
  }
})

onBeforeUnmount(() => {
  stopPolling()
  stopRestorePolling()
  document.removeEventListener('visibilitychange', handleVisibilityChange)
})
</script>

<style scoped>
.modal-enter-active,
.modal-leave-active {
  transition: opacity 0.2s ease;
}
.modal-enter-from,
.modal-leave-to {
  opacity: 0;
}
</style>
