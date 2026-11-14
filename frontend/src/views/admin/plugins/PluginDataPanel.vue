<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { saveAs } from 'file-saver'
import * as pluginsAPI from '@/api/admin/plugins'
import type { PluginDataUsage } from '@/api/admin/plugins.types'
import DataTable from '@/components/common/table/DataTable.vue'
import type { Column } from '@/components/common/table/types'
import Input from '@/components/common/Input.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import SettingsNotice from '@/components/common/settings/SettingsNotice.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import { formatBytes } from '@/utils/format'
import { pluginErrorMessage } from './pluginErrors'

// PluginDataPanel.vue manages local business files independently of installation.
// Disabled owners can export backups, and uninstalled owners can remove retained
// files and KV. Restore selects a plugin identity and a matching ZIP whose
// destination is absent or empty; the backend validates bytes before publication.
//
// One step-up controller serializes file operations and preserves the selected
// upload during verification. Inventory reads use cancellation and generation
// checks so late responses cannot replace newer results or update an unmounted
// panel. Unmount also cancels pending verification, preventing a deferred write
// from starting after navigation. File transfers already admitted by the backend
// retain their ordinary server completion and failure behavior.

// Inventory and operation state belong to this panel, including the restore draft
// retained across step-up verification. Destructive deletion requires a selected owner.
const { t } = useI18n()
const stepUp = useStepUp()
const items = ref<PluginDataUsage[]>([])
const loading = ref(false)
const busy = ref(false)
const failure = ref('')
const restoreID = ref('')
const restoreFile = ref<File | null>(null)
const deleting = ref<PluginDataUsage | null>(null)
let active = true
let generation = 0
let controller: AbortController | null = null
// Columns follow the active locale and identify each owner, usage observation, and available file operation.
const columns = computed<Column[]>(() => [
  { key: 'plugin_id', label: t('admin.plugins.data.owner') },
  { key: 'status', label: t('admin.plugins.status') },
  { key: 'bytes', label: t('admin.plugins.data.size') },
  { key: 'files', label: t('admin.plugins.data.files') },
  { key: 'actions', label: t('admin.plugins.actions') },
])

/** load refreshes retained directory observations and rejects superseded responses.
 * Replacing a read aborts its network request. Existing rows remain visible on
 * failure, and teardown prevents either data or error publication afterward.
 * Generation checks also guard final loading cleanup, because an aborted read
 * can reject after its replacement has already started. Only that replacement
 * may publish inventory or release its own loading indicator.
 */
async function load(): Promise<void> {
  if (!active) return
  const current = ++generation
  // Cancel the preceding inventory read before publishing ownership of its
  // replacement, so only the newest generation can clear the loading state.
  controller?.abort()
  controller = new AbortController()
  loading.value = true
  try {
    const result = await pluginsAPI.listData(controller.signal)
    if (active && current === generation) items.value = result
  } catch (error) {
    if (active && current === generation)
      failure.value = pluginErrorMessage(error, t('admin.plugins.data.failed'))
  } finally {
    if (active && current === generation) loading.value = false
  }
}

/** run serializes a data action and reconciles inventory after either outcome.
 * The same action can be retried by native administrator verification. Cancellation
 * leaves its draft available, and unmount suppresses later local state updates.
 * The busy flag covers verification and final readback so another operation
 * cannot reuse the pending dialog resolver while approval is outstanding.
 * Teardown settles that approval before any later response can publish local
 * changes or trigger another read from the removed panel.
 */
async function run(action: () => Promise<void>): Promise<void> {
  if (busy.value || !active) return
  busy.value = true
  failure.value = ''
  try {
    await stepUp.run(action)
    if (active) deleting.value = null
  } catch (error) {
    if (active && !isStepUpCancelled(error))
      failure.value = pluginErrorMessage(error, t('admin.plugins.data.failed'))
  } finally {
    // Refresh even after transport failure because server-side publication may
    // already have completed before the browser stopped receiving its response.
    if (active) {
      await load()
      busy.value = false
    }
  }
}

/** refresh clears prior feedback before requesting a new inventory observation. */
function refresh(): void {
  failure.value = ''
  void load()
}

/** backup downloads one stopped owner's snapshot and saves it while mounted. */
function backup(item: PluginDataUsage): void {
  void run(async () => {
    const blob = await pluginsAPI.backupData(item.plugin_id)
    if (active) saveAs(blob, `${item.plugin_id}-data.zip`)
  })
}

/** chooseFile retains the selected archive for upload and possible step-up retry. */
function chooseFile(event: Event): void {
  restoreFile.value = (event.target as HTMLInputElement).files?.[0] ?? null
}

/** restore submits the captured identity and file to an empty stopped destination.
 * Inputs remain disabled during the operation. Capturing both values before
 * verification keeps a retry bound to the same archive and destination owner.
 */
function restore(): void {
  const id = restoreID.value.trim()
  const file = restoreFile.value
  if (!id || !file) return
  void run(() => pluginsAPI.restoreData(id, file))
}

/** purge deletes the confirmed uninstalled owner's retained local files and KV. */
function purge(): void {
  const item = deleting.value
  if (item) void run(() => pluginsAPI.purgeData(item.plugin_id))
}

onMounted(() => {
  void load()
})
onBeforeUnmount(() => {
  // Stop read publication and settle verification before releasing this panel;
  // an approval delivered after navigation must not start another storage operation.
  active = false
  controller?.abort()
  stepUp.onCancel()
})
</script>

<template>
  <div class="space-y-4">
    <SettingsNotice>{{ t('admin.plugins.data.hint') }}</SettingsNotice>
    <SettingsNotice v-if="failure" tone="error">{{ failure }}</SettingsNotice>
    <div class="flex justify-end">
      <button type="button" class="btn btn-secondary" :disabled="busy || loading" @click="refresh">
        {{ t('admin.plugins.refresh') }}
      </button>
    </div>
    <DataTable :columns="columns" :data="items" :loading="loading" row-key="plugin_id">
      <template #empty>{{ t('admin.plugins.data.empty') }}</template>
      <template #cell-status="{ row }">
        {{
          row.enabled
            ? t('admin.plugins.enabled')
            : row.installed
              ? t('admin.plugins.states.stopped')
              : t('admin.plugins.data.retained')
        }}
      </template>
      <template #cell-bytes="{ row }">{{ formatBytes(row.bytes) }}</template>
      <template #cell-actions="{ row }">
        <div class="flex flex-wrap gap-2">
          <button
            type="button"
            class="btn btn-secondary btn-sm"
            :disabled="busy || row.enabled"
            @click="backup(row)"
          >
            {{ t('admin.plugins.data.export') }}
          </button>
          <button
            v-if="!row.installed"
            type="button"
            class="btn btn-danger btn-sm"
            :disabled="busy"
            @click="deleting = row"
          >
            {{ t('admin.plugins.data.purge') }}
          </button>
        </div>
      </template>
    </DataTable>
    <form class="card space-y-4 p-4" @submit.prevent="restore">
      <h2 class="font-medium">{{ t('admin.plugins.data.restore') }}</h2>
      <p class="text-sm text-gray-500 dark:text-gray-400">{{
        t('admin.plugins.data.restoreHint')
      }}</p>
      <Input
        id="plugin-data-owner"
        v-model="restoreID"
        :label="t('admin.plugins.data.owner')"
        :disabled="busy"
        required
      />
      <div>
        <label for="plugin-data-archive" class="input-label mb-1.5 block">{{
          t('admin.plugins.data.archive')
        }}</label>
        <input
          id="plugin-data-archive"
          type="file"
          accept=".zip,application/zip"
          class="input w-full"
          :disabled="busy"
          required
          @change="chooseFile"
        />
      </div>
      <button
        type="submit"
        class="btn btn-primary"
        :disabled="busy || !restoreID.trim() || !restoreFile"
      >
        {{ t('admin.plugins.data.restore') }}
      </button>
    </form>
    <ConfirmDialog
      :show="!!deleting"
      :title="t('admin.plugins.data.purge')"
      :message="t('admin.plugins.data.purgeHint', { id: deleting?.plugin_id ?? '' })"
      :loading="busy"
      danger
      @confirm="purge"
      @cancel="deleting = null"
    />
    <TotpStepUpDialog :controller="stepUp" />
  </div>
</template>
