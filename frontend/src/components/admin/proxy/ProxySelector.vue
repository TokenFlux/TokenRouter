<template>
  <div class="relative" ref="containerRef">
    <button
      type="button"
      @click="toggle"
      :disabled="disabled"
      :class="[
        'input input-trigger',
        'text-gray-900 dark:text-gray-100',
        isOpen && 'select-trigger-open',
        disabled && 'select-trigger-disabled',
      ]"
    >
      <span class="select-value">
        {{ selectedLabel }}
      </span>
      <span class="select-icon">
        <Icon
          name="chevronDown"
          size="sm"
          :class="['transition-transform duration-normal', isOpen && 'rotate-180']"
          :animate-on-hover="false"
        />
      </span>
    </button>

    <MotionTransition name="dropdown-fade">
      <div v-if="isOpen" class="select-dropdown">
        <!-- Search and Batch Test Header -->
        <div class="select-header">
          <div class="select-search">
            <Icon name="search" size="sm" class="text-gray-400" />
            <input
              ref="searchInputRef"
              v-model="searchQuery"
              type="text"
              :placeholder="t('admin.proxies.searchProxies')"
              class="select-search-input"
              @click.stop
            />
          </div>
          <button
            v-if="proxies.length > 0"
            type="button"
            @click.stop="handleBatchTest"
            :disabled="batchTesting"
            class="batch-test-btn"
            :title="t('admin.proxies.batchTest')"
          >
            <Icon
              name="loader"
              size="sm"
              :animate-on-hover="false"
              v-if="batchTesting"
              class="h-4 w-4 animate-spin"
            />
            <Icon v-else name="play" size="sm" />
          </button>
        </div>

        <!-- Options list -->
        <div class="select-options">
          <!-- No Proxy option -->
          <div
            data-icon-trigger
            @click="selectOption(null)"
            :class="['select-option', modelValue === null && 'select-option-selected']"
          >
            <span class="select-option-label">{{ t('admin.providers.noProxy') }}</span>
            <Icon
              v-if="modelValue === null"
              name="check"
              size="sm"
              class="text-primary-500"
              :animate-on-hover="false"
            />
          </div>

          <!-- Proxy options -->
          <div
            data-icon-trigger
            v-for="proxy in filteredProxies"
            :key="proxy.id"
            @click="selectOption(proxy.id)"
            :class="['select-option', modelValue === proxy.id && 'select-option-selected']"
          >
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2">
                <span class="truncate font-medium">{{ proxy.name }}</span>
                <!-- Provider count badge -->
                <span
                  v-if="proxy.provider_count !== undefined"
                  class="inline-flex flex-shrink-0 items-center rounded-compact bg-gray-100 px-1.5 py-0.5 text-xs text-gray-600 dark:bg-dark-600 dark:text-gray-400"
                >
                  {{ proxy.provider_count }}
                </span>
                <!-- Test result badges -->
                <template v-if="testResults[proxy.id]">
                  <span
                    v-if="testResults[proxy.id].success"
                    class="inline-flex flex-shrink-0 items-center gap-1 rounded-compact bg-emerald-100 px-1.5 py-0.5 text-xs text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-400"
                  >
                    <span v-if="testResults[proxy.id].country">{{
                      testResults[proxy.id].country
                    }}</span>
                    <span v-if="testResults[proxy.id].latency_ms"
                      >{{ testResults[proxy.id].latency_ms }}ms</span
                    >
                  </span>
                  <span
                    v-else
                    class="inline-flex flex-shrink-0 items-center rounded-compact bg-red-100 px-1.5 py-0.5 text-xs text-red-700 dark:bg-red-900/30 dark:text-red-400"
                  >
                    {{ t('admin.proxies.testFailed') }}
                  </span>
                </template>
              </div>
              <div class="truncate text-xs text-gray-500 dark:text-gray-400">
                {{ proxy.protocol }}://{{ proxy.host }}:{{ proxy.port }}
              </div>
            </div>

            <!-- Individual test button -->
            <button
              type="button"
              @click.stop="handleTestProxy(proxy)"
              :disabled="testingProxyIds.has(proxy.id)"
              class="test-btn"
              :title="t('admin.proxies.testConnection')"
            >
              <Icon
                name="loader"
                size="xs"
                :animate-on-hover="false"
                v-if="testingProxyIds.has(proxy.id)"
                class="h-3.5 w-3.5 animate-spin"
              />
              <Icon v-else name="play" size="xs" />
            </button>

            <Icon
              v-if="modelValue === proxy.id"
              name="check"
              size="sm"
              class="flex-shrink-0 text-primary-500"
              :animate-on-hover="false"
            />
          </div>

          <!-- Empty state -->
          <div v-if="filteredProxies.length === 0 && searchQuery" class="select-empty">
            {{ t('common.noOptionsFound') }}
          </div>
        </div>
      </div>
    </MotionTransition>
  </div>
</template>

<script setup lang="ts">
import MotionTransition from '@/components/common/MotionTransition.vue'
import { ref, reactive, computed, onMounted, onUnmounted, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import Icon from '@/components/icons/Icon.vue'
import type { Proxy } from '@/api/admin/proxies.types'

// ProxySelector.vue presents an administrator proxy picker with individual and
// batch connectivity tests. The caller owns the selected proxy ID and candidate
// list, while this component keeps search text, dropdown visibility, and the
// latest test results for the current mounted picker.

// The translation function supplies picker labels while the API returns
// connectivity probe result messages.
const { t } = useI18n()

// ProxyTestResult records connectivity feedback rendered beside an administrator
// proxy option.
interface ProxyTestResult {
  success: boolean // Connectivity probe success flag.
  message: string // Connectivity probe result message.
  latency_ms?: number // Round-trip latency in milliseconds.
  ip_address?: string // Observed outgoing IP address.
  city?: string // Observed IP city name.
  region?: string // Observed IP region name.
  country?: string // Observed IP country name.
}

// Props supplies available proxies and the controlled selection for this picker.
interface Props {
  modelValue: number | null // Selected proxy; null means direct.
  proxies: Proxy[] // Available administrator proxy records.
  disabled?: boolean // Disable opening the picker.
}

// props supplies controlled selection and candidates, with interaction enabled
// unless the caller disables it.
const props = withDefaults(defineProps<Props>(), {
  disabled: false,
})

// emit publishes a changed proxy ID so the owning form can update its selection.
const emit = defineEmits<{
  'update:modelValue': [
    value: number | null,
  ] /* Selected proxy identifier; null selects direct connection. */
}>()

// Picker visibility, search input, and element references coordinate filtering,
// outside clicks, and opening focus.
const isOpen = ref(false)
const searchQuery = ref('')
const containerRef = ref<HTMLElement | null>(null)
const searchInputRef = ref<HTMLInputElement | null>(null)

// Test results and busy markers belong to this picker instance and update
// individual option feedback.
const testResults = reactive<Record<number, ProxyTestResult>>({})
const testingProxyIds = reactive(new Set<number>())
const batchTesting = ref(false)

// Selected proxy and label computations resolve the controlled ID against the
// latest candidate list.
const selectedProxy = computed(() => {
  if (props.modelValue === null) return null
  return props.proxies.find((p) => p.id === props.modelValue) || null
})

const selectedLabel = computed(() => {
  if (!selectedProxy.value) {
    return t('admin.providers.noProxy')
  }
  const proxy = selectedProxy.value
  return `${proxy.name} (${proxy.protocol}://${proxy.host}:${proxy.port})`
})

// filteredProxies matches the search against proxy names and hosts while
// retaining the supplied candidate order.
const filteredProxies = computed(() => {
  if (!searchQuery.value) {
    return props.proxies
  }
  const query = searchQuery.value.toLowerCase()
  return props.proxies.filter((proxy) => {
    const name = proxy.name.toLowerCase()
    const host = proxy.host.toLowerCase()
    return name.includes(query) || host.includes(query)
  })
})

// toggle opens or closes the picker unless disabled and focuses search after
// opening. The next DOM update creates the search input before focus is
// requested.
const toggle = () => {
  if (props.disabled) return
  isOpen.value = !isOpen.value
  if (isOpen.value) {
    nextTick(() => {
      searchInputRef.value?.focus()
    })
  }
}

// selectOption publishes a proxy ID or direct connection and closes the picker.
// Search text is cleared so the next opening displays all currently available
// proxy options.
const selectOption = (value: number | null) => {
  emit('update:modelValue', value)
  isOpen.value = false
  searchQuery.value = ''
}

// handleTestProxy records connectivity feedback for one proxy and releases its
// busy marker after completion. Repeated individual tests for a marked proxy
// return immediately; failures become failed result records.
const handleTestProxy = async (proxy: Proxy) => {
  if (testingProxyIds.has(proxy.id)) return

  testingProxyIds.add(proxy.id)
  try {
    const result = await adminAPI.proxies.testProxy(proxy.id)
    testResults[proxy.id] = result
  } catch (error: any) {
    // Store a displayable failure for this option so one rejected request does
    // not prevent other probe results.
    testResults[proxy.id] = {
      success: false, // connection probe failure
      message: error.response?.data?.detail || 'Test failed', // stored probe failure reason
    }
  } finally {
    testingProxyIds.delete(proxy.id)
  }
}

// handleBatchTest probes the supplied proxy list concurrently and records each
// result independently. Empty lists and repeated batch calls return immediately,
// while failed probes become result records before the batch busy state clears.
const handleBatchTest = async () => {
  if (batchTesting.value || props.proxies.length === 0) return

  batchTesting.value = true

  // Start every supplied proxy test together, then clear the batch flag after
  // each request has recorded success or failure.
  const testPromises = props.proxies.map(async (proxy) => {
    testingProxyIds.add(proxy.id)
    try {
      const result = await adminAPI.proxies.testProxy(proxy.id)
      testResults[proxy.id] = result
    } catch (error: any) {
      // Store a displayable failure for this option so one rejected request does
      // not prevent other probe results.
      testResults[proxy.id] = {
        success: false, // connection probe failure
        message: error.response?.data?.detail || 'Test failed', // stored probe failure reason
      }
    } finally {
      testingProxyIds.delete(proxy.id)
    }
  })

  await Promise.all(testPromises)
  batchTesting.value = false
}

// handleClickOutside closes the picker and clears search when a document click
// falls outside its root. Clicks within the picker keep its current open and
// search state.
const handleClickOutside = (event: MouseEvent) => {
  if (containerRef.value && !containerRef.value.contains(event.target as Node)) {
    isOpen.value = false
    searchQuery.value = ''
  }
}

// handleEscape closes an open picker and clears its active search query. Other
// keys and Escape while already closed leave the current picker state untouched.
const handleEscape = (event: KeyboardEvent) => {
  if (event.key === 'Escape' && isOpen.value) {
    isOpen.value = false
    searchQuery.value = ''
  }
}

// Register document dismissal handlers when the picker mounts so outside clicks and
// Escape can dismiss its open menu throughout its mounted lifetime.
onMounted(() => {
  document.addEventListener('click', handleClickOutside)
  document.addEventListener('keydown', handleEscape)
})

// Remove the same document handlers during teardown so global events cannot retain
// and update a picker after its component has been removed.
onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside)
  document.removeEventListener('keydown', handleEscape)
})
</script>

<style scoped>
/*
  The input recipe owns trigger geometry; these rules style open and disabled states.
  The open state keeps the hover border like Select instead of drawing a halo.
*/
.select-trigger-open {
  @apply border-black/20 dark:border-dark-500;
}

.select-trigger-disabled {
  @apply cursor-not-allowed bg-gray-100 opacity-60 dark:bg-dark-950;
}

.select-value {
  @apply flex-1 truncate text-left;
}

.select-icon {
  @apply flex-shrink-0 text-gray-400 dark:text-dark-400;
}

.select-dropdown {
  @apply absolute z-tooltip mt-2 w-full;
  @apply bg-white dark:bg-dark-900;
  @apply rounded-control;
  @apply border border-primary-900/10 dark:border-dark-600;
  @apply shadow-lg shadow-black/10 dark:shadow-black/30;
  @apply overflow-hidden;
}

.select-header {
  @apply flex items-center gap-2 px-3 py-2;
  @apply border-b border-primary-900/10 dark:border-dark-600;
}

.select-search {
  @apply flex flex-1 items-center gap-2;
}

.select-search-input {
  @apply flex-1 bg-transparent text-sm;
  @apply text-gray-900 dark:text-gray-100;
  @apply placeholder:text-gray-400 dark:placeholder:text-dark-400;
  @apply focus:outline-none;
}

.batch-test-btn {
  @apply flex-shrink-0 rounded-control p-1.5;
  @apply text-primary-900/90 hover:text-emerald-600 dark:hover:text-emerald-400;
  @apply hover:bg-emerald-50 dark:hover:bg-emerald-900/20;
  @apply transition-colors disabled:cursor-not-allowed disabled:opacity-50;
}

.select-options {
  @apply max-h-60 overflow-y-auto py-1;
}

.select-option {
  @apply flex items-center justify-between gap-2;
  @apply px-4 py-2.5 text-sm;
  @apply text-gray-700 dark:text-gray-300;
  @apply cursor-pointer transition-colors duration-fast;
  @apply hover:bg-gray-50 dark:hover:bg-dark-800;
}

.select-option-selected {
  /* Selected options combine a neutral background with brand text and a checkmark. */
  @apply bg-gray-100 dark:bg-dark-700;
  @apply text-primary-700 dark:text-primary-300;
}

.select-option-label {
  @apply truncate;
}

.select-empty {
  @apply px-4 py-8 text-center text-sm;
  @apply text-primary-900/90 dark:text-dark-400;
}

.test-btn {
  @apply flex-shrink-0 rounded-compact p-1;
  @apply text-gray-400 hover:text-emerald-600 dark:hover:text-emerald-400;
  @apply hover:bg-emerald-50 dark:hover:bg-emerald-900/20;
  @apply transition-colors disabled:cursor-not-allowed disabled:opacity-50;
}
</style>
