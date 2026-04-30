<template>
  <div id="openai-oauth-import-defaults" class="card">
    <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 class="text-lg font-semibold text-gray-900 dark:text-white">
        {{ t('admin.accounts.openAIOAuthImportDefaultsTitle') }}
      </h2>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
        {{ t('admin.accounts.openAIOAuthImportDefaultsDescription') }}
      </p>
    </div>

    <div class="space-y-5 p-6">
      <div v-if="loading" class="flex items-center gap-2 text-sm text-gray-500 dark:text-gray-400">
        <div class="h-4 w-4 animate-spin rounded-full border-b-2 border-primary-600"></div>
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
              <textarea v-model="form.notes" rows="2" class="input"></textarea>
            </div>
            <div>
              <label class="input-label">{{ t('admin.accounts.concurrency') }}</label>
              <input v-model="form.concurrency" type="number" min="0" step="1" class="input" />
            </div>
            <div>
              <label class="input-label">{{ t('admin.accounts.priority') }}</label>
              <input v-model="form.priority" type="number" min="0" step="1" class="input" />
            </div>
            <div>
              <label class="input-label">{{ t('admin.accounts.billingRateMultiplier') }}</label>
              <input v-model="form.rateMultiplier" type="number" min="0" step="0.01" class="input" />
            </div>
            <div>
              <label class="input-label">{{ t('admin.accounts.expiresAt') }}</label>
              <input v-model="form.expiresAt" type="number" min="0" step="1" class="input" />
            </div>
            <div>
              <label class="input-label">{{ t('admin.accounts.autoPauseOnExpired') }}</label>
              <select v-model="form.autoPauseOnExpired" class="input">
                <option value="unset">{{ t('admin.accounts.openAIOAuthImportDefaultsUnset') }}</option>
                <option value="true">{{ t('common.yes') }}</option>
                <option value="false">{{ t('common.no') }}</option>
              </select>
            </div>
          </div>
        </section>

        <section class="space-y-3 border-t border-gray-100 pt-5 dark:border-dark-700">
          <div class="text-sm font-medium text-gray-900 dark:text-white">
            {{ t('admin.accounts.openAIOAuthImportDefaultsOpenAIOptions') }}
          </div>
          <div class="space-y-4">
            <div class="flex items-center justify-between gap-4">
              <div>
                <label class="input-label mb-0">{{ t('admin.accounts.openai.oauthPassthrough') }}</label>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                  {{ t('admin.accounts.openai.oauthPassthroughDesc') }}
                </p>
              </div>
              <Toggle v-model="openaiPassthrough" />
            </div>
            <div class="flex items-center justify-between gap-4">
              <div>
                <label class="input-label mb-0">{{ t('admin.accounts.openai.wsMode') }}</label>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                  {{ t('admin.accounts.openai.wsModeDesc') }}
                </p>
              </div>
              <select v-model="wsMode" class="input w-52">
                <option value="off">{{ t('admin.accounts.openai.wsModeOff') }}</option>
                <option value="ctx_pool">{{ t('admin.accounts.openai.wsModeCtxPool') }}</option>
                <option value="passthrough">{{ t('admin.accounts.openai.wsModePassthrough') }}</option>
              </select>
            </div>
            <div class="flex items-center justify-between gap-4">
              <div>
                <label class="input-label mb-0">{{ t('admin.accounts.openai.codexCLIOnly') }}</label>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                  {{ t('admin.accounts.openai.codexCLIOnlyDesc') }}
                </p>
              </div>
              <Toggle v-model="codexCLIOnly" />
            </div>
            <div class="flex items-center justify-between gap-4">
              <div>
                <label class="input-label mb-0">{{ t('admin.accounts.openai.compactMode') }}</label>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                  {{ t('admin.accounts.openai.compactModeDesc') }}
                </p>
              </div>
              <select v-model="compactMode" class="input w-44">
                <option value="auto">{{ t('admin.accounts.openai.compactModeAuto') }}</option>
                <option value="force_on">{{ t('admin.accounts.openai.compactModeForceOn') }}</option>
                <option value="force_off">{{ t('admin.accounts.openai.compactModeForceOff') }}</option>
              </select>
            </div>
          </div>
        </section>

        <section class="space-y-3 border-t border-gray-100 pt-5 dark:border-dark-700">
          <div class="text-sm font-medium text-gray-900 dark:text-white">
            {{ t('admin.accounts.modelWhitelist') }}
          </div>
          <ModelWhitelistSelector v-model="defaultAllowedModels" platform="openai" />
        </section>

        <section class="space-y-3 border-t border-gray-100 pt-5 dark:border-dark-700">
          <div class="text-sm font-medium text-gray-900 dark:text-white">
            {{ t('admin.accounts.modelMapping') }}
          </div>
          <p class="text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.mapRequestModels') }}
          </p>
          <div v-if="defaultModelMappings.length > 0" class="space-y-2">
            <div
              v-for="(mapping, index) in defaultModelMappings"
              :key="getDefaultModelMappingKey(mapping)"
              class="flex items-center gap-2"
            >
              <input
                v-model="mapping.from"
                type="text"
                class="input flex-1"
                :placeholder="t('admin.accounts.requestModel')"
              />
              <svg
                class="h-4 w-4 flex-shrink-0 text-gray-400"
                fill="none"
                viewBox="0 0 24 24"
                stroke="currentColor"
              >
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  stroke-width="2"
                  d="M14 5l7 7m0 0l-7 7m7-7H3"
                />
              </svg>
              <input
                v-model="mapping.to"
                type="text"
                class="input flex-1"
                :placeholder="t('admin.accounts.actualModel')"
              />
              <button
                type="button"
                class="rounded-lg p-2 text-red-500 transition-colors hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20"
                @click="removeDefaultModelMapping(index)"
              >
                <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    stroke-width="2"
                    d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16"
                  />
                </svg>
              </button>
            </div>
          </div>
          <button
            type="button"
            class="w-full rounded-lg border-2 border-dashed border-gray-300 px-4 py-2 text-gray-600 transition-colors hover:border-gray-400 hover:text-gray-700 dark:border-dark-500 dark:text-gray-400 dark:hover:border-dark-400 dark:hover:text-gray-300"
            @click="addDefaultModelMapping"
          >
            <svg
              class="mr-1 inline h-4 w-4"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
            >
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4" />
            </svg>
            {{ t('admin.accounts.addMapping') }}
          </button>
          <div class="flex flex-wrap gap-2">
            <button
              v-for="preset in presetMappings"
              :key="preset.label"
              type="button"
              :class="['rounded-lg px-3 py-1 text-xs transition-colors', preset.color]"
              @click="addDefaultPresetMapping(preset.from, preset.to)"
            >
              + {{ preset.label }}
            </button>
          </div>
        </section>

        <section class="grid grid-cols-1 gap-4 border-t border-gray-100 pt-5 dark:border-dark-700 md:grid-cols-2">
          <div>
            <label class="input-label">{{ t('admin.accounts.openAIOAuthImportDefaultsCredentialsJson') }}</label>
            <textarea
              v-model="credentialsJson"
              rows="8"
              class="input font-mono text-xs"
              spellcheck="false"
            ></textarea>
          </div>
          <div>
            <label class="input-label">{{ t('admin.accounts.openAIOAuthImportDefaultsExtraJson') }}</label>
            <textarea
              v-model="extraJson"
              rows="8"
              class="input font-mono text-xs"
              spellcheck="false"
            ></textarea>
          </div>
        </section>

        <div class="flex justify-end">
          <button type="button" class="btn btn-primary" :disabled="saving" @click="save">
            {{ saving ? t('common.saving') : t('common.save') }}
          </button>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api'
import type { OpenAIOAuthImportDefaults } from '@/api/admin/settings'
import {
  buildModelMappingObject,
  getPresetMappingsByPlatform,
  splitPersistedModelRestriction
} from '@/composables/useModelWhitelist'
import ModelWhitelistSelector from '@/components/account/ModelWhitelistSelector.vue'
import Toggle from '@/components/common/Toggle.vue'
import { useAppStore } from '@/stores'
import { createStableObjectKeyResolver } from '@/utils/stableObjectKey'
import {
  OPENAI_WS_MODE_OFF,
  isOpenAIWSModeEnabled,
  resolveOpenAIWSModeFromExtra,
  type OpenAIWSMode
} from '@/utils/openaiWsMode'
import type { OpenAICompactMode } from '@/types'

type AutoPauseDefault = 'unset' | 'true' | 'false'
type NumberInputValue = string | number
interface ModelMapping {
  from: string
  to: string
}

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(true)
const saving = ref(false)
const defaultAllowedModels = ref<string[]>([])
const defaultModelMappings = ref<ModelMapping[]>([])
const credentialsJson = ref('{}')
const extraJson = ref('{}')
const openaiPassthrough = ref(false)
const codexCLIOnly = ref(false)
const wsMode = ref<OpenAIWSMode>(OPENAI_WS_MODE_OFF)
const compactMode = ref<OpenAICompactMode>('auto')
const form = reactive({
  notes: '',
  concurrency: '' as NumberInputValue,
  priority: '' as NumberInputValue,
  rateMultiplier: '' as NumberInputValue,
  expiresAt: '' as NumberInputValue,
  autoPauseOnExpired: 'unset' as AutoPauseDefault
})

const forbiddenCredentialFields = new Set([
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

const forbiddenExtraFields = new Set(['email', 'name'])
const presetMappings = computed(() => getPresetMappingsByPlatform('openai'))
const getDefaultModelMappingKey = createStableObjectKeyResolver<ModelMapping>('openai-oauth-default-mapping')
const structuredExtraKeys = [
  'openai_passthrough',
  'openai_oauth_passthrough',
  'openai_oauth_responses_websockets_v2_mode',
  'openai_oauth_responses_websockets_v2_enabled',
  'responses_websockets_v2_enabled',
  'openai_ws_enabled',
  'codex_cli_only',
  'openai_compact_mode'
]

const numberToInput = (value: unknown): string => {
  return typeof value === 'number' && Number.isFinite(value) ? String(value) : ''
}

const stringifyJsonObject = (value: Record<string, unknown>): string => {
  return JSON.stringify(value, null, 2)
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

const parseOptionalNumber = (value: NumberInputValue, label: string, integer: boolean): number | undefined => {
  // number 输入框在运行时可能回传 number，这里统一转成文本后复用原有校验。
  const trimmed = String(value).trim()
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

const normalizeModelMappingObject = (value: unknown): Record<string, string> | undefined => {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, string>
    : undefined
}

const addDefaultModelMapping = () => {
  defaultModelMappings.value.push({ from: '', to: '' })
}

const removeDefaultModelMapping = (index: number) => {
  defaultModelMappings.value.splice(index, 1)
}

const addDefaultPresetMapping = (from: string, to: string) => {
  if (defaultModelMappings.value.some((mapping) => mapping.from === from)) {
    appStore.showInfo(t('admin.accounts.mappingExists', { model: from }))
    return
  }
  defaultModelMappings.value.push({ from, to })
}

const isCompactMode = (value: unknown): value is OpenAICompactMode => {
  return value === 'auto' || value === 'force_on' || value === 'force_off'
}

const hydrate = (defaults: OpenAIOAuthImportDefaults) => {
  const account = defaults.account || {}
  form.notes = typeof account.notes === 'string' ? account.notes : ''
  form.concurrency = numberToInput(account.concurrency)
  form.priority = numberToInput(account.priority)
  form.rateMultiplier = numberToInput(account.rate_multiplier)
  form.expiresAt = numberToInput(account.expires_at)
  form.autoPauseOnExpired =
    typeof account.auto_pause_on_expired === 'boolean'
      ? account.auto_pause_on_expired ? 'true' : 'false'
      : 'unset'

  const credentials = { ...(defaults.credentials || {}) }
  const modelRestriction = splitPersistedModelRestriction(
    normalizeModelMappingObject(credentials.model_mapping),
    credentials.model_whitelist
  )
  defaultAllowedModels.value = modelRestriction.allowedModels
  defaultModelMappings.value = modelRestriction.modelMappings
  delete credentials.model_whitelist
  delete credentials.model_mapping
  credentialsJson.value = stringifyJsonObject(credentials)

  const extra = { ...(defaults.extra || {}) }
  openaiPassthrough.value = extra.openai_passthrough === true || extra.openai_oauth_passthrough === true
  codexCLIOnly.value = extra.codex_cli_only === true
  wsMode.value = resolveOpenAIWSModeFromExtra(extra, {
    modeKey: 'openai_oauth_responses_websockets_v2_mode',
    enabledKey: 'openai_oauth_responses_websockets_v2_enabled',
    fallbackEnabledKeys: ['responses_websockets_v2_enabled', 'openai_ws_enabled'],
    defaultMode: OPENAI_WS_MODE_OFF
  })
  compactMode.value = isCompactMode(extra.openai_compact_mode) ? extra.openai_compact_mode : 'auto'
  for (const key of structuredExtraKeys) {
    delete extra[key]
  }
  extraJson.value = stringifyJsonObject(extra)
}

const load = async () => {
  loading.value = true
  try {
    const defaults = await adminAPI.settings.getOpenAIOAuthImportDefaults()
    hydrate(defaults)
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.accounts.openAIOAuthImportDefaultsLoadFailed'))
  } finally {
    loading.value = false
  }
}

const buildAccountDefaults = (): OpenAIOAuthImportDefaults['account'] => {
  const account: NonNullable<OpenAIOAuthImportDefaults['account']> = {}
  if (form.notes.trim() !== '') {
    account.notes = form.notes
  }

  const concurrency = parseOptionalNumber(form.concurrency, t('admin.accounts.concurrency'), true)
  if (concurrency !== undefined) account.concurrency = concurrency

  const priority = parseOptionalNumber(form.priority, t('admin.accounts.priority'), true)
  if (priority !== undefined) account.priority = priority

  const rateMultiplier = parseOptionalNumber(form.rateMultiplier, t('admin.accounts.billingRateMultiplier'), false)
  if (rateMultiplier !== undefined) account.rate_multiplier = rateMultiplier

  const expiresAt = parseOptionalNumber(form.expiresAt, t('admin.accounts.expiresAt'), true)
  if (expiresAt !== undefined) account.expires_at = expiresAt

  if (form.autoPauseOnExpired !== 'unset') {
    account.auto_pause_on_expired = form.autoPauseOnExpired === 'true'
  }

  return Object.keys(account).length > 0 ? account : undefined
}

const save = async () => {
  saving.value = true
  try {
    const credentials = parseJsonObject(
      credentialsJson.value,
      t('admin.accounts.openAIOAuthImportDefaultsCredentialsJson')
    )
    const extra = parseJsonObject(extraJson.value, t('admin.accounts.openAIOAuthImportDefaultsExtraJson'))

    delete credentials.model_whitelist
    delete credentials.model_mapping
    for (const key of structuredExtraKeys) {
      delete extra[key]
    }

    if (!rejectForbiddenFields(credentials, 'credentials', forbiddenCredentialFields)) return
    if (!rejectForbiddenFields(extra, 'extra', forbiddenExtraFields)) return

    if (openaiPassthrough.value) {
      extra.openai_passthrough = true
    }
    if (wsMode.value !== OPENAI_WS_MODE_OFF) {
      extra.openai_oauth_responses_websockets_v2_mode = wsMode.value
      extra.openai_oauth_responses_websockets_v2_enabled = isOpenAIWSModeEnabled(wsMode.value)
    }
    if (codexCLIOnly.value) {
      extra.codex_cli_only = true
    }
    if (compactMode.value !== 'auto') {
      extra.openai_compact_mode = compactMode.value
    }

    const modelMapping = buildModelMappingObject('mapping', [], defaultModelMappings.value)
    const updatedCredentials: Record<string, unknown> = {
      ...credentials,
      model_whitelist: [...defaultAllowedModels.value]
    }
    if (modelMapping) {
      updatedCredentials.model_mapping = modelMapping
    }

    const updated = await adminAPI.settings.updateOpenAIOAuthImportDefaults({
      account: buildAccountDefaults(),
      credentials: updatedCredentials,
      extra: Object.keys(extra).length > 0 ? extra : undefined
    })
    hydrate(updated)
    appStore.showSuccess(t('admin.accounts.openAIOAuthImportDefaultsSaved'))
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.accounts.openAIOAuthImportDefaultsSaveFailed'))
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>
