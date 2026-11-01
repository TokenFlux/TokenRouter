<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { TrButton, TrCard, TrEmpty, TrTable, useHost, useMessages } from '@tokenflux/tokenrouter-plugin-sdk'
import { messages } from '../messages'

// AdminPage.vue lists the example's persisted check-in records through its
// administrator-only backend route. Cursor pagination appends records without
// discarding the current page on failure, while page teardown cancels requests
// that no longer have a mounted owner for their results.

/** HistoryRow contains one stored daily reward and its completion state. */
interface HistoryRow {
  user_id: number // Recipient account identifier.
  day: string // Host-calendar check-in date.
  amount: string // Captured decimal reward amount.
  completed: boolean // Confirmed wallet completion flag.
}

/** HistoryPage carries a bounded list and the next exclusive KV cursor. */
interface HistoryPage {
  rows: HistoryRow[] // Returned check-in history records.
  next_cursor: string // Next page continuation key.
}

// The mounted history page owns its accumulated rows and one request at a time.
// Failed pagination keeps both existing rows and the cursor needed for retry.
const host = useHost()
const t = useMessages(messages)
const records = ref<HistoryRow[]>([])
const cursor = ref('')
const loading = ref(false)
const failure = ref('')
const controller = new AbortController()
const columns = computed(() => [
  { key: 'user_id', title: t('user') },
  { key: 'day', title: t('day') },
  { key: 'amount', title: t('amount') },
  { key: 'state', title: t('status') },
])
const rows = computed(() =>
  records.value.map((row) => ({
    ...row,
    state: t(row.completed ? 'done' : 'pending'),
  })),
)

/** loadNext appends a successful page and advances its continuation cursor.
 * Overlapping requests are ignored while loading. Failed requests retain the
 * previous cursor and visible records so the same page can be retried.
 */
async function loadNext(): Promise<void> {
  if (loading.value) return
  loading.value = true
  failure.value = ''
  try {
    const page = await host.request<HistoryPage>('GET', '/admin/history', {
      query: { cursor: cursor.value },
      signal: controller.signal,
    })
    if (controller.signal.aborted) return
    records.value.push(...page.rows)
    cursor.value = page.next_cursor
  } catch {
    if (!controller.signal.aborted) failure.value = t('loadFailed')
  } finally {
    loading.value = false
  }
}

onMounted(loadNext)
// Cancel the page-owned request on navigation so a removed administrator page
// cannot append records or publish feedback after its visible lifetime ends.
onBeforeUnmount(() => controller.abort())
</script>

<template>
  <section class="checkin-page">
    <header class="checkin-heading">
      <h1>{{ t('history') }}</h1>
      <p>{{ t('historyDescription') }}</p>
    </header>
    <TrCard>
      <TrTable v-if="rows.length || loading" :columns="columns" :rows="rows" :loading="loading" />
      <TrEmpty v-else-if="!failure" :title="t('empty')" />
      <p v-if="failure" class="checkin-error" role="alert">{{ failure }}</p>
      <TrButton v-if="cursor || failure" variant="secondary" :loading="loading" @click="loadNext">{{
        failure ? t('retry') : t('more')
      }}</TrButton>
    </TrCard>
  </section>
</template>
