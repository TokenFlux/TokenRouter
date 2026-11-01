<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import {
  HostRequestError,
  TrAmount,
  TrButton,
  TrCard,
  TrSkeleton,
  useHost,
  useMessages,
} from '@tokenflux/tokenrouter-plugin-sdk'
import { messages } from '../messages'

// HomePage.vue presents the daily reward and keeps one claim action available
// until the backend confirms completion. A completed credit remains visible
// even if refreshing the host account fails, preventing a display failure from
// inviting another financial operation. Request cleanup follows page lifetime.

/** CheckinStatus contains the example backend's current-day presentation values. */
interface CheckinStatus {
  checked_in: boolean // Confirmed daily completion state.
  reward: string // Current decimal reward amount.
  notice: string // Administrator-configured notice text.
}

// Page state belongs to this mounted plugin page. The abort controller cancels
// reads and writes when navigation removes the page from its host container.
const host = useHost()
const t = useMessages(messages)
const status = ref<CheckinStatus | null>(null)
const loading = ref(true)
const claiming = ref(false)
const failure = ref('')
const refreshFailed = ref(false)
const controller = new AbortController()

/** loadStatus reads completion without creating a daily claim or changing balance.
 * Loading failures leave a retry action visible. Abort caused by page removal
 * is ignored because the removed component no longer owns visible feedback.
 */
async function loadStatus(): Promise<void> {
  loading.value = true
  failure.value = ''
  try {
    const response = await host.request<CheckinStatus>('GET', '/status', {
      signal: controller.signal,
    })
    if (!controller.signal.aborted) status.value = response
  } catch {
    if (!controller.signal.aborted) failure.value = t('loadFailed')
  } finally {
    loading.value = false
  }
}

/** refreshBalance retries only the host account display after a completed reward.
 * It never calls the check-in endpoint again. Failure preserves the completed
 * state and keeps this separate display-recovery action available to the user.
 */
async function refreshBalance(): Promise<void> {
  try {
    await host.refreshAccount()
    refreshFailed.value = false
  } catch {
    refreshFailed.value = true
  }
}

/** claim records completion before refreshing the account shown by the host.
 * Duplicate completion responses reconcile the page instead of offering another
 * credit. Other failures preserve the available claim action for a safe retry.
 *
 * The backend fixes reward intent through its KV record and wallet replay key.
 * This component therefore retries the same business operation without inventing
 * a browser-side financial identity. A failed account refresh affects only the
 * display, while the confirmed completion state prevents accidental duplicate actions.
 */
async function claim(): Promise<void> {
  if (claiming.value || status.value?.checked_in) return
  claiming.value = true
  failure.value = ''
  try {
    await host.request('POST', '/checkin', { signal: controller.signal })
    if (controller.signal.aborted) return
    if (status.value) status.value.checked_in = true
    await refreshBalance()
    if (!controller.signal.aborted) host.toast.success(t('received'))
  } catch (error) {
    if (controller.signal.aborted) return
    if (error instanceof HostRequestError && error.code === 'ALREADY_CHECKED_IN') {
      if (status.value) status.value.checked_in = true
      await refreshBalance()
    } else if (!controller.signal.aborted) {
      failure.value = t('failed')
    }
  } finally {
    claiming.value = false
  }
}

onMounted(loadStatus)
// Cancel outstanding requests when the host removes this page, so its responses
// cannot continue owning user feedback after navigation has selected another page.
onBeforeUnmount(() => controller.abort())
</script>

<template>
  <section class="checkin-page">
    <header class="checkin-heading">
      <h1>{{ t('title') }}</h1>
      <p>{{ t('description') }}</p>
    </header>
    <TrCard class="checkin-reward">
      <TrSkeleton v-if="loading" :rows="3" />
      <template v-else-if="status">
        <p class="checkin-label">{{ t('reward') }}</p>
        <div class="checkin-amount"><TrAmount :value="status.reward" /></div>
        <p v-if="status.notice">{{ status.notice }}</p>
        <div v-if="status.checked_in" class="checkin-completed" role="status">
          <strong>{{ t('completed') }}</strong>
          <p>{{ t('next') }}</p>
        </div>
        <TrButton v-else :loading="claiming" :disabled="claiming" @click="claim">{{
          claiming ? t('claiming') : t('claim')
        }}</TrButton>
        <template v-if="refreshFailed"
          ><p role="alert">{{ t('refreshFailed') }}</p>
          <TrButton variant="secondary" @click="refreshBalance">{{
            t('refreshBalance')
          }}</TrButton></template
        >
      </template>
      <template v-if="failure"
        ><p class="checkin-error" role="alert">{{ failure }}</p>
        <TrButton v-if="!status" variant="secondary" @click="loadStatus">{{
          t('retry')
        }}</TrButton></template
      >
    </TrCard>
  </section>
</template>
