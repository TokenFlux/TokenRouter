<template>
  <BaseDialog
    :show="show"
    :title="t('admin.tlsFingerprintRouters.title')"
    width="wide"
    @close="$emit('close')"
  >
    <div class="space-y-4">
      <!-- Router list header actions. -->
      <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <p class="text-sm text-gray-500 dark:text-gray-400">
          {{ t('admin.tlsFingerprintRouters.description') }}
        </p>
        <div class="flex items-center gap-2">
          <button
            type="button"
            class="btn btn-secondary btn-sm"
            :disabled="loading"
            @click="loadRouters"
          >
            <Icon name="refresh" size="sm" :class="['mr-1', loading ? 'animate-spin' : '']" />
            {{ t('common.refresh') }}
          </button>
          <button type="button" class="btn btn-primary btn-sm" @click="openCreateModal">
            <Icon name="plus" size="sm" class="mr-1" />
            {{ t('admin.tlsFingerprintRouters.createRouter') }}
          </button>
        </div>
      </div>

      <EmptyState
        v-if="!loading && routers.length === 0"
        :title="t('admin.tlsFingerprintRouters.noRouters')"
        :description="t('admin.tlsFingerprintRouters.createFirstRouter')"
      >
        <template #icon>
          <Icon name="swap" class="empty-state-icon h-10 w-10" />
        </template>
      </EmptyState>

      <div
        v-else
        class="max-h-96 overflow-auto rounded-control border border-gray-200 dark:border-dark-600"
      >
        <table
          :aria-busy="loading"
          class="min-w-full divide-y divide-gray-200 dark:divide-dark-700"
        >
          <thead class="sticky top-0 bg-gray-50 dark:bg-dark-700">
            <tr>
              <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-400">
                {{ t('admin.tlsFingerprintRouters.columns.name') }}
              </th>
              <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-400">
                {{ t('admin.tlsFingerprintRouters.columns.description') }}
              </th>
              <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-400">
                {{ t('admin.tlsFingerprintRouters.columns.rules') }}
              </th>
              <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-400">
                {{ t('admin.tlsFingerprintRouters.columns.status') }}
              </th>
              <th class="px-3 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-400">
                {{ t('admin.tlsFingerprintRouters.columns.actions') }}
              </th>
            </tr>
          </thead>
          <TableSkeletonBody v-if="loading" :columns="5" cell-class="px-3 py-2" />
          <tbody
            v-else
            class="divide-y divide-gray-200 bg-white dark:divide-dark-700 dark:bg-dark-800"
          >
            <tr
              v-for="router in routers"
              :key="router.id"
              class="hover:bg-gray-50 dark:hover:bg-dark-700"
            >
              <td class="px-3 py-2">
                <div class="font-medium text-gray-900 dark:text-white text-sm">
                  {{ router.name }}
                </div>
              </td>
              <td class="px-3 py-2">
                <div
                  v-if="router.description"
                  class="max-w-xs truncate text-sm text-gray-500 dark:text-gray-400"
                >
                  {{ router.description }}
                </div>
                <div v-else class="text-xs text-gray-400 dark:text-gray-600">-</div>
              </td>
              <td class="px-3 py-2 text-sm text-gray-700 dark:text-gray-300">
                {{ router.rules?.length ?? 0 }}
              </td>
              <td class="px-3 py-2">
                <button
                  type="button"
                  :class="[
                    'rounded-full px-2 py-0.5 text-xs font-medium',
                    router.enabled
                      ? 'bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-300'
                      : 'bg-gray-200 text-gray-600 dark:bg-dark-600 dark:text-gray-300',
                  ]"
                  @click="toggleRouter(router)"
                >
                  {{ router.enabled ? t('common.enabled') : t('common.disabled') }}
                </button>
              </td>
              <td class="px-3 py-2">
                <div class="flex items-center gap-1">
                  <button
                    type="button"
                    class="p-1 text-gray-500 hover:text-primary-600 dark:hover:text-primary-400"
                    :title="t('common.edit')"
                    @click="handleEdit(router)"
                  >
                    <Icon name="edit" size="sm" />
                  </button>
                  <button
                    type="button"
                    class="p-1 text-gray-500 hover:text-primary-600 dark:hover:text-primary-400"
                    :title="t('admin.tlsFingerprintRouters.copyYaml')"
                    :aria-label="t('admin.tlsFingerprintRouters.copyYaml')"
                    @click="handleCopyYaml(router)"
                  >
                    <Icon name="copy" size="sm" />
                  </button>
                  <button
                    type="button"
                    class="p-1 text-gray-500 hover:text-red-600 dark:hover:text-red-400"
                    :title="t('common.delete')"
                    @click="handleDelete(router)"
                  >
                    <Icon name="trash" size="sm" />
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <template #footer>
      <div class="flex justify-end">
        <button type="button" class="btn btn-secondary" @click="$emit('close')">
          {{ t('common.close') }}
        </button>
      </div>
    </template>

    <BaseDialog
      :show="showCreateModal || showEditModal"
      :title="
        showEditModal
          ? t('admin.tlsFingerprintRouters.editRouter')
          : t('admin.tlsFingerprintRouters.createRouter')
      "
      width="wide"
      :z-index="60"
      @close="closeFormModal"
    >
      <form class="space-y-4" @submit.prevent="handleSubmit">
        <!-- Pasted router YAML populates the editable configuration after the input receives its text. -->
        <div>
          <label class="input-label">{{ t('admin.tlsFingerprintRouters.form.pasteYaml') }}</label>
          <textarea
            v-model="yamlInput"
            rows="4"
            class="input font-mono text-xs"
            :placeholder="t('admin.tlsFingerprintRouters.form.pasteYamlPlaceholder')"
            @paste="handleYamlPaste"
          />
          <div class="mt-1 flex flex-col gap-2 sm:flex-row sm:items-center">
            <button type="button" class="btn btn-secondary btn-sm" @click="parseYamlInput">
              {{ t('admin.tlsFingerprintRouters.form.parseYaml') }}
            </button>
            <p class="text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.tlsFingerprintRouters.form.pasteYamlHint') }}
            </p>
          </div>
        </div>

        <hr class="border-gray-200 dark:border-dark-600" />

        <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
          <div>
            <label class="input-label">{{ t('admin.tlsFingerprintRouters.form.name') }}</label>
            <input v-model="form.name" type="text" required class="input" />
          </div>
          <div>
            <label class="input-label">{{
              t('admin.tlsFingerprintRouters.form.description')
            }}</label>
            <input v-model="form.description" type="text" class="input" />
          </div>
        </div>

        <div
          class="flex items-center justify-between rounded-control border border-gray-200 p-3 dark:border-dark-600"
        >
          <div>
            <label class="input-label mb-0">{{
              t('admin.tlsFingerprintRouters.form.enabled')
            }}</label>
          </div>
          <Toggle v-model="form.enabled" variant="flush" off-tone="soft" />
        </div>

        <div class="space-y-3 rounded-control border border-gray-200 p-3 dark:border-dark-600">
          <div>
            <label class="input-label mb-0">{{
              t('admin.tlsFingerprintRouters.form.chatgptOAuthTokenSettings')
            }}</label>
            <p class="input-hint">
              {{ t('admin.tlsFingerprintRouters.form.chatgptOAuthTokenSettingsHint') }}
            </p>
          </div>
          <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
            <div>
              <label class="input-label text-xs">{{
                t('admin.tlsFingerprintRouters.form.chatgptOAuthTokenUserAgent')
              }}</label>
              <input
                v-model="form.chatgpt_oauth_token_user_agent"
                type="text"
                class="input font-mono text-sm"
              />
              <p class="input-hint">
                {{ t('admin.tlsFingerprintRouters.form.chatgptOAuthTokenUserAgentHint') }}
              </p>
            </div>
            <div>
              <label class="input-label text-xs">{{
                t('admin.tlsFingerprintRouters.form.chatgptOAuthTokenProfile')
              }}</label>
              <Select
                :model-value="form.chatgpt_oauth_token_tls_fingerprint_profile_id"
                :options="tokenTLSProfileOptions"
                @change="
                  form.chatgpt_oauth_token_tls_fingerprint_profile_id =
                    normalizeTokenTLSProfileId($event)
                "
              />
              <p class="input-hint">
                {{ t('admin.tlsFingerprintRouters.form.chatgptOAuthTokenProfileHint') }}
              </p>
            </div>
          </div>
        </div>

        <div class="space-y-3 rounded-control border border-gray-200 p-3 dark:border-dark-600">
          <div>
            <label class="input-label mb-0">{{
              t('admin.tlsFingerprintRouters.form.codexInviteResetSettings')
            }}</label>
            <p class="input-hint">
              {{ t('admin.tlsFingerprintRouters.form.codexInviteResetSettingsHint') }}
            </p>
          </div>
          <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
            <div>
              <label class="input-label text-xs">{{
                t('admin.tlsFingerprintRouters.form.codexInviteResetUserAgent')
              }}</label>
              <input
                v-model="form.codex_invite_reset_user_agent"
                type="text"
                class="input font-mono text-sm"
              />
              <p class="input-hint">
                {{ t('admin.tlsFingerprintRouters.form.codexInviteResetUserAgentHint') }}
              </p>
            </div>
            <div>
              <label class="input-label text-xs">{{
                t('admin.tlsFingerprintRouters.form.codexInviteResetProfile')
              }}</label>
              <Select
                :model-value="form.codex_invite_reset_tls_fingerprint_profile_id"
                :options="codexInviteResetTLSProfileOptions"
                @change="
                  form.codex_invite_reset_tls_fingerprint_profile_id =
                    normalizeTokenTLSProfileId($event)
                "
              />
              <p class="input-hint">
                {{ t('admin.tlsFingerprintRouters.form.codexInviteResetProfileHint') }}
              </p>
            </div>
          </div>
        </div>

        <RuleListEditor
          :items="form.rules"
          :title="t('admin.tlsFingerprintRouters.form.rules')"
          :add-label="t('admin.tlsFingerprintRouters.form.addRule')"
          :empty-text="t('admin.tlsFingerprintRouters.form.noRules')"
          variant="card"
          :item-label="
            (index) => t('admin.tlsFingerprintRouters.form.ruleTitle', { index: index + 1 })
          "
          reorderable
          @add="addRule"
          @remove="removeRule"
          @move="moveRule"
        >
          <template #row="{ item: rule }">
            <div class="space-y-3">
              <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
                <div>
                  <label class="input-label text-xs">{{
                    t('admin.tlsFingerprintRouters.form.ruleName')
                  }}</label>
                  <input v-model="rule.name" type="text" class="input" />
                </div>
                <div>
                  <label class="input-label text-xs">{{
                    t('admin.tlsFingerprintRouters.form.profile')
                  }}</label>
                  <Select
                    :model-value="rule.tls_fingerprint_profile_id"
                    :options="profileOptions"
                    @change="rule.tls_fingerprint_profile_id = Number($event ?? 0)"
                  />
                </div>
                <div>
                  <label class="input-label text-xs">{{
                    t('admin.tlsFingerprintRouters.form.matchType')
                  }}</label>
                  <Select
                    :model-value="rule.match_type"
                    :options="matchTypeOptions"
                    @change="rule.match_type = normalizeMatchType($event)"
                  />
                </div>
                <div>
                  <label class="input-label text-xs">{{
                    t('admin.tlsFingerprintRouters.form.pattern')
                  }}</label>
                  <input
                    v-model="rule.pattern"
                    type="text"
                    required
                    class="input font-mono text-sm"
                  />
                </div>
                <div>
                  <label class="input-label text-xs">{{
                    t('admin.tlsFingerprintRouters.form.upstreamUserAgent')
                  }}</label>
                  <input
                    v-model="rule.upstream_user_agent"
                    type="text"
                    class="input font-mono text-sm"
                  />
                  <p class="input-hint">
                    {{ t('admin.tlsFingerprintRouters.form.upstreamUserAgentHint') }}
                  </p>
                </div>
                <div>
                  <label class="input-label text-xs">{{
                    t('admin.tlsFingerprintRouters.form.upstreamOriginator')
                  }}</label>
                  <input
                    v-model="rule.upstream_originator"
                    type="text"
                    class="input font-mono text-sm"
                  />
                  <p class="input-hint">
                    {{ t('admin.tlsFingerprintRouters.form.upstreamOriginatorHint') }}
                  </p>
                </div>
              </div>

              <div
                class="flex flex-wrap items-center gap-4 text-sm text-gray-700 dark:text-gray-300"
              >
                <label class="inline-flex items-center gap-2">
                  <input
                    v-model="rule.enabled"
                    type="checkbox"
                    class="rounded-compact border-gray-300 text-primary-600 focus:ring-primary-500"
                  />
                  <span>{{ t('admin.tlsFingerprintRouters.form.ruleEnabled') }}</span>
                </label>
                <label class="inline-flex items-center gap-2">
                  <input
                    v-model="rule.case_sensitive"
                    type="checkbox"
                    class="rounded-compact border-gray-300 text-primary-600 focus:ring-primary-500"
                  />
                  <span>{{ t('admin.tlsFingerprintRouters.form.caseSensitive') }}</span>
                </label>
              </div>
            </div>
          </template>
        </RuleListEditor>
      </form>

      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-secondary" @click="closeFormModal">
            {{ t('common.cancel') }}
          </button>
          <button
            type="button"
            class="btn btn-primary"
            :disabled="submitting"
            @click="handleSubmit"
          >
            <Icon
              v-if="submitting"
              name="refresh"
              size="sm"
              class="mr-1 animate-spin"
              :animate-on-hover="false"
            />
            {{ showEditModal ? t('common.update') : t('common.create') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <ConfirmDialog
      :show="showDeleteDialog"
      :title="t('admin.tlsFingerprintRouters.deleteRouter')"
      :message="
        t('admin.tlsFingerprintRouters.deleteConfirmMessage', { name: deletingRouter?.name })
      "
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="confirmDelete"
      @cancel="showDeleteDialog = false"
    />
  </BaseDialog>
</template>

<script setup lang="ts">
import TableSkeletonBody from '@/components/common/TableSkeletonBody.vue'
import Toggle from '@/components/common/Toggle.vue'
import { computed, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import { useClipboard } from '@/composables/useClipboard'
import { formatYamlString, parseYamlScalar } from './tlsFingerprintYaml'
import type { TLSFingerprintProfile } from '@/api/admin/tlsFingerprintProfile'
import type {
  TLSFingerprintRouter,
  TLSFingerprintRouterMatchType,
  TLSFingerprintRouterRule,
} from '@/api/admin/tlsFingerprintRouter'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import RuleListEditor from '@/components/common/RuleListEditor.vue'

/**
 * TLSFingerprintRoutersModal.vue manages ordered TLS routing rules, request-header overrides,
 * and dedicated token or invitation transport settings. Profile selectors distinguish disabled
 * dedicated overrides, the built-in default, random selection, and saved profile identifiers.
 * Editing copies normalized rules into a local draft before submission.
 *
 * YAML import parses a limited router-and-rule structure into temporary values before
 * publishing them to the form when a nonempty router name was recognized. Rule order remains
 * the request-matching order, and exported scalars use JSON quoting. The parser retains null
 * dedicated-profile values separately from zero because they select different transport
 * behavior. Persistence validates nonempty patterns and browser-compilable regular expressions,
 * then closes the draft only after the server accepts it.
 */

/**
 * props controls the outer management dialog; opening refreshes routers and profile choices
 * while nested editors retain their own local visibility.
 */
const props = defineProps<{
  show: boolean // parent-controlled routers dialog visibility
}>()

/**
 * emit delegates outer-dialog closure to the parent while this component owns router editing,
 * confirmation selection, and request feedback.
 */
const emit = defineEmits<{
  close: [] // request parent dialog closure
}>()

void emit // The template emits close through $emit; retain this script reference for type checking.

/**
 * Translation, application toasts, and clipboard access support router editing, configuration
 * export, and create, update, toggle, or delete feedback.
 */
const { t } = useI18n()
const appStore = useAppStore()
const { copyToClipboard } = useClipboard()

/**
 * Router and profile lists, request flags, selected records, nested-dialog visibility, and
 * pasted YAML belong to this mounted management dialog.
 */
const routers = ref<TLSFingerprintRouter[]>([])
const profiles = ref<TLSFingerprintProfile[]>([])
const loading = ref(false)
const submitting = ref(false)
const showCreateModal = ref(false)
const showEditModal = ref(false)
const showDeleteDialog = ref(false)
const editingRouter = ref<TLSFingerprintRouter | null>(null)
const deletingRouter = ref<TLSFingerprintRouter | null>(null)
const yamlInput = ref('')
let yamlPasteTimer: ReturnType<typeof setTimeout> | null = null

/**
 * clearYamlPasteTimer cancels any deferred router YAML paste timer before closing
 * or unmounting the dialog.
 */
function clearYamlPasteTimer() {
  if (yamlPasteTimer !== null) {
    clearTimeout(yamlPasteTimer)
    yamlPasteTimer = null
  }
}

/**
 * form owns router metadata, optional dedicated user agents and profile identifiers, and an
 * ordered copied rule list that is persisted on submit.
 */
const form = reactive({
  name: '', // editable router display name
  description: '', // editable router explanatory text
  enabled: true, // router matching enabled flag
  chatgpt_oauth_token_user_agent: '', // dedicated token request user agent
  /**
   * Null disables this dedicated override; zero selects the built-in profile and negative one
   * selects random.
   */
  chatgpt_oauth_token_tls_fingerprint_profile_id: null as number | null,
  codex_invite_reset_user_agent: '', // dedicated invitation request user agent
  /**
   * Null disables this dedicated override; zero selects the built-in profile and negative one
   * selects random.
   */
  codex_invite_reset_tls_fingerprint_profile_id: null as number | null,
  /**
   * Local ordered rule copies determine the submitted matching sequence and can be reordered
   * before persistence.
   */
  rules: [] as TLSFingerprintRouterRule[],
})

/**
 * Profile choices use zero for built-in default and negative one for random selection when
 * saved profiles exist; dedicated selectors add disabled null.
 */
const profileOptions = computed<SelectOption[]>(() => [
  { value: 0, label: t('admin.providers.quotaControl.tlsFingerprint.defaultProfile') },
  ...(profiles.value.length > 0
    ? [{ value: -1, label: t('admin.providers.quotaControl.tlsFingerprint.randomProfile') }]
    : []),
  ...profiles.value.map((profile) => ({ value: profile.id, label: profile.name })),
])

const tokenTLSProfileOptions = computed<SelectOption[]>(() => [
  { value: null, label: t('admin.tlsFingerprintRouters.form.chatgptOAuthTokenProfileDisabled') },
  ...profileOptions.value,
])

const codexInviteResetTLSProfileOptions = computed<SelectOption[]>(() => [
  { value: null, label: t('admin.tlsFingerprintRouters.form.codexInviteResetProfileDisabled') },
  ...profileOptions.value,
])

/**
 * matchTypeOptions offers contains, prefix, exact, and regex matching while the form preserves
 * rule order for endpoint-side routing evaluation.
 */
const matchTypeOptions = computed<SelectOption[]>(() => [
  { value: 'contains', label: t('admin.tlsFingerprintRouters.matchTypes.contains') },
  { value: 'prefix', label: t('admin.tlsFingerprintRouters.matchTypes.prefix') },
  { value: 'exact', label: t('admin.tlsFingerprintRouters.matchTypes.exact') },
  { value: 'regex', label: t('admin.tlsFingerprintRouters.matchTypes.regex') },
])

watch(
  () => props.show,
  (visible) => {
    if (visible) {
      void loadRouters()
      void loadProfiles()
    } else {
      clearYamlPasteTimer()
    }
  },
  { immediate: true },
)

onUnmounted(() => {
  clearYamlPasteTimer()
})

/**
 * normalizeRules returns copied rule records with defaults for missing fields and trimmed
 * upstream headers. Rule order is retained, unknown match types become contains, and absent
 * profile identifiers become zero.
 */
function normalizeRules(rules: TLSFingerprintRouterRule[] | undefined): TLSFingerprintRouterRule[] {
  return (rules || []).map((rule) => ({
    name: rule.name || '',
    enabled: rule.enabled !== false,
    match_type: normalizeMatchType(rule.match_type),
    pattern: rule.pattern || '',
    case_sensitive: rule.case_sensitive === true,
    tls_fingerprint_profile_id: Number(rule.tls_fingerprint_profile_id ?? 0),
    upstream_user_agent: rule.upstream_user_agent?.trim() || '',
    upstream_originator: rule.upstream_originator?.trim() || '',
  }))
}

/**
 * normalizeMatchType accepts the prefix, exact, and regex match modes and returns contains
 * for every other input. Draft hydration and YAML import use this default when a mode is
 * absent or unrecognized.
 */
function normalizeMatchType(value: unknown): TLSFingerprintRouterMatchType {
  return value === 'prefix' || value === 'exact' || value === 'regex' ? value : 'contains'
}

/**
 * normalizeTokenTLSProfileId maps empty values to null and accepts finite numeric values at
 * least negative one. It retains zero and negative one sentinels separately from disabled
 * dedicated-profile settings.
 */
function normalizeTokenTLSProfileId(value: unknown): number | null {
  if (value === null || value === undefined || value === '') return null
  const parsed = Number(value)
  return Number.isFinite(parsed) && parsed >= -1 ? parsed : null
}

/**
 * loadRouters refreshes the router list and manages loading feedback. Failure retains the
 * displayed list and reports a toast, allowing the administrator to continue inspecting the
 * last available router records.
 */
async function loadRouters() {
  loading.value = true
  try {
    routers.value = await adminAPI.tlsFingerprintRouters.list()
  } catch (error) {
    appStore.showError(t('admin.tlsFingerprintRouters.loadFailed'))
    console.error('Error loading TLS fingerprint routers:', error)
  } finally {
    loading.value = false
  }
}

/**
 * loadProfiles fetches available fingerprint profile choices for router editing. Failure clears
 * the list, leaving default selectors available and preventing stale saved-profile choices from
 * being offered by this request.
 */
async function loadProfiles() {
  try {
    profiles.value = await adminAPI.tlsFingerprintProfiles.list()
  } catch {
    // A failed profile lookup removes saved choices, while built-in defaults remain available
    // and the editor cannot present stale profile records.
    profiles.value = []
  }
}

/**
 * resetForm clears router metadata, dedicated transport overrides, ordered rules, and pasted
 * YAML.
 */
function resetForm() {
  form.name = ''
  form.description = ''
  form.enabled = true
  form.chatgpt_oauth_token_user_agent = ''
  form.chatgpt_oauth_token_tls_fingerprint_profile_id = null
  form.codex_invite_reset_user_agent = ''
  form.codex_invite_reset_tls_fingerprint_profile_id = null
  form.rules = []
  yamlInput.value = ''
}

/**
 * openCreateModal resets the router draft, clears edit ownership, and opens creation.
 */
function openCreateModal() {
  resetForm()
  editingRouter.value = null
  showCreateModal.value = true
}

/**
 * handleEdit copies router settings and normalized rules into the shared draft. Dedicated
 * profile values retain disabled null versus zero or random sentinels, and prior pasted YAML is
 * cleared before editing.
 */
function handleEdit(router: TLSFingerprintRouter) {
  editingRouter.value = router
  form.name = router.name
  form.description = router.description || ''
  form.enabled = router.enabled
  form.chatgpt_oauth_token_user_agent = router.chatgpt_oauth_token_user_agent?.trim() || ''
  form.chatgpt_oauth_token_tls_fingerprint_profile_id = normalizeTokenTLSProfileId(
    router.chatgpt_oauth_token_tls_fingerprint_profile_id ?? null,
  )
  form.codex_invite_reset_user_agent = router.codex_invite_reset_user_agent?.trim() || ''
  form.codex_invite_reset_tls_fingerprint_profile_id = normalizeTokenTLSProfileId(
    router.codex_invite_reset_tls_fingerprint_profile_id ?? null,
  )
  form.rules = normalizeRules(router.rules)
  yamlInput.value = ''
  showEditModal.value = true
}

/**
 * closeFormModal closes both router editors, clears edit selection, and resets the shared
 * draft.
 */
function closeFormModal() {
  clearYamlPasteTimer()
  showCreateModal.value = false
  showEditModal.value = false
  editingRouter.value = null
  resetForm()
}

/**
 * addRule appends a fresh enabled contains rule to the ordered router draft.
 */
function addRule() {
  form.rules.push(createEmptyRule())
}

/**
 * removeRule removes the rule at the supplied index from the editable ordered list.
 */
function removeRule(index: number) {
  form.rules.splice(index, 1)
}

/**
 * moveRule moves one draft rule to a valid target index. Out-of-range targets are ignored,
 * while callers supply a valid source index so the ordered matching list remains usable.
 */
function moveRule(index: number, target: number) {
  if (target < 0 || target >= form.rules.length) return
  const [rule] = form.rules.splice(index, 1)
  form.rules.splice(target, 0, rule)
}

/**
 * parseYamlBoolean accepts true, yes, or one after scalar normalization and lowercasing.
 */
function parseYamlBoolean(value: string): boolean {
  const normalized = parseYamlScalar(value).toLowerCase()
  return normalized === 'true' || normalized === 'yes' || normalized === '1'
}

/**
 * parseYamlNumber converts a simple scalar to a finite number or zero. Empty scalars also
 * become zero, matching the default-profile sentinel used by ordinary routing rule entries.
 */
function parseYamlNumber(value: string): number {
  const parsed = Number(parseYamlScalar(value))
  return Number.isFinite(parsed) ? parsed : 0
}

/**
 * parseYamlNullableNumber preserves empty or null YAML markers as disabled dedicated-profile
 * values. Other scalars use the profile-id normalizer, retaining zero and random selection
 * separately from disabled overrides.
 */
function parseYamlNullableNumber(value: string): number | null {
  const trimmed = value.trim()
  if (!trimmed || trimmed === 'null' || trimmed === '~') return null
  return normalizeTokenTLSProfileId(parseYamlScalar(value))
}

/**
 * createEmptyRule creates an enabled, case-insensitive contains rule using profile zero, the
 * built-in default profile. The name, pattern, and header overrides start empty, so callers
 * populate the matching pattern before validation permits saving.
 */
function createEmptyRule(): TLSFingerprintRouterRule {
  return {
    name: '',
    enabled: true,
    match_type: 'contains',
    pattern: '',
    case_sensitive: false,
    tls_fingerprint_profile_id: 0,
    upstream_user_agent: '',
    upstream_originator: '',
  }
}

/**
 * validateRules requires a nonblank pattern for every draft rule and compiles regex patterns
 * with the browser RegExp constructor. It reports the first validation error and stops
 * submission by returning false.
 */
function validateRules(): boolean {
  for (const rule of form.rules) {
    if (!rule.pattern.trim()) {
      appStore.showError(t('admin.tlsFingerprintRouters.form.patternRequired'))
      return false
    }
    if (rule.match_type === 'regex') {
      try {
        new RegExp(rule.pattern)
      } catch {
        appStore.showError(t('admin.tlsFingerprintRouters.form.regexInvalid'))
        return false
      }
    }
  }
  return true
}

/**
 * handleSubmit validates the router name and ordered rules before create or update. Success
 * closes and resets the draft before awaiting list refresh; failure retains the draft and
 * reports request feedback.
 */
async function handleSubmit() {
  if (!form.name.trim() || !validateRules()) return
  submitting.value = true
  try {
    const payload = {
      name: form.name.trim(),
      description: form.description.trim() || null,
      enabled: form.enabled,
      chatgpt_oauth_token_user_agent: form.chatgpt_oauth_token_user_agent.trim(),
      chatgpt_oauth_token_tls_fingerprint_profile_id:
        form.chatgpt_oauth_token_tls_fingerprint_profile_id,
      codex_invite_reset_user_agent: form.codex_invite_reset_user_agent.trim(),
      codex_invite_reset_tls_fingerprint_profile_id:
        form.codex_invite_reset_tls_fingerprint_profile_id,
      rules: normalizeRules(form.rules),
    }
    if (editingRouter.value) {
      await adminAPI.tlsFingerprintRouters.update(editingRouter.value.id, payload)
      appStore.showSuccess(t('admin.tlsFingerprintRouters.updateSuccess'))
    } else {
      await adminAPI.tlsFingerprintRouters.create(payload)
      appStore.showSuccess(t('admin.tlsFingerprintRouters.createSuccess'))
    }
    closeFormModal()
    await loadRouters()
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.tlsFingerprintRouters.saveFailed'))
  } finally {
    submitting.value = false
  }
}

/**
 * RouterYamlDraft holds router-level fields until a supported pasted configuration is accepted.
 */
interface RouterYamlDraft {
  name: string // editable router display name
  description: string // editable router explanatory text
  enabled: boolean // router matching enabled flag
  chatgpt_oauth_token_user_agent: string // dedicated token request user agent
  /**
   * Null disables the dedicated token profile; zero selects the built-in profile and negative
   * one selects random.
   */
  chatgpt_oauth_token_tls_fingerprint_profile_id: number | null
  codex_invite_reset_user_agent: string // dedicated invitation request user agent
  /**
   * Null disables the dedicated invitation profile; zero selects the built-in profile and
   * negative one selects random.
   */
  codex_invite_reset_tls_fingerprint_profile_id: number | null
}

/**
 * applyRouterYamlField applies a recognized router-level field to the temporary YAML draft. It
 * returns true only when a nonempty name is parsed; unrelated fields return false without
 * indicating overall parse failure.
 */
function applyRouterYamlField(draft: RouterYamlDraft, key: string, rawValue: string): boolean {
  const value = parseYamlScalar(rawValue)

  switch (key) {
    case 'name':
      draft.name = value
      return Boolean(value)
    case 'description':
      draft.description = value
      return false
    case 'enabled':
      draft.enabled = parseYamlBoolean(rawValue)
      return false
    case 'chatgpt_oauth_token_user_agent':
      draft.chatgpt_oauth_token_user_agent = value
      return false
    case 'chatgpt_oauth_token_tls_fingerprint_profile_id':
      draft.chatgpt_oauth_token_tls_fingerprint_profile_id = parseYamlNullableNumber(rawValue)
      return false
    case 'codex_invite_reset_user_agent':
      draft.codex_invite_reset_user_agent = value
      return false
    case 'codex_invite_reset_tls_fingerprint_profile_id':
      draft.codex_invite_reset_tls_fingerprint_profile_id = parseYamlNullableNumber(rawValue)
      return false
    default:
      return false
  }
}

/**
 * applyRuleYamlField applies one recognized field to the current temporary rule. Scalar
 * conversion follows each field type, and unknown keys are ignored while match types use the
 * shared normalization policy.
 */
function applyRuleYamlField(rule: TLSFingerprintRouterRule, key: string, rawValue: string) {
  const value = parseYamlScalar(rawValue)

  switch (key) {
    case 'name':
      rule.name = value
      break
    case 'enabled':
      rule.enabled = parseYamlBoolean(rawValue)
      break
    case 'match_type':
      rule.match_type = normalizeMatchType(value)
      break
    case 'pattern':
      rule.pattern = value
      break
    case 'case_sensitive':
      rule.case_sensitive = parseYamlBoolean(rawValue)
      break
    case 'tls_fingerprint_profile_id':
      rule.tls_fingerprint_profile_id = parseYamlNumber(rawValue)
      break
    case 'upstream_user_agent':
      rule.upstream_user_agent = value
      break
    case 'upstream_originator':
      rule.upstream_originator = value
      break
  }
}

/**
 * parseYamlInput reads the supported router root and ordered rules into temporary state. Scalar
 * lines use recognized router or rule keys, list markers start fresh rules, and indentation
 * separates subsequent rule properties from router properties. Blank lines, comments, and
 * supported root labels are skipped.
 *
 * A nonempty parsed name permits publishing the temporary metadata and normalized rule list to
 * the form. Without that marker, parsing reports failure and keeps the existing draft
 * unchanged. This is a limited reader for exported configuration, not a general YAML parser,
 * and submission still validates rule patterns after the draft has been populated.
 */
function parseYamlInput() {
  const text = yamlInput.value.trim()
  if (!text) return

  const nextRules: TLSFingerprintRouterRule[] = []
  const draft: RouterYamlDraft = {
    name: form.name,
    description: form.description,
    enabled: form.enabled,
    chatgpt_oauth_token_user_agent: form.chatgpt_oauth_token_user_agent,
    chatgpt_oauth_token_tls_fingerprint_profile_id:
      form.chatgpt_oauth_token_tls_fingerprint_profile_id,
    codex_invite_reset_user_agent: form.codex_invite_reset_user_agent,
    codex_invite_reset_tls_fingerprint_profile_id:
      form.codex_invite_reset_tls_fingerprint_profile_id,
  }
  const lines = text.split('\n')
  let currentRule: TLSFingerprintRouterRule | null = null
  let foundName = false
  let inRules = false
  let rulesIndent = -1

  for (const line of lines) {
    const indent = line.match(/^\s*/)?.[0].length ?? 0
    const trimmed = line.trim()

    // Root wrappers and YAML comments carry no editable field; skipping them keeps router
    // metadata and ordered rule parsing in the recognized structure.
    if (
      !trimmed ||
      trimmed.startsWith('#') ||
      trimmed === 'tls_fingerprint_router:' ||
      trimmed === 'router:'
    ) {
      continue
    }

    const listMatch = trimmed.match(/^-\s*(.*)$/)
    if (listMatch) {
      currentRule = createEmptyRule()
      nextRules.push(currentRule)
      inRules = true
      const inlineField = listMatch[1].trim()
      if (inlineField) {
        const match = inlineField.match(/^(\w+):\s*(.*)$/)
        if (match) applyRuleYamlField(currentRule, match[1], match[2])
      }
      continue
    }

    const match = trimmed.match(/^(\w+):\s*(.*)$/)
    if (!match) continue

    const [, key, rawValue] = match
    if (key === 'rules') {
      inRules = true
      rulesIndent = indent
      currentRule = null
      continue
    }

    if (inRules && currentRule !== null && indent > rulesIndent) {
      applyRuleYamlField(currentRule, key, rawValue)
      continue
    }

    inRules = false
    currentRule = null
    if (applyRouterYamlField(draft, key, rawValue)) {
      foundName = true
    }
  }

  // Publish metadata and rule drafts together only after recognizing a nonempty router name,
  // keeping failed parses from partially changing the editor.
  if (foundName) {
    form.name = draft.name
    form.description = draft.description
    form.enabled = draft.enabled
    form.chatgpt_oauth_token_user_agent = draft.chatgpt_oauth_token_user_agent
    form.chatgpt_oauth_token_tls_fingerprint_profile_id =
      draft.chatgpt_oauth_token_tls_fingerprint_profile_id
    form.codex_invite_reset_user_agent = draft.codex_invite_reset_user_agent
    form.codex_invite_reset_tls_fingerprint_profile_id =
      draft.codex_invite_reset_tls_fingerprint_profile_id
    form.rules = normalizeRules(nextRules)
    appStore.showSuccess(t('admin.tlsFingerprintRouters.form.yamlParsed'))
  } else {
    appStore.showError(t('admin.tlsFingerprintRouters.form.yamlParseFailed'))
  }
}

/**
 * handleYamlPaste delays parsing by fifty milliseconds so v-model can receive the pasted text,
 * replacing any pending paste timer so closing or unmounting can cancel it cleanly.
 */
function handleYamlPaste() {
  clearYamlPasteTimer()
  yamlPasteTimer = setTimeout(() => {
    yamlPasteTimer = null
    parseYamlInput()
  }, 50)
}

/**
 * formatYamlNullableNumber emits null for absent dedicated-profile overrides and numeric text
 * otherwise. It retains zero for the built-in profile, distinguishing that selection from a
 * disabled dedicated transport override.
 */
function formatYamlNullableNumber(value: number | null | undefined): string {
  return value === null || typeof value === 'undefined' ? 'null' : String(value)
}

/**
 * buildRouterYaml serializes router metadata, dedicated overrides, and normalized rules in
 * their existing order. Strings use JSON quoting and nullable profile identifiers retain null
 * versus numeric sentinels for the paste reader.
 */
function buildRouterYaml(router: TLSFingerprintRouter): string {
  const lines = [
    'tls_fingerprint_router:',
    `  name: ${formatYamlString(router.name)}`,
    `  description: ${formatYamlString(router.description || '')}`,
    `  enabled: ${router.enabled ? 'true' : 'false'}`,
    `  chatgpt_oauth_token_user_agent: ${formatYamlString(router.chatgpt_oauth_token_user_agent || '')}`,
    `  chatgpt_oauth_token_tls_fingerprint_profile_id: ${formatYamlNullableNumber(router.chatgpt_oauth_token_tls_fingerprint_profile_id)}`,
    `  codex_invite_reset_user_agent: ${formatYamlString(router.codex_invite_reset_user_agent || '')}`,
    `  codex_invite_reset_tls_fingerprint_profile_id: ${formatYamlNullableNumber(router.codex_invite_reset_tls_fingerprint_profile_id)}`,
    '  rules:',
  ]

  for (const rule of normalizeRules(router.rules)) {
    lines.push(
      `    - name: ${formatYamlString(rule.name)}`,
      `      enabled: ${rule.enabled ? 'true' : 'false'}`,
      `      match_type: ${formatYamlString(rule.match_type)}`,
      `      pattern: ${formatYamlString(rule.pattern)}`,
      `      case_sensitive: ${rule.case_sensitive ? 'true' : 'false'}`,
      `      tls_fingerprint_profile_id: ${rule.tls_fingerprint_profile_id}`,
      `      upstream_user_agent: ${formatYamlString(rule.upstream_user_agent || '')}`,
      `      upstream_originator: ${formatYamlString(rule.upstream_originator || '')}`,
    )
  }

  return lines.join('\n')
}

/**
 * handleCopyYaml copies the selected router export with localized success feedback.
 */
function handleCopyYaml(router: TLSFingerprintRouter) {
  copyToClipboard(buildRouterYaml(router), t('admin.tlsFingerprintRouters.copyYamlSuccess'))
}

/**
 * toggleRouter persists the opposite enabled state and reloads routers after success. Failure
 * leaves the current list available and shows an error toast rather than locally reversing the
 * row value.
 */
async function toggleRouter(router: TLSFingerprintRouter) {
  try {
    await adminAPI.tlsFingerprintRouters.update(router.id, { enabled: !router.enabled })
    await loadRouters()
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.tlsFingerprintRouters.toggleFailed'))
  }
}

/**
 * handleDelete selects a router and opens its deletion confirmation dialog.
 */
function handleDelete(router: TLSFingerprintRouter) {
  deletingRouter.value = router
  showDeleteDialog.value = true
}

/**
 * confirmDelete deletes the selected router before clearing confirmation and refreshing the
 * list. Failed deletion retains the selected target and reports an error, allowing inspection
 * or a later retry.
 */
async function confirmDelete() {
  if (!deletingRouter.value) return
  try {
    await adminAPI.tlsFingerprintRouters.delete(deletingRouter.value.id)
    appStore.showSuccess(t('admin.tlsFingerprintRouters.deleteSuccess'))
    showDeleteDialog.value = false
    deletingRouter.value = null
    await loadRouters()
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.tlsFingerprintRouters.deleteFailed'))
  }
}
</script>
