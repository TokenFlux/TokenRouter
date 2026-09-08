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
              <Select v-model="form.autoPauseOnExpired" :options="autoPauseOnExpiredOptions" />
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
            <CodexImageToolModeSelector
              v-model="codexImageToolMode"
              test-id-prefix="openai-oauth-default-codex-image-tool"
            />
            <div class="flex flex-col items-stretch gap-3 sm:flex-row sm:items-center sm:justify-between sm:gap-4">
              <div class="min-w-0">
                <label class="input-label mb-0">{{ t('admin.accounts.openai.wsMode') }}</label>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                  {{ t('admin.accounts.openai.wsModeDesc') }}
                </p>
              </div>
              <Select v-model="wsMode" :options="wsModeOptions" class="w-full sm:w-52" />
            </div>
            <div class="flex flex-col items-stretch gap-3 sm:flex-row sm:items-center sm:justify-between sm:gap-4">
              <div class="min-w-0">
                <label class="input-label mb-0">{{ t('admin.accounts.openai.clientPolicy') }}</label>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                  {{ t('admin.accounts.openai.clientPolicyDesc') }}
                </p>
              </div>
              <Select
                v-model="openAIOAuthClientPolicy"
                :options="openAIOAuthClientPolicyOptions"
                class="w-full sm:w-64"
                data-testid="openai-oauth-default-client-policy"
              />
            </div>
            <div
              v-if="openAIOAuthClientPolicy === 'codex_only'"
              class="flex items-center justify-between gap-4 border-l-2 border-gray-200 pl-4 dark:border-dark-600"
            >
              <div>
                <label class="input-label mb-0">
                  {{ t('admin.accounts.openai.codexCLIOnlyAllowClaudeCode') }}
                </label>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                  {{ t('admin.accounts.openai.codexCLIOnlyAllowClaudeCodeDesc') }}
                </p>
              </div>
              <Toggle
                v-model="codexCLIOnlyAllowClaudeCode"
                data-testid="openai-oauth-default-codex-allow-claude-code-toggle"
              />
            </div>
            <div class="space-y-4 border-t border-gray-100 pt-4 dark:border-dark-700">
              <div class="space-y-2">
                <div class="flex items-center justify-between gap-4">
                  <label class="input-label mb-0">{{ t('admin.accounts.autoPause5hDisabled') }}</label>
                  <Toggle
                    v-model="autoPause5hDisabled"
                    data-testid="openai-oauth-default-auto-pause-5h-disabled"
                  />
                </div>
                <p class="input-hint">{{ t('admin.accounts.autoPauseDisabledHint') }}</p>
              </div>
              <div>
                <label class="input-label">{{ t('admin.accounts.autoPause5hThreshold') }}</label>
                <input
                  v-model="autoPause5hThreshold"
                  type="number"
                  min="0"
                  max="100"
                  step="0.1"
                  class="input"
                  :disabled="autoPause5hDisabled"
                  data-testid="openai-oauth-default-auto-pause-5h-threshold"
                />
                <p class="input-hint">{{ t('admin.accounts.autoPauseThresholdHint') }}</p>
              </div>
              <div class="space-y-2">
                <div class="flex items-center justify-between gap-4">
                  <label class="input-label mb-0">{{ t('admin.accounts.autoPause7dDisabled') }}</label>
                  <Toggle
                    v-model="autoPause7dDisabled"
                    data-testid="openai-oauth-default-auto-pause-7d-disabled"
                  />
                </div>
                <p class="input-hint">{{ t('admin.accounts.autoPauseDisabledHint') }}</p>
              </div>
              <div>
                <label class="input-label">{{ t('admin.accounts.autoPause7dThreshold') }}</label>
                <input
                  v-model="autoPause7dThreshold"
                  type="number"
                  min="0"
                  max="100"
                  step="0.1"
                  class="input"
                  :disabled="autoPause7dDisabled"
                  data-testid="openai-oauth-default-auto-pause-7d-threshold"
                />
                <p class="input-hint">{{ t('admin.accounts.autoPauseThresholdHint') }}</p>
              </div>
            </div>
            <OpenAICompactionCheckbox v-model="nativeCompactV2Mode" test-id="openai-oauth-default-native-compaction-v2-mode"
              :label="t('admin.accounts.openai.nativeCompactV2Mode')" :hint="t('admin.accounts.openai.nativeCompactV2ModeDesc')" />
            <OpenAICompactionCheckbox v-model="compactMode" test-id="openai-oauth-default-compact-mode"
              :label="t('admin.accounts.openai.compactMode')" :hint="t('admin.accounts.openai.compactModeDesc')" />
            <div class="space-y-3 border-t border-gray-100 pt-4 dark:border-dark-700">
              <div class="flex items-center justify-between gap-4">
                <div>
                  <label class="input-label mb-0">
                    {{ t('admin.accounts.quotaControl.tlsFingerprint.label') }}
                  </label>
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    {{ t('admin.accounts.quotaControl.tlsFingerprint.hint') }}
                  </p>
                </div>
                <Toggle v-model="tlsFingerprintEnabled" data-testid="openai-oauth-default-tls-fingerprint-toggle" />
              </div>
              <Select
                v-if="tlsFingerprintEnabled"
                v-model="tlsFingerprintProfileId"
                :options="tlsFingerprintProfileOptions"
                class="w-full md:w-64"
                data-testid="openai-oauth-default-tls-fingerprint-profile"
              />
              <div v-if="tlsFingerprintEnabled" class="space-y-1">
                <Select
                  v-model="tlsFingerprintRouterId"
                  :options="tlsFingerprintRouterOptions"
                  class="w-full md:w-64"
                  data-testid="openai-oauth-default-tls-fingerprint-router"
                />
                <p class="input-hint">{{ t('admin.accounts.quotaControl.tlsFingerprint.routerHint') }}</p>
              </div>
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
import OpenAICompactionCheckbox from '@/components/account/OpenAICompactionCheckbox.vue'
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
import CodexImageToolModeSelector from '@/components/account/CodexImageToolModeSelector.vue'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'
import { useAppStore } from '@/stores'
import { createStableObjectKeyResolver } from '@/utils/stableObjectKey'
import {
  applyCodexImageToolMode,
  CODEX_IMAGE_GENERATION_BRIDGE_KEY,
  CODEX_IMAGE_GENERATION_POLICY_KEY,
  LEGACY_CODEX_IMAGE_GENERATION_BRIDGE_KEY,
  readCodexImageToolMode,
  type CodexImageToolMode
} from '@/utils/codexImageToolMode'
import {
  OPENAI_WS_MODE_OFF,
  isOpenAIWSModeEnabled,
  resolveOpenAIWSModeFromExtra,
  type OpenAIWSMode
} from '@/utils/openaiWsMode'
import type { OpenAICompactMode, OpenAIOAuthClientPolicy } from '@/types'

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
const codexImageToolMode = ref<CodexImageToolMode>('inherit')
const openAIOAuthClientPolicy = ref<OpenAIOAuthClientPolicy>('any')
const codexCLIOnlyAllowClaudeCode = ref(false)
const wsMode = ref<OpenAIWSMode>(OPENAI_WS_MODE_OFF)
const compactMode = ref<OpenAICompactMode>('force_on')
const nativeCompactV2Mode = ref<OpenAICompactMode>('force_on')
const tlsFingerprintEnabled = ref(false)
const tlsFingerprintProfileId = ref<number | null>(null)
const tlsFingerprintProfiles = ref<{ id: number; name: string }[]>([])
const tlsFingerprintRouterId = ref<number | null>(null)
const tlsFingerprintRouters = ref<{ id: number; name: string }[]>([])
const autoPause5hThreshold = ref<NumberInputValue>('')
const autoPause7dThreshold = ref<NumberInputValue>('')
const autoPause5hDisabled = ref(false)
const autoPause7dDisabled = ref(false)
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
  'openai_oauth_client_policy',
  'codex_cli_only',
  'codex_cli_only_allowed_clients',
  CODEX_IMAGE_GENERATION_BRIDGE_KEY,
  LEGACY_CODEX_IMAGE_GENERATION_BRIDGE_KEY,
  CODEX_IMAGE_GENERATION_POLICY_KEY,
  'auto_pause_5h_threshold',
  'auto_pause_7d_threshold',
  'auto_pause_5h_disabled',
  'auto_pause_7d_disabled',
  'openai_native_compaction_v2_mode',
  'openai_compact_mode',
  'enable_tls_fingerprint',
  'tls_fingerprint_profile_id',
  'tls_fingerprint_router_id'
]

const autoPauseOnExpiredOptions = computed<SelectOption[]>(() => [
  { value: 'unset', label: t('admin.accounts.openAIOAuthImportDefaultsUnset') },
  { value: 'true', label: t('common.yes') },
  { value: 'false', label: t('common.no') }
])

const wsModeOptions = computed<SelectOption[]>(() => [
  { value: 'off', label: t('admin.accounts.openai.wsModeOff') },
  { value: 'ctx_pool', label: t('admin.accounts.openai.wsModeCtxPool') },
  { value: 'passthrough', label: t('admin.accounts.openai.wsModePassthrough') }
])

const openAIOAuthClientPolicyOptions = computed<SelectOption[]>(() => [
  { value: 'any', label: t('admin.accounts.openai.clientPolicyAny') },
  { value: 'codex_only', label: t('admin.accounts.openai.clientPolicyCodexOnly') },
  {
    value: 'tls_router_matched_only',
    label: t('admin.accounts.openai.clientPolicyTLSRouterMatchedOnly')
  }
])




const tlsFingerprintProfileOptions = computed<SelectOption[]>(() => [
  { value: null, label: t('admin.accounts.quotaControl.tlsFingerprint.defaultProfile') },
  ...(tlsFingerprintProfiles.value.length > 0
    ? [{ value: -1, label: t('admin.accounts.quotaControl.tlsFingerprint.randomProfile') }]
    : []),
  ...tlsFingerprintProfiles.value.map((profile) => ({ value: profile.id, label: profile.name }))
])

const tlsFingerprintRouterOptions = computed<SelectOption[]>(() => [
  { value: null, label: t('admin.accounts.quotaControl.tlsFingerprint.noRouter') },
  ...tlsFingerprintRouters.value.map((router) => ({ value: router.id, label: router.name }))
])

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

const parseOptionalPercent = (value: NumberInputValue, label: string): number | undefined => {
  // 百分比阈值在界面按 0-100 展示，保存时再换算成 0-1 的比例。
  const parsed = parseOptionalNumber(value, label, false)
  if (parsed !== undefined && parsed > 100) {
    throw new Error(t('admin.accounts.openAIOAuthImportDefaultsInvalidPercent', { label }))
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



const normalizeTLSFingerprintProfileId = (value: unknown): number | null => {
  // 导入模板保存为 JSON，profile_id 可能来自数字或数字字符串，这里统一归一化。
  if (typeof value === 'number' && Number.isInteger(value)) {
    return value === 0 ? null : value
  }
  if (typeof value === 'string' && value.trim() !== '') {
    const parsed = Number(value)
    return Number.isInteger(parsed) && parsed !== 0 ? parsed : null
  }
  return null
}

const normalizeOpenAIOAuthClientPolicy = (
  policy: unknown,
  legacyCodexCLIOnly: unknown
): OpenAIOAuthClientPolicy => {
  // 新字段优先；没有新字段时继续读取旧的 codex_cli_only 开关。
  if (policy === 'codex_only' || policy === 'tls_router_matched_only' || policy === 'any') {
    return policy
  }
  return legacyCodexCLIOnly === true ? 'codex_only' : 'any'
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
  codexImageToolMode.value = readCodexImageToolMode(extra)
  openAIOAuthClientPolicy.value = normalizeOpenAIOAuthClientPolicy(
    extra.openai_oauth_client_policy,
    extra.codex_cli_only
  )
  codexCLIOnlyAllowClaudeCode.value =
    Array.isArray(extra.codex_cli_only_allowed_clients) &&
    extra.codex_cli_only_allowed_clients.includes('claude_code')
  autoPause5hThreshold.value =
    typeof extra.auto_pause_5h_threshold === 'number' && Number.isFinite(extra.auto_pause_5h_threshold)
      ? String(extra.auto_pause_5h_threshold * 100)
      : ''
  autoPause7dThreshold.value =
    typeof extra.auto_pause_7d_threshold === 'number' && Number.isFinite(extra.auto_pause_7d_threshold)
      ? String(extra.auto_pause_7d_threshold * 100)
      : ''
  autoPause5hDisabled.value = extra.auto_pause_5h_disabled === true
  autoPause7dDisabled.value = extra.auto_pause_7d_disabled === true
  wsMode.value = resolveOpenAIWSModeFromExtra(extra, {
    modeKey: 'openai_oauth_responses_websockets_v2_mode',
    enabledKey: 'openai_oauth_responses_websockets_v2_enabled',
    fallbackEnabledKeys: ['responses_websockets_v2_enabled', 'openai_ws_enabled'],
    defaultMode: OPENAI_WS_MODE_OFF
  })
  compactMode.value = extra.openai_compact_mode === 'force_off' ? 'force_off' : 'force_on'
  nativeCompactV2Mode.value = extra.openai_native_compaction_v2_mode === 'force_off' ? 'force_off' : 'force_on'
  tlsFingerprintEnabled.value = extra.enable_tls_fingerprint === true
  tlsFingerprintProfileId.value = tlsFingerprintEnabled.value
    ? normalizeTLSFingerprintProfileId(extra.tls_fingerprint_profile_id)
    : null
  tlsFingerprintRouterId.value = tlsFingerprintEnabled.value
    ? normalizeTLSFingerprintProfileId(extra.tls_fingerprint_router_id)
    : null
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

const loadTLSFingerprintProfiles = async () => {
  try {
    const profiles = await adminAPI.tlsFingerprintProfiles.list()
    tlsFingerprintProfiles.value = profiles.map((profile) => ({ id: profile.id, name: profile.name }))
  } catch {
    // 模板保存不依赖 profile 列表，加载失败时保留内置默认选项。
    tlsFingerprintProfiles.value = []
  }
}

const loadTLSFingerprintRouters = async () => {
  try {
    const routers = await adminAPI.tlsFingerprintRouters.list()
    tlsFingerprintRouters.value = routers.map((router) => ({ id: router.id, name: router.name }))
  } catch {
    // 路由器列表加载失败时仅隐藏可选项，不影响保存其它默认配置。
    tlsFingerprintRouters.value = []
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
    applyCodexImageToolMode(extra, codexImageToolMode.value)

    if (!rejectForbiddenFields(credentials, 'credentials', forbiddenCredentialFields)) return
    if (!rejectForbiddenFields(extra, 'extra', forbiddenExtraFields)) return

    if (openaiPassthrough.value) {
      extra.openai_passthrough = true
    }
    if (wsMode.value !== OPENAI_WS_MODE_OFF) {
      extra.openai_oauth_responses_websockets_v2_mode = wsMode.value
      extra.openai_oauth_responses_websockets_v2_enabled = isOpenAIWSModeEnabled(wsMode.value)
    }
    extra.openai_oauth_client_policy = openAIOAuthClientPolicy.value
    // 继续写旧字段，方便旧版本服务端或旧账号逻辑读取；非 Codex 模式显式清 false。
    extra.codex_cli_only = openAIOAuthClientPolicy.value === 'codex_only'
    if (openAIOAuthClientPolicy.value === 'codex_only' && codexCLIOnlyAllowClaudeCode.value) {
      extra.codex_cli_only_allowed_clients = ['claude_code']
    }
    const autoPause5hPercent = parseOptionalPercent(
      autoPause5hThreshold.value,
      t('admin.accounts.autoPause5hThreshold')
    )
    const autoPause7dPercent = parseOptionalPercent(
      autoPause7dThreshold.value,
      t('admin.accounts.autoPause7dThreshold')
    )
    if (autoPause5hPercent !== undefined && autoPause5hPercent > 0) {
      extra.auto_pause_5h_threshold = autoPause5hPercent / 100
    }
    if (autoPause7dPercent !== undefined && autoPause7dPercent > 0) {
      extra.auto_pause_7d_threshold = autoPause7dPercent / 100
    }
    if (autoPause5hDisabled.value) {
      extra.auto_pause_5h_disabled = true
    }
    if (autoPause7dDisabled.value) {
      extra.auto_pause_7d_disabled = true
    }
    extra.openai_compact_mode = compactMode.value
    extra.openai_native_compaction_v2_mode = nativeCompactV2Mode.value
    if (tlsFingerprintEnabled.value) {
      extra.enable_tls_fingerprint = true
      if (tlsFingerprintProfileId.value !== null) {
        extra.tls_fingerprint_profile_id = tlsFingerprintProfileId.value
      }
      if (tlsFingerprintRouterId.value !== null) {
        extra.tls_fingerprint_router_id = tlsFingerprintRouterId.value
      }
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

onMounted(() => {
  void load()
  void loadTLSFingerprintProfiles()
  void loadTLSFingerprintRouters()
})
</script>
