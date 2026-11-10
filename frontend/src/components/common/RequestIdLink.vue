<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { getActivePinia } from 'pinia'
import { useAuthStore } from '@/stores'
import { useI18n } from 'vue-i18n'
import { useClipboard } from '@/composables/useClipboard'
import { COPY_FEEDBACK_MS } from '@/constants/ui'
import Icon from '@/components/icons/Icon.vue'
import IdBadge from '@/components/common/IdBadge.vue'

/**
 * RequestIdLink.vue displays a copyable request identifier and an optional
 * administrator diagnostic link. Role changes update link availability, while
 * clipboard feedback belongs to the value that was copied so reused table cells do not
 * display a checkmark for a different request.
 *
 * The copy operation captures the identifier before awaiting the clipboard helper.
 * Successful completion records that captured value and replaces the feedback timeout;
 * the template compares it with the current prop before showing success. Unmount
 * clears the currently registered timeout. The component accepts rendering without an
 * active Pinia instance, in which case it renders the identifier with its copy action.
 */

/**
 * props supplies the request identifier, diagnostic link permission, and choice
 * between truncated inline text and full wrapping display.
 */
const props = withDefaults(
  defineProps<{
    value?: string | null /* current editor value */
    /**
     * link permits diagnostic navigation for administrators; false keeps the copy
     * action available while rendering the identifier as ordinary text.
     */
    link?: boolean
    /**
     * full allows the complete identifier to wrap across lines; false constrains the
     * inline display and truncates the text.
     */
    full?: boolean
  }>(),
  {
    link: true,
    full: false,
  },
)

/**
 * authStore reads administrator status when Pinia is available; rendering outside a
 * store context keeps the copyable text path.
 */
const authStore = getActivePinia() ? useAuthStore() : null
/**
 * The i18n translator resolves this component's message keys using the current
 * application locale and its configured translation catalog.
 */
const { t } = useI18n()
/**
 * The clipboard composable owns copying and user feedback, and the template delegates
 * its copy action through this binding.
 */
const { copyToClipboard } = useClipboard()
/**
 * copiedValue records the identifier whose clipboard operation succeeded, allowing
 * reused cells to compare feedback with their current value.
 */
const copiedValue = ref('')
/**
 * copiedTimer owns the current feedback expiry timeout and is replaced after
 * successful copies or cleared during unmount.
 */
let copiedTimer: ReturnType<typeof setTimeout> | undefined

/**
 * canOpenDetails permits diagnostic navigation when the caller enables linking and the
 * current authenticated role is administrator.
 */
const canOpenDetails = computed(() => props.link && authStore?.isAdmin === true)
/**
 * destination targets the administrator request lookup route with the current
 * identifier in its request_id query field.
 */
const destination = computed(() => ({
  path: '/admin/requests',
  query: { request_id: props.value || undefined },
}))

/**
 * showCopied requires successful feedback to belong to the identifier currently
 * rendered, hiding stale feedback when a cell is reused.
 */
const showCopied = computed(() => Boolean(copiedValue.value) && copiedValue.value === props.value)

/**
 * copy captures the displayed identifier and publishes feedback after the clipboard
 * helper succeeds. Empty values and unsuccessful copies leave existing feedback
 * unchanged. The captured value is compared with the current prop by showCopied, so a
 * changed row does not show success for another request. Successful copies replace the
 * existing timeout, and the component cleanup clears the currently registered timer
 * when it unmounts.
 */
async function copy() {
  const value = props.value
  if (!value || !(await copyToClipboard(value, t('requests.copied')))) return
  copiedValue.value = value
  /**
   * Replace the earlier feedback expiry before starting another timer, so an older
   * timeout cannot clear a later successful copy indicator.
   */
  clearTimeout(copiedTimer)
  copiedTimer = setTimeout(() => {
    copiedValue.value = ''
  }, COPY_FEEDBACK_MS)
}

/**
 * Unmounting cancels the currently registered feedback timer so its pending expiry
 * does not clear copy feedback after this instance leaves the page.
 */
onBeforeUnmount(() => clearTimeout(copiedTimer))
</script>

<template>
  <span
    v-if="value"
    class="inline-flex min-w-0 items-center gap-1.5 align-middle"
    :class="full ? 'max-w-full' : 'max-w-56'"
  >
    <IdBadge />
    <RouterLink
      v-if="canOpenDetails"
      :to="destination"
      :title="value"
      :class="full ? 'break-all' : 'truncate'"
      class="min-w-0 font-mono text-xs text-link"
      @click.stop
      >{{ value }}</RouterLink
    >
    <span
      v-else
      class="min-w-0 font-mono text-xs text-gray-600 dark:text-dark-200"
      :class="full ? 'break-all' : 'truncate'"
      :title="value"
      >{{ value }}</span
    >
    <button
      type="button"
      class="shrink-0 rounded-compact p-0.5 transition-colors hover:bg-gray-200 dark:hover:bg-dark-700"
      :class="
        showCopied
          ? 'text-emerald-500'
          : 'text-gray-400 hover:text-gray-600 dark:text-dark-400 dark:hover:text-dark-200'
      "
      :aria-label="t('requests.copy')"
      :title="showCopied ? t('common.copied') : t('requests.copy')"
      @click.stop="copy"
    >
      <Icon :name="showCopied ? 'check' : 'copy'" size="sm" class="h-3.5 w-3.5" />
    </button>
  </span>
  <span v-else class="text-sm text-gray-400 dark:text-dark-500">-</span>
</template>
