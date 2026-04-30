<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.dataImportTitle')"
    width="normal"
    close-on-click-outside
    :close-on-escape="!defaultsDialogOpen"
    @close="handleClose"
  >
    <form id="import-data-form" class="space-y-4" @submit.prevent="handleImport">
      <div class="text-sm text-gray-600 dark:text-dark-300">
        {{ t('admin.accounts.dataImportHint') }}
      </div>
      <div
        class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-xs text-amber-600 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-400"
      >
        {{ t('admin.accounts.dataImportWarning') }}
      </div>

      <div>
        <label class="input-label">{{ t('admin.accounts.dataImportFile') }}</label>
        <div
          class="flex flex-col gap-3 rounded-lg border border-dashed border-gray-300 bg-gray-50 px-4 py-3 dark:border-dark-600 dark:bg-dark-800 sm:flex-row sm:items-center sm:justify-between"
        >
          <div class="min-w-0">
            <div class="truncate text-sm text-gray-700 dark:text-dark-200">
              {{ fileName || t('admin.accounts.dataImportSelectFile') }}
            </div>
            <div class="text-xs text-gray-500 dark:text-dark-400">JSON (.json)</div>
          </div>
          <div class="flex shrink-0 flex-wrap gap-2">
            <button
              type="button"
              class="btn btn-secondary inline-flex items-center gap-1.5"
              :disabled="importing"
              @click="openDefaultsDialog"
            >
              <Icon name="cog" size="sm" />
              {{ t('admin.accounts.openAIOAuthImportDefaults') }}
            </button>
            <button
              type="button"
              class="btn btn-secondary inline-flex items-center gap-1.5"
              @click="openFilePicker"
            >
              <Icon name="upload" size="sm" />
              {{ t('common.chooseFile') }}
            </button>
          </div>
        </div>
        <input
          ref="fileInput"
          type="file"
          class="hidden"
          accept="application/json,.json"
          @change="handleFileChange"
        />
      </div>

      <div
        v-if="result"
        class="space-y-2 rounded-xl border border-gray-200 p-4 dark:border-dark-700"
      >
        <div class="text-sm font-medium text-gray-900 dark:text-white">
          {{ t('admin.accounts.dataImportResult') }}
        </div>
        <div class="text-sm text-gray-700 dark:text-dark-300">
          {{ t('admin.accounts.dataImportResultSummary', result) }}
        </div>

        <div v-if="errorItems.length" class="mt-2">
          <div class="text-sm font-medium text-red-600 dark:text-red-400">
            {{ t('admin.accounts.dataImportErrors') }}
          </div>
          <div
            class="mt-2 max-h-48 overflow-auto rounded-lg bg-gray-50 p-3 font-mono text-xs dark:bg-dark-800"
          >
            <div v-for="(item, idx) in errorItems" :key="idx" class="whitespace-pre-wrap">
              {{ item.kind }} {{ item.name || item.proxy_key || '-' }} - {{ item.message }}
            </div>
          </div>
        </div>
      </div>
    </form>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button class="btn btn-secondary" type="button" :disabled="importing" @click="handleClose">
          {{ t('common.cancel') }}
        </button>
        <button
          class="btn btn-primary"
          type="submit"
          form="import-data-form"
          :disabled="importing"
        >
          {{ importing ? t('admin.accounts.dataImporting') : t('admin.accounts.dataImportButton') }}
        </button>
      </div>
    </template>
  </BaseDialog>

  <BaseDialog
    :show="defaultsDialogOpen"
    :title="t('admin.accounts.openAIOAuthImportDefaultsTitle')"
    width="wide"
    :z-index="60"
    @close="closeDefaultsDialog"
  >
    <form id="openai-oauth-import-defaults-form" class="space-y-5" @submit.prevent="saveDefaults">
      <div v-if="defaultsLoading" class="py-8 text-center text-sm text-gray-500 dark:text-dark-400">
        {{ t('common.loading') }}
      </div>
      <template v-else>
        <section class="space-y-3">
          <div class="text-sm font-medium text-gray-900 dark:text-white">
            {{ t('admin.accounts.openAIOAuthImportDefaultsAccount') }}
          </div>
          <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
            <div class="md:col-span-2">
              <label class="input-label">{{ t('admin.accounts.notes') }}</label>
              <textarea v-model="defaultsForm.notes" rows="2" class="input"></textarea>
            </div>
            <div>
              <label class="input-label">{{ t('admin.accounts.concurrency') }}</label>
              <input v-model="defaultsForm.concurrency" type="number" min="0" step="1" class="input" />
            </div>
            <div>
              <label class="input-label">{{ t('admin.accounts.priority') }}</label>
              <input v-model="defaultsForm.priority" type="number" min="0" step="1" class="input" />
            </div>
            <div>
              <label class="input-label">{{ t('admin.accounts.billingRateMultiplier') }}</label>
              <input v-model="defaultsForm.rateMultiplier" type="number" min="0" step="0.01" class="input" />
            </div>
            <div>
              <label class="input-label">{{ t('admin.accounts.expiresAt') }}</label>
              <input v-model="defaultsForm.expiresAt" type="number" min="0" step="1" class="input" />
            </div>
            <div>
              <label class="input-label">{{ t('admin.accounts.autoPauseOnExpired') }}</label>
              <select v-model="defaultsForm.autoPauseOnExpired" class="input">
                <option value="unset">{{ t('admin.accounts.openAIOAuthImportDefaultsUnset') }}</option>
                <option value="true">{{ t('common.yes') }}</option>
                <option value="false">{{ t('common.no') }}</option>
              </select>
            </div>
          </div>
        </section>

        <section class="space-y-3">
          <div class="text-sm font-medium text-gray-900 dark:text-white">
            {{ t('admin.accounts.modelWhitelist') }}
          </div>
          <ModelWhitelistSelector v-model="defaultAllowedModels" platform="openai" />
        </section>

        <section class="grid grid-cols-1 gap-4 md:grid-cols-2">
          <div>
            <label class="input-label">{{ t('admin.accounts.openAIOAuthImportDefaultsCredentialsJson') }}</label>
            <textarea
              v-model="defaultsCredentialsJson"
              rows="8"
              class="input font-mono text-xs"
              spellcheck="false"
            ></textarea>
          </div>
          <div>
            <label class="input-label">{{ t('admin.accounts.openAIOAuthImportDefaultsExtraJson') }}</label>
            <textarea
              v-model="defaultsExtraJson"
              rows="8"
              class="input font-mono text-xs"
              spellcheck="false"
            ></textarea>
          </div>
        </section>
      </template>
    </form>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button
          class="btn btn-secondary"
          type="button"
          :disabled="defaultsSaving"
          @click="closeDefaultsDialog"
        >
          {{ t('common.cancel') }}
        </button>
        <button
          class="btn btn-primary"
          type="submit"
          form="openai-oauth-import-defaults-form"
          :disabled="defaultsLoading || defaultsSaving"
        >
          {{ defaultsSaving ? t('common.saving') : t('common.save') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import ModelWhitelistSelector from '@/components/account/ModelWhitelistSelector.vue'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import { normalizeModelWhitelist } from '@/composables/useModelWhitelist'
import type { OpenAIOAuthImportDefaults } from '@/api/admin/settings'
import type { AdminDataImportResult } from '@/types'

interface Props {
  show: boolean
}

interface Emits {
  (e: 'close'): void
  (e: 'imported'): void
}

type AutoPauseDefault = 'unset' | 'true' | 'false'

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const { t } = useI18n()
const appStore = useAppStore()

const importing = ref(false)
const file = ref<File | null>(null)
const result = ref<AdminDataImportResult | null>(null)

const fileInput = ref<HTMLInputElement | null>(null)
const fileName = computed(() => file.value?.name || '')

const errorItems = computed(() => result.value?.errors || [])

const defaultsDialogOpen = ref(false)
const defaultsLoading = ref(false)
const defaultsSaving = ref(false)
const defaultAllowedModels = ref<string[]>([])
const defaultsCredentialsJson = ref('{}')
const defaultsExtraJson = ref('{}')
const defaultsForm = reactive({
  notes: '',
  concurrency: '',
  priority: '',
  rateMultiplier: '',
  expiresAt: '',
  autoPauseOnExpired: 'unset' as AutoPauseDefault
})

const forbiddenCredentialDefaultFields = new Set([
  'access_token',
  'refresh_token',
  'id_token',
  'expires_at',
  'email',
  'client_id',
  'chatgpt_account_id',
  'chatgpt_user_id',
  'organization_id',
  'plan_type',
  'subscription_expires_at'
])

const forbiddenExtraDefaultFields = new Set(['email', 'name'])

watch(
  () => props.show,
  (open) => {
    if (open) {
      file.value = null
      result.value = null
      defaultsDialogOpen.value = false
      if (fileInput.value) {
        fileInput.value.value = ''
      }
    }
  }
)

const openFilePicker = () => {
  fileInput.value?.click()
}

const handleFileChange = (event: Event) => {
  const target = event.target as HTMLInputElement
  file.value = target.files?.[0] || null
}

const handleClose = () => {
  if (importing.value) return
  emit('close')
}

const closeDefaultsDialog = () => {
  if (defaultsSaving.value) return
  defaultsDialogOpen.value = false
}

const openDefaultsDialog = async () => {
  defaultsDialogOpen.value = true
  await loadDefaults()
}

const loadDefaults = async () => {
  defaultsLoading.value = true
  try {
    const defaults = await adminAPI.settings.getOpenAIOAuthImportDefaults()
    hydrateDefaults(defaults)
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.accounts.openAIOAuthImportDefaultsLoadFailed'))
  } finally {
    defaultsLoading.value = false
  }
}

const hydrateDefaults = (defaults: OpenAIOAuthImportDefaults) => {
  const account = defaults.account || {}
  defaultsForm.notes = typeof account.notes === 'string' ? account.notes : ''
  defaultsForm.concurrency = numberToInput(account.concurrency)
  defaultsForm.priority = numberToInput(account.priority)
  defaultsForm.rateMultiplier = numberToInput(account.rate_multiplier)
  defaultsForm.expiresAt = numberToInput(account.expires_at)
  defaultsForm.autoPauseOnExpired =
    typeof account.auto_pause_on_expired === 'boolean'
      ? (account.auto_pause_on_expired ? 'true' : 'false')
      : 'unset'

  const credentials = { ...(defaults.credentials || {}) }
  defaultAllowedModels.value = normalizeModelWhitelist(credentials.model_whitelist)
  delete credentials.model_whitelist
  defaultsCredentialsJson.value = stringifyJsonObject(credentials)
  defaultsExtraJson.value = stringifyJsonObject(defaults.extra || {})
}

const numberToInput = (value: unknown): string => {
  return typeof value === 'number' && Number.isFinite(value) ? String(value) : ''
}

const stringifyJsonObject = (value: Record<string, unknown>): string => {
  return JSON.stringify(value, null, 2)
}

const readFileAsText = async (sourceFile: File): Promise<string> => {
  if (typeof sourceFile.text === 'function') {
    return sourceFile.text()
  }

  if (typeof sourceFile.arrayBuffer === 'function') {
    const buffer = await sourceFile.arrayBuffer()
    return new TextDecoder().decode(buffer)
  }

  return await new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result ?? ''))
    reader.onerror = () => reject(reader.error || new Error('Failed to read file'))
    reader.readAsText(sourceFile)
  })
}

const parseJsonObject = (text: string, label: string): Record<string, unknown> => {
  const trimmed = text.trim()
  if (!trimmed) {
    return {}
  }

  const parsed = JSON.parse(trimmed)
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new Error(t('admin.accounts.openAIOAuthImportDefaultsJsonObjectRequired', { label }))
  }
  return parsed as Record<string, unknown>
}

const parseOptionalNumber = (value: string, label: string, integer: boolean): number | undefined => {
  const trimmed = value.trim()
  if (!trimmed) {
    return undefined
  }

  const parsed = Number(trimmed)
  if (!Number.isFinite(parsed) || parsed < 0 || (integer && !Number.isInteger(parsed))) {
    throw new Error(t('admin.accounts.openAIOAuthImportDefaultsInvalidNumber', { label }))
  }
  return parsed
}

const rejectForbiddenFields = (
  fields: Record<string, unknown>,
  section: string,
  forbidden: Set<string>
): boolean => {
  for (const key of Object.keys(fields)) {
    if (forbidden.has(key.trim().toLowerCase())) {
      appStore.showError(t('admin.accounts.openAIOAuthImportDefaultsForbiddenField', { section, field: key }))
      return false
    }
  }
  return true
}

const buildAccountDefaults = (): OpenAIOAuthImportDefaults['account'] => {
  const account: NonNullable<OpenAIOAuthImportDefaults['account']> = {}
  if (defaultsForm.notes.trim() !== '') {
    account.notes = defaultsForm.notes
  }

  const concurrency = parseOptionalNumber(defaultsForm.concurrency, t('admin.accounts.concurrency'), true)
  if (concurrency !== undefined) account.concurrency = concurrency

  const priority = parseOptionalNumber(defaultsForm.priority, t('admin.accounts.priority'), true)
  if (priority !== undefined) account.priority = priority

  const rateMultiplier = parseOptionalNumber(defaultsForm.rateMultiplier, t('admin.accounts.billingRateMultiplier'), false)
  if (rateMultiplier !== undefined) account.rate_multiplier = rateMultiplier

  const expiresAt = parseOptionalNumber(defaultsForm.expiresAt, t('admin.accounts.expiresAt'), true)
  if (expiresAt !== undefined) account.expires_at = expiresAt

  if (defaultsForm.autoPauseOnExpired !== 'unset') {
    account.auto_pause_on_expired = defaultsForm.autoPauseOnExpired === 'true'
  }

  return Object.keys(account).length > 0 ? account : undefined
}

const saveDefaults = async () => {
  defaultsSaving.value = true
  try {
    const credentials = parseJsonObject(
      defaultsCredentialsJson.value,
      t('admin.accounts.openAIOAuthImportDefaultsCredentialsJson')
    )
    const extra = parseJsonObject(
      defaultsExtraJson.value,
      t('admin.accounts.openAIOAuthImportDefaultsExtraJson')
    )

    delete credentials.model_whitelist
    if (!rejectForbiddenFields(credentials, 'credentials', forbiddenCredentialDefaultFields)) return
    if (!rejectForbiddenFields(extra, 'extra', forbiddenExtraDefaultFields)) return

    const payload: OpenAIOAuthImportDefaults = {
      account: buildAccountDefaults(),
      credentials: {
        ...credentials,
        model_whitelist: [...defaultAllowedModels.value]
      }
    }
    if (Object.keys(extra).length > 0) {
      payload.extra = extra
    }

    const updated = await adminAPI.settings.updateOpenAIOAuthImportDefaults(payload)
    hydrateDefaults(updated)
    appStore.showSuccess(t('admin.accounts.openAIOAuthImportDefaultsSaved'))
    defaultsDialogOpen.value = false
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.accounts.openAIOAuthImportDefaultsSaveFailed'))
  } finally {
    defaultsSaving.value = false
  }
}

const handleImport = async () => {
  if (!file.value) {
    appStore.showError(t('admin.accounts.dataImportSelectFile'))
    return
  }

  importing.value = true
  try {
    const text = await readFileAsText(file.value)
    const dataPayload = JSON.parse(text)

    const res = await adminAPI.accounts.importData({
      data: dataPayload,
      skip_default_group_bind: true
    })

    result.value = res

    const msgParams: Record<string, unknown> = {
      account_created: res.account_created,
      account_failed: res.account_failed,
      proxy_created: res.proxy_created,
      proxy_reused: res.proxy_reused,
      proxy_failed: res.proxy_failed,
    }
    if (res.account_failed > 0 || res.proxy_failed > 0) {
      appStore.showError(t('admin.accounts.dataImportCompletedWithErrors', msgParams))
    } else {
      appStore.showSuccess(t('admin.accounts.dataImportSuccess', msgParams))
      emit('imported')
    }
  } catch (error: any) {
    if (error instanceof SyntaxError) {
      appStore.showError(t('admin.accounts.dataImportParseFailed'))
    } else {
      appStore.showError(error?.message || t('admin.accounts.dataImportFailed'))
    }
  } finally {
    importing.value = false
  }
}
</script>
