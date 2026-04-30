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
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api'
import type { OpenAIOAuthImportDefaults } from '@/api/admin/settings'
import { normalizeModelWhitelist } from '@/composables/useModelWhitelist'
import ModelWhitelistSelector from '@/components/account/ModelWhitelistSelector.vue'
import Toggle from '@/components/common/Toggle.vue'
import { useAppStore } from '@/stores'
import {
  OPENAI_WS_MODE_OFF,
  isOpenAIWSModeEnabled,
  resolveOpenAIWSModeFromExtra,
  type OpenAIWSMode
} from '@/utils/openaiWsMode'
import type { OpenAICompactMode } from '@/types'

type AutoPauseDefault = 'unset' | 'true' | 'false'

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(true)
const saving = ref(false)
const defaultAllowedModels = ref<string[]>([])
const credentialsJson = ref('{}')
const extraJson = ref('{}')
const openaiPassthrough = ref(false)
const codexCLIOnly = ref(false)
const wsMode = ref<OpenAIWSMode>(OPENAI_WS_MODE_OFF)
const compactMode = ref<OpenAICompactMode>('auto')
const form = reactive({
  notes: '',
  concurrency: '',
  priority: '',
  rateMultiplier: '',
  expiresAt: '',
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
  defaultAllowedModels.value = normalizeModelWhitelist(credentials.model_whitelist)
  delete credentials.model_whitelist
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

    const updated = await adminAPI.settings.updateOpenAIOAuthImportDefaults({
      account: buildAccountDefaults(),
      credentials: {
        ...credentials,
        model_whitelist: [...defaultAllowedModels.value]
      },
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
