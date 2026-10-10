<template>
  <div>
    <!-- Multi-select Dropdown -->
    <div class="relative mb-3">
      <div data-icon-trigger
        @click="toggleDropdown"
        class="cursor-pointer rounded-control border border-gray-300 bg-white px-3 py-2 dark:border-dark-500 dark:bg-dark-700"
      >
        <TransitionGroup name="motion-list" tag="div" class="relative grid grid-cols-2 gap-1.5" @before-leave="prepareListLeave" @before-enter="restoreEnteringElement">
          <span
            v-for="model in modelValue"
            :key="model"
            class="inline-flex items-center justify-between gap-1 rounded-compact bg-gray-100 px-2 py-1 text-xs text-gray-700 dark:bg-dark-600 dark:text-gray-300"
          >
            <span class="flex items-center gap-1 truncate">
              <ModelIcon :model="model" size="14px" />
              <span class="truncate">{{ model }}</span>
            </span>
            <button
              type="button"
              @click.stop="removeModel(model)"
              class="shrink-0 rounded-full hover:bg-gray-200 dark:hover:bg-dark-700"
            >
              <Icon name="x" size="xs" class="h-3.5 w-3.5" :stroke-width="2" />
            </button>
          </span>
        </TransitionGroup>
        <div class="mt-2 flex items-center justify-between border-t border-gray-200 pt-2 dark:border-dark-600">
          <span class="text-xs text-gray-400">{{ t('admin.providers.modelCount', { count: modelValue.length }) }}</span>
          <Icon name="chevronDown" size="md" :animate-on-hover="false" class="h-5 w-5 text-gray-400" />
        </div>
      </div>
      <!-- Dropdown List -->
      <MotionTransition name="dropdown-fade">
        <div
          v-if="showDropdown" :inert="!(showDropdown) || undefined"
          class="dropdown left-0 right-0 top-full z-50 mt-1 py-0"
        >
          <div class="sticky top-0 border-b border-gray-200 bg-white p-2 dark:border-dark-600 dark:bg-dark-900">
            <input
              v-model="searchQuery"
              type="text"
              class="input w-full text-sm"
              :placeholder="t('admin.providers.searchModels')"
              @click.stop
            />
          </div>
          <div class="max-h-52 overflow-auto">
            <div
              v-for="model in filteredModels"
              :key="model.value"
              data-testid="model-option"
              class="group flex items-center hover:bg-gray-100 dark:hover:bg-dark-800"
            >
              <button
                type="button"
                data-testid="select-model"
                class="flex min-w-0 flex-1 items-center gap-2 px-3 py-2 text-left text-sm"
                @click="toggleModel(model.value)"
              >
                <span
                  :class="[
                    'flex h-4 w-4 shrink-0 items-center justify-center rounded-compact border',
                    modelValue.includes(model.value)
                      ? 'border-primary-700 bg-primary-700 text-white dark:border-primary-600 dark:bg-primary-600'
                      : 'border-gray-300 dark:border-dark-500'
                  ]"
                >
                  <Icon
                    name="check"
                    size="xs"
                    :animate-on-hover="false"
                    v-if="modelValue.includes(model.value)"
                    class="h-3 w-3"
                  />
                </span>
                <ModelIcon :model="model.value" size="18px" />
                <span class="truncate text-gray-900 dark:text-white">{{ model.value }}</span>
              </button>
              <button
                type="button"
                data-testid="copy-model-id"
                class="mr-2 shrink-0 rounded-compact p-1.5 text-gray-400 opacity-70 transition-colors hover:bg-gray-200 hover:text-primary-600 focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-black/10 group-hover:opacity-100 dark:text-gray-500 dark:hover:bg-dark-700 dark:hover:text-primary-400 dark:focus-visible:ring-primary-500"
                :title="`${t('common.copy')} ${model.value}`"
                :aria-label="`${t('common.copy')} ${model.value}`"
                @click="copyModelId(model.value)"
              >
                <Icon name="copy" size="sm" />
              </button>
            </div>
            <div v-if="loadingCatalog" class="px-3 py-2 text-sm text-gray-500">{{ t('common.loading') }}</div>
            <button v-if="catalogFailed" type="button" class="btn btn-secondary m-2" @click.stop="loadCatalog()">{{ t('common.retry') }}</button>
            <button v-if="!loadingCatalog && !catalogFailed && props.models === undefined && catalogModels.length < catalogTotal" type="button" class="btn btn-secondary m-2" @click.stop="loadCatalog(true)">{{ t('admin.providers.loadMoreModels') }}</button>
            <div v-if="!loadingCatalog && !catalogFailed && filteredModels.length === 0" class="px-3 py-4 text-center text-sm text-gray-500">
              {{ t('admin.providers.noMatchingModels') }}
            </div>
          </div>
        </div>
      </MotionTransition>
    </div>

    <!-- Quick Actions -->
    <div class="mb-4 flex flex-wrap gap-2">
      <button
        v-if="canSyncUpstream"
        type="button"
        @click="syncUpstreamModels"
        :disabled="isSyncingUpstream"
        class="rounded-control border border-emerald-200 px-3 py-1.5 text-sm text-emerald-600 hover:bg-emerald-50 disabled:cursor-not-allowed disabled:opacity-60 dark:border-emerald-800 dark:text-emerald-400 dark:hover:bg-emerald-900/30"
      >
        {{ isSyncingUpstream ? t('admin.providers.syncUpstreamModelsLoading') : t('admin.providers.syncUpstreamModels') }}
      </button>
      <button
        type="button"
        @click="clearAll"
        class="rounded-control border border-red-200 px-3 py-1.5 text-sm text-red-600 hover:bg-red-50 dark:border-red-800 dark:text-red-400 dark:hover:bg-red-900/30"
      >
        {{ t('admin.providers.clearAllModels') }}
      </button>
    </div>

    <!-- Custom Model Input -->
    <div class="mb-3">
      <label class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">{{ t('admin.providers.customModelName') }}</label>
      <div class="flex gap-2">
        <input
          v-model="customModel"
          type="text"
          class="input flex-1"
          :placeholder="t('admin.providers.enterCustomModelName')"
          @keydown.enter.prevent="handleEnter"
          @compositionstart="isComposing = true"
          @compositionend="isComposing = false"
        />
        <button
          type="button"
          @click="addCustom"
          class="rounded-control bg-primary-50 px-4 py-2 text-sm font-medium text-primary-600 hover:bg-primary-100 dark:bg-primary-900/30 dark:text-primary-400 dark:hover:bg-primary-900/50"
        >
          {{ t('admin.providers.addModel') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { prepareListLeave, restoreEnteringElement } from '@/utils/leavingElement'

import MotionTransition from '@/components/common/MotionTransition.vue'
import { ref, computed, watch, onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { providersAPI } from '@/api/admin/providers'
import type { SyncUpstreamPreviewParams } from '@/api/admin/providers'
import { useClipboard } from '@/composables/useClipboard'
import ModelIcon from '@/components/common/ModelIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import { modelAttributesAPI } from '@/api/admin/modelAttributes'
import { SEARCH_DEBOUNCE_MS } from '@/constants/ui'

const { t } = useI18n()

const props = defineProps<{
  modelValue: string[]
  platform?: string
  platforms?: string[]
  models?: string[]
  providerId?: number
  syncCredentials?: {
    platform: string
    type: string
    base_url?: string
    api_key: string
  }
}>()

const emit = defineEmits<{
  'update:modelValue': [value: string[]]
}>()

const appStore = useAppStore()
const { copyToClipboard } = useClipboard()

const showDropdown = ref(false)
const searchQuery = ref('')
const customModel = ref('')
const isComposing = ref(false)
const isSyncingUpstream = ref(false)
const normalizedPlatforms = computed(() => {
  const rawPlatforms =
    props.platforms && props.platforms.length > 0
      ? props.platforms
      : props.platform
        ? [props.platform]
        : []

  return Array.from(
    new Set(
      rawPlatforms
        .map(platform => platform?.trim())
        .filter((platform): platform is string => Boolean(platform))
    )
  )
})

const upstreamSyncPlatforms = new Set([
  'anthropic',
  'openai',
  'gemini',
  'antigravity',
  'grok',
  'kimi',
  'zhipu',
  'deepseek',
  'jev'
])
const canSyncUpstream = computed(() => {
  if (props.providerId) {
    if (normalizedPlatforms.value.length === 0) return true
    return normalizedPlatforms.value.some(platform => upstreamSyncPlatforms.has(platform.toLowerCase()))
  }
  if (props.syncCredentials) {
    return upstreamSyncPlatforms.has(props.syncCredentials.platform.toLowerCase())
  }
  return false
})

// 目录查询只更新候选，已选 ID 由表单持有。
const catalogModels = ref<string[]>([])
const catalogPage = ref(0)
const catalogTotal = ref(0)
const loadingCatalog = ref(false)
const catalogFailed = ref(false)
let queryVersion = 0
let searchTimer: ReturnType<typeof setTimeout> | undefined

const loadCatalog = async (append = false) => {
  if (props.models !== undefined) return
  const version = ++queryVersion
  const page = append ? catalogPage.value + 1 : 1
  loadingCatalog.value = true
  catalogFailed.value = false
  try {
    const result = await modelAttributesAPI.defaults({ search: searchQuery.value.trim(), page, page_size: 50 })
    if (version !== queryVersion) return
    catalogModels.value = [...new Set([...(append ? catalogModels.value : []), ...result.items.map(item => item.model)])]
    catalogPage.value = page
    catalogTotal.value = result.total
  } catch {
    if (version === queryVersion) catalogFailed.value = true
  } finally {
    if (version === queryVersion) loadingCatalog.value = false
  }
}

watch([showDropdown, searchQuery, () => props.models], () => {
  queryVersion++
  clearTimeout(searchTimer)
  loadingCatalog.value = false
  catalogFailed.value = false
  catalogModels.value = []
  catalogTotal.value = 0
  if (showDropdown.value && props.models === undefined) {
    searchTimer = setTimeout(() => { void loadCatalog() }, SEARCH_DEBOUNCE_MS)
  }
})
onBeforeUnmount(() => {
  queryVersion++
  clearTimeout(searchTimer)
})

const filteredModels = computed(() => {
  const query = searchQuery.value.toLowerCase().trim()
  const ids = props.models === undefined ? catalogModels.value : props.models.filter(id => id.toLowerCase().includes(query))
  return [...new Set(ids)].map(id => ({ value: id, label: id }))
})

const toggleDropdown = () => {
  showDropdown.value = !showDropdown.value
  if (!showDropdown.value) searchQuery.value = ''
}

const removeModel = (model: string) => {
  emit('update:modelValue', props.modelValue.filter(m => m !== model))
}

const toggleModel = (model: string) => {
  if (props.modelValue.includes(model)) {
    removeModel(model)
  } else {
    emit('update:modelValue', [...props.modelValue, model])
  }
}

const copyModelId = async (model: string) => {
  await copyToClipboard(model)
}

const addCustom = () => {
  const model = customModel.value.trim()
  if (!model) return
  if (props.modelValue.includes(model)) {
    appStore.showInfo(t('admin.providers.modelExists'))
    return
  }
  emit('update:modelValue', [...props.modelValue, model])
  customModel.value = ''
}

const handleEnter = () => {
  if (!isComposing.value) addCustom()
}

const syncUpstreamModels = async () => {
  if (isSyncingUpstream.value) return
  if (!props.providerId && !props.syncCredentials) return

  isSyncingUpstream.value = true
  try {
    let result
    if (props.providerId) {
      result = await providersAPI.syncUpstreamModels(props.providerId)
    } else if (props.syncCredentials) {
      result = await providersAPI.syncUpstreamModelsPreview(props.syncCredentials as SyncUpstreamPreviewParams)
    } else {
      return
    }

    const upstreamModels = result.models.map(model => model.trim()).filter(Boolean)
    if (upstreamModels.length === 0) {
      appStore.showInfo(t('admin.providers.syncUpstreamModelsEmpty'))
      return
    }

    const newModels = [...props.modelValue]
    let addedCount = 0
    for (const model of upstreamModels) {
      if (!newModels.includes(model)) {
        newModels.push(model)
        addedCount += 1
      }
    }

    emit('update:modelValue', newModels)
    if (addedCount > 0) {
      appStore.showSuccess(t('admin.providers.syncUpstreamModelsSuccess', { count: addedCount, total: upstreamModels.length }))
    } else {
      appStore.showInfo(t('admin.providers.syncUpstreamModelsNoChanges', { count: upstreamModels.length }))
    }
  } catch (error) {
    const message = error instanceof Error ? error.message : t('admin.providers.syncUpstreamModelsFailed')
    appStore.showError(t('admin.providers.syncUpstreamModelsError', { message }))
  } finally {
    isSyncingUpstream.value = false
  }
}

const clearAll = () => {
  emit('update:modelValue', [])
}

</script>
