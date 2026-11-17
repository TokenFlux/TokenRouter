<template>
  <BaseDialog
    :show="show"
    :title="t('admin.mcp.oauth.authorizeTitle')"
    width="normal"
    :close-on-escape="!submitting"
    @close="close"
    @after-leave="emit('after-leave')"
  >
    <div class="space-y-5" :aria-busy="loading || submitting">
      <span v-if="loading" class="sr-only" role="status">{{ t('common.loading') }}</span>
      <p
        v-if="error"
        class="rounded-control border border-red-200 p-3 text-sm text-red-600 dark:border-red-900 dark:text-red-400"
        role="alert"
        >{{ error }}</p
      >
      <template v-if="loading || review">
        <dl class="grid gap-4 sm:grid-cols-2">
          <div
            ><dt class="text-sm text-gray-500 dark:text-gray-400">{{
              t('admin.mcp.oauth.account')
            }}</dt
            ><dd class="mt-1 break-all font-medium"
              ><Skeleton v-if="loading" :width="132" :height="14" class="max-w-full" />
              <template v-else>{{ auth.user?.email }}</template></dd
            ></div
          >
          <div
            ><dt class="text-sm text-gray-500 dark:text-gray-400">{{
              t('admin.mcp.oauth.application')
            }}</dt
            ><dd class="mt-1 break-words font-medium"
              ><Skeleton v-if="loading" :width="156" :height="14" class="max-w-full" />
              <template v-else>{{ review?.request.client.client_name }}</template></dd
            ></div
          >
          <div class="sm:col-span-2"
            ><dt class="text-sm text-gray-500 dark:text-gray-400">{{
              t('admin.mcp.oauth.client')
            }}</dt
            ><dd class="mt-1 break-all font-mono text-sm"
              ><Skeleton v-if="loading" :width="192" :height="14" class="max-w-full" />
              <template v-else>{{ review?.request.client.client_id }}</template></dd
            ></div
          >
          <div
            ><dt class="text-sm text-gray-500 dark:text-gray-400">{{
              t('admin.mcp.oauth.callback')
            }}</dt
            ><dd class="mt-1 break-all text-sm"
              ><Skeleton v-if="loading" :width="192" :height="14" class="max-w-full" />
              <template v-else>{{ callbackOrigin }}</template></dd
            ></div
          >
          <div
            ><dt class="text-sm text-gray-500 dark:text-gray-400">{{
              t('admin.mcp.oauth.requestExpires')
            }}</dt
            ><dd class="mt-1 text-sm"
              ><Skeleton v-if="loading" :width="108" :height="14" class="max-w-full" />
              <template v-else>{{
                review ? formatDateTime(review.request.expires_at) : ''
              }}</template></dd
            ></div
          >
        </dl>
        <p class="text-sm text-gray-600 dark:text-gray-400">{{
          t('admin.mcp.oauth.identityHint')
        }}</p>
        <div v-if="loading" class="space-y-3" aria-hidden="true" data-loading-skeleton>
          <div class="flex flex-wrap items-center justify-between gap-2">
            <span class="input-label mb-0">{{ t('admin.mcp.permissions') }}</span>
            <Skeleton :width="72" :height="14" />
          </div>
          <p class="input-hint">{{ t('admin.mcp.permissionHint') }}</p>
          <div>
            <span class="input-label">{{ t('admin.mcp.preset') }}</span>
            <Skeleton :height="36" class="rounded-control" />
            <Skeleton width="72%" :height="14" class="mt-1.5" />
          </div>
          <Skeleton :width="134" :height="16" />
        </div>
        <fieldset v-else-if="review" :disabled="submitting || expired" class="min-w-0 space-y-4">
          <McpPermissionPicker
            :key="review.request.id"
            v-model="selected"
            :operations="review.operations"
            :skip-browser-confirmation="review.grant?.skip_browser_confirmation ?? false"
          />
        </fieldset>
        <p class="text-sm text-gray-600 dark:text-gray-400">{{
          t('admin.mcp.oauth.lifetimeHint')
        }}</p>
        <p v-if="expired" role="alert" class="text-sm text-amber-700 dark:text-amber-400">{{
          t('admin.mcp.oauth.expired')
        }}</p>
      </template>
      <button
        v-if="error && !review"
        class="btn btn-secondary"
        type="button"
        :disabled="loading"
        @click="load"
        >{{ t('admin.mcp.refresh') }}</button
      >
    </div>
    <template v-if="loading || review" #footer>
      <div class="flex justify-end gap-2">
        <button
          class="btn btn-secondary"
          type="button"
          :disabled="loading || submitting || expired"
          @click="decide(false)"
          >{{ t('admin.mcp.oauth.deny') }}</button
        >
        <button
          class="btn btn-primary"
          type="button"
          :disabled="loading || submitting || expired || !selected.size"
          @click="decide(true)"
          >{{ submitting ? t('common.processing') : t('admin.mcp.oauth.authorize') }}</button
        >
      </div>
    </template>
  </BaseDialog>
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Skeleton from '@/components/common/Skeleton.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { useStepUp, isStepUpCancelled, StepUpCancelledError } from '@/composables/useStepUp'
import { useAuthStore } from '@/stores/auth'
import { formatDateTime } from '@/utils/format'
import * as api from '@/api/admin/mcp'
import McpPermissionPicker from './McpPermissionPicker.vue'
import { permissionPresets } from './permissions'

// McpAuthorizeDialog.vue presents a session-bound application request over MCP management.
// The server supplies client identity, callback, and the current operation
// catalogue; the administrator chooses exact permissions and approves or denies.
// First connections start with safe reads, while returning applications show
// their existing permission selection for deliberate review before replacement.
//
// Closing the dialog or changing its request or account cancels pending reads.
// Each decision attempt and response requires the same active review and
// administrator, so a late challenge cannot reopen verification for a replaced
// request. Callback navigation requires that same context. Failed submissions
// retain the selected permissions, and local expiry disables submission until the
// client starts a new authorization flow.

// Browser identity and shared verification UI belong to this mounted consent dialog.
const props = defineProps<{
  show: boolean // Parent-owned dialog visibility.
  requestId: string // Incoming OAuth request identifier.
}>()
const emit = defineEmits<{
  (event: 'close'): void // Requests local workflow dismissal.
  (event: 'after-leave'): void // Reports completed dialog exit.
}>()
const { t } = useI18n()
const auth = useAuthStore()
const stepUp = useStepUp()
const review = ref<api.MCPOAuthReview | null>(null)
const selected = ref(new Set<string>())
const loading = ref(false)
const submitting = ref(false)
const error = ref('')
const now = ref(Date.now())
let controller: AbortController | null = null
let disposed = false
// The expiry timer updates display state only; the server independently validates the deadline.
const expiryTimer = setInterval(() => {
  now.value = Date.now()
}, 1000)
const expired = computed(
  () => !!review.value && Date.parse(review.value.request.expires_at) <= now.value,
)
const callbackOrigin = computed(() =>
  review.value ? new URL(review.value.redirect_uri).origin : '',
)

// messageOf exposes server feedback or the supplied localized request failure text.
function messageOf(value: unknown): string {
  return value instanceof Error
    ? value.message
    : value && typeof value === 'object' && 'message' in value
      ? String(value.message)
      : t('admin.mcp.requestFailed')
}

// load binds the current request and publishes only its latest authenticated review.
// A changed request or account aborts the preceding read and clears its approval
// proof. Each successful load initializes permissions from existing consent or safe
// reads, intersected with the current catalogue. The request's controller decides
// which result can update the dialog; an older request cannot replace a newer
// review or end its loading indicator. Missing identifiers leave a dismissible
// error without contacting the authorization API.
async function load() {
  // Replacing a review invalidates both its pending read and any verification
  // prompt, so their results cannot resume a decision for the replaced account.
  controller?.abort()
  stepUp.onCancel()
  const current = new AbortController()
  controller = current
  review.value = null
  error.value = ''
  loading.value = false
  const id = props.requestId
  if (typeof id !== 'string' || !id) {
    error.value = t('admin.mcp.oauth.expired')
    return
  }
  loading.value = true
  try {
    const result = await api.getOAuthReview(id, current.signal)
    if (current.signal.aborted || disposed) return
    const known = new Set(result.operations.map((operation) => operation.operation_id))
    selected.value = new Set(
      (
        result.grant?.permissions ??
        permissionPresets(result.operations).find((preset) => preset.id === 'readOnly')
          ?.permissions ??
        []
      ).filter((permission) => known.has(permission)),
    )
    review.value = result
  } catch (cause) {
    if (!current.signal.aborted && !disposed) error.value = messageOf(cause)
  } finally {
    if (controller === current) loading.value = false
  }
}

// decide submits the current permission choice and navigates after a committed decision.
// TOTP verification retries the same session-bound request. Failures retain the
// draft, and account changes prevent an old callback from navigating the new session.
// The administrator and review are captured before submission, then checked again
// before each API attempt and before exposing its result or failure to TOTP.
// The returned URL must match the reviewed callback origin and path before
// navigation; a changed dialog or request leaves the response unused. Concurrent
// submissions are ignored until the pending one ends.
async function decide(approve: boolean) {
  const current = review.value
  const userID = auth.user?.id
  const requestController = controller
  if (!current || expired.value || submitting.value || (approve && !selected.value.size)) return
  submitting.value = true
  error.value = ''
  const permissions = [...selected.value]
  const requestID = current.request.id
  // isCurrent ties retries to the reviewed request, account, and visibility.
  // The read controller remains aborted after closing or replacing a review,
  // including transitions that later return to the same request identifier.
  function isCurrent(): boolean {
    return (
      !disposed &&
      props.show &&
      props.requestId === requestID &&
      review.value === current &&
      auth.user?.id === userID &&
      !!requestController &&
      !requestController.signal.aborted
    )
  }
  try {
    const target = await stepUp.run(async () => {
      if (!isCurrent()) throw new StepUpCancelledError()
      try {
        const result = await api.decideOAuthRequest(
          current.request.id,
          current.nonce,
          approve,
          permissions,
        )
        if (!isCurrent()) throw new StepUpCancelledError()
        return result
      } catch (cause) {
        // Check failures before useStepUp classifies them: a delayed challenge
        // for an abandoned review must not open a prompt or retry its decision.
        if (!isCurrent()) throw new StepUpCancelledError()
        throw cause
      }
    })
    // A completed decision belongs to the original administrator and request;
    // leaving this workflow must prevent its callback from replacing the current page.
    if (!isCurrent()) return
    const expected = new URL(current.redirect_uri)
    const callback = new URL(target)
    if (
      callback.origin !== expected.origin ||
      callback.pathname !== expected.pathname ||
      callback.hash
    )
      throw new Error(t('admin.mcp.oauth.callbackInvalid'))
    window.location.assign(callback.href)
  } catch (cause) {
    if (isCurrent() && !isStepUpCancelled(cause)) error.value = messageOf(cause)
  } finally {
    submitting.value = false
  }
}

// close rejects a usable request before returning to its validated client callback.
// An expired request or failed workflow can be dismissed locally. In-flight
// decisions keep the dialog open until their result can be presented safely.
function close() {
  if (submitting.value) return
  if (review.value && !expired.value && !error.value) {
    void decide(false)
    return
  }
  emit('close')
}

// Watch scalar identities so ordinary profile refreshes cannot reset the draft.
// Closing retains rendered content for the shell's exit and cancels pending reads.
watch(
  [() => props.show, () => props.requestId, () => auth.user?.id],
  ([show]) => {
    if (show) void load()
    else {
      controller?.abort()
      stepUp.onCancel()
    }
  },
  { immediate: true },
)
// Disposal cancels pending reads and verification, then releases the local expiry
// timer so an unmounted dialog cannot keep updating its review state.
onBeforeUnmount(() => {
  disposed = true
  controller?.abort()
  stepUp.onCancel()
  clearInterval(expiryTimer)
})
</script>
