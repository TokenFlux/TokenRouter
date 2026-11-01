<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { logs } from '@/api/admin/plugins'
import type { PluginInstallation, PluginLogLine } from '@/api/admin/plugins.types'
import SearchInput from '@/components/common/SearchInput.vue'
import Skeleton from '@/components/common/Skeleton.vue'
import Toggle from '@/components/common/Toggle.vue'
import SettingsNotice from '@/components/common/settings/SettingsNotice.vue'
import Icon from '@/components/icons/Icon.vue'
import { useClipboard } from '@/composables/useClipboard'
import { vSegmented } from '@/directives/segmented'
import { formatDateTime } from '@/utils/format'
import { pluginErrorMessage } from '../pluginErrors'

// PluginLogsPanel.vue shows the bounded log buffer the host keeps for one plugin
// process. Lines are filtered locally by level and text, newest at the bottom, and
// the view follows new lines only while the reader is already at the bottom so
// scrolling back through history is not interrupted. Automatic refresh polls every
// five seconds while the panel is mounted and the browser tab is visible.
//
// One cancellable read owns publication at a time. A failed refresh keeps the lines
// already shown, and a generation prevents an aborted or superseded request from
// replacing newer lines after the reader switches tabs or plugins.

/** LevelFilter names the severity filters offered above the log list. */
type LevelFilter = 'all' | 'error' | 'warn' | 'info'

// Props identifies the installation whose process buffer is read.
const props = defineProps<{
  installation: PluginInstallation // Installation whose logs are shown.
}>()
const { t } = useI18n()
const { copyToClipboard } = useClipboard()

// REFRESH_MS is the automatic refresh interval for a visible log panel.
const REFRESH_MS = 5000
// FOLLOW_SLACK_PX treats a scroll position this close to the end as "at the bottom".
const FOLLOW_SLACK_PX = 24

// Buffer state and local filters; the scroller element tracks whether to follow new lines.
const lines = shallowRef<PluginLogLine[]>([])
const loading = ref(false)
const error = ref('')
const level = ref<LevelFilter>('all')
const search = ref('')
const autoRefresh = ref(true)
const scroller = ref<HTMLElement | null>(null)
let controller: AbortController | null = null
let generation = 0
let timer: ReturnType<typeof setInterval> | null = null

// levels labels the severity filters; visible applies severity and text filters.
const levels = computed(() =>
  (['all', 'error', 'warn', 'info'] as const).map((key) => ({
    key,
    label: t(`admin.plugins.logView.levels.${key}`),
  })),
)
const visible = computed(() => {
  const term = search.value.trim().toLocaleLowerCase()
  return lines.value.filter(
    (line) =>
      (level.value === 'all' || severity(line.level) === level.value) &&
      (!term || line.message.toLocaleLowerCase().includes(term)),
  )
})

/** severity folds publisher level spellings into the three filterable groups. */
function severity(value: string): Exclude<LevelFilter, 'all'> {
  const normalized = value.toLowerCase()
  if (normalized === 'error' || normalized === 'fatal' || normalized === 'panic') return 'error'
  if (normalized === 'warn' || normalized === 'warning') return 'warn'
  return 'info'
}

/** levelClass maps a line's severity onto the shared semantic text colors. */
function levelClass(value: string): string {
  const group = severity(value)
  if (group === 'error') return 'text-red-600 dark:text-red-400'
  if (group === 'warn') return 'text-amber-600 dark:text-amber-400'
  return 'text-gray-400 dark:text-dark-500'
}

/** atBottom reports whether the reader is following the newest lines. */
function atBottom(): boolean {
  const element = scroller.value
  if (!element) return true
  return element.scrollHeight - element.scrollTop - element.clientHeight <= FOLLOW_SLACK_PX
}

/** refresh reads the latest buffer and keeps following the end when the reader was there.
 * A replacement request cancels earlier work. Success, failure, and loading cleanup
 * all check the generation, so a cancelled request cannot overwrite newer state.
 */
async function refresh(): Promise<void> {
  const current = ++generation
  controller?.abort()
  controller = new AbortController()
  loading.value = true
  try {
    const result = await logs(props.installation.plugin_id, controller.signal)
    if (current !== generation) return
    const follow = atBottom()
    lines.value = result
    error.value = ''
    await nextTick()
    if (follow && scroller.value) scroller.value.scrollTop = scroller.value.scrollHeight
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

/** copyVisible copies the filtered lines as plain text in their displayed order. */
function copyVisible(): void {
  void copyToClipboard(
    visible.value
      .map((line) => `${line.time} ${line.level.toUpperCase()} ${line.message}`)
      .join('\n'),
  )
}

/** schedule starts or stops automatic refresh to match the switch. */
function schedule(enabled: boolean): void {
  if (timer) clearInterval(timer)
  timer = null
  if (!enabled) return
  // Skip ticks while a read is pending or the browser tab is hidden; the next
  // visible tick catches up because the host returns the whole bounded buffer.
  timer = setInterval(() => {
    if (!loading.value && document.visibilityState === 'visible') void refresh()
  }, REFRESH_MS)
}

// A different plugin starts from an empty buffer so its lines never mix with the
// previous plugin's; the automatic refresh follows the switch from its first value.
watch(
  () => props.installation.plugin_id,
  () => {
    lines.value = []
    error.value = ''
    void refresh()
  },
  { immediate: true },
)
watch(autoRefresh, schedule, { immediate: true })
onBeforeUnmount(() => {
  generation++
  controller?.abort()
  schedule(false)
})
</script>

<template>
  <section class="card flex min-h-0 flex-1 flex-col overflow-hidden">
    <div
      class="flex flex-wrap items-center gap-2 border-b border-gray-100 px-4 py-3 dark:border-dark-700"
    >
      <div
        v-segmented
        class="segmented h-9 items-stretch"
        role="radiogroup"
        :aria-label="t('admin.plugins.logView.level')"
      >
        <button
          v-for="item in levels"
          :key="item.key"
          type="button"
          role="radio"
          :aria-checked="level === item.key"
          class="segmented-item px-3 text-sm"
          :class="{ 'segmented-item-active': level === item.key }"
          @click="level = item.key"
          >{{ item.label }}</button
        >
      </div>
      <SearchInput
        v-model="search"
        class="min-w-0 flex-1 sm:w-56 sm:flex-none"
        :placeholder="t('admin.plugins.logView.search')"
      />
      <div class="ml-auto flex items-center gap-2">
        <label class="flex h-9 items-center gap-2 px-1 text-sm text-gray-600 dark:text-dark-300">
          <Toggle v-model="autoRefresh" size="sm" />{{ t('admin.plugins.logView.autoRefresh') }}
        </label>
        <button
          type="button"
          class="btn btn-secondary btn-icon"
          :title="t('common.copy')"
          :aria-label="t('common.copy')"
          :disabled="!visible.length"
          @click="copyVisible"
          ><Icon name="copy" size="sm"
        /></button>
        <button
          type="button"
          class="btn btn-secondary btn-icon"
          :title="t('common.refresh')"
          :aria-label="t('common.refresh')"
          :disabled="loading"
          @click="refresh"
          ><Icon name="refresh" size="sm" :class="loading && 'animate-spin'"
        /></button>
      </div>
    </div>
    <SettingsNotice v-if="error" tone="error" class="mx-4 mt-3">{{ error }}</SettingsNotice>
    <div
      ref="scroller"
      class="min-h-80 flex-1 overflow-y-auto py-2"
      :aria-busy="loading"
      role="log"
    >
      <div v-if="loading && !lines.length" class="space-y-2 px-4 py-2">
        <Skeleton v-for="row in 8" :key="row" :width="`${40 + ((row * 37) % 55)}%`" height="12px" />
      </div>
      <p
        v-else-if="!visible.length"
        class="px-4 py-10 text-center text-sm text-gray-500 dark:text-dark-400"
        >{{ t(lines.length ? 'admin.plugins.logView.noMatches' : 'admin.plugins.logsEmpty') }}</p
      >
      <ol v-else class="font-mono text-xs leading-5">
        <li
          v-for="(line, index) in visible"
          :key="`${line.time}:${index}`"
          class="flex gap-3 px-4 py-0.5 hover:bg-gray-50 dark:hover:bg-dark-800"
          :class="severity(line.level) === 'error' && 'bg-red-50/60 dark:bg-red-500/5'"
        >
          <time class="w-36 shrink-0 tabular-nums text-gray-400 dark:text-dark-500">{{
            formatDateTime(line.time)
          }}</time>
          <span class="w-12 shrink-0 uppercase" :class="levelClass(line.level)">{{
            line.level
          }}</span>
          <span
            class="min-w-0 flex-1 whitespace-pre-wrap break-words text-gray-800 dark:text-dark-100"
            >{{ line.message }}</span
          >
        </li>
      </ol>
    </div>
    <p
      class="border-t border-gray-100 px-4 py-2 text-xs text-gray-500 dark:border-dark-700 dark:text-dark-400"
      >{{ t('admin.plugins.logsHint') }}</p
    >
  </section>
</template>
