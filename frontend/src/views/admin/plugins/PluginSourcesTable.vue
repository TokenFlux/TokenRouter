<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, reactive, ref, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'
import * as pluginsAPI from '@/api/admin/plugins'
import type { PluginIndex, PluginIndexInput } from '@/api/admin/plugins.types'
import type { Column } from '@/components/common/table/types'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/table/DataTable.vue'
import Input from '@/components/common/Input.vue'
import Toggle from '@/components/common/Toggle.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import SettingsNotice from '@/components/common/settings/SettingsNotice.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { useClipboard } from '@/composables/useClipboard'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import { useAppStore } from '@/stores/app'
import { formatDateTime, formatRelativeTime } from '@/utils/format'
import PluginTrustMark from './PluginTrustMark.vue'
import { pluginErrorMessage } from './pluginErrors'

// PluginSourcesTable.vue lists catalog source registrations and owns their add, edit,
// enable, and delete flows. Registration success means the source was saved; its
// separately polled signature and sync columns report whether distribution
// validation has completed, with the latest fetch error attached to the sync time.
// Builtin authority fields remain read-only, while administrators can change its
// enablement.
//
// One step-up controller serializes source mutations and keeps the active form or
// delete confirmation open until a write settles. Failure preserves entered values,
// and every attempt asks the parent to reread current source observations. Deleting
// a source explicitly retains installed code and business data, so this control
// never shares the uninstall dialog's purge decision or its destructive wording.

// Props carries parent-owned observations and admission policy; local drafts remain
// independent of polling so a saved source update cannot erase unfinished form input.
const props = defineProps<{
  indexes: PluginIndex[] // Current registered source observations.
  loading: boolean // Source read is currently pending.
  disabled: boolean // Global plugin execution is disabled.
}>()
const emit = defineEmits<{
  changed: [] // Requests authoritative source readback.
}>()
const { t } = useI18n()
const app = useAppStore()
const { copyToClipboard } = useClipboard()
const stepUp = useStepUp()
const busy = ref(false)
const editing = ref(false)
const deleting = ref(false)
const selected = shallowRef<PluginIndex | null>(null)
const draft = reactive({ name: '', url: '', public_key: '', enabled: true })
const errors = reactive({ name: '', url: '', public_key: '' })
const failure = ref('')
const form = ref<HTMLFormElement | null>(null)
let active = true
const columns = computed<Column[]>(() => [
  { key: 'name', label: t('admin.plugins.indexSettings.name') },
  { key: 'signature', label: t('admin.plugins.indexSettings.signature') },
  { key: 'cached_at', label: t('admin.plugins.indexSettings.lastAccepted') },
  { key: 'enabled', label: t('admin.plugins.enabled') },
  { key: 'actions', label: t('admin.plugins.actions') },
])

/** edit copies one source into a stable form draft or starts a new registration.
 * New sources start enabled with empty authority fields. Locked operations and
 * emergency disablement leave the existing selection and draft untouched.
 */
function edit(item: PluginIndex | null): void {
  if (busy.value || props.disabled) return
  selected.value = item
  Object.assign(draft, {
    name: item?.name ?? '',
    url: item?.url ?? '',
    public_key: item?.public_key ?? '',
    enabled: item?.enabled ?? true,
  })
  Object.assign(errors, { name: '', url: '', public_key: '' })
  failure.value = ''
  editing.value = true
}

/** validate checks source fields before submission and preserves every rejected input.
 * The backend repeats these checks and owns uniqueness and builtin restrictions.
 * Public keys remain public verification material; no private signing key is accepted here.
 */
function validate(): boolean {
  Object.assign(errors, { name: '', url: '', public_key: '' })
  if (selected.value?.builtin) return true
  if (
    !draft.name.trim() ||
    Array.from(draft.name.trim()).length > 100 ||
    /\p{Cc}/u.test(draft.name)
  )
    errors.name = t('admin.plugins.indexSettings.invalidName')
  try {
    const parsed = new URL(draft.url.trim())
    if (
      parsed.protocol !== 'https:' ||
      !parsed.hostname ||
      parsed.username ||
      parsed.password ||
      parsed.hash
    )
      errors.url = t('admin.plugins.indexSettings.invalidURL')
  } catch {
    errors.url = t('admin.plugins.indexSettings.invalidURL')
  }
  const key = draft.public_key.trim()
  if (key) {
    try {
      const decoded = atob(key)
      if (decoded.length !== 32 || btoa(decoded) !== key)
        errors.public_key = t('admin.plugins.indexSettings.invalidKey')
    } catch {
      errors.public_key = t('admin.plugins.indexSettings.invalidKey')
    }
  }
  return !Object.values(errors).some(Boolean)
}

/** mutate serializes a reviewed write and requests readback after success or failure.
 * The supplied callback owns the endpoint-specific input. Cancellation of step-up
 * retains the current form without an error; other failures remain available for retry.
 *
 * The local busy state owns both verification and the dispatched write. The
 * operation never edits parent rows optimistically, because a transport failure
 * cannot establish whether persistence happened. Disposal suppresses later feedback,
 * while a live owner requests independent source readback before another deliberate
 * attempt and keeps the user's entered values available when the write rejects them.
 */
async function mutate(action: () => Promise<unknown>): Promise<boolean> {
  if (busy.value || props.disabled || !active) return false
  busy.value = true
  failure.value = ''
  try {
    await stepUp.run(action)
    return active
  } catch (error) {
    if (active && !isStepUpCancelled(error))
      failure.value = pluginErrorMessage(error, t('admin.plugins.saveFailed'))
    return false
  } finally {
    // A lost response cannot prove that the registration write failed. Always
    // reread parent observations before the administrator decides whether to retry it.
    if (active) {
      emit('changed')
      busy.value = false
    }
  }
}

/** save submits a validated draft and closes only after registration persistence succeeds.
 * Builtin forms send only enabled state. Validation or persistence failures retain
 * the selected source and editable draft for correction without replacing parent observations.
 */
async function save(): Promise<void> {
  if (!validate()) {
    await nextTick()
    form.value?.querySelector<HTMLInputElement>('.input-error')?.focus()
    return
  }
  const item = selected.value
  const input: PluginIndexInput = item?.builtin
    ? { enabled: draft.enabled }
    : {
        name: draft.name.trim(),
        url: draft.url.trim(),
        public_key: draft.public_key.trim(),
        enabled: draft.enabled,
      }
  if (
    await mutate(() =>
      item ? pluginsAPI.updateIndex(item.id, input) : pluginsAPI.createIndex(input),
    )
  ) {
    editing.value = false
    app.showSuccess(t('admin.plugins.saved'))
  }
}

/** toggle persists source availability without optimistically changing the parent-owned row. */
async function toggle(item: PluginIndex, enabled: boolean): Promise<void> {
  await mutate(() => pluginsAPI.updateIndex(item.id, { enabled }))
}

/** askDelete selects a custom source before explaining retained installations in confirmation. */
function askDelete(item: PluginIndex): void {
  if (item.builtin || busy.value || props.disabled) return
  selected.value = item
  failure.value = ''
  deleting.value = true
}

/** remove deletes the confirmed registration and closes after its saved outcome is known.
 * A builtin source cannot enter deletion. Failure leaves confirmation open with
 * its selected source while the shared mutation owner requests authoritative readback.
 */
async function remove(): Promise<void> {
  const item = selected.value
  if (!item || item.builtin) return
  if (await mutate(() => pluginsAPI.deleteIndex(item.id))) deleting.value = false
}

/** closeEditor keeps source edits visible while their verification or write is pending. */
function closeEditor(): void {
  if (!busy.value) editing.value = false
}

/** closeDelete dismisses source removal only before its write owns the confirmation. */
function closeDelete(): void {
  if (!busy.value) deleting.value = false
}

onBeforeUnmount(() => {
  active = false
  stepUp.onCancel()
})
defineExpose({ create: () => edit(null) })
</script>

<template>
  <TablePageLayout>
    <template v-if="failure && !editing && !deleting" #actions>
      <SettingsNotice tone="error">{{ failure }}</SettingsNotice>
    </template>
    <template #table
      ><DataTable
        :columns="columns"
        :data="indexes"
        :loading="loading && !indexes.length"
        row-key="id"
      >
        <template #cell-name="{ row }">
          <div class="flex min-w-0 max-w-md flex-col gap-1">
            <div class="flex items-center gap-2">
              <span class="truncate font-medium text-gray-900 dark:text-dark-50">{{
                row.name
              }}</span>
              <span v-if="row.builtin" class="badge badge-gray">{{
                t('admin.plugins.indexSettings.builtin')
              }}</span>
            </div>
            <div class="flex min-w-0 items-center gap-1">
              <span
                class="truncate font-mono text-xs text-gray-500 dark:text-dark-400"
                :title="row.url"
                >{{ row.url }}</span
              >
              <button
                type="button"
                class="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-compact text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-700 dark:text-dark-400 dark:hover:bg-dark-800 dark:hover:text-dark-100"
                :title="t('common.copy')"
                :aria-label="t('common.copy')"
                @click="copyToClipboard(row.url)"
                ><Icon name="copy" size="xs"
              /></button>
            </div>
          </div>
        </template>
        <template #cell-signature="{ row }">
          <span
            v-if="row.refreshing"
            class="inline-flex items-center gap-1.5 text-gray-500 dark:text-dark-400"
            ><Icon name="loader" size="sm" class="animate-spin" :animate-on-hover="false" />{{
              t('admin.plugins.indexSettings.refreshing')
            }}</span
          >
          <PluginTrustMark v-else :trust="row.signature_status" label />
        </template>
        <template #cell-cached_at="{ row }">
          <span
            class="inline-flex items-center gap-1.5 text-sm"
            :class="
              row.last_error ? 'text-red-600 dark:text-red-400' : 'text-gray-600 dark:text-dark-300'
            "
            :title="row.cached_at ? formatDateTime(row.cached_at) : undefined"
          >
            <Icon
              v-if="row.last_error"
              name="exclamationCircle"
              size="sm"
              :animate-on-hover="false"
            />{{
              row.cached_at
                ? formatRelativeTime(row.cached_at)
                : t('admin.plugins.indexSettings.never')
            }}
          </span>
          <p
            v-if="row.last_error"
            class="mt-1 line-clamp-2 max-w-xs whitespace-normal break-words text-xs text-red-600 dark:text-red-400"
            :title="row.last_error"
            >{{ row.last_error }}</p
          >
        </template>
        <template #cell-enabled="{ row }"
          ><Toggle
            :model-value="row.enabled"
            :disabled="disabled || busy"
            :aria-label="`${t('admin.plugins.enabled')} ${row.name}`"
            @update:model-value="toggle(row, $event)"
        /></template>
        <template #cell-actions="{ row }"
          ><div class="flex gap-1"
            ><button
              type="button"
              class="btn btn-ghost btn-icon btn-sm"
              :disabled="disabled || busy"
              :title="t('common.edit')"
              :aria-label="t('common.edit')"
              @click="edit(row)"
              ><Icon name="edit" size="sm" /></button
            ><button
              v-if="!row.builtin"
              type="button"
              class="btn btn-ghost btn-icon btn-sm text-red-600 dark:text-red-400"
              :disabled="disabled || busy"
              :title="t('common.delete')"
              :aria-label="t('common.delete')"
              @click="askDelete(row)"
              ><Icon name="trash" size="sm" /></button></div
        ></template>
        <template #empty>
          <EmptyState
            :title="t('admin.plugins.sourcesPage.empty')"
            :description="t('admin.plugins.sourcesPage.emptyHint')"
          >
            <template #icon><Icon name="server" size="xl" class="empty-state-icon" /></template>
          </EmptyState>
        </template> </DataTable
    ></template>
  </TablePageLayout>
  <BaseDialog
    :show="editing"
    :title="t(selected ? 'admin.plugins.indexSettings.edit' : 'admin.plugins.indexSettings.add')"
    @close="closeEditor"
  >
    <form ref="form" class="space-y-4" @submit.prevent="save">
      <Input
        id="plugin-index-name"
        v-model="draft.name"
        :label="t('admin.plugins.indexSettings.name')"
        :disabled="busy || selected?.builtin"
        :error="errors.name"
        required
      />
      <Input
        id="plugin-index-url"
        v-model="draft.url"
        :label="t('admin.plugins.indexSettings.url')"
        :disabled="busy || selected?.builtin"
        :error="errors.url"
        placeholder="https://example.com/v1/index.json"
        required
      />
      <Input
        id="plugin-index-key"
        v-model="draft.public_key"
        :label="t('admin.plugins.indexSettings.publicKey')"
        :disabled="busy || selected?.builtin"
        :error="errors.public_key"
        :hint="t('admin.plugins.indexSettings.keyHint')"
      />
      <label class="flex items-center justify-between gap-4 text-sm"
        >{{ t('admin.plugins.enabled') }}<Toggle v-model="draft.enabled" :disabled="busy"
      /></label>
      <p v-if="selected?.builtin" class="input-hint">{{
        t('admin.plugins.indexSettings.builtinHint')
      }}</p>
      <SettingsNotice v-else-if="!draft.public_key.trim()" tone="warning">{{
        t('admin.plugins.indexSettings.unsignedHint')
      }}</SettingsNotice>
      <SettingsNotice v-if="failure" tone="error">{{ failure }}</SettingsNotice>
    </form>
    <template #footer
      ><button type="button" class="btn btn-secondary" :disabled="busy" @click="closeEditor">{{
        t('common.cancel')
      }}</button
      ><button type="button" class="btn btn-primary" :disabled="busy || disabled" @click="save">{{
        busy ? t('common.saving') : t('common.save')
      }}</button></template
    >
  </BaseDialog>
  <ConfirmDialog
    :show="deleting"
    :title="t('admin.plugins.indexSettings.deleteTitle', { name: selected?.name })"
    :message="t('admin.plugins.indexSettings.deleteHint')"
    :confirm-text="t('common.delete')"
    :loading="busy"
    danger
    @cancel="closeDelete"
    @confirm="remove"
    ><SettingsNotice v-if="failure" tone="error">{{ failure }}</SettingsNotice></ConfirmDialog
  >
  <TotpStepUpDialog :controller="stepUp" />
</template>
