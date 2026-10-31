<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { logs } from '@/api/admin/plugins'
import type { PluginInstallation, PluginLogLine } from '@/api/admin/plugins.types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ContentSkeleton from '@/components/common/ContentSkeleton.vue'
import Icon from '@/components/icons/Icon.vue'
import { pluginText } from '@/components/plugin/configurationFields'
import { formatDateTime } from '@/utils/format'
import { pluginErrorMessage } from './pluginErrors'

// PluginLogsDialog.vue reads the bounded native log buffer for the selected plugin.
// The dialog owns one cancellable read and preserves previously displayed lines if
// an explicit refresh fails. Opening another installation clears the old buffer
// before requesting its records, so diagnostic messages cannot appear under the
// identity of a different plugin.
//
// A generation guards request publication and loading cleanup. Closing the dialog
// cancels its read, while a late response from that canceled request cannot replace
// another selection's lines or failure state. The native dialog body supplies the
// sole scroll area; wrapped log text stays within its width without a nested console.

// Props identifies the selected installation while leaving native dialog visibility
// and exit-transition timing under the parent page's ownership.
const props = defineProps<{
  show: boolean // Parent-controlled dialog visibility.
  installation: PluginInstallation | null // Installation whose logs are requested.
}>()
const emit = defineEmits<{ close: [] /* Requests parent-owned dialog dismissal. */ }>()
const { t, locale } = useI18n()
const lines = shallowRef<PluginLogLine[]>([])
const loading = ref(false)
const error = ref('')
let controller: AbortController | null = null
let generation = 0
const name = computed(() =>
  pluginText(props.installation?.manifest.name, locale.value, props.installation?.plugin_id ?? ''),
)

/** refresh retrieves the latest bounded buffer for the current visible installation.
 * The previous buffer remains readable during manual refresh and after its failure.
 * A replacement request cancels earlier work and reserves its own publication generation.
 *
 * Success, failure, and loading cleanup all check that generation before updating
 * the dialog. Closing or changing installations invalidates it before canceling
 * transport, so late completion cannot overwrite a newer selection even when the
 * HTTP client delivers a response concurrently with cancellation.
 */
async function refresh(): Promise<void> {
  const id = props.installation?.plugin_id
  if (!props.show || !id) return
  const current = ++generation
  controller?.abort()
  controller = new AbortController()
  loading.value = true
  error.value = ''
  try {
    const result = await logs(id, controller.signal)
    if (current === generation) lines.value = result
  } catch (failure) {
    if (current === generation)
      error.value = pluginErrorMessage(failure, t('admin.plugins.loadFailed'))
  } finally {
    if (current === generation) {
      loading.value = false
      controller = null
    }
  }
}

/** levelClass maps retained log severity onto the existing native semantic text colors. */
function levelClass(level: string): string {
  if (level === 'error') return 'text-red-600 dark:text-red-400'
  if (level === 'warn' || level === 'warning') return 'text-amber-700 dark:text-amber-400'
  return 'text-gray-500 dark:text-dark-400'
}

watch(
  () => [props.show, props.installation?.plugin_id],
  () => {
    // Seal the old selection before aborting transport; a cancellation callback must
    // not own error publication after another installation has taken this dialog.
    generation++
    controller?.abort()
    controller = null
    loading.value = false
    if (props.show) {
      lines.value = []
      void refresh()
    }
  },
  { immediate: true },
)
onBeforeUnmount(() => {
  generation++
  controller?.abort()
})
</script>

<template>
  <BaseDialog
    :show="show"
    :title="`${t('admin.plugins.logs')} · ${name}`"
    :subtitle="installation?.plugin_id"
    width="wide"
    @close="emit('close')"
  >
    <template #header-actions>
      <button
        type="button"
        class="btn btn-ghost btn-icon"
        :title="t('common.refresh')"
        :aria-label="t('common.refresh')"
        :disabled="loading"
        @click="refresh"
        ><Icon name="refresh" size="sm"
      /></button>
    </template>
    <div class="space-y-4" :aria-busy="loading">
      <p class="input-hint">{{ t('admin.plugins.logsHint') }}</p>
      <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
      <ContentSkeleton v-if="loading && !lines.length" :rows="4" />
      <p v-else-if="!lines.length && !error" class="text-sm text-gray-500 dark:text-dark-400">{{
        t('admin.plugins.logsEmpty')
      }}</p>
      <ol v-else class="space-y-2 font-mono text-xs leading-relaxed">
        <li
          v-for="(line, index) in lines"
          :key="`${line.time}:${index}`"
          class="break-words whitespace-pre-wrap"
        >
          <span class="text-gray-500 dark:text-dark-400">{{ formatDateTime(line.time) }}</span>
          <span :class="levelClass(line.level)" class="mx-2 uppercase">{{ line.level }}</span
          ><span>{{ line.message }}</span>
        </li>
      </ol>
    </div>
  </BaseDialog>
</template>
